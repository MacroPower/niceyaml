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
	// ErrNilPath indicates a resolve call on a nil [*Path].
	ErrNilPath = errors.New("nil path")

	// ErrNoDocument indicates a nil document or a document without a body.
	ErrNoDocument = errors.New("no document")

	// ErrNotFound indicates that nothing exists at the path in the document.
	ErrNotFound = errors.New("not found")

	// ErrWildcard indicates a request for a single node or token from a path
	// with a `[*]` or `..` selector. Use [Path.Nodes] for such paths.
	ErrWildcard = errors.New("wildcard path matches any number of nodes")
)

// Part selects which token of a resolved node a [Path] refers to.
//
// The zero value is [PartNode], so a [Path] from [Parse] or [Builder.Path]
// refers to the node itself.
type Part int

const (
	// PartNode targets the node the path resolves to.
	PartNode Part = iota
	// PartKey targets the key of the mapping entry the path resolves to.
	PartKey
	// PartValue targets the value of the mapping entry the path resolves to.
	PartValue
)

// String returns the part name: "node", "key", or "value".
func (p Part) String() string {
	switch p {
	case PartNode:
		return "node"
	case PartKey:
		return "key"
	case PartValue:
		return "value"
	default:
		return "Part(" + strconv.Itoa(int(p)) + ")"
	}
}

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
// characters.
func quoteName(name string) string {
	if !strings.ContainsAny(name, reservedNameChars) {
		return name
	}

	escaped := strings.NewReplacer(`\`, `\\`, `'`, `\'`).Replace(name)

	return "'" + escaped + "'"
}

// Builder constructs a [*Path] with method chaining.
//
// Builders are immutable: each selector method returns a new Builder,
// leaving the receiver unchanged. This makes it safe to share a Builder as
// a common prefix:
//
//	spec := paths.Root().Child("spec")
//	replicas := spec.Child("replicas").Value() // $.spec.replicas
//	image := spec.Child("image").Value()       // $.spec.image
//
// Finalize with [Builder.Path] for the node itself, or with [Builder.Key] or
// [Builder.Value] to target one part of a mapping entry.
//
// To start from a path expression string instead, use [Parse].
//
// Create instances with [Root].
type Builder struct {
	segments []segment
}

// Root creates a new [Builder] starting at the root path ($).
func Root() *Builder {
	return &Builder{}
}

// extend returns a new [Builder] with the receiver's selectors plus segs.
func (b *Builder) extend(segs ...segment) *Builder {
	merged := make([]segment, 0, len(b.segments)+len(segs))
	merged = append(merged, b.segments...)
	merged = append(merged, segs...)

	return &Builder{segments: merged}
}

// Child appends `.name` selectors for each name to the path.
func (b *Builder) Child(name ...string) *Builder {
	segs := make([]segment, 0, len(name))
	for _, n := range name {
		segs = append(segs, segment{kind: segmentChild, name: n})
	}

	return b.extend(segs...)
}

// Index appends `[idx]` selectors for each index to the path.
func (b *Builder) Index(idx ...int) *Builder {
	segs := make([]segment, 0, len(idx))
	for _, i := range idx {
		segs = append(segs, segment{kind: segmentIndex, index: i})
	}

	return b.extend(segs...)
}

// IndexAll appends a `[*]` wildcard selector to the path.
func (b *Builder) IndexAll() *Builder {
	return b.extend(segment{kind: segmentIndexAll})
}

// Recursive appends a `..selector` recursive descent selector to the path.
func (b *Builder) Recursive(selector string) *Builder {
	return b.extend(segment{kind: segmentRecursive, name: selector})
}

// finalize returns a [*Path] over a copy of the builder's selectors.
func (b *Builder) finalize(part Part) *Path {
	segs := make([]segment, len(b.segments))
	copy(segs, b.segments)

	return &Path{segments: segs, part: part}
}

// Path finalizes the builder and returns a [*Path] targeting [PartNode].
//
// Use [Builder.Key] or [Builder.Value] instead to target one part of a
// mapping entry.
func (b *Builder) Path() *Path {
	return b.finalize(PartNode)
}

// Key finalizes the builder and returns a [*Path] targeting [PartKey].
func (b *Builder) Key() *Path {
	return b.finalize(PartKey)
}

// Value finalizes the builder and returns a [*Path] targeting [PartValue].
func (b *Builder) Value() *Path {
	return b.finalize(PartValue)
}

// Path is a location in a YAML document, given as a sequence of selectors
// from the document root plus the [Part] of the resolved node it refers to.
//
// [Path.String] returns the selectors as a path expression, so
// [Parse] reads it back as an equal Path with [PartNode]. The part travels
// separately through [Path.Part].
//
// Create instances with [Parse], [MustParse], [Builder.Path], [Builder.Key],
// or [Builder.Value].
type Path struct {
	segments []segment
	part     Part
}

// Part returns the [Part] the path targets. A nil Path targets [PartNode].
func (p *Path) Part() Part {
	if p == nil {
		return PartNode
	}

	return p.part
}

// withPart returns a copy of p targeting part.
func (p *Path) withPart(part Part) *Path {
	if p == nil {
		return nil
	}

	c := *p
	c.part = part

	return &c
}

// Key returns a copy of the path targeting [PartKey].
func (p *Path) Key() *Path {
	return p.withPart(PartKey)
}

// Value returns a copy of the path targeting [PartValue].
func (p *Path) Value() *Path {
	return p.withPart(PartValue)
}

// String returns the path expression, such as "$.metadata.name".
//
// The [Part] is not part of the expression, so [Parse] accepts the result.
func (p *Path) String() string {
	if p == nil {
		return ""
	}

	var sb strings.Builder

	sb.WriteByte('$')

	for _, seg := range p.segments {
		sb.WriteString(seg.String())
	}

	return sb.String()
}

// YAMLPath returns the equivalent [*yaml.Path] for use with the goccy/go-yaml
// API, such as [yaml.Path.ReplaceWithNode]. The [Part] has no equivalent
// there, so the result omits it.
func (p *Path) YAMLPath() *yaml.Path {
	if p == nil {
		return nil
	}

	pb := (&yaml.PathBuilder{}).Root()

	for _, seg := range p.segments {
		switch seg.kind {
		case segmentChild:
			pb = pb.Child(quoteName(seg.name))
		case segmentIndex:
			pb = pb.Index(uint(max(seg.index, 0))) //nolint:gosec // Clamped to non-negative.
		case segmentIndexAll:
			pb = pb.IndexAll()
		case segmentRecursive:
			pb = pb.Recursive(seg.name)
		}
	}

	return pb.Build()
}

// wildcard reports whether any selector can match more than one node.
func (p *Path) wildcard() bool {
	for _, seg := range p.segments {
		if seg.kind == segmentIndexAll || seg.kind == segmentRecursive {
			return true
		}
	}

	return false
}

// matches resolves the path in doc and returns every match.
//
// Returns [ErrNilPath] for a nil Path and [ErrNoDocument] when doc or its
// body is nil.
func (p *Path) matches(doc *ast.DocumentNode) ([]match, error) {
	if p == nil {
		return nil, ErrNilPath
	}

	if doc == nil || doc.Body == nil {
		return nil, ErrNoDocument
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
func (p *Path) single(doc *ast.DocumentNode) (match, error) {
	if p != nil && p.wildcard() {
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

// Nodes resolves every node the path selects in doc, in document order,
// ignoring the [Part]. A path without `[*]` or `..` selectors yields at most
// one node; an empty result means nothing exists at the path.
//
// It looks through anchors and aliases, so each node is the content the
// path names. Selectors follow aliases to their anchor and see the entries a
// `<<` merge key brings into a mapping.
//
// Returns [ErrNilPath] for a nil Path and [ErrNoDocument] when doc or its
// body is nil.
func (p *Path) Nodes(doc *ast.DocumentNode) ([]ast.Node, error) {
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

// Node resolves the node at the path in doc, ignoring the [Part].
//
// It looks through anchors and aliases, so the result is the content the
// path names. Selectors follow aliases to their anchor and see the entries a
// `<<` merge key brings into a mapping.
//
// Returns [ErrNilPath] for a nil Path, [ErrNoDocument] when doc or its body
// is nil, [ErrWildcard] for a path with a `[*]` or `..` selector (use
// [Path.Nodes] for those), and wraps [ErrNotFound] when nothing exists at
// the path.
func (p *Path) Node(doc *ast.DocumentNode) (ast.Node, error) {
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

// Token resolves the [*token.Token] the path refers to in doc.
//
// The path resolves against the document body only, so the same path
// resolves to different tokens in different documents of one file. Returns
// the same errors as [Path.Node].
//
// For [PartKey], Token returns the key token of the mapping entry the last
// selector picked. For [PartValue] and [PartNode], and for PartKey when the
// path ends at a sequence element or the root, Token returns the token that
// starts the resolved node: a scalar's own token, the first key of a
// mapping, or the first element of a sequence. An alias resolves to its own
// token rather than the anchor's content, since that is where the path
// points in the source.
func (p *Path) Token(doc *ast.DocumentNode) (*token.Token, error) {
	m, err := p.single(doc)
	if err != nil {
		return nil, err
	}

	if p.part == PartKey && m.entry != nil {
		return m.entry.Key.GetToken(), nil
	}

	return firstToken(m.node), nil
}
