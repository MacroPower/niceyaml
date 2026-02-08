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
// It provides multiple finalization options:
//   - [Builder.Path] returns the underlying [*YAMLPath] directly.
//   - [Builder.Key] returns a [*Path] targeting [PartKey].
//   - [Builder.Value] returns a [*Path] targeting [PartValue].
//
// Create instances with [Root], [FromString], or [MustFromString].
type Builder struct {
	pb    *yaml.PathBuilder
	built *YAMLPath // Set by FromString; bypasses pb.Build().
}

// Root creates a new [Builder] starting at the root path ($).
func Root() *Builder {
	pb := &yaml.PathBuilder{}

	return &Builder{pb: pb.Root()}
}

// Child appends `.name` selectors for each name to the path.
func (b *Builder) Child(name ...string) *Builder {
	for _, n := range name {
		b.pb = b.pb.Child(n)
	}

	return b
}

// Index appends `[idx]` selectors for each index to the path.
func (b *Builder) Index(idx ...int) *Builder {
	for _, i := range idx {
		b.pb = b.pb.Index(uint(i)) //nolint:gosec // Indices are non-negative.
	}

	return b
}

// IndexAll appends a `[*]` wildcard selector to the path.
func (b *Builder) IndexAll() *Builder {
	b.pb = b.pb.IndexAll()

	return b
}

// Recursive appends a `..selector` recursive descent selector to the path.
func (b *Builder) Recursive(selector string) *Builder {
	b.pb = b.pb.Recursive(selector)

	return b
}

// build returns the underlying [*YAMLPath], using the pre-built path if set.
func (b *Builder) build() *YAMLPath {
	if b.built != nil {
		return b.built
	}

	return b.pb.Build()
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
	return &Path{
		path:   b.build(),
		target: PartKey,
	}
}

// Value finalizes the builder targeting [PartValue] and returns a [*Path].
func (b *Builder) Value() *Path {
	return &Path{
		path:   b.build(),
		target: PartValue,
	}
}

// FromString parses a YAML path expression (e.g., "$.foo.bar") into a [*Builder].
//
// The returned builder can be finalized with [Builder.Key] or [Builder.Value]
// to select the target part:
//
//	path, err := paths.FromString("$.metadata.name")
//	if err != nil {
//		return err
//	}
//	keyPath := path.Key()   // targets the key
//	valPath := path.Value() // targets the value
func FromString(expr string) (*Builder, error) {
	yp, err := yaml.PathString(expr)
	if err != nil {
		return nil, fmt.Errorf("parse path %q: %w", expr, err)
	}

	return &Builder{built: yp}, nil
}

// MustFromString is like [FromString] but panics if the expression is invalid.
//
// Use for path expressions that are known to be valid at compile time.
func MustFromString(expr string) *Builder {
	b, err := FromString(expr)
	if err != nil {
		panic(err)
	}

	return b
}

// Path represents a location in a YAML document, combining a [*YAMLPath] with a
// target [Part] (key or value).
//
// Create instances with [Builder.Key] or [Builder.Value].
type Path struct {
	path   *YAMLPath
	target Part
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

// Token resolves the [token.Token] at this path in the given file.
//
// If the target is [PartKey] and the path points to a mapping value, Token
// returns the key token. Otherwise, it returns the value node's token.
func (p *Path) Token(file *ast.File) (*token.Token, error) {
	if p == nil || p.path == nil {
		return nil, errors.New("nil path")
	}

	node, err := p.path.FilterFile(file)
	if err != nil {
		return nil, fmt.Errorf("filter from ast.File by YAMLPath: %w", err)
	}

	if p.target == PartKey {
		if keyToken := findKeyToken(file, node); keyToken != nil {
			return keyToken, nil
		}
	}

	return node.GetToken(), nil
}

// findKeyToken finds the KEY token for the given node by looking at its parent.
//
// Returns nil if the node is not a value in a mapping (e.g., array element or
// root).
func findKeyToken(file *ast.File, node ast.Node) *token.Token {
	if file == nil || node == nil || len(file.Docs) == 0 {
		return nil
	}

	parent := ast.Parent(file.Docs[0].Body, node)
	if parent == nil {
		return nil
	}

	if mv, ok := parent.(*ast.MappingValueNode); ok {
		return mv.Key.GetToken()
	}

	return nil
}
