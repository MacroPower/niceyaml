package paths

import (
	"errors"
	"fmt"

	"github.com/goccy/go-yaml"
	"github.com/goccy/go-yaml/ast"
	"github.com/goccy/go-yaml/token"
)

// YAMLPath is a type alias for [yaml.Path].
type YAMLPath = yaml.Path

var (
	// ErrNilPath indicates a nil [*Path] was asked to resolve a token.
	ErrNilPath = errors.New("nil path")

	// ErrNoDocument indicates a nil document or a document without a body.
	ErrNoDocument = errors.New("no document")
)

// Part represents a specific part of a mapping entry.
type Part int

const (
	// PartKey targets the key of a mapping entry.
	PartKey Part = iota
	// PartValue targets the value of a mapping entry.
	PartValue
)

// Builder constructs YAML paths with method chaining.
//
// Builders are immutable: each selector method returns a new Builder,
// leaving the receiver unchanged. This makes it safe to share a Builder as
// a common prefix:
//
//	spec := paths.Root().Child("spec")
//	replicas := spec.Child("replicas").Value() // $.spec.replicas.(value)
//	image := spec.Child("image").Value()       // $.spec.image.(value)
//
// It provides multiple finalization options:
//   - [Builder.Path] returns the underlying [*YAMLPath] directly.
//   - [Builder.Key] returns a [*Path] targeting [PartKey].
//   - [Builder.Value] returns a [*Path] targeting [PartValue].
//
// To start from a path expression string instead, parse it with [Parse] and
// pass the result to [NewPath].
//
// Create instances with [Root].
type Builder struct {
	ops []func(*yaml.PathBuilder) *yaml.PathBuilder
}

// Root creates a new [Builder] starting at the root path ($).
func Root() *Builder {
	return &Builder{}
}

// extend returns a new [Builder] with the receiver's selectors plus ops.
func (b *Builder) extend(ops ...func(*yaml.PathBuilder) *yaml.PathBuilder) *Builder {
	merged := make([]func(*yaml.PathBuilder) *yaml.PathBuilder, 0, len(b.ops)+len(ops))
	merged = append(merged, b.ops...)
	merged = append(merged, ops...)

	return &Builder{ops: merged}
}

// Child appends `.name` selectors for each name to the path.
func (b *Builder) Child(name ...string) *Builder {
	ops := make([]func(*yaml.PathBuilder) *yaml.PathBuilder, 0, len(name))
	for _, n := range name {
		ops = append(ops, func(pb *yaml.PathBuilder) *yaml.PathBuilder {
			return pb.Child(n)
		})
	}

	return b.extend(ops...)
}

// Index appends `[idx]` selectors for each index to the path.
func (b *Builder) Index(idx ...int) *Builder {
	ops := make([]func(*yaml.PathBuilder) *yaml.PathBuilder, 0, len(idx))
	for _, i := range idx {
		ops = append(ops, func(pb *yaml.PathBuilder) *yaml.PathBuilder {
			return pb.Index(uint(i)) //nolint:gosec // Indices are non-negative.
		})
	}

	return b.extend(ops...)
}

// IndexAll appends a `[*]` wildcard selector to the path.
func (b *Builder) IndexAll() *Builder {
	return b.extend(func(pb *yaml.PathBuilder) *yaml.PathBuilder {
		return pb.IndexAll()
	})
}

// Recursive appends a `..selector` recursive descent selector to the path.
func (b *Builder) Recursive(selector string) *Builder {
	return b.extend(func(pb *yaml.PathBuilder) *yaml.PathBuilder {
		return pb.Recursive(selector)
	})
}

// build returns the [*YAMLPath] the selectors describe.
func (b *Builder) build() *YAMLPath {
	pb := (&yaml.PathBuilder{}).Root()
	for _, op := range b.ops {
		pb = op(pb)
	}

	return pb.Build()
}

// Path finalizes the builder and returns the underlying [*YAMLPath].
//
// Use [Builder.Key] or [Builder.Value] instead to get a [*Path] with
// [Part] targeting.
func (b *Builder) Path() *YAMLPath {
	return b.build()
}

