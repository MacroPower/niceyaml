package paths

import (
	"errors"

	"github.com/goccy/go-yaml/ast"

	"go.jacobcolvin.com/niceyaml/internal/pathshook"
)

// init sets what the paths package hands to the internal packages of
// niceyaml through [pathshook], as that package describes.
//
//nolint:gochecknoinits // The hooks reach no API, so only init can set them.
func init() {
	pathshook.MappingKeys = func(r any, mapping *ast.MappingNode) (map[string]int, []int) {
		res, _ := r.(*Resolver) //nolint:errcheck // The hook takes a *Resolver.
		if res == nil || res.resolver == nil || mapping == nil {
			return nil, nil
		}

		keys := res.resolver.mappingKeys(mapping)

		return keys.names, keys.merges
	}

	pathshook.UnresolvedAlias = func(err error) (*ast.AliasNode, bool) {
		a, ok := errors.AsType[*aliasError](err)
		if !ok || a.alias == nil {
			return nil, false
		}

		return a.alias, true
	}
}
