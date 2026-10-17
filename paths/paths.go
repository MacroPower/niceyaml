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
	// ErrNoDocument indicates a document without content to resolve in.
	// Such a document is nil, lacks a body, holds only directives or
	// comments, or holds only whitespace. Nothing exists at any path in
	// such a document, so the errors that wrap ErrNoDocument also wrap
	// [ErrNotFound]. The root path of such a document under a "---" header
	// still resolves, to the null at the header. [Resolver.Node] and
	// [Resolver.Token] return ErrNoDocument for each of these documents.
	// [Resolver.Nodes] and [Resolver.Matches] return it only for a nil
	// document, and do not select anything in the others.
	ErrNoDocument = errors.New("document has no content")

	// ErrNotFound indicates that nothing exists at the path in the document.
	ErrNotFound = errors.New("not found")

	// ErrAlias indicates an alias on the path that does not name an anchor
	// or that leads back to itself. Such an alias does not resolve to any
	// content.
	ErrAlias = errors.New("alias does not resolve")

	// ErrWildcard indicates a request for a single node or token from a path
	// with a `.*`, `[*]`, or `..` selector. Use [Resolver.Nodes] for such paths.
	ErrWildcard = errors.New("wildcard path matches any number of nodes")

	// ErrNoYAMLPath indicates a path with a `.*` or `..*` selector. The
	// goccy/go-yaml path syntax does not have a selector for either.
	// [Path.YAMLPath] returns it.
	ErrNoYAMLPath = errors.New("selector has no goccy/go-yaml equivalent")

	// ErrExcessiveAliasing indicates a path whose selectors reach far more
	// nodes than the document holds. A `[*]` selector lists the elements
	// of a sequence once for each alias that leads to it, and a `.*`
	// selector lists the entries of a mapping the same way. A few hundred
	// bytes of nested aliases can thus make a path select millions of
	// nodes. The limit lets a selector reach as many nodes as the document
	// holds. Past that count, the extra nodes can make up only the share of
	// all the nodes the selector reaches that gopkg.in/yaml.v3 allows the
	// aliases in a document it decodes. It is the same error value as
	// [go.jacobcolvin.com/niceyaml.ErrExcessiveAliasing].
	ErrExcessiveAliasing = aliaslimit.ErrExcessiveAliasing

	// ErrExcessiveMerging indicates a path whose key lookups read far more
	// nodes under `<<` merge keys than the document holds. A key lookup
	// reads the value of each merge key it reaches, and each element of
	// that value when it is a sequence of sources. It continues into the
	// mappings those merge keys bring in until it finds the key. A lookup
	// on a chain of merges can thus read the whole chain. A lookup that
	// reaches many mappings that merge one sequence through an alias reads
	// that sequence once for each of them.
	//
	// Some selectors look up many keys. A `..name` selector looks up each
	// key that a later merge key might override. It also looks up `<<` at
	// the last merge key of each mapping it walks, to learn whether a merge
	// brings in a real `<<` key. That lookup reads the whole merge chain
	// behind the mapping, whatever name the selector searches for. A
	// `.name` after a `[*]` looks up its key in each element, and a `.*`
	// looks up each key of its mapping and of the mappings it merges. Each
	// of these selectors can read one chain many times.
	//
	// A selector can read 64 times as many of these nodes as the document
	// holds, or 524,288 of them, whichever is more. A call of
	// [Resolver.Entry] can read as many as one selector, and the calls of
	// one [EntryFinder] can read that many between them.
	ErrExcessiveMerging = errors.New("excessive merging")

	// The nameEscaper replacer escapes the backslashes and single quotes of
	// a selector name before goccyString wraps the name in single quotes.
	// A [strings.Replacer] is safe for concurrent use, so every call of
	// goccyString shares this one.
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

// lineSeparator is the Unicode line separator, U+2028.
const lineSeparator = '\u2028'

// paragraphSeparator is the Unicode paragraph separator, U+2029.
const paragraphSeparator = '\u2029'

