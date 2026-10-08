package niceyaml_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// dependencyPolicy is the rule for one third-party library the exported API
// names. The package documentation states the rule for go-yaml, and this
// test applies the same rule to every library it lists.
type dependencyPolicy struct {
	// The library, for the report.
	name string
	// The import paths whose identifiers the policy covers.
	packages []string
	// The local name of an import path whose base is not its package
	// name.
	localNames map[string]string
	// The identifiers the exported API may name, as "pkg.Name".
	types []string
	// The identifiers that pass the library's own values through, as
	// "pkg.Name". The exported API may name one only in an identifier
	// that carries the prefix.
	passThrough []string
	// The prefix an identifier must carry to pass one of the library's
	// values through.
	prefix string
}

var policies = []dependencyPolicy{
	{
		name: "go-yaml",
		packages: []string{
			"github.com/goccy/go-yaml",
			"github.com/goccy/go-yaml/ast",
			"github.com/goccy/go-yaml/token",
			"github.com/goccy/go-yaml/parser",
			"github.com/goccy/go-yaml/lexer",
		},
		localNames: map[string]string{"github.com/goccy/go-yaml": "yaml"},
		// Add to the list only with a matching change to the dependency
		// policy in the package documentation.
		types: []string{
			// The document model niceyaml wraps rather than hides.
			"ast.File",
			"ast.DocumentNode",
			"ast.Node",
			"token.Token",
			"token.Tokens",
			"token.Type",
			"token.Position",
			// Interop with go-yaml's own path API.
			"yaml.Path",
		},
		// Add to this list too only with a matching change to the
		// dependency policy in the package documentation.
		passThrough: []string{
			// Escape hatch.
			"yaml.EncodeOption",
			// The comments WithYAMLComments collects in a decode and
			// encoder.WithYAMLComments adds. The encoder applies them
			// itself, since go-yaml's own option for them works only when
			// go-yaml writes the document.
			"yaml.CommentMap",
			// The check WithYAMLStructValidator runs on each struct of a
			// decode.
			"yaml.StructValidator",
		},
		prefix: "YAML",
	},
	{
		name:     "x/jsonschema",
		packages: []string{"go.jacobcolvin.com/x/jsonschema"},
		types: []string{
			// A validator compiled elsewhere, which schema.FromJSONSchema
			// wraps.
			"jsonschema.Validator",
		},
		// Escape hatch.
		passThrough: []string{"jsonschema.ValidateOption"},
		prefix:      "JSONSchema",
	},
}

// TestExportedAPI_DependencyPolicy walks every exported declaration in the
// repository's public packages. It checks that any third-party type a
// declaration names is on the allowlist of its library's policy, and that
// the pass-through types only appear in identifiers with the library's
// prefix.
func TestExportedAPI_DependencyPolicy(t *testing.T) {
	t.Parallel()

	var violations []string

	err := filepath.WalkDir(".", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		if d.IsDir() {
			name := d.Name()
			if path != "." && (strings.HasPrefix(name, ".") || slices.Contains(
				[]string{"internal", "examples", "cmd", "testdata", "ci"}, name,
			)) {
				return filepath.SkipDir
			}

			return nil
		}

		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}

		violations = append(violations, checkFile(t, path)...)

		return nil
	})
	require.NoError(t, err)
	require.Empty(t, violations, "exported API names third-party types outside the policy")
}

// TestCheckFile_Coverage checks that checkFile reads the declarations that
// expose a type outside a named field or a plain receiver: embedded fields,
// untyped vars, and methods on generic receivers.
func TestCheckFile_Coverage(t *testing.T) {
	t.Parallel()

	const header = `package p

import (
	"github.com/goccy/go-yaml"
	"github.com/goccy/go-yaml/ast"
	"github.com/goccy/go-yaml/parser"
)

`

	tcs := map[string]struct {
		src  string
		want []string
	}{
		"embedded field": {
			src:  `type Foo struct{ ast.MappingNode }`,
			want: []string{"Foo.MappingNode"},
		},
		"embedded pointer": {
			src:  `type Foo struct{ *ast.MappingNode }`,
			want: []string{"Foo.MappingNode"},
		},
		"embedded allowed type": {
			src: `type Foo struct{ *ast.File }`,
		},
		"untyped var": {
			src:  `var Default = parser.ParseComments`,
			want: []string{"Default"},
		},
		"untyped var from call": {
			src:  `var Default = ast.Mapping(nil, false)`,
			want: []string{"Default"},
		},
		"function literal body": {
			src: `var Parse = func(src []byte) (*ast.File, error) {
	return parser.ParseBytes(src, parser.ParseComments)
}`,
		},
		"pass-through type without the prefix": {
			src:  `func WithOptions(opts ...yaml.EncodeOption) {}`,
			want: []string{"WithOptions"},
		},
		"pass-through type with the prefix": {
			src: `func WithYAMLOptions(opts ...yaml.EncodeOption) {}`,
		},
		"method on receiver with type parameters": {
			src: `type Map[K comparable, V any] struct{}

func (m *Map[K, V]) Node() *ast.MappingNode { return nil }`,
			want: []string{"Node"},
		},
		"method on unexported receiver with type parameters": {
			src: `type pair[K comparable, V any] struct{}

func (p *pair[K, V]) Node() *ast.MappingNode { return nil }`,
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			path := filepath.Join(t.TempDir(), "src.go")
			require.NoError(t, os.WriteFile(path, []byte(header+tc.src), 0o600))

			var got []string

			// A violation reads "file:line:col: Owner names ...".
			for _, v := range checkFile(t, path) {
				_, rest, _ := strings.Cut(v, ": ")
				owner, _, _ := strings.Cut(rest, " ")
				got = append(got, owner)
			}

			assert.Equal(t, tc.want, got)
		})
	}
}

