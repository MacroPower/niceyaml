// Package paths locates nodes and tokens in a YAML document.
//
// A [Path] is a sequence of selectors from the document root, written in the
// YAMLPath syntax that goccy/go-yaml uses (`$.metadata.name`,
// `$.items[0]`), plus a [Part]. Standard path expressions point at nodes, but
// error highlighting and precise editing often need one token of a mapping
// entry rather than the whole node, so the Part says whether a path refers to
// the node itself, the entry's key, or the entry's value:
//
//	keyPath := paths.Root().Child("metadata", "name").Key()
//	valPath := paths.Root().Child("metadata", "name").Value()
//
// Both paths print as `$.metadata.name` and resolve to the same node, but
// [Path.Token] returns different tokens: the key token "name" for the first
// and the value token for the second. [Path.Node] ignores the Part and
// returns the node. Both resolve within a single document, so callers
// working with multi-document files pick the document first.
//
// # Resolution
//
// Selectors apply to the content of a node, so an anchor (`&name`) or tag
// (`!!map`) on a value is transparent, an alias (`*name`) resolves to the
// anchor it names, and a mapping key lookup sees the entries a `<<` merge
// key brings in. A key the mapping defines itself wins over a merged one,
// and when `<<` lists several sources the earlier source wins. A token
// found through an alias or merge key sits where the anchor defines it,
// which is where the offending text is.
//
// When several anchors share a name, an alias refers to the last one before
// it, which is the anchor the goccy/go-yaml decoder uses. An alias with no
// anchor of its name before it, or one that leads back to itself, has no
// content, so resolving through it returns an error wrapping [ErrAlias].
//
// The wildcard selectors `[*]` and `..name` select any number of nodes, so
// [Path.Token] and [Path.Node] reject them with [ErrWildcard]; use
// [Path.Nodes] to list every match. [ErrNotFound] means nothing exists at
// the path, and [ErrAlias] means an alias on the path names no anchor or
// forms a cycle. When the document has no content to resolve in, the error
// wraps [ErrNoDocument] along with ErrNotFound.
//
// # Integration with niceyaml.Error
//
// [Path] is directly usable with [niceyaml.WithPath] to highlight either keys
// or values in error messages. The error carries the path, and
// [niceyaml.Source.Bind] resolves it against the document:
//
//	err := niceyaml.NewError(
//		"invalid value",
//		niceyaml.WithPath(paths.Root().Child("spec", "replicas").Value()),
//	)
//	fmt.Printf("%+v\n", source.Bind(err))
//
// # Parsing Path Expressions
//
// Use [Parse] to read a path expression. The result targets [PartNode];
// derive the other parts with [Path.Key] and [Path.Value]:
//
//	p, err := paths.Parse("$.metadata.name")
//	keyPath := p.Key()     // targets the key
//	valPath := p.Value()   // targets the value
//
// [MustParse] panics on invalid input, useful for package-level variables:
//
//	var namePath = paths.MustParse("$.items[0].name").Value()
//
// [Path.String] returns the expression without the part, so
// Parse(p.String()) yields a path with the same selectors.
//
// # Building Paths
//
// Use [Root] to start at the document root and chain selectors. The result
// targets [PartNode] until [Path.Key] or [Path.Value] picks a part:
//
//	paths.Root().Child("items").Index(0).Child("name")        // $.items[0].name
//	paths.Root().Child("items").Index(0).Child("name").Key()  // the same, key token
//	paths.Root().Child("spec").IndexAll().Value()             // $.spec[*]
//	paths.Root().Recursive("name").Value()                    // $..name
//
// A Path is a value that never changes, so a common prefix can be shared
// safely:
//
//	spec := paths.Root().Child("spec")
//	replicas := spec.Child("replicas").Value()  // $.spec.replicas
//	image := spec.Child("image").Value()        // $.spec.image
//
// For the goccy/go-yaml API, [Path.YAMLPath] converts the selectors to a
// [*yaml.Path].
package paths