// quoteName returns name in the form [Parse] accepts as a child or
// recursive selector. It wraps the name in single quotes when the name is
// empty or contains reserved characters. An unquoted empty name leaves a
// bare `.`, which Parse rejects. The function also quotes a name that
// holds `:` or whitespace, though Parse reads such a name unquoted too.
// The quotes keep the name apart from the ": " that follows a path in an
// error message. They also keep a space at either end of the name visible.
//
// A name that holds a control character also comes back in quotes, with
// the character written as an escape that Parse reads back. The escape is
// `\n` for a line feed, `\r` for a carriage return, `\t` for a tab, and
// `\u` with four hexadecimal digits for any other control character. The
// Unicode line and paragraph separators, U+2028 and U+2029, are not
// control characters, but they also come back as `\u` escapes. A path
// thus stays on one line and does not send a control character to a
// terminal, whatever key it names.
func quoteName(name string) string {
	if name != "" && !strings.ContainsAny(name, reservedNameChars) &&
		!strings.ContainsFunc(name, isSeparatorRune) {
		return name
	}

	var sb strings.Builder

	sb.Grow(len(name) + len("''"))
	sb.WriteByte('\'')

	for i := 0; i < len(name); {
		r, size := utf8.DecodeRuneInString(name[i:])

		switch {
		case r == '\\' || r == '\'':
			sb.WriteByte('\\')
			sb.WriteRune(r)

		case r == '\n':
			sb.WriteString(`\n`)
		case r == '\r':
			sb.WriteString(`\r`)
		case r == '\t':
			sb.WriteString(`\t`)
		case unicode.IsControl(r) || r == lineSeparator || r == paragraphSeparator:
			fmt.Fprintf(&sb, `\u%04x`, r)
		default:
			// The bytes go out as they are, so a name that is no valid
			// UTF-8 keeps its bytes.
			sb.WriteString(name[i : i+size])
		}

		i += size
	}

	sb.WriteByte('\'')

	return sb.String()
}

// isSeparatorRune reports whether r is `:`, whitespace, or a control
// character. Colons and whitespace read as part of the text around a path
// rather than as part of a name. Control characters change how a terminal
// shows that text.
func isSeparatorRune(r rune) bool {
	return r == ':' || unicode.IsSpace(r) || unicode.IsControl(r)
}

