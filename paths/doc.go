// Package paths locates nodes and tokens in a YAML document.
//
// A [Path] is a sequence of selectors from the document root, written in the
// YAMLPath syntax that goccy/go-yaml uses (`$.metadata.name`,
// `$.items[0]`). A path selects a node, which for a mapping entry is its
// value. Error highlighting and precise editing often need the key of an
// entry rather than its value, so [Path.Key] appends a `~` selector that
// picks the key instead, and every method reads which one from the Path:
//
//	p := paths.Root().Child("metadata", "name")
//	node, err := p.Node(doc)          // the value node
//	value, err := p.Token(doc)        // the token that starts the value
//	key, err := p.Key().Token(doc)    // the key token "name"
//
// Every method resolves within a single document, so callers working with
// multi-document files pick the document first.
//
// Each [Path] method walks the whole document to bind its aliases before it
// resolves. A caller that resolves many paths in one document creates one
// [Resolver], which binds them once, and resolves each path through it:
//
//	r := paths.NewResolver(doc)
//	for _, p := range fields {
//		tk, err := r.Token(p)
//		// ...
//	}
//
// # Resolution
//
// Selectors apply to the content of a node. An anchor (`&name`) or tag
// (`!!map`) on a value is transparent, and an alias (`*name`) resolves to
// the anchor it names. A mapping key lookup sees the entries a `<<` merge
// key brings in. A key the mapping defines itself wins over a merged one.
// A child selector matches an alias used as a key as it would the content
// of its anchor, so `*k` after `&k name` matches `.name`, and it matches a
// block scalar key (`|` or `>`) by its content. [Resolver.KeyName] gives
// the text a child selector matches a key node by, for a caller that
// builds a path from the keys it walks.
//
// [go.jacobcolvin.com/niceyaml.WithAllowDuplicateKeys] lets the parser
// accept a mapping that defines one key twice. A path then selects the
// later entry, whose value the decoder keeps, and a `..name` selector
// lists only that entry. A mapping may hold several `<<` keys the same
// way. The decoder merges all of them and a key lookup sees the entries of
// each, but `..name` visits only the inline mapping of the last one. When
// `<<` lists several sources the later source wins, which is the source
// the goccy/go-yaml decoder takes. YAML 1.1 gives the earlier source
// precedence instead. A token found through an alias or merge key sits
// where the anchor defines it, which is where the offending text is.
//
// The decoder handles one shape of own key in two ways. When a `<<` comes
// after a key of the same name, the decoder overwrites that key as it
// fills a map and reports a duplicate key as it fills a struct. A path
// keeps the mapping's own key either way.
//
// A mapping may also hold a merge key next to a real key with the text
// `<<`, such as an alias key whose anchor holds that text. The decoder
// keeps them apart, and so does a path. The `.'<<'` selector names the
// real key, so `..name` lists the entries under the real key and skips
// the merge key's inline mapping, since no path through `<<` reaches it.
//
// When several anchors share a name, an alias refers to the last one before
// it, which is the anchor the goccy/go-yaml decoder uses when it fills a
// map. The decoder records an anchor on the value of a `<<` merge key only
// for the aliases other merge keys name. An alias elsewhere refers to
// such an anchor only when no other anchor of its name comes before the
// alias. The decoder reads each mapping a `<<` merge key brings in again at
// the merge key, so the anchors inside that mapping count again there. It
// also looks up the aliases inside that mapping again, while a path keeps
// the anchor each alias refers to where the mapping defines it. When the
// decoder fills a struct it resolves aliases in the order of the struct's
// fields, which a path does not follow. An alias with no anchor of its name
// before it, or one that leads back to itself, has no content, so resolving
// through it returns an error wrapping [ErrAlias].
//
// The wildcard selectors `[*]` and `..name` select any number of nodes, so
// [Path.Token] and [Path.Node] reject them with [ErrWildcard]. Use
// [Path.Nodes] to list every match. [ErrNotFound] means nothing exists at
// the path, and [ErrAlias] means an alias on the path names no anchor or
// forms a cycle. When the document has no content to resolve in, the error
// wraps [ErrNoDocument] along with ErrNotFound.
//
// # Error Highlighting
//
// Pass a [Path] to [go.jacobcolvin.com/niceyaml.AtPath], which highlights
// the value at the path, or the key of the entry for a path from
// [Path.Key]. The error carries the path, and
// [go.jacobcolvin.com/niceyaml.Node.Bind] resolves it against the
// document:
//
//	err := niceyaml.NewError(
//		"invalid value",
//		niceyaml.AtPath(paths.Root().Child("spec", "replicas")),
//	)
//	fmt.Printf("%+v\n", doc.Bind(err))
//
// # Parsing Path Expressions
//
// Use [Parse] to read a path expression:
//
//	p, err := paths.Parse("$.metadata.name")
//
// [MustParse] panics on invalid input, so it suits package-level variables:
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
//	paths.Root().Child("spec").Key()                    // $.spec~
//
// A Path is a value that never changes, so callers can share a common
// prefix safely:
//
//	spec := paths.Root().Child("spec")
//	replicas := spec.Child("replicas") // $.spec.replicas
//	image := spec.Child("image")       // $.spec.image
//
// [Path.Join] appends one path to another, so a path written from a node
// of the document, such as one a check on a decoded value reports,
// resolves from the root:
//
//	spec.Join(paths.Root().Child("replicas")) // $.spec.replicas
//
// For the goccy/go-yaml API, [Path.YAMLPath] converts the selectors to a
// [*yaml.Path].
package paths
