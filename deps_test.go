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
	// The prefix an identifier must carry to pass one of the library's
	// option types through.
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
			// Escape hatches, only in identifiers with a YAML prefix.
			"yaml.DecodeOption",
			"yaml.EncodeOption",
			"parser.Option",
		},
		prefix: "YAML",
	},
	{
		name:     "x/jsonschema",
		packages: []string{"go.jacobcolvin.com/x/jsonschema"},
		types: []string{
			// A validator compiled elsewhere, which schema.NewValidator
			// adapts.
			"jsonschema.Validator",
			// Escape hatch, only in identifiers with a JSONSchema prefix.
			"jsonschema.ValidateOption",
		},
		prefix: "JSONSchema",
	},
}

// TestExportedAPI_DependencyPolicy walks every exported declaration in the
// module and checks that any third-party type it names is on the allowlist
// of its library's policy, and that the pass-through options only appear in
// identifiers with the library's prefix.
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
			case !slices.Contains(policy.types, ref):
				violations = append(violations, pos.String()+": "+owner+" names "+policy.name+" type "+ref)
			case strings.HasSuffix(ref, "Option") && !strings.Contains(owner, policy.prefix):
				violations = append(
					violations,
					pos.String()+": "+owner+" passes "+ref+" through without a "+policy.prefix+" prefix",
				)
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
					for _, name := range s.Names {
						if name.IsExported() && s.Type != nil {
							report(name.Name, s.Type)
						}
					}
				}
			}
		}
	}

	return violations
}

// reportType reports the third-party references in an exported type. A struct
// contributes only its exported fields, each under its own name, so the
// prefix rule reads the field's name.
func reportType(report func(string, ast.Node), spec *ast.TypeSpec) {
	st, ok := spec.Type.(*ast.StructType)
	if !ok {
		report(spec.Name.Name, spec.Type)

		return
	}

	for _, field := range st.Fields.List {
		for _, name := range field.Names {
			if name.IsExported() {
				report(spec.Name.Name+"."+name.Name, field.Type)
			}
		}
	}
}

// receiverExported reports whether a method's receiver type is exported.
func receiverExported(recv *ast.FieldList) bool {
	if len(recv.List) == 0 {
		return false
	}

	expr := recv.List[0].Type
	if star, ok := expr.(*ast.StarExpr); ok {
		expr = star.X
	}

	if idx, ok := expr.(*ast.IndexExpr); ok {
		expr = idx.X
	}

	ident, ok := expr.(*ast.Ident)

	return ok && ident.IsExported()
}
