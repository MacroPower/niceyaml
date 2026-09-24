package paths

import (
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/goccy/go-yaml"
	"github.com/goccy/go-yaml/ast"
	"github.com/goccy/go-yaml/token"
)

var (
	// ErrNoDocument indicates a document with no content to resolve in: a
	// nil document, a document without a body, or a document holding only
	// directives or comments. Errors that wrap it also wrap [ErrNotFound],
	// since nothing exists at any path in such a document.
	ErrNoDocument = errors.New("document has no content")

	// ErrNotFound indicates that nothing exists at the path in the document.
	ErrNotFound = errors.New("not found")

	// ErrAlias indicates an alias on the path that names no anchor or that
	// leads back to itself, so it has no content.
	ErrAlias = errors.New("alias does not resolve")

	// ErrWildcard indicates a request for a single node or token from a path
	// with a `[*]` or `..` selector. Use [Path.Nodes] for such paths.
	ErrWildcard = errors.New("wildcard path matches any number of nodes")
)

// segmentKind identifies the selector a [segment] applies.
type segmentKind int

const (
	segmentChild     segmentKind = iota // .name
	segmentIndex                        // [n]
	segmentIndexAll                     // [*]
	segmentRecursive                    // ..name
	segmentKey                          // ~
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
	default:
		return ""
	}
}

// reservedNameChars are the characters that force a selector name into
// single quotes so that [Parse] reads it back as one selector.
const reservedNameChars = ".*[]$'~"