// Key finalizes the builder targeting [PartKey] and returns a [*Path].
func (b *Builder) Key() *Path {
	return NewPath(b.build(), PartKey)
}

// Value finalizes the builder targeting [PartValue] and returns a [*Path].
func (b *Builder) Value() *Path {
	return NewPath(b.build(), PartValue)
}

// Parse parses a YAML path expression (e.g., "$.foo.bar") into a
// [*YAMLPath].
//
// Pair the result with a [Part] through [NewPath] to use it where a [*Path]
// is expected:
//
//	yp, err := paths.Parse("$.metadata.name")
//	if err != nil {
//		return err
//	}
//	keyPath := paths.NewPath(yp, paths.PartKey)
func Parse(expr string) (*YAMLPath, error) {
	yp, err := yaml.PathString(expr)
	if err != nil {
		return nil, fmt.Errorf("parse path %q: %w", expr, err)
	}

	return yp, nil
}

// MustParse is like [Parse] but panics if the expression is invalid.
//
// Use for path expressions that are known to be valid at compile time.
func MustParse(expr string) *YAMLPath {
	yp, err := Parse(expr)
	if err != nil {
		panic(err)
	}

	return yp
}

// Path represents a location in a YAML document, combining a [*YAMLPath] with a
// target [Part] (key or value).
//
// Create instances with [NewPath], [Builder.Key], or [Builder.Value].
type Path struct {
	path   *YAMLPath
	target Part
}

// NewPath creates a new [*Path] that targets part of the mapping entry at p.
func NewPath(p *YAMLPath, part Part) *Path {
	return &Path{
		path:   p,
		target: part,
	}
}

// Path returns the underlying [*YAMLPath].
func (p *Path) Path() *YAMLPath {
	if p == nil {
		return nil
	}

	return p.path
}

// Part returns the target [Part] (key or value).
func (p *Path) Part() Part {
	if p == nil {
		return PartValue
	}

	return p.target
}

// String returns the path as a string with a `.(key)` or `.(value)` suffix.
func (p *Path) String() string {
	if p == nil || p.path == nil {
		return ""
	}

	basePath := p.path.String()

	switch p.target {
	case PartKey:
		return basePath + ".(key)"
	case PartValue:
		return basePath + ".(value)"
	default:
		return basePath
	}
}

// Token resolves the [token.Token] at this path in the given document.
//
// The path is resolved against the document body only, so the same path
// resolves to different tokens in different documents of one file. Returns
// [ErrNilPath] for a nil Path, [ErrNoDocument] when doc or its body is nil,
// and wraps [yaml.ErrNotFoundNode] when nothing exists at the path.
//
// If the target is [PartKey] and the path points to a mapping value, Token
// returns the key token. Otherwise, it returns the value node's token.
func (p *Path) Token(doc *ast.DocumentNode) (*token.Token, error) {
	if p == nil || p.path == nil {
		return nil, ErrNilPath
	}

	if doc == nil || doc.Body == nil {
		return nil, ErrNoDocument
	}

	node, err := p.path.FilterNode(doc.Body)
	if err != nil {
		return nil, fmt.Errorf("filter document by YAMLPath: %w", err)
	}

	if node == nil {
		return nil, fmt.Errorf("filter document by YAMLPath ( %s ): %w", p.path, yaml.ErrNotFoundNode)
	}

	if p.target == PartKey {
		if keyToken := findKeyToken(doc, node); keyToken != nil {
			return keyToken, nil
		}
	}

	return node.GetToken(), nil
}

// findKeyToken finds the KEY token for the given node by looking at its parent.
//
// Returns nil if the node is not a value in a mapping (e.g., array element or
// root).
func findKeyToken(doc *ast.DocumentNode, node ast.Node) *token.Token {
	parent := ast.Parent(doc.Body, node)
	if parent == nil {
		return nil
	}

	if mv, ok := parent.(*ast.MappingValueNode); ok {
		return mv.Key.GetToken()
	}

	return nil
}
