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
// A caller that walks down a document resolves each step from the node
// above it with [Resolver.NodeFrom], so the path it resolves stays short
// however deep the walk goes.
//
// A [go.jacobcolvin.com/niceyaml.Node] holds a Resolver for its document,
// which its Resolver method returns, so a caller with a Node creates none.
//
// # Resolution
//
// Selectors apply to the content of a node. An anchor (`&name`) or tag
// (`!!map`) on a value is transparent, and an alias (`*name`) resolves to
// the anchor it names. A mapping key lookup sees the entries a `<<` merge
// key brings in. Where the mapping and a merge both define a key, the later
// entry in document order wins, as it does when the decoder fills a map.
// A child selector matches an alias used as a key as it would the content
// of its anchor, so `*k` after `&k name` matches `.name`, and it matches a
// block scalar key (`|` or `>`) by its content. [Resolver.KeyName] gives
// the text a child selector matches a key node by, for a caller that
// builds a path from the keys it walks. A key with no name, such as an
// alias key with no anchor before it, matches no child selector, so a
// `..name` selector skips its entry and everything below that entry.
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
// When the decoder fills a struct, it reports a key that both the mapping
// and a merge define as a duplicate, unless
// [go.jacobcolvin.com/niceyaml.WithAllowDuplicateKeys] allows it. It then
// keeps the later entry, as it does for a map. A `..name` selector skips an
// entry of the mapping when a `<<` after it brings in the same key, since a
// path through that key selects the merged entry.
//
// A mapping may also hold a merge key next to a real key with the text
// `<<`, such as an alias key whose anchor holds that text. The decoder
// keeps them apart, and so does a path. A merge brings in such a real key
// as it does any other. The `.'<<'` selector names the real key, whether
// the mapping holds it or a merge brings it in, so `..name` skips the
// merge key's inline mapping, since no path through `<<` reaches it.
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
// through it returns an error wrapping [ErrAlias]. Unless a `<<` merge key
// names it, an alias inside the content of the anchor it refers to leads
// back to itself, so the decoder reads it as null. [Path.Token] still finds
// the token of such an alias.
//
// A `.*` selector selects every entry of a mapping, as `[*]` selects every
// element of a sequence, so one path reaches each job of a workflow or
// each service of a Compose file. It selects the entries a `.name`
// selector resolves in that mapping, one for each name. It thus lists an
// entry a `<<` merge key brings in, at the place of that merge key, and
// the later of two entries with one key. It leaves out the merge key
// itself and a key with no name. The path of each match spells the key as
// the source does, so the entry under the key `0x10` lies at
// `$.ports.0x10`, where a decoded map holds it under 16.
//
// A `..*` selector selects every node below the one it starts from, at any
// depth, so one path reaches each key and each element of a document. It
// visits what a `..name` selector visits, so it lists each node once,
// where the source writes it, and does not follow aliases. It leaves out
// the entry of a `<<` merge key and the sources that key lists, as `.*`
// leaves out the merge key, and lists the entries of a mapping written
// inline there.
//
// The wildcard selectors `.*`, `[*]`, `..name`, and `..*` select any
// number of nodes, so [Path.Token] and [Path.Node] reject them with
// [ErrWildcard]. Use [Path.Nodes] to list every match. [ErrNotFound] means
// nothing exists at the path, and [ErrAlias] means an alias on the path
// names no anchor or forms a cycle. When the document has no content to
// resolve in, the error wraps [ErrNoDocument] along with ErrNotFound.
//
// A `[*]` selector lists the elements of a sequence once for each alias
// that leads to it, as a `.*` selector lists the entries of a mapping, so
// nested aliases can make a path select many times the nodes the document
// holds. A resolve stops with
// [ErrExcessiveAliasing] once the aliases make up too much of what one
// selector reaches, under the rule gopkg.in/yaml.v3 applies to the aliases
// in a document it decodes.
//
// A key lookup reads the mappings that `<<` merge keys bring in until it
// finds the key, so a lookup on a chain of merges can read the whole
// chain. It reads a list of merge sources again for each mapping that
// merges that list through an alias. A `..name` selector looks up each
// key that a later merge key may override. It also looks up `<<` at the
// last merge key of each mapping it walks, to learn whether a merge brings
// in a real `<<` key. That lookup reads the whole merge chain behind the
// mapping, whatever name the selector searches for. A `.name` after a
// `[*]` looks up its key in each element, and a `.*` looks up each key of
// its mapping and of the mappings it merges. Each of these can make one
// selector read a chain many times. A resolve stops with
// [ErrExcessiveMerging] once those reads come to many times the nodes the
// document holds. One lookup alone can read that many, so [Path.Node] and
// [Path.Token] return the error as well.
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
//	paths.Root().Child("jobs").ChildAll()               // $.jobs.*
//	paths.Root().Recursive("name")                      // $..name
//	paths.Root().RecursiveAll()                         // $..*
//	paths.Root().Child("spec").Key()                    // $.spec~
//
// A Path is a value that never changes, so callers can share a common
// prefix safely:
//
//	spec := paths.Root().Child("spec")
//	replicas := spec.Child("replicas") // $.spec.replicas
//	image := spec.Child("image")       // $.spec.image
//
// [Path.Join] appends one or more paths to another, so a path written
// from a node of the document, such as one a check on a decoded value
// reports, resolves from the root:
//
//	spec.Join(paths.Root().Child("replicas")) // $.spec.replicas
//
// [Path.Parent] drops the last selector, so a caller walks from a path
// up to the root, and [Path.CutPrefix] drops the leading ones:
//
//	replicas.Parent() // $.spec, true
//
// # Reading Selectors
//
// [Path.Selectors] yields each selector of a path as a [Selector], whose
// kind, name, and index a caller reads without parsing [Path.String]. A
// caller builds another form of the path from them, such as a JSON
// Pointer or a breadcrumb. [Path.Last] gives the last selector alone,
// which for a match of `.*` or `[*]` names the entry or element the match
// is:
//
//	for _, m := range matches { // $.jobs.*
//		sel, _ := m.Path.Last()
//		fmt.Println(sel.Name) // build, test, ...
//	}
//
// The name of a selector is the text it matches a key by. In a path the
// library resolves, that is the text [Resolver.KeyName] gives the key, so
// the entry under the key `0x10` has the name 0x10, where a decoded map
// and the JSON form of the document hold the key 16.
//
// # Comparing and Encoding
//
// A Path holds its selectors in a slice, so the `==` operator does not
// compile for it and it cannot key a map. [Path.Equal] compares two paths
// selector by selector, and [Path.String] gives the key for a map:
//
//	if p.Equal(replicas) {
//		// ...
//	}
//
//	seen := map[string]bool{p.String(): true}
//
// A Path implements [encoding.TextMarshaler] and
// [encoding.TextUnmarshaler] with the same expression, so a report in
// JSON writes a path as a string, and a struct field of type Path decodes
// from one:
//
//	type Rule struct {
//		Path paths.Path `json:"path"`
//	}
//
// For the goccy/go-yaml API, [Path.YAMLPath] converts the selectors to a
// [*yaml.Path]. That syntax has no `.*` or `..*` selector, so a path that
// holds one does not convert, and YAMLPath returns an error wrapping
// [ErrNoYAMLPath].
package paths
