// Package paths locates nodes and tokens in a YAML document.
//
// A [Path] is a sequence of selectors from the document root, written in the
// YAMLPath syntax that goccy/go-yaml uses (`$.metadata.name`,
// `$.items[0]`). A path selects a node, which for a mapping entry is its
// value. Error highlighting and precise editing often need one token of an
// entry rather than the whole node, so a Path resolves to either token of
// the entry it selects:
//
//	p := paths.Root().Child("metadata", "name")
//	node, err := p.Node(doc)      // the value node
//	value, err := p.Token(doc)    // the token that starts the value
//	key, err := p.KeyToken(doc)   // the key token "name"
//
// Every method resolves within a single document, so callers working with
// multi-document files pick the document first.
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
// [Path] is directly usable with [niceyaml.WithPath], which highlights the
// value at the path, and [niceyaml.WithKey], which highlights the key of the
// entry. The error carries the path, and [niceyaml.Document.Bind] resolves
// it against the document:
//
//	err := niceyaml.NewError(
//		"invalid value",
//		niceyaml.WithPath(paths.Root().Child("spec", "replicas")),
//	)
//	fmt.Printf("%+v\n", doc.Bind(err))
//
// # Parsing Path Expressions
//
// Use [Parse] to read a path expression:
//
//	p, err := paths.Parse("$.metadata.name")
//
// [MustParse] panics on invalid input, useful for package-level variables:
//
//	var namePath = paths.MustParse("$.items[0].name")
//
// [Path.String] returns the expression, so Parse(p.String()) yields an
// equal path.
//
// # Building Paths
//
// Use [Root] to start at the document root and chain selectors:
//
//	paths.Root().Child("items").Index(0).Child("name")  // $.items[0].name
//	paths.Root().Child("spec").IndexAll()               // $.spec[*]
//	paths.Root().Recursive("name")                      // $..name
//
// A Path is a value that never changes, so a common prefix can be shared
// safely:
//
//	spec := paths.Root().Child("spec")
//	replicas := spec.Child("replicas") // $.spec.replicas
//	image := spec.Child("image")       // $.spec.image
//
// For the goccy/go-yaml API, [Path.YAMLPath] converts the selectors to a
// [*yaml.Path].
package paths