// Path is a location in a YAML document, given as a sequence of selectors
// that apply from where the path starts: `$` or `@`.
//
// The two starting points follow RFC 9535 JSONPath. A path that starts at
// `$` reads from the document root, wherever it resolves. A path that
// starts at `@` reads from the current node: the node that resolves the
// path, binds an error that carries it, or rebases it. [Doc] starts a
// path at `$`, and [Current] starts one at `@`:
//
//	paths.Doc().Child("spec", "replicas")  // $.spec.replicas
//	paths.Current().Child("replicas")      // @.replicas
//
// A check written for a type reports its paths from `@`, the value it
// checked. The [go.jacobcolvin.com/niceyaml.Node] of that value puts
// those paths under its own path when it binds them. Every path that the
// library returns, such as [go.jacobcolvin.com/niceyaml.Node.Path] or the
// path of a bound error, starts at `$`. Such a path names the same node
// from any Node of the document. A resolve without a current node, such as
// [Resolver.Node], reads an `@` path from the document root.
//
// A Path is a value and never changes. Each selector method returns a new
// Path and leaves the receiver as it was, so a Path is safe to share as a
// common prefix:
//
//	spec := paths.Doc().Child("spec")
//	replicas := spec.Child("replicas") // $.spec.replicas
//	image := spec.Child("image")       // $.spec.image
//
// The zero value is `@` with no selectors, the same as [Current]. A path
// selects a node, and for a mapping entry that node is the value.
// [Path.Key] appends the `~` selector, which picks the key of the entry
// instead. One Path thus names either node of an entry. [Resolver.Token],
// [go.jacobcolvin.com/niceyaml.AtPath], and every other function that
// takes a Path read the key or the value from the Path alone.
//
// [Path.String] returns the path as a path expression, `$` or `@` and the
// selectors after it, and [Parse] reads it back as an equal Path. The
// `==` operator does not compile for a Path, so [Path.Equal] compares two
// paths. A Path writes itself as that expression through
// [Path.MarshalText] and reads it through [Path.UnmarshalText], so it
// encodes as a string in JSON and YAML. [Path.Selectors] yields the
// selectors themselves, so a caller reads each name and index without
// parsing that expression.
//
// Create instances with [Doc], [Current], [Parse], or [MustParse].
type Path struct {
	segments []segment
	// Whether the path starts at `$`, the document root, rather than at
	// `@`, the current node.
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
// returns p itself.
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
// appended. The `.*` selector selects the value of every entry of a
// mapping. It selects the entries that a `.name` selector resolves in that
// mapping, one for each name. It thus sees the entries a `<<` merge key
// brings in and leaves out the merge key itself:
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
// each index.
//
// Panics if an index is negative, because a sequence does not have an
// element at a negative index and [Parse] rejects the selector. A search
// such as [slices.Index] returns -1 when it does not find a match, so a
// caller should check that result before it builds the path.
func (p Path) Index(idx ...int) Path {
	segs := make([]segment, 0, len(idx))
	for _, i := range idx {
		if i < 0 {
			panic(fmt.Sprintf("paths.Path.Index: index %d is below 0", i))
		}

		segs = append(segs, segment{kind: segmentIndex, index: i})
	}

	return p.extend(segs...)
}

// IndexAll returns a copy of the path with a `[*]` wildcard selector
// appended.
func (p Path) IndexAll() Path {
	return p.extend(segment{kind: segmentIndexAll})
}

// Key returns a copy of the path with a `~` selector appended. The `~`
// selector picks the key of the mapping entry that the selector before it
// picked, rather than its value. It looks through the `?` indicator of an
// explicit key. It keeps a tag on the key, as a path to a value keeps a
// tag on the value. The key thus decodes as it does when the decoder
// reads the mapping, and [Resolver.Token] still gives the key text.
//
// Where the path does not select an entry, such as where it selects a
// sequence element or the root, the `~` selects the node the path already
// does. An error at such a path thus highlights the same text with or
// without the `~`. A path to a mapping or a sequence under a key already
// points at that key, as [Resolver.Token] describes. The `~` there changes
// the node the path selects but not the token it points at.
//
//	name := paths.Doc().Child("metadata", "name")
//	r := paths.NewResolver(doc)
//	value, err := r.Token(name)         // the token of the value
//	key, err := r.Token(name.Key())     // the key token "name"
func (p Path) Key() Path {
	return p.extend(segment{kind: segmentKey})
}

// Recursive returns a copy of the path with a `..selector` recursive descent
// selector appended.
func (p Path) Recursive(selector string) Path {
	return p.extend(segment{kind: segmentRecursive, name: selector})
}

// RecursiveAll returns a copy of the path with a `..*` recursive wildcard
// selector appended. The `..*` selector selects every node below the node
// that the path before it selects, at any depth and in document order.
// Those nodes are the value of each mapping entry and each element of a
// sequence. The selector follows the rules [Resolver.Nodes] gives for a
// `..name` selector. It thus lists each node once, where the source writes
// it, and does not follow aliases. It leaves out the entry of a `<<` merge
// key, as `.*` does, and the sources that key lists, but walks a mapping
// written inline there:
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
// appended in order. With Join, a path written from one node of a
// document resolves from the root:
//
//	hours := paths.Doc().Child("spec", "hours")
//	open := paths.Current().Child("open")
//	hours.Join(open) // $.spec.hours.open
//
// An `@` path in qs reads from the node that the path before it selects,
// so Join appends its selectors. When qs does not hold a `$` path, the
// result thus starts where the receiver does. A `$` path reads from the
// document root wherever it appears, so it replaces the path before it,
// and Join appends the paths after it:
//
//	hours.Join(paths.Doc().Child("name")) // $.name
//
// Join returns the receiver as it is when qs is empty or holds only
// [Current], and joining an `@` path to Current yields that path. Join
// copies each selector once, so joining many short paths in one call takes
// time in proportion to their total length.
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
// of a document becomes one that reads from a node of it:
//
//	hours := paths.Doc().Child("spec", "hours")
//	open := paths.Doc().Child("spec", "hours", "open")
//	open.CutPrefix(hours) // @.open, true
//
// The rest reads from the node that prefix selects. It is thus an `@`
// path wherever the receiver starts, and a path cut by itself is
// [Current]. A path starts with prefix when both start at the same point
// and the selectors of prefix lead its own. A `$` path thus never starts
// with an `@` path, and an `@` path never starts with a `$` path. A path
// that does not start with prefix comes back as it is, with false.
//
// Two selectors match when they are equal. A `[*]` thus matches `[*]` but
// not the index of an element it selects, and `.*` matches `.*` but not
// the name of an entry it selects.
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
// of the same entry. The parent of a path that ends in `.*`, `[*]`,
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
// current node. That node is the root only for a resolve without a
// current node, so IsRoot reports false for such a path. [Path.Len]
// counts the selectors of a path that starts at either point.
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
// or index. A wildcard therefore equals the same wildcard but does not
// equal a selector it stands for. The path `$.items[*]` thus differs from
// `$.items[0]`, and `$.jobs.*` differs from `$.jobs.build`. Equal
// compares the paths and not the nodes they select, so `$.name` differs
// from `@.name`, and two paths that reach one node through an alias
// differ. Paths that print the same [Path.String] are equal, so a caller
// can key a map of paths by that string.
func (p Path) Equal(q Path) bool {
	return p.absolute == q.absolute && slices.Equal(p.segments, q.segments)
}

// String returns the path expression, such as "$.metadata.name" or
// "@.name", which [Parse] reads back. It starts with `$` or `@`. A name
// that holds a reserved character, `:`, or whitespace comes back in
// single quotes, as in "$.'x: y'". A control character in a name comes
// back as an escape inside the quotes, as in `$.'a\nb'` for a line feed
// and `$.'a\u001bb'` for an escape. A Unicode line or paragraph separator
// also comes back as an escape. The expression thus stays on one line and
// does not send a control character to a terminal that prints it.
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
// Returns the error of Parse for a malformed expression and leaves the
// path as it was. That error wraps [ErrInvalidPath].
func (p *Path) UnmarshalText(text []byte) error {
	parsed, err := Parse(string(text))
	if err != nil {
		return err
	}

	*p = parsed

	return nil
}

// YAMLPath returns the equivalent [*yaml.Path] for use with the goccy/go-yaml
// API, such as [yaml.Path.FilterNode] on a tree the caller parsed. Every
// Node of a Source shares the tree that
// [go.jacobcolvin.com/niceyaml.Source.File] returns.
// [go.jacobcolvin.com/niceyaml.Node.AST] and
// [go.jacobcolvin.com/niceyaml.Node.DocumentAST] return parts of that tree.
// A caller should thus run a call that edits a tree, such as
// [yaml.Path.ReplaceWithNode], on a tree of its own.
//
// The goccy/go-yaml syntax lacks `@`, and a goccy/go-yaml path always
// reads from the node that a call such as FilterNode starts at. YAMLPath
// writes `$` for both `$` and `@`, so the result of either path applies
// its selectors from that node. A caller with an `@` path should pass
// FilterNode the node that the path reads from.
//
// The result holds each child name as its raw text. [yaml.Path.FilterNode]
// and [yaml.Path.ReplaceWithNode] compare that name with the text of the
// first token of each key, but [Resolver.Node] compares it with the name
// [Resolver.KeyName] gives. A key with an anchor, a tag, an alias, an
// explicit `?`, or a block scalar header matches the text of that
// indicator, such as `&`, `!!str`, `*`, `?`, or `|-`, rather than its name.
// FilterNode and ReplaceWithNode do not see the entries a `<<` merge key
// brings in. Among duplicate keys, FilterNode selects the first and
// ReplaceWithNode replaces all of them. [Resolver.Node] selects the last.
//
// FilterNode also strips single quotes from around a name and quotes from
// around the text of a key. The result for Child("'id'") thus selects the
// key id. A path does not have a goccy/go-yaml string form when it holds
// an empty name, a name that is not valid UTF-8, or a recursive name with
// `.`, `[`, `]`, `$`, or `*`. YAMLPath builds such a path with
// [yaml.PathBuilder], whose ReplaceWithNode skips any child name that
// holds `.` or `*`.
//
// The String of the result is the goccy/go-yaml form. That form differs
// from [Path.String] for names with reserved characters, and
// [yaml.PathString] does not always read it back. The goccy/go-yaml
// syntax does not quote recursive selectors, so Recursive("a.b") prints as
// `$..a.b`. [yaml.PathString] reads that text as two selectors. Use
// [Path.String] for a form that [Parse] reads back.
//
// The goccy/go-yaml syntax does not have a selector for the key of an
// entry. YAMLPath thus leaves out the `~` selector from [Path.Key], and
// the selectors after it apply to the value. The path $.a~.b becomes
// $.a.b, which can select a node where [Resolver.Node] does not find one.
//
// The goccy/go-yaml syntax also lacks a selector for every entry of a
// mapping, and its `[*]` lists only the elements of a sequence. Dropping
// the `.*` selector from [Path.ChildAll] leaves a path to the mapping
// rather than to its entries. YAMLPath thus returns an error wrapping
// [ErrNoYAMLPath] for a path that holds a `.*` selector. The goccy/go-yaml
// `..` takes only a name, so YAMLPath also returns that error for the
// `..*` selector from [Path.RecursiveAll]. A caller can resolve such a
// path with [Resolver.Matches] and convert the path of each match.
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
// quotes around a name with `.` or `*` and escapes the quotes inside it.
// FilterNode strips the outer quotes but keeps the escapes, so a path that
// the builder makes from the name a'.b does not match any key. The builder
// keeps a name that starts and ends with a single quote as it is, so
// builderName wraps the raw name itself.
func builderName(name string) string {
	enclosed := strings.HasPrefix(name, "'") && strings.HasSuffix(name, "'")
	if enclosed || !strings.ContainsAny(name, ".*") {
		return name
	}

	return "'" + name + "'"
}

// goccyString returns the path in the syntax that [yaml.PathString] reads,
// with every child name quoted so that PathString holds it as its raw
// text. It reports false for a path that syntax cannot hold. PathString
// rejects an empty name and reads a name that is not valid UTF-8 as a
// different name. The syntax does not quote a recursive name, and it lacks
// a selector for every entry of a mapping or for every node below one.
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
// A document under a "---" header is the null document when it does not
// hold content. Its body is nil, or the body holds only comments or
// directives. A parse that keeps comments leaves them in the body of a
// comment-only document. A path with no selectors, or with only `~`
// selectors, matches that null at the header. An error about the document
// as a whole thus points at its header line. No deeper path matches
// anything there.
//
// In a document without content and without a header, no path matches
// anything. A file of whitespace alone parses to such a document, whose
// body is a placeholder scalar, and [astnode.HasContent] does not find
// content in it.
//
// Returns an error wrapping [ErrNotFound] and [ErrNoDocument] when doc is
// nil.
func (p Path) matches(r *resolver, doc *ast.DocumentNode) ([]match, error) {
	if doc != nil && doc.Start != nil && !astnode.HasContent(doc.Body) && p.selectsRoot() {
		return []match{{node: ast.Null(doc.Start), segs: slices.Clone(p.segments)}}, nil
	}

	if doc == nil {
		return nil, fmt.Errorf("resolve %s: %w: %w", p, ErrNotFound, ErrNoDocument)
	}

	if !astnode.HasContent(doc.Body) {
		return nil, nil
	}

	found, err := r.resolve(doc.Body, p.segments)
	if err != nil {
		return nil, fmt.Errorf("resolve %s: %w", p, err)
	}

	return found, nil
}

// selectsRoot reports whether the path names the root node. A path names
// the root when it does not have segments, or has only the `~` segments
// of [Path.Key]. On the root, a `~` segment selects the root itself.
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
// wraps [ErrNotFound] when nothing exists at the path, together with
// [ErrNoDocument] when doc is nil or does not hold content.
func (p Path) single(r *resolver, doc *ast.DocumentNode) (match, error) {
	if p.wildcard() {
		return match{}, fmt.Errorf("resolve %s: %w", p, ErrWildcard)
	}

	found, err := p.matches(r, doc)
	if err != nil {
		return match{}, err
	}

	if len(found) == 0 {
		if !astnode.HasContent(doc.Body) {
			return match{}, fmt.Errorf("resolve %s: %w: %w", p, ErrNotFound, ErrNoDocument)
		}

		return match{}, fmt.Errorf("resolve %s: %w", p, ErrNotFound)
	}

	return found[0], nil
}

// singleFrom resolves the path from node with r, a resolver for the
// document that node belongs to, to exactly one match, as [Path.single]
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

// Match is one node a [Path] selects in a document, together with the path
// that selects that node alone. That path is the path as given, with each
// `[*]` selector replaced by the index of the element and each `.*` selector
// by the name of the entry, as [Resolver.KeyName] gives it. In place of
// each `..name` or `..*` selector, the path holds the selectors from the
// node that selector applied to down to the node it found. The path thus
// names the node wherever it lies. The path starts at `$` because the
// node lies in the document, so each match of an `@` path holds the `$`
// path with its selectors.
//
// Receive instances from [Resolver.Matches].
type Match struct {
	Node ast.Node
	Path Path
}

// tokenOf returns the token that p points at for m, a match of p, as
// [astnode.PathToken] finds it. A tree built by hand might hold a typed
// nil where the parser always puts a node. Such a node does not have a
// token to point at, so tokenOf returns an error wrapping [ErrNotFound].
func (p Path) tokenOf(m match) (*token.Token, error) {
	tk := astnode.PathToken(m.node, m.parent())
	if tk == nil {
		return nil, fmt.Errorf("resolve %s: %w: node has no token", p, ErrNotFound)
	}

	return tk, nil
}
