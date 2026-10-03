package paths

import (
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/goccy/go-yaml"
	"github.com/goccy/go-yaml/ast"
	"github.com/goccy/go-yaml/token"

	"go.jacobcolvin.com/niceyaml/internal/aliaslimit"
	"go.jacobcolvin.com/niceyaml/internal/astnode"
)

var (
	// ErrNoDocument indicates a document with no content to resolve in: a
	// nil document, a document without a body, a document holding only
	// directives or comments, or a document of whitespace alone. Errors
	// that wrap it also wrap [ErrNotFound], since nothing exists at any
	// path in such a document. The root path of such a document under a
	// "---" header still resolves, to the null at the header.
	ErrNoDocument = errors.New("document has no content")

	// ErrNotFound indicates that nothing exists at the path in the document.
	ErrNotFound = errors.New("not found")

	// ErrAlias indicates an alias on the path that names no anchor or that
	// leads back to itself, so it has no content.
	ErrAlias = errors.New("alias does not resolve")

	// ErrWildcard indicates a request for a single node or token from a path
	// with a `.*`, `[*]`, or `..` selector. Use [Path.Nodes] for such paths.
	ErrWildcard = errors.New("wildcard path matches any number of nodes")

	// ErrNoYAMLPath indicates a path with a `.*` or `..*` selector, which
	// the goccy/go-yaml path syntax has no selector for. [Path.YAMLPath]
	// returns it.
	ErrNoYAMLPath = errors.New("selector has no goccy/go-yaml equivalent")

	// ErrExcessiveAliasing indicates a path whose selectors reach far more
	// nodes than the document holds. A `[*]` selector lists the elements
	// of a sequence once for each alias that leads to it, and a `.*`
	// selector lists the entries of a mapping the same way. A few hundred
	// bytes of nested aliases can thus make a path select millions of
	// nodes. A selector may reach each node of the document once, and the
	// nodes it reaches past that count may make up only the share of them
	// that gopkg.in/yaml.v3 allows the aliases in a document it decodes.
	// It is the same error value as
	// [go.jacobcolvin.com/niceyaml.ErrExcessiveAliasing].
	ErrExcessiveAliasing = aliaslimit.ErrExcessiveAliasing

	// ErrExcessiveMerging indicates a path whose key lookups read far more
	// nodes under `<<` merge keys than the document holds. A key lookup
	// reads the value of each merge key it reaches, and each element of
	// that value when it is a sequence of sources. It goes on into the
	// mappings those merge keys bring in until it finds the key, so a
	// lookup on a chain of merges can read the whole chain. A lookup that
	// reaches many mappings that merge one sequence through an alias reads
	// that sequence once for each of them. A `..name` selector looks up
	// each key that a later merge key may override. It also looks up `<<`
	// at the last merge key of each mapping it walks, to learn whether a
	// merge brings in a real `<<` key. That lookup reads the whole merge
	// chain behind the mapping, whatever name the selector searches for. A
	// `.name` after a `[*]` looks up its key in each element, and a `.*`
	// looks up each key of its mapping and of the mappings it merges. Each
	// of these can make one selector read a chain many times. A selector
	// may read 64 times as many of these nodes as the document holds, or
	// 524,288 of them when that is more. A call of [Resolver.Entry] may
	// read as many as one selector, and the calls of one [EntryFinder] may
	// read that many between them.
	ErrExcessiveMerging = errors.New("excessive merging")

	// Before quoteName or goccyString wraps a selector name in single
	// quotes, nameEscaper escapes its backslashes and single quotes. A
	// [strings.Replacer] is safe for concurrent use, so every call shares
	// this one.
	nameEscaper = strings.NewReplacer(`\`, `\\`, `'`, `\'`)
)

// segmentKind identifies the selector a [segment] applies.
type segmentKind int

const (
	segmentChild        segmentKind = iota // .name
	segmentIndex                           // [n]
	segmentIndexAll                        // [*]
	segmentRecursive                       // ..name
	segmentKey                             // ~
	segmentChildAll                        // .*
	segmentRecursiveAll                    // ..*
)

// segment is one selector of a [Path].
type segment struct {
	name  string
	index int
	kind  segmentKind
}

// String returns the segment in path expression syntax.
func (s segment) String() string {
	switch s.kind {
	case segmentChild:
		return "." + quoteName(s.name)
	case segmentIndex:
		return "[" + strconv.Itoa(s.index) + "]"
	case segmentIndexAll:
		return "[*]"
	case segmentRecursive:
		return ".." + quoteName(s.name)
	case segmentKey:
		return "~"
	case segmentChildAll:
		return ".*"
	case segmentRecursiveAll:
		return "..*"
	default:
		return ""
	}
}

// reservedNameChars are the characters that force a selector name into
// single quotes so that [Parse] reads it back as one selector.
const reservedNameChars = ".*[]$'~"

// quoteName returns name in the form [Parse] accepts as a child or
// recursive selector. It wraps the name in single quotes when it contains
// reserved characters or is empty, since an unquoted empty name would leave
// a bare `.` that Parse rejects. It also quotes a name that holds `:` or
// whitespace, which Parse reads back unquoted too, so the name stays apart
// from the ": " that follows a path in an error message, and a space at
// either end of it stays visible.
func quoteName(name string) string {
	if name != "" && !strings.ContainsAny(name, reservedNameChars) &&
		!strings.ContainsFunc(name, isSeparatorRune) {
		return name
	}

	escaped := nameEscaper.Replace(name)

	return "'" + escaped + "'"
}

// isSeparatorRune reports whether r is `:` or whitespace, which read as
// part of the text around a path rather than part of a name.
func isSeparatorRune(r rune) bool {
	return r == ':' || unicode.IsSpace(r)
}

// Path is a location in a YAML document, given as a sequence of selectors
// that apply from where the path starts: `$` or `@`.
//
// The two starting points follow RFC 9535 JSONPath. A path that starts at
// `$` reads from the document root, wherever it resolves. A path that
// starts at `@` reads from the current node: the node that resolves the
// path, binds an error that carries it, or rebases it. [Doc] and
// [Current] start a path at each:
//
//	paths.Doc().Child("spec", "replicas")  // $.spec.replicas
//	paths.Current().Child("replicas")      // @.replicas
//
// A check written for a type reports its paths from `@`, the value it
// checked, and the [go.jacobcolvin.com/niceyaml.Node] of that value puts
// them under its own path when it binds them. A path the library hands
// out, such as [go.jacobcolvin.com/niceyaml.Node.Path] or the path of a
// bound error, starts at `$`, so it names the same node from any Node of
// the document. A resolve that has no current node, such as
// [Path.Node] on a document, reads an `@` path from the document root.
//
// A Path is a value and never changes. Each selector method returns a new
// Path and leaves the receiver as it was, so a Path is safe to share as a
// common prefix:
//
//	spec := paths.Doc().Child("spec")
//	replicas := spec.Child("replicas") // $.spec.replicas
//	image := spec.Child("image")       // $.spec.image
//
// The zero value is the current node, the same as [Current]. A path
// selects a node, which for a mapping entry is its value. [Path.Key]
// appends the `~` selector, which picks the key of that entry instead, so
// one Path names either node of an entry and every method that takes a
// Path, such as [Path.Token] or [go.jacobcolvin.com/niceyaml.AtPath],
// reads the key or the value from the Path alone.
//
// [Path.String] returns the path as a path expression, `$` or `@` and the
// selectors after it, and [Parse] reads it back as an equal Path.
// [Path.Equal] compares two paths, since the `==` operator does not
// compile for a Path. A Path writes itself as that expression through
// [Path.MarshalText] and reads it through [Path.UnmarshalText], so it
// encodes as a string in JSON and YAML. [Path.Selectors] yields the
// selectors themselves, so a caller reads each name and index without
// parsing that expression.
//
// Create instances with [Doc], [Current], [Parse], or [MustParse].
type Path struct {
	segments []segment
	// The path starts at `$`, the document root, rather than at `@`, the
	// current node.
	absolute bool
}

// Doc creates a new [Path] at the document root, `$`, with no selectors.
func Doc() Path {
	return Path{absolute: true}
}

// Current creates a new [Path] at the current node, `@`, with no
// selectors. It is the zero Path.
func Current() Path {
	return Path{}
}

// extend returns a copy of p with segs appended to its selectors. The copy
// owns its selectors, so extending the same prefix twice yields two
// independent paths. The copy starts where p does. With no segs, extend
// returns p itself, so extending by nothing leaves a path equal to the
// receiver.
func (p Path) extend(segs ...segment) Path {
	if len(segs) == 0 {
		return p
	}

	merged := make([]segment, 0, len(p.segments)+len(segs))
	merged = append(merged, p.segments...)
	merged = append(merged, segs...)

	return Path{segments: merged, absolute: p.absolute}
}

// Child returns a copy of the path with a `.name` selector appended for
// each name.
func (p Path) Child(name ...string) Path {
	segs := make([]segment, 0, len(name))
	for _, n := range name {
		segs = append(segs, segment{kind: segmentChild, name: n})
	}

	return p.extend(segs...)
}

// ChildAll returns a copy of the path with a `.*` wildcard selector
// appended, which selects the value of every entry of a mapping. It
// selects the entries that a `.name` selector resolves in that mapping,
// one for each name, so it sees the entries a `<<` merge key brings in and
// leaves out the merge key itself:
//
//	paths.Doc().Child("jobs").ChildAll()                 // $.jobs.*
//	paths.Doc().Child("jobs").ChildAll().Child("steps")  // $.jobs.*.steps
//	paths.Doc().Child("jobs").ChildAll().Key()           // $.jobs.*~
//
// The selector for the one key named `*` is Child("*"), which prints as
// `.'*'`.
func (p Path) ChildAll() Path {
	return p.extend(segment{kind: segmentChildAll})
}

// Index returns a copy of the path with an `[idx]` selector appended for
// each index. An index below zero selects element 0, so Index(-1) is the
// same path as Index(0).
func (p Path) Index(idx ...int) Path {
	segs := make([]segment, 0, len(idx))
	for _, i := range idx {
		segs = append(segs, segment{kind: segmentIndex, index: max(i, 0)})
	}

	return p.extend(segs...)
}

// IndexAll returns a copy of the path with a `[*]` wildcard selector
// appended.
func (p Path) IndexAll() Path {
	return p.extend(segment{kind: segmentIndexAll})
}

// Key returns a copy of the path with a `~` selector appended, which picks
// the key of the mapping entry the selector before it picked rather than
// its value. The selector looks through the `?` indicator of an explicit
// key. It keeps a tag on the key, as a path to a value keeps a tag on the
// value, so the key decodes as it does when the decoder reads the
// mapping, and [Path.Token] still gives the key text. Where the path
// selects no entry, such as a sequence element or the root, the `~`
// selects the node the path already does, so an error at such a path
// highlights the same text with or without it.
//
//	name := paths.Doc().Child("metadata", "name")
//	value, err := name.Token(doc)       // the token that starts the value
//	key, err := name.Key().Token(doc)   // the key token "name"
func (p Path) Key() Path {
	return p.extend(segment{kind: segmentKey})
}

// Recursive returns a copy of the path with a `..selector` recursive descent
// selector appended.
func (p Path) Recursive(selector string) Path {
	return p.extend(segment{kind: segmentRecursive, name: selector})
}

// RecursiveAll returns a copy of the path with a `..*` recursive wildcard
// selector appended, which selects every node below the node the path
// before it selects: the value of each mapping entry and each element of
// a sequence, at any depth, in document order. It follows the rules
// [Path.Nodes] gives for a `..name` selector, so it lists each node once,
// where the source writes it, and does not follow aliases. It leaves out
// the entry of a `<<` merge key, as `.*` does, and the sources that key
// lists, but walks a mapping written inline there:
//
//	paths.Doc().RecursiveAll()               // $..*
//	paths.Doc().Child("spec").RecursiveAll() // $.spec..*
//	paths.Doc().RecursiveAll().Key()         // $..*~
//
// The selector for every entry with the key `*` is Recursive("*"), which
// prints as `..'*'`.
func (p Path) RecursiveAll() Path {
	return p.extend(segment{kind: segmentRecursiveAll})
}

// Join returns a copy of the path with the selectors of each of qs
// appended in order, so a path written from one node of a document
// resolves from the root:
//
//	hours := paths.Doc().Child("spec", "hours")
//	open := paths.Current().Child("open")
//	hours.Join(open) // $.spec.hours.open
//
// An `@` path in qs reads from the node the path before it selects, so
// Join appends its selectors, and the result starts where the receiver
// does. A `$`
// path reads from the document root wherever it appears, so it replaces
// the path before it, and Join appends the paths after it:
//
//	hours.Join(paths.Doc().Child("name")) // $.name
//
// Joining [Current], or nothing, changes nothing, and joining an `@` path
// to Current yields that path. Join copies each selector once, so joining
// many short paths in one call takes time linear in their total length.
func (p Path) Join(qs ...Path) Path {
	start, rest := p, qs

	for i, q := range slices.Backward(qs) {
		if q.absolute {
			start, rest = q, qs[i+1:]

			break
		}
	}

	n := len(start.segments)
	for _, q := range rest {
		n += len(q.segments)
	}

	if n == len(start.segments) {
		return start
	}

	merged := make([]segment, 0, n)
	merged = append(merged, start.segments...)

	for _, q := range rest {
		merged = append(merged, q.segments...)
	}

	return Path{segments: merged, absolute: start.absolute}
}

// CutPrefix returns the path without the leading selectors of prefix and
// reports whether the path starts with prefix, as [strings.CutPrefix] does
// for a string. It undoes [Path.Join], so a path that reads from the root
// of a document reads from one node of it:
//
//	hours := paths.Doc().Child("spec", "hours")
//	open := paths.Doc().Child("spec", "hours", "open")
//	open.CutPrefix(hours) // @.open, true
//
// The rest reads from the node prefix selects, so it is an `@` path
// wherever the receiver starts, and a path cut by itself is [Current]. A
// path starts with prefix when both start at the same point and the
// selectors of prefix lead its own, so a `$` path never starts with an
// `@` path, nor an `@` path with a `$` path. A path that does not start
// with prefix comes back as it is, with false. Two selectors match when
// they are equal, so `[*]` matches `[*]` and not the index of an element
// it selects, and `.*` matches `.*` and not the name of an entry it
// selects.
func (p Path) CutPrefix(prefix Path) (Path, bool) {
	n := len(prefix.segments)
	if p.absolute != prefix.absolute || n > len(p.segments) || !slices.Equal(p.segments[:n], prefix.segments) {
		return p, false
	}

	if n == len(p.segments) {
		return Current(), true
	}

	return Path{segments: slices.Clone(p.segments[n:])}, true
}

// Parent returns the path without its last selector and true, or the path
// as it is and false for a path with no selector to drop:
//
//	name := paths.Doc().Child("items").Index(0).Child("name")
//	name.Parent() // $.items[0], true
//
// The parent starts where the path does. Parent drops one selector of
// any kind. The parent of a path that ends in `~` is the path to the value
// of the same entry, and the parent of a path that ends in `.*`, `[*]`,
// `..name`, or `..*` is the path to the node that selector reads.
func (p Path) Parent() (Path, bool) {
	n := len(p.segments)

	switch n {
	case 0:
		return p, false
	case 1:
		return Path{absolute: p.absolute}, true
	}

	return Path{segments: slices.Clone(p.segments[:n-1]), absolute: p.absolute}, true
}

// IsRoot reports whether the path names the document root: `$` with no
// selectors, as [Doc] creates. A path of `@` with no selectors names the
// current node, which is the root only for a resolve that has no current
// node, so IsRoot reports false for it. [Path.Len] reports whether any
// path holds selectors.
func (p Path) IsRoot() bool {
	return p.absolute && len(p.segments) == 0
}

// IsAbsolute reports whether the path starts at `$`, the document root,
// rather than at `@`, the current node.
func (p Path) IsAbsolute() bool {
	return p.absolute
}

// Equal reports whether the path starts at the same point as q and holds
// the same selectors, in the same order. A Path holds a slice, so the `==`
// operator does not compile for it, and Equal compares two paths in its
// place.
//
// Two selectors are equal when they have the same kind and the same name
// or index. A wildcard therefore equals the same wildcard and no selector
// it stands for, so `$.items[*]` differs from `$.items[0]`, and `$.jobs.*`
// from `$.jobs.build`. Equal compares the paths and not the nodes they
// select, so `$.name` differs from `@.name`, and two paths that reach one
// node through an alias differ. Paths that print the same [Path.String]
// are equal, and that string keys a map of paths.
func (p Path) Equal(q Path) bool {
	return p.absolute == q.absolute && slices.Equal(p.segments, q.segments)
}

// String returns the path expression, such as "$.metadata.name" or
// "@.name", which [Parse] reads back. It starts with `$` or `@`. A name
// that holds a reserved character, `:`, or whitespace comes back in
// single quotes, as in "$.'x: y'".
func (p Path) String() string {
	var sb strings.Builder

	if p.absolute {
		sb.WriteByte('$')
	} else {
		sb.WriteByte('@')
	}

	for _, seg := range p.segments {
		sb.WriteString(seg.String())
	}

	return sb.String()
}

// MarshalText implements [encoding.TextMarshaler]. The text is the path
// expression [Path.String] returns, so a Path in a report writes as a
// string, such as "$.items[0].name" in JSON.
func (p Path) MarshalText() ([]byte, error) {
	return []byte(p.String()), nil
}

// UnmarshalText implements [encoding.TextUnmarshaler]. It reads a path
// expression as [Parse] does, so a Path decodes from a string of JSON or
// YAML, and from the text [Path.MarshalText] writes.
//
// Returns the error of Parse for a malformed expression, which wraps
// [ErrInvalidPath], and leaves the path as it was.
func (p *Path) UnmarshalText(text []byte) error {
	parsed, err := Parse(string(text))
	if err != nil {
		return err
	}

	*p = parsed

	return nil
}

// YAMLPath returns the equivalent [*yaml.Path] for use with the goccy/go-yaml
// API, such as [yaml.Path.FilterNode] on a tree the caller parsed. Every Node
// of a Source shares the tree [go.jacobcolvin.com/niceyaml.Source.File]
// returns, and [go.jacobcolvin.com/niceyaml.Node.AST] and
// [go.jacobcolvin.com/niceyaml.Node.DocumentAST] return parts of that tree,
// so a call that edits a tree, such as [yaml.Path.ReplaceWithNode], runs on
// a tree of the caller's own.
//
// The goccy/go-yaml syntax has no `@`, and a goccy/go-yaml path always
// reads from the node a call such as FilterNode starts at. YAMLPath
// writes `$` for both `$` and `@`, so the result of an `@` path applies
// its selectors from that node, and so does the result of a `$` path. A
// caller with an `@` path passes FilterNode the node the path reads from.
//
// The result holds each child name as its raw text. [yaml.Path.FilterNode]
// and [yaml.Path.ReplaceWithNode] compare that name with the text of the
// first token of each key, where [Path.Node] compares it with the name
// [Resolver.KeyName] gives. A key with an anchor, a tag, an alias, an
// explicit `?`, or a block scalar header matches the text of that
// indicator, such as `&`, `!!str`, `*`, `?`, or `|-`, rather than its name.
// Neither function sees the entries a `<<` merge key brings in. Among
// duplicate keys, FilterNode selects the first and ReplaceWithNode
// replaces them all, where [Path.Node] selects the last.
//
// FilterNode also strips single quotes from around a name and quotes from
// around the text of a key, so Child("'id'") may select the key id. A path
// has no goccy/go-yaml string form when it holds an empty name, a name that
// is not valid UTF-8, or a recursive name with `.`, `[`, `]`, `$`, or `*`.
// YAMLPath builds such a path with [yaml.PathBuilder], whose
// ReplaceWithNode skips any child name that holds `.` or `*`.
//
// The String of the result is the goccy/go-yaml form, which differs from
// [Path.String] for names with reserved characters, and [yaml.PathString]
// does not always read it back. The goccy/go-yaml syntax has no quoting for recursive
// selectors, so Recursive("a.b") prints as `$..a.b`, which
// [yaml.PathString] reads as two selectors. Use [Path.String] for a form
// that [Parse] reads back.
//
// The goccy/go-yaml syntax has no selector for the key of an entry, so
// YAMLPath leaves out the `~` selector from [Path.Key] and the selectors
// after it apply to the value. The path $.a~.b becomes $.a.b, which can
// select a node where [Path.Node] finds nothing.
//
// The goccy/go-yaml syntax has no selector for every entry of a mapping
// either, and its `[*]` lists the elements of a sequence alone. A path
// without the `.*` selector from [Path.ChildAll] would name the mapping
// in place of its entries, so YAMLPath returns an error wrapping
// [ErrNoYAMLPath] for a path that holds one. Its `..` takes a name alone,
// so YAMLPath returns that error for the `..*` selector from
// [Path.RecursiveAll] too. A caller resolves such a path with
// [Path.Matches] and converts the path of each match.
func (p Path) YAMLPath() (*yaml.Path, error) {
	// Only a path that yaml.PathString reads holds a quoted name as its raw
	// text. The builder holds a name with `.` or `*` still quoted, and the
	// goccy replace compares that quoted text with each key, so it matches
	// none. A path from PathString takes no further builder selectors, so a
	// path without a string form comes from the builder whole.
	if s, ok := p.goccyString(); ok {
		yp, err := yaml.PathString(s)
		if err == nil {
			return yp, nil
		}
	}

	pb := (&yaml.PathBuilder{}).Root()

	for _, seg := range p.segments {
		switch seg.kind {
		case segmentChild:
			pb = pb.Child(builderName(seg.name))
		case segmentIndex:
			pb = pb.Index(uint(seg.index)) //nolint:gosec // Index and Parse never store a negative.
		case segmentIndexAll:
			pb = pb.IndexAll()
		case segmentRecursive:
			pb = pb.Recursive(seg.name)
		case segmentKey:
			// No goccy selector names a key.
		case segmentChildAll, segmentRecursiveAll:
			return nil, fmt.Errorf("convert %s: %w", p, ErrNoYAMLPath)
		}
	}

	return pb.Build(), nil
}

// builderName returns the text to pass [yaml.PathBuilder.Child] so that
// [yaml.Path.FilterNode] compares keys with name. The builder puts single
// quotes around a name with `.` or `*` and escapes the quotes inside it,
// and FilterNode strips the outer quotes but keeps the escapes, so a'.b
// would match no key. The builder keeps a name that starts and ends with a
// single quote as it is, so builderName wraps the raw name itself.
func builderName(name string) string {
	enclosed := strings.HasPrefix(name, "'") && strings.HasSuffix(name, "'")
	if enclosed || !strings.ContainsAny(name, ".*") {
		return name
	}

	return "'" + name + "'"
}

// goccyString returns the path in the syntax [yaml.PathString] reads, with
// every child name quoted so that PathString holds it as its raw text. It
// reports false for a path that syntax cannot hold. PathString rejects an
// empty name, has no quoting for a recursive name, has no selector for
// every entry of a mapping or every node below one, and reads a name that
// is not valid UTF-8 as a different name.
func (p Path) goccyString() (string, bool) {
	var sb strings.Builder

	sb.WriteByte('$')

	for _, seg := range p.segments {
		if !utf8.ValidString(seg.name) {
			return "", false
		}

		switch seg.kind {
		case segmentChild:
			if seg.name == "" {
				return "", false
			}

			sb.WriteString(".'" + nameEscaper.Replace(seg.name) + "'")

		case segmentIndex:
			sb.WriteString("[" + strconv.Itoa(seg.index) + "]")
		case segmentIndexAll:
			sb.WriteString("[*]")
		case segmentRecursive:
			if seg.name == "" || strings.ContainsAny(seg.name, ".[]$*") {
				return "", false
			}

			sb.WriteString(".." + seg.name)

		case segmentKey:
			// No goccy selector names a key.
		case segmentChildAll, segmentRecursiveAll:
			return "", false
		}
	}

	return sb.String(), true
}

// wildcard reports whether any selector can match more than one node.
func (p Path) wildcard() bool {
	for _, seg := range p.segments {
		switch seg.kind {
		case segmentChildAll, segmentIndexAll, segmentRecursive, segmentRecursiveAll:
			return true
		case segmentChild, segmentIndex, segmentKey:
		}
	}

	return false
}

// matches resolves the path in doc with r, a resolver for doc, and
// returns every match.
//
// A document below a "---" header that holds no content is the null
// document. Its body is nil, or it holds only comments or directives, as a
// parse that keeps comments leaves in a comment-only document. A path with
// no selectors matches that null at the header, so an error about the
// document as a whole points at its header line. Every deeper path has no
// node to reach.
//
// Returns an error wrapping [ErrNotFound] and [ErrNoDocument] when doc is
// nil, when a document without a header holds no content, or when a path
// with segments meets a document without content. A file of whitespace
// alone parses to a document without a header whose body is a
// placeholder scalar, and [astnode.HasContent] finds no content in it.
func (p Path) matches(r *resolver, doc *ast.DocumentNode) ([]match, error) {
	if doc != nil && doc.Start != nil && !astnode.HasContent(doc.Body) && p.selectsRoot() {
		return []match{{node: ast.Null(doc.Start), segs: slices.Clone(p.segments)}}, nil
	}

	if doc == nil || !astnode.HasContent(doc.Body) {
		return nil, fmt.Errorf("resolve %s: %w: %w", p, ErrNotFound, ErrNoDocument)
	}

	found, err := r.resolve(doc.Body, p.segments)
	if err != nil {
		return nil, fmt.Errorf("resolve %s: %w", p, err)
	}

	return found, nil
}

// selectsRoot reports whether the path names the root node. A path names
// the root when it has no segments, or only the `~` segments of
// [Path.Key], which on the root select the node the path already does.
func (p Path) selectsRoot() bool {
	for _, s := range p.segments {
		if s.kind != segmentKey {
			return false
		}
	}

	return true
}

// single resolves the path in doc with r, a resolver for doc, to exactly
// one match.
//
// Returns [ErrWildcard] for a path with a `.*`, `[*]`, or `..` selector and
// wraps [ErrNotFound] when nothing exists at the path.
func (p Path) single(r *resolver, doc *ast.DocumentNode) (match, error) {
	if p.wildcard() {
		return match{}, fmt.Errorf("resolve %s: %w", p, ErrWildcard)
	}

	found, err := p.matches(r, doc)
	if err != nil {
		return match{}, err
	}

	if len(found) == 0 {
		return match{}, fmt.Errorf("resolve %s: %w", p, ErrNotFound)
	}

	return found[0], nil
}

// singleFrom resolves the path from node with r, a resolver for the
// document node belongs to, to exactly one match, as [Path.single]
// resolves it from the root of a document.
//
// Returns [ErrWildcard] for a path with a `.*`, `[*]`, or `..` selector and
// wraps [ErrNotFound] when nothing exists at the path.
func (p Path) singleFrom(r *resolver, node ast.Node) (match, error) {
	if p.wildcard() {
		return match{}, fmt.Errorf("resolve %s: %w", p, ErrWildcard)
	}

	found, err := r.resolve(node, p.segments)
	if err != nil {
		return match{}, fmt.Errorf("resolve %s: %w", p, err)
	}

	if len(found) == 0 {
		return match{}, fmt.Errorf("resolve %s: %w", p, ErrNotFound)
	}

	return found[0], nil
}

// Nodes resolves every node the path selects in doc, in document order,
// from the document root, whether the path starts at `$` or `@`, as
// [Path.Node] does. A path without `.*`, `[*]`, or `..` selectors yields
// at most one node, and an empty result means nothing exists at the path.
// Nodes lists one node for each path that selects it, as [Path.Matches]
// does. A node that several aliases or `<<` merge keys lead to appears
// once for each, in the place of that alias or merge key. A `..name` or
// `..*` selector lists each node once, even when chained `..` selectors
// reach it more than once.
//
// It looks through anchors and aliases, so each node is the content the
// path names, and stops at a tag, as [Path.Node] does. The `.name`, `.*`,
// `[n]`, and `[*]` selectors follow aliases to their anchor and see the
// entries a `<<` merge key brings into a mapping.
//
// The `.*` selector lists the value of each entry that a `.name` selector
// resolves in a mapping, once for each name. It lists an entry a `<<`
// merge key brings in at the place of that merge key, and the entries of
// one merge key in the order its sources first name them. Among entries
// that share a key it lists the one a path through that key selects, so a
// later entry or a later merge wins. It leaves out a merge key, whose
// value is a source of entries, and an entry whose key has no name, as
// [Resolver.KeyName] reports it. On a node that is not a mapping it
// selects nothing, as `[*]` does on a node that is not a sequence.
//
// The `..name` selector looks through
// an alias or tag on the node it starts from, as the other selectors do.
// Below that node it visits each entry once, where the source defines it.
// It does not follow aliases there, including one a `<<` merge key names,
// and it does not list the entries a merge key brings into a mapping under
// that mapping. It walks a mapping written inline under a `<<` key as it
// walks any other value, and lists its entries under the `<<` selector even
// when a later source or a key of the mapping itself overrides them. When a
// path through `<<` selects a real key with the text `<<`, whether the
// mapping holds it or a merge brings it in, the `..name` selector skips the
// merge key and its inline mapping, since no path through `<<` reaches
// them. It skips an entry that a later entry with the same key shadows,
// whether that entry belongs to its mapping or comes from a later `<<`
// merge key. It also skips an entry whose key has no name, as
// [Resolver.KeyName] reports it, and everything below that entry, since no
// path names them. An alias key with no anchor before it has no name, and
// so does one whose anchor holds a collection.
//
// The `..*` selector visits what the `..name` selector visits. It lists
// the value of every entry it visits, whatever its key, and every element
// of a sequence it visits, each before the nodes below it. It leaves out
// the entry of a `<<` merge key, as `.*` does, and each element of a
// sequence that lists the sources of one, but lists the entries of a
// mapping written inline there.
//
// Wraps [ErrNoDocument], together with [ErrNotFound], when the document has
// no content to resolve in, and [ErrAlias] when an alias on the path does
// not resolve, including one under a tag. A `.*` selector reads every `<<`
// merge key of its mapping and of the mappings it merges, so an alias one
// of them names counts as on the path. A `..*` selector lists every value
// below the node it starts from, so an alias it lists counts as on the
// path too. Wraps [ErrExcessiveAliasing] when
// aliases lead a selector to far more nodes than the document holds, and
// [ErrExcessiveMerging] when the key lookups of a selector read far more
// nodes under `<<` merge keys than that. [Path.Matches] returns the same
// nodes with the path that selects each one alone.
func (p Path) Nodes(doc *ast.DocumentNode) ([]ast.Node, error) {
	found, err := p.Matches(doc)
	if err != nil {
		return nil, err
	}

	nodes := make([]ast.Node, 0, len(found))
	for _, m := range found {
		nodes = append(nodes, m.Node)
	}

	return nodes, nil
}

// Match is one node a [Path] selects in a document, together with the path
// that selects that node alone. That path is the path as given, with each
// `[*]` selector replaced by the index of the element and each `.*` selector
// by the name of the entry, as [Resolver.KeyName] gives it. Each `..name`
// or `..*` selector gives way to the selectors from the node it applied to
// down to the node it found, so the path names the node wherever it lies.
// The path starts at `$`, since the node lies in the document, so a match
// of an `@` path holds the `$` path with its selectors.
//
// Receive instances from [Path.Matches].
type Match struct {
	Node ast.Node
	Path Path
}

// Matches resolves every node the path selects in doc, as [Path.Nodes]
// does, and returns each with the path that selects it alone. A caller
// that checks each element of a sequence, each entry of a mapping, or
// each node a `..` selector finds thus reports the one it checked:
//
//	for _, m := range matches {
//		fmt.Println(m.Path) // $.items[0], $.items[1], ...
//	}
//
// A path without `.*`, `[*]`, or `..` selectors yields at most one match,
// whose path is the path as given, at `$`. The path of a node reached
// through an alias is the path as written, not the location of the
// anchor, and the path of an entry a `<<` merge key brings in is the path
// of the mapping that merges it. Returns the errors [Path.Nodes] returns.
func (p Path) Matches(doc *ast.DocumentNode) ([]Match, error) {
	return NewResolver(doc).Matches(p)
}

// Node resolves the node at the path in doc.
//
// The document root is the node a `$` path reads from. A resolve in a
// document has no other node to start at, so an `@` path reads from the
// document root too, and selects the node the `$` path with its
// selectors selects.
//
// It looks through anchors and aliases, so the result is the content the
// path names. It stops at a tag, which decides how that content decodes,
// and keeps any anchor or alias under the tag. The `.name` and `[n]`
// selectors follow aliases to their anchor and see the entries a `<<`
// merge key brings into a mapping.
//
// Returns [ErrWildcard] for a path with a `.*`, `[*]`, or `..` selector,
// which needs [Path.Nodes]. Wraps [ErrNotFound] when nothing exists at the
// path, together with [ErrNoDocument] when the document has no content to
// resolve in. Wraps [ErrAlias] when an alias on the path does not resolve,
// including one under a tag, and [ErrExcessiveMerging] when the key lookups
// of a selector read far more nodes under `<<` merge keys than the document
// holds.
func (p Path) Node(doc *ast.DocumentNode) (ast.Node, error) {
	return NewResolver(doc).Node(p)
}

// Token resolves the [*token.Token] that starts the node the path selects
// in doc: a scalar's own token, the first key of a mapping, or the first
// element of a sequence. For a mapping entry that is the token of its
// value, and for a path ending in the `~` selector from [Path.Key] it is
// the token of the key. An alias resolves to its own token rather than the
// anchor's content, since that is where the path points in the source.
//
// The path resolves against the document body only, from its root whether
// the path starts at `$` or `@`, so the same path resolves to different
// tokens in different documents of one file. Token returns the same
// errors as [Path.Node], except that it does not look through the node
// the last selector reaches. An alias there that does not resolve, such
// as one that names no anchor or one inside the content of its own
// anchor, yields the alias's own token rather than [ErrAlias].
// Token still returns [ErrAlias] for an alias an earlier selector
// resolves through.
func (p Path) Token(doc *ast.DocumentNode) (*token.Token, error) {
	return NewResolver(doc).Token(p)
}

// tokenOf returns the token that starts node. A tree built by hand may hold
// a typed nil where the parser always puts a node, and such a node has no
// token to point at, which is [ErrNotFound].
func (p Path) tokenOf(node ast.Node) (*token.Token, error) {
	tk := astnode.FirstToken(node)
	if tk == nil {
		return nil, fmt.Errorf("resolve %s: %w: node has no token", p, ErrNotFound)
	}

	return tk, nil
}
