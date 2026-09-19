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
	// leads back to itself, so the content it stands for cannot be reached.
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
	default:
		return ""
	}
}

// reservedNameChars are the characters that force a selector name into
// single quotes so that [Parse] reads it back as one selector.
const reservedNameChars = ".*[]$'"

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
// A Path is a value and never changes: each selector method returns a new
// Path and leaves the receiver as it was, so a Path is safe to share as a
// common prefix:
//
//	spec := paths.Root().Child("spec")
//	replicas := spec.Child("replicas") // $.spec.replicas
//	image := spec.Child("image")       // $.spec.image
//
// The zero value is the document root, the same as [Root]. A path selects a
// node, which for a mapping entry is its value. [Path.Token] returns the
// token that starts that node and [Path.KeyToken] the key of the entry, so
// the same Path names either token of an entry.
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

// Recursive returns a copy of the path with a `..selector` recursive descent
// selector appended.
func (p Path) Recursive(selector string) Path {
	return p.extend(segment{kind: segmentRecursive, name: selector})
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
// API, such as [yaml.Path.ReplaceWithNode].
//
// The result selects the same names as the Path, but its String is the
// goccy/go-yaml form, which differs from [Path.String] for names with
// reserved characters and is not always re-parseable. The builder has no
// quoting for recursive selectors, so Recursive("a.b") prints as `$..a.b`,
// which [yaml.PathString] reads as two selectors. Use [Path.String] for a
// form that [Parse] reads back.
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
// Returns an error wrapping [ErrNotFound] and [ErrNoDocument] when doc or
// its body is nil or the body is a directive or a comment, which is what a
// parse that keeps comments leaves as the body of a comment-only document.
func (p Path) matches(doc *ast.DocumentNode) ([]match, error) {
	if doc == nil || doc.Body == nil || !hasContent(doc.Body) {
		return nil, fmt.Errorf("resolve %s: %w: %w", p, ErrNotFound, ErrNoDocument)
	}

	found, err := newResolver(doc).resolve(doc.Body, p.segments)
	if err != nil {
		return nil, fmt.Errorf("resolve %s: %w", p, err)
	}

	return found, nil
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
// not resolve.
func (p Path) Nodes(doc *ast.DocumentNode) ([]ast.Node, error) {
	found, err := p.matches(doc)
	if err != nil {
		return nil, err
	}

	r := newResolver(doc)
	nodes := make([]ast.Node, 0, len(found))

	for _, m := range found {
		node, err := r.deref(m.node)
		if err != nil {
			return nil, fmt.Errorf("resolve %s: %w", p, err)
		}

		nodes = append(nodes, node)
	}

	return nodes, nil
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

	return node, nil
}

// Token resolves the [*token.Token] that starts the node the path selects
// in doc: a scalar's own token, the first key of a mapping, or the first
// element of a sequence. For a mapping entry that is the token of its
// value; [Path.KeyToken] returns the key. An alias resolves to its own
// token rather than the anchor's content, since that is where the path
// points in the source.
//
// The path resolves against the document body only, so the same path
// resolves to different tokens in different documents of one file. Returns
// the same errors as [Path.Node], except [ErrAlias]: Token does not
// dereference the node it resolves to, so an alias that names no anchor
// still yields the alias's own token.
func (p Path) Token(doc *ast.DocumentNode) (*token.Token, error) {
	m, err := p.single(doc)
	if err != nil {
		return nil, err
	}

	return p.tokenOf(m.node)
}

// KeyToken resolves the [*token.Token] of the key of the mapping entry the
// last selector of the path picked, looking through the `?` indicator of an
// explicit key and any anchor or tag on the key. A path that ends at a
// sequence element or at the root picks no entry, and KeyToken then returns
// the token [Path.Token] returns. It resolves as Token does and returns the
// same errors.
func (p Path) KeyToken(doc *ast.DocumentNode) (*token.Token, error) {
	m, err := p.single(doc)
	if err != nil {
		return nil, err
	}

	node := m.node
	if m.entry != nil {
		if key := keyContent(m.entry.Key); key != nil {
			node = key
		}
	}

	return p.tokenOf(node)
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
