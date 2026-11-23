package niceyaml

import (
	"github.com/goccy/go-yaml/ast"
	"github.com/goccy/go-yaml/token"

	"go.jacobcolvin.com/niceyaml/internal/astnode"
	"go.jacobcolvin.com/niceyaml/internal/yamldecode"
	"go.jacobcolvin.com/niceyaml/paths"
)

// knownKeys returns the token of each known key of cfg that the document
// of n holds below the node of n. An `@` known key reads from the node of
// n, and a known key with a wildcard names every entry that it selects,
// as [WithAllowedFields] describes. A known key that a path reaches
// through an alias or a `<<` merge key lies in the anchored mapping. It
// returns nil for a decode under [UnknownFieldsIgnore], which does not
// report any unknown field.
func (n *Node) knownKeys(cfg decodeConfig) map[*token.Token]bool {
	if cfg.unknownFields == UnknownFieldsIgnore || len(cfg.knownKeys) == 0 {
		return nil
	}

	res := n.doc.pathResolver()
	keys := make(map[*token.Token]bool, len(cfg.knownKeys))

	for _, p := range cfg.knownKeys {
		if last, ok := p.Last(); ok && last.Kind == paths.SelectorKey {
			p, _ = p.Parent()
		}

		matches, err := res.Matches(n.base.Join(p))
		if err != nil {
			continue
		}

		for _, m := range matches {
			rel, ok := m.Path.CutPrefix(n.base)
			if !ok || rel.Len() == 0 {
				continue
			}

			// Only an entry of a mapping has a key to admit.
			if last, _ := rel.Last(); last.Kind != paths.SelectorChild {
				continue
			}

			tk, err := res.Token(m.Path.Key())
			if err == nil && tk != nil {
				keys[tk] = true
			}
		}
	}

	return keys
}

// allowKnownKeys returns the option that has the decoder of a decode of n
// with cfg accept each known key of cfg, as [Node.knownKeys] finds them.
// The decoder accepts such a key at every read of its mapping, through
// each alias and each `<<` merge key, wherever a struct does not have a
// field for it. It matches the token of each key that it reads, so a key
// of a reference document from [WithReferences] at the line and the
// column of a known key stays unknown. It returns nil when cfg does not
// admit any key.
func (n *Node) allowKnownKeys(cfg decodeConfig) yamldecode.DecodeOption {
	known := n.knownKeys(cfg)
	if len(known) == 0 {
		return nil
	}

	return yamldecode.AllowUnknownField(func(key ast.Node, _ string) bool {
		// The decoder passes the key as the mapping writes it. The path
		// to the key points under the `?` of an explicit key and under the
		// anchors and tags on it, as [astnode.KeyToken] does.
		if explicit, ok := key.(*ast.MappingKeyNode); ok {
			key = explicit.Value
		}

		return known[astnode.PathToken(key, astnode.Parent{})]
	})
}