// checkFile returns the policy violations in the exported declarations of
// the Go file at path.
func checkFile(t *testing.T, path string) []string {
	t.Helper()

	src, err := os.ReadFile(path)
	require.NoError(t, err)

	fset := token.NewFileSet()

	file, err := parser.ParseFile(fset, path, src, parser.SkipObjectResolution)
	require.NoError(t, err)

	// Map local import names to the policy that covers them.
	covered := map[string]*dependencyPolicy{}

	for _, imp := range file.Imports {
		importPath := strings.Trim(imp.Path.Value, `"`)

		for i := range policies {
			policy := &policies[i]
			if !slices.Contains(policy.packages, importPath) {
				continue
			}

			name := filepath.Base(importPath)
			if local, ok := policy.localNames[importPath]; ok {
				name = local
			}

			if imp.Name != nil {
				name = imp.Name.Name
			}

			covered[name] = policy
		}
	}

	if len(covered) == 0 {
		return nil
	}

	var violations []string

	report := func(owner string, node ast.Node) {
		ast.Inspect(node, func(n ast.Node) bool {
			sel, ok := n.(*ast.SelectorExpr)
			if !ok {
				return true
			}

			pkg, ok := sel.X.(*ast.Ident)
			if !ok {
				return true
			}

			policy, ok := covered[pkg.Name]
			if !ok {
				return true
			}

			ref := pkg.Name + "." + sel.Sel.Name
			pos := fset.Position(sel.Pos())

			switch {
			case slices.Contains(policy.passThrough, ref):
				if !strings.Contains(owner, policy.prefix) {
					violations = append(
						violations,
						pos.String()+": "+owner+" passes "+ref+" through without a "+policy.prefix+" prefix",
					)
				}

			case !slices.Contains(policy.types, ref):
				violations = append(violations, pos.String()+": "+owner+" names "+policy.name+" type "+ref)
			}

			return true
		})
	}

	for _, decl := range file.Decls {
		switch d := decl.(type) {
		case *ast.FuncDecl:
			if !d.Name.IsExported() || (d.Recv != nil && !receiverExported(d.Recv)) {
				continue
			}

			report(d.Name.Name, d.Type)

		case *ast.GenDecl:
			for _, spec := range d.Specs {
				switch s := spec.(type) {
				case *ast.TypeSpec:
					if s.Name.IsExported() {
						reportType(report, s)
					}

				case *ast.ValueSpec:
					for i, name := range s.Names {
						if !name.IsExported() {
							continue
						}

						switch {
						case s.Type != nil:
							report(name.Name, s.Type)
						case len(s.Values) == len(s.Names):
							report(name.Name, valueType(s.Values[i]))
						case len(s.Values) == 1:
							// A call that returns one value per name.
							report(name.Name, valueType(s.Values[0]))
						}
					}
				}
			}
		}
	}

	return violations
}

// reportType reports the third-party references in an exported type. A struct
// contributes only its exported fields, each under a "Type.Field" name, so
// the prefix rule reads the field's name along with the type's. An embedded
// field counts under the name of the type it embeds, exported or not,
// because the struct promotes that type's exported fields and methods.
func reportType(report func(string, ast.Node), spec *ast.TypeSpec) {
	st, ok := spec.Type.(*ast.StructType)
	if !ok {
		report(spec.Name.Name, spec.Type)

		return
	}

	for _, field := range st.Fields.List {
		if len(field.Names) == 0 {
			report(spec.Name.Name+"."+typeName(field.Type), field.Type)

			continue
		}

		for _, name := range field.Names {
			if name.IsExported() {
				report(spec.Name.Name+"."+name.Name, field.Type)
			}
		}
	}
}

// valueType returns the part of an untyped var or const value that decides
// its type. A function literal yields its signature and a composite literal
// its type, so neither a function body nor the elements of a literal count.
// A call yields its function, which also covers a conversion.
func valueType(expr ast.Expr) ast.Node {
	switch e := expr.(type) {
	case *ast.ParenExpr:
		return valueType(e.X)
	case *ast.UnaryExpr:
		return valueType(e.X)
	case *ast.FuncLit:
		return e.Type
	case *ast.CompositeLit:
		return e.Type
	case *ast.CallExpr:
		return e.Fun
	}

	return expr
}

// typeName returns the name of the type in a receiver or embedded field,
// without the pointer, the type arguments, or the package qualifier.
func typeName(expr ast.Expr) string {
	if star, ok := expr.(*ast.StarExpr); ok {
		expr = star.X
	}

	switch e := expr.(type) {
	case *ast.IndexExpr:
		expr = e.X
	case *ast.IndexListExpr:
		expr = e.X
	}

	switch e := expr.(type) {
	case *ast.Ident:
		return e.Name
	case *ast.SelectorExpr:
		return e.Sel.Name
	}

	return ""
}

// receiverExported reports whether a method's receiver type is exported.
func receiverExported(recv *ast.FieldList) bool {
	return len(recv.List) > 0 && token.IsExported(typeName(recv.List[0].Type))
}