// quoteName returns name in the form [Parse] accepts as a child or
// recursive selector, wrapping it in single quotes when it contains reserved
// characters or is empty, since an unquoted empty name would leave a bare
// `.` that Parse rejects.
func quoteName(name string) string {
	if name != "" && !strings.ContainsAny(name, reservedNameChars) {
		return name
	}

	escaped := strings.NewReplacer(`\`, `\\`, `'`, `\'`).Replace(name)

	return "'" + escaped + "'"
}

// Path is a location in a YAML document, given as a sequence of selectors
// from the document root.
//
// A Path is a value and never changes. Each selector method returns a new
// Path and leaves the receiver as it was, so a Path is safe to share as a
// common prefix:
//
//	spec := paths.Root().Child("spec")
//	replicas := spec.Child("replicas") // $.spec.replicas
//	image := spec.Child("image")       // $.spec.image
//
// The zero value is the document root, the same as [Root]. A path selects a
// node, which for a mapping entry is its value. [Path.Key] appends the `~`
// selector, which picks the key of that entry instead, so one Path names
// either node of an entry and every method that takes a Path, such as
// [Path.Token] or [niceyaml.AtPath], reads the key or the value from the
// Path alone.
//
// [Path.String] returns the selectors as a path expression, and [Parse]
// reads it back as an equal Path.
//
// Create instances with [Root], [Parse], or [MustParse].
type Path struct {
	segments []segment
}

// Root creates a new [Path] at the document root ($).
func Root() Path {
	return Path{}
}

// extend returns a copy of p with segs appended to its selectors. The copy
// owns its selectors, so extending the same prefix twice yields two
// independent paths.
func (p Path) extend(segs ...segment) Path {
	merged := make([]segment, 0, len(p.segments)+len(segs))
	merged = append(merged, p.segments...)
	merged = append(merged, segs...)

	return Path{segments: merged}
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
// key and any anchor or tag on the key. Where the path selects no entry,
// such as a sequence element or the root, the `~` selects the node the
// path already does, so an error at such a path highlights the same text
// with or without it.
//
//	name := paths.Root().Child("metadata", "name")
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

// Join returns a copy of the path with the selectors of q appended, so a
// path written from one node of a document resolves from the root:
//
//	hours := paths.Root().Child("spec", "hours")
//	open := paths.Root().Child("open")
//	hours.Join(open) // $.spec.hours.open
//
// Joining the root changes nothing, and joining to the root yields q.
func (p Path) Join(q Path) Path {
	return p.extend(q.segments...)
}

// IsRoot reports whether the path holds no selectors, so it names the
// document root as [Root] does.
func (p Path) IsRoot() bool {
	return len(p.segments) == 0
}

// String returns the path expression, such as "$.metadata.name", which
// [Parse] reads back.
func (p Path) String() string {
	var sb strings.Builder

	sb.WriteByte('$')

	for _, seg := range p.segments {
		sb.WriteString(seg.String())
	}

	return sb.String()
}

// YAMLPath returns the equivalent [*yaml.Path] for use with the goccy/go-yaml
// API, such as [yaml.Path.FilterNode] on a tree the caller parsed. The
// tree a Source hands out through its File and AST methods is shared
// with every Node of the Source, so a call that edits a tree, such as
// [yaml.Path.ReplaceWithNode], runs on a tree of the caller's own.
//
// The result selects the same names as the Path, but its String is the
// goccy/go-yaml form, which differs from [Path.String] for names with
// reserved characters and is not always re-parseable. The builder has no
// quoting for recursive selectors, so Recursive("a.b") prints as `$..a.b`,
// which [yaml.PathString] reads as two selectors. Use [Path.String] for a
// form that [Parse] reads back.
//
// YAMLPath leaves out the `~` selector from [Path.Key], since goccy/go-yaml
// has no selector for the key of an entry, so the result selects the value.
func (p Path) YAMLPath() *yaml.Path {
	pb := (&yaml.PathBuilder{}).Root()

	for _, seg := range p.segments {
		switch seg.kind {
		case segmentChild:
			// The builder quotes names itself, and the goccy filter strips
			// only the quotes it adds, so a name quoted here would never
			// match its key.
			pb = pb.Child(seg.name)
		case segmentIndex:
			pb = pb.Index(uint(seg.index)) //nolint:gosec // Index and Parse never store a negative.
		case segmentIndexAll:
			pb = pb.IndexAll()
		case segmentRecursive:
			pb = pb.Recursive(seg.name)
		case segmentKey:
			// No goccy selector names a key.
		}
	}

	return pb.Build()
}

// wildcard reports whether any selector can match more than one node.
func (p Path) wildcard() bool {
	for _, seg := range p.segments {
		if seg.kind == segmentIndexAll || seg.kind == segmentRecursive {
			return true
		}
	}

	return false
}

// hasContent reports whether body holds a value to resolve in. A directive
// or a comment group is not content.
func hasContent(body ast.Node) bool {
	switch body.Type() {
	case ast.DirectiveType, ast.CommentType:
		return false
	default:
		return true
	}
}

// matches resolves the path in doc and returns every match.
//
// A document with a nil body below a "---" header is the null document, and
// the root path matches that null at the header, so an error about the
// document as a whole points at the line it was written on. Every deeper
// path has no node to reach.
//
// Returns an error wrapping [ErrNotFound] and [ErrNoDocument] when doc is
// nil, when a path with segments meets a nil body, or when the body is a
// directive or a comment, which is what a parse that keeps comments leaves
// as the body of a comment-only document.
func (p Path) matches(doc *ast.DocumentNode) ([]match, error) {
	if doc != nil && doc.Body == nil && doc.Start != nil && p.selectsRoot() {
		return []match{{node: ast.Null(doc.Start)}}, nil
	}

	if doc == nil || doc.Body == nil || !hasContent(doc.Body) {
		return nil, fmt.Errorf("resolve %s: %w: %w", p, ErrNotFound, ErrNoDocument)
	}

	found, err := newResolver(doc).resolve(doc.Body, p.segments)
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

// single resolves the path in doc to exactly one match.
//
// Returns [ErrWildcard] for a path with a `[*]` or `..` selector and wraps
// [ErrNotFound] when nothing exists at the path.
func (p Path) single(doc *ast.DocumentNode) (match, error) {
	if p.wildcard() {
		return match{}, fmt.Errorf("resolve %s: %w", p, ErrWildcard)
	}

	found, err := p.matches(doc)
	if err != nil {
		return match{}, err
	}

	if len(found) == 0 {
		return match{}, fmt.Errorf("resolve %s: %w", p, ErrNotFound)
	}

	return found[0], nil
}

// Nodes resolves every node the path selects in doc, in document order. A
// path without `[*]` or `..` selectors yields at most one node; an empty
// result means nothing exists at the path. Each node appears once, even
// when chained `..` selectors reach it more than once.
//
// It looks through anchors and aliases, so each node is the content the
// path names. The `.name`, `[n]`, and `[*]` selectors follow aliases to
// their anchor and see the entries a `<<` merge key brings into a mapping.
// The `..name` selector visits each entry once, where the source defines
// it, so it neither follows aliases nor looks into merge sources.
//
// Wraps [ErrNoDocument], together with [ErrNotFound], when the document has
// no content to resolve in, and [ErrAlias] when an alias on the path does
// not resolve. [Path.Matches] returns the same nodes with the path that
// selects each one alone.
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

// Match is one node a [Path] selects in a document, together with the
// path that selects that node alone: the path as given, with each `[*]`
// selector replaced by the index of the element and each `..name`
// selector by the selectors from the node it applied to down to the
// entry it found, so the path names the node wherever it lies.
//
// Receive instances from [Path.Matches].
type Match struct {
	Node ast.Node
	Path Path
}

// Matches resolves every node the path selects in doc, as [Path.Nodes]
// does, and returns each with the path that selects it alone, so a
// caller that checks each element of a sequence or each entry a `..name`
// selector finds reports the one it checked:
//
//	for _, m := range matches {
//		fmt.Println(m.Path) // $.items[0], $.items[1], ...
//	}
//
// A path without `[*]` or `..` selectors yields at most one match, whose
// path is the path as given. The path of a node reached through an alias
// is the path as written, not the location of the anchor, and the path
// of an entry a `<<` merge key brings in is the path of the mapping that
// merges it. Returns the errors [Path.Nodes] returns.
func (p Path) Matches(doc *ast.DocumentNode) ([]Match, error) {
	found, err := p.matches(doc)
	if err != nil {
		return nil, err
	}

	r := newResolver(doc)
	matches := make([]Match, 0, len(found))

	for _, m := range found {
		node, err := r.deref(m.node)
		if err != nil {
			return nil, fmt.Errorf("resolve %s: %w", p, err)
		}

		// A tree built by hand may hold a nil where the parser always
		// puts a node, and a nil is nothing to list.
		if isNilNode(node) {
			continue
		}

		matches = append(matches, Match{Node: node, Path: Path{segments: m.segs}})
	}

	return matches, nil
}

// Node resolves the node at the path in doc.
//
// It looks through anchors and aliases, so the result is the content the
// path names. The `.name` and `[n]` selectors follow aliases to their anchor
// and see the entries a `<<` merge key brings into a mapping.
//
// Returns [ErrWildcard] for a path with a `[*]` or `..` selector (use
// [Path.Nodes] for those), and wraps [ErrNotFound] when nothing exists at
// the path, together with [ErrNoDocument] when the document has no content
// to resolve in, and [ErrAlias] when an alias on the path does not resolve.
func (p Path) Node(doc *ast.DocumentNode) (ast.Node, error) {
	m, err := p.single(doc)
	if err != nil {
		return nil, err
	}

	node, err := newResolver(doc).deref(m.node)
	if err != nil {
		return nil, fmt.Errorf("resolve %s: %w", p, err)
	}

	// A tree built by hand may hold a nil where the parser always puts a
	// node, and a path that reaches one selects nothing.
	if isNilNode(node) {
		return nil, fmt.Errorf("resolve %s: %w", p, ErrNotFound)
	}

	return node, nil
}

// Token resolves the [*token.Token] that starts the node the path selects
// in doc: a scalar's own token, the first key of a mapping, or the first
// element of a sequence. For a mapping entry that is the token of its
// value, and for a path ending in the `~` selector from [Path.Key] it is
// the token of the key. An alias resolves to its own token rather than the
// anchor's content, since that is where the path points in the source.
//
// The path resolves against the document body only, so the same path
// resolves to different tokens in different documents of one file. Returns
// the same errors as [Path.Node], except that Token does not look through
// the node the last selector reaches, so an alias there that names no
// anchor yields the alias's own token rather than [ErrAlias]. Token still
// returns [ErrAlias] for an alias an earlier selector resolves through.
func (p Path) Token(doc *ast.DocumentNode) (*token.Token, error) {
	m, err := p.single(doc)
	if err != nil {
		return nil, err
	}

	return p.tokenOf(m.node)
}

// tokenOf returns the token that starts node. A tree built by hand may hold
// a typed nil where the parser always puts a node, and such a node has no
// token to point at, which is [ErrNotFound].
func (p Path) tokenOf(node ast.Node) (*token.Token, error) {
	tk := firstToken(node)
	if tk == nil {
		return nil, fmt.Errorf("%w: %s has no token", ErrNotFound, p)
	}

	return tk, nil
}
