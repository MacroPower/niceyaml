package paths

import (
	"errors"
	"fmt"

	"github.com/goccy/go-yaml"
	"github.com/goccy/go-yaml/ast"
	"github.com/goccy/go-yaml/token"
)

// YAMLPath is an alias for [yaml.Path].
type YAMLPath = yaml.Path

// Part represents a specific part of a mapping entry.
type Part int

const (
	// PartKey represents the key part of a mapping entry.
	PartKey Part = iota
	// PartValue represents the value part of a mapping entry.
	PartValue
)

// Builder builds YAML paths.
//
// It provides multiple construction options:
//   - [Builder.Path] builds the underlying [*YAMLPath] directly.
//   - [Builder.Key] builds a [*Path] using [PartKey].
//   - [Builder.Value] builds a [*Path] using [PartValue].
//
// Create initialized instances with [Root].
type Builder struct {
	pb *yaml.PathBuilder
}

// Root creates a new [*Builder] initialized to the root path ($).
func Root() *Builder {
	pb := &yaml.PathBuilder{}

	return &Builder{pb: pb.Root()}
}

// Child adds `.name` for each name to the path.
func (b *Builder) Child(name ...string) *Builder {
	for _, n := range name {
		b.pb = b.pb.Child(n)
	}

	return b
}

// Index adds `[idx]` for each index to the path.
func (b *Builder) Index(idx ...uint) *Builder {
	for _, i := range idx {
		b.pb = b.pb.Index(i)
	}

	return b
}

// IndexAll adds `[*]` to the path.
func (b *Builder) IndexAll() *Builder {
	b.pb = b.pb.IndexAll()

	return b
}

// Recursive adds a recursive descent selector to the path.
func (b *Builder) Recursive(selector string) *Builder {
	b.pb = b.pb.Recursive(selector)

	return b
}

// Path finalizes the builder and returns the underlying [*YAMLPath].
// Use [Builder.Key] or [Builder.Value] instead to get a [*Path] with target information.
func (b *Builder) Path() *YAMLPath {
	return b.pb.Build()
}

// Key finalizes the path targeting the key and returns a [*Path].
func (b *Builder) Key() *Path {
	return &Path{
		path:   b.pb.Build(),
		target: PartKey,
	}
}

// Value finalizes the path targeting the value and returns a [*Path].
func (b *Builder) Value() *Path {
	return &Path{
		path:   b.pb.Build(),
		target: PartValue,
	}
}

// Path represents a location in a YAML document, combining a [*YAMLPath]
// with a target [Part] specification (key or value).
// Create instances by calling [Builder.Key] or [Builder.Value].
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

// Part returns the [Part] (key or value target).
func (p *Path) Part() Part {
	if p == nil {
		return PartValue
	}

	return p.target
}

// String returns the string representation of the path.
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

// Token resolves the token at this path in the given YAML file.
// If the target is [PartKey] and the path points to a mapping value, returns the key token.
// Otherwise, returns the value node's token.
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
// Returns nil if the node is not a value in a mapping (e.g., array element or root).
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
