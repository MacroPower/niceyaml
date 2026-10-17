// Package paths locates nodes and tokens in a YAML document.
//
// A [Path] is a sequence of selectors that starts at `$` or `@`. Paths use
// the YAMLPath syntax of goccy/go-yaml (`$.metadata.name`, `$.items[0]`),
// with one difference. A quoted name writes a control character as an
// escape, such as `\n`, but goccy/go-yaml writes the character itself. The
// escape keeps a path that names a key with a line feed on one line.
//
// A path selects a node, and for a mapping entry that node is the value.
// Error highlighting and precise editing often need the key of an entry
// rather than its value. [Path.Key] appends a `~` selector that picks the
// key instead, and a [Resolver] reads from the path which of the two to
// select:
//
//	p := paths.Doc().Child("metadata", "name")
//	r := paths.NewResolver(doc)
//	node, err := r.Node(p)            // the value node
//	value, err := r.Token(p)          // the token of the value
//	key, err := r.Token(p.Key())      // the key token "name"
//
// [Resolver.Token] gives the one token a path points at, and an error at
// the path binds to that token. A scalar is its own token. A mapping or a
// sequence spans many lines, so its path points at the token that
// introduces it. The value of a mapping entry points at the key of the
// entry, and an element of a block sequence points at its "-". Any other
// flow mapping or flow sequence points at its own "{" or "[". A block
// mapping or a block sequence at the root points at its first key or its
// first element:
//
//	spec:            # $.spec points at "spec"
//	  ports:         # $.spec.ports points at "ports"
//	    - name: web  # $.spec.ports[0] points at "-"
//	      port: 80   # $.spec.ports[0].port points at "80"
//
// A Resolver resolves within a single document, so a caller with a
// multi-document file must pick the document first. [NewResolver] walks
// the document once to bind its aliases. Every path resolves through
// those bindings, so a caller should create one Resolver for a document
// and resolve each of its paths through it:
//
//	r := paths.NewResolver(doc)
//	for _, p := range fields {
//		tk, err := r.Token(p)
//		// ...
//	}
//
// A caller that walks down a document can resolve each step from the node
// above it with [Resolver.NodeFrom]. Each path then stays short, however
// deep the walk goes.
//
// A [go.jacobcolvin.com/niceyaml.Node] holds a Resolver for its document,
// and its Resolver method returns it. A caller with a Node can use that
// Resolver rather than create one.
//
// # Starting Points
//
// A path starts at one of two points, as in RFC 9535 JSONPath. A `$` path
// reads from the document root, wherever it resolves. An `@` path reads
// from the current node: the node that resolves the path, binds an error
// that carries it, or rebases it. [Doc] starts a path at `$`, and
// [Current] at `@`:
//
//	paths.Doc().Child("spec", "replicas") // $.spec.replicas
//	paths.Current().Child("replicas")     // @.replicas
//
// A check written for a type reports `@` paths, which read from the value
// it checked. The check thus reports the right place wherever the value
// sits in a document. The [go.jacobcolvin.com/niceyaml.Node] of that value
// resolves an `@` path from itself and a `$` path from the root of its
// document. Every path that the niceyaml packages return starts at `$`,
// including the path of a Node and the path of a bound error. Any Node of
// the document resolves such a path to the same node.
//
// Except for [Resolver.NodeFrom], the [Resolver] methods that resolve a
// path start at the document root. They read an `@` path from the root,
// as they read a `$` path, and the paths they return start at `$`.
// NodeFrom takes the node that an `@` path reads from.
//
// # Resolution
//
// Selectors apply to the content of a node. An anchor (`&name`) or tag
// (`!!map`) on a value is transparent, and an alias (`*name`) resolves to
// the anchor it names. A mapping key lookup sees the entries a `<<` merge
// key brings in. Where the mapping and a merge both define a key, the
// later entry in document order wins, as it does when the decoder fills a
// map.
//
// A child selector matches an alias key by the content of its anchor, so
// `*k` after `&k name` matches `.name`. It matches a block scalar key (`|`
// or `>`) by its content. [Resolver.KeyName] gives the text that a child
// selector matches a key node by. A caller can use it to build a path from
// the keys it walks. Some keys do not have a name, such as an alias key
// that does not have an anchor before it. A child selector never matches
// such a key. A `..name` selector thus skips its entry and everything
// below that entry.
//
// [go.jacobcolvin.com/niceyaml.WithAllowDuplicateKeys] lets the parser
// accept a mapping that defines one key twice. A path then selects the
// later entry, whose value the decoder keeps, and a `..name` selector
// lists only that entry. A mapping can hold several `<<` keys the same
// way. The decoder merges all of them and a key lookup sees the entries of
// each, but `..name` visits only the inline mapping of the last one.
//
// When `<<` lists several sources, the later source wins, as it does in
// the goccy/go-yaml decoder. YAML 1.1 gives the earlier source precedence
// instead. A token found through an alias or merge key sits where the
// anchor defines it. That place holds the offending text.
//
// The decoder also keeps the later entry when it fills a struct. When a
// `<<` after an entry of a mapping brings in the same key, a path through
// that key selects the merged entry. A `..name` selector thus skips the
// entry of the mapping.
//
// A mapping can also hold a merge key next to a real key with the text
// `<<`, such as an alias key whose anchor holds that text. The decoder
// keeps the two apart, and so does a path. A merge brings in such a real
// key as it does any other. The `.'<<'` selector names the real key,
// whether the mapping holds it or a merge brings it in. Paths through `<<`
// never reach the inline mapping of the merge key, so `..name` skips that
// mapping.
//
// When several anchors share a name, an alias refers to the last one
// before it. The goccy/go-yaml decoder uses that anchor when it fills a
// map. The decoder records an anchor on the value of a `<<` merge key only
// for the aliases that other merge keys name. An alias elsewhere refers to
// such an anchor only when the document does not define another anchor of
// its name before the alias.
//
// At each `<<` merge key, the decoder reads again each mapping that the
// merge key brings in. The anchors inside that mapping thus count again
// there. The decoder also looks up the aliases inside that mapping again.
// A path instead keeps each of those aliases bound to the anchor it refers
// to where the mapping defines it. When the decoder fills a struct, it
// resolves aliases in the order of the struct's fields. A path does not
// follow that order.
//
// An alias does not resolve when it leads back to itself or when the document
// does not define an anchor of its name before it. A resolve through such an
// alias returns an error wrapping [ErrAlias]. An alias inside the content of
// the anchor it refers to leads back to itself, unless a `<<` merge key names
// it. The decoder reads such an alias as null, and [Resolver.Token] still
// finds its token.
//
// A document can read the anchors of reference documents that the Resolver
// does not hold, such as those of niceyaml.WithReferences.
// [WithReferenceAnchors] names those anchors. A resolve through an alias
// that reads one returns an error that wraps [ErrReferenceAlias] as well as
// ErrAlias.
//
// A `.*` selector selects every entry of a mapping, as `[*]` selects every
// element of a sequence. One path thus reaches each job of a workflow or
// each service of a Compose file. The `.*` selector selects the entries
// that a `.name` selector resolves in that mapping, one for each name. It
// lists each entry that a `<<` merge key brings in, at the place of that
// merge key. Of two entries with one key, it lists the later. It leaves
// out the merge key itself and a key that does not have a name.
//
// The path of each match spells the key as the source does. The entry
// under the key `0x10` thus lies at `$.ports.0x10`, but a decoded map
// holds it at the key 16.
//
// A `..*` selector selects every node below the one it starts from, at any
// depth. One path thus reaches each entry and each element of a document.
// The `..*` selector visits what a `..name` selector visits, so it lists
// each node once, where the source writes it, and does not follow
// aliases. It leaves out the entry of a `<<` merge key and the sources
// that key lists, as `.*` leaves out the merge key. It still lists the
// entries of a mapping written inline there.
//
// The wildcard selectors `.*`, `[*]`, `..name`, and `..*` select any
// number of nodes, so [Resolver.Token] and [Resolver.Node] reject them
// with [ErrWildcard]. Use [Resolver.Nodes] to list every match.
// [ErrNotFound] means that the document does not hold a node at the path.
// [ErrAlias] means an alias on the path forms a cycle or does not have an
// anchor of its name before it, and [ErrReferenceAlias] means that such an
// alias reads a reference document.
//
// When the document does not have content to resolve in, Resolver.Token
// and Resolver.Node return an error that wraps both [ErrNoDocument] and
// ErrNotFound. Resolver.Nodes returns that error only for a nil document.
// In any other document without content, it returns an empty result, as
// it does for a path that does not select any node.
//
// A `[*]` selector lists the elements of a sequence once for each alias
// that leads to the sequence. A `.*` selector lists the entries of a
// mapping the same way. Nested aliases can thus make a path select many
// times the number of nodes in the document. A resolve stops with
// [ErrExcessiveAliasing] when the aliases make up too large a share of
// what one selector reaches. The limit on that share is the one that
// gopkg.in/yaml.v3 applies to the aliases in a document it decodes.
//
// A key lookup reads the mappings that `<<` merge keys bring in until it
// finds the key. A lookup on a chain of merges can thus read the whole
// chain. It reads a list of merge sources again for each mapping that
// merges that list through an alias. A `..name` selector looks up each
// key that a later merge key might override. It also looks up `<<` at the
// last merge key of each mapping it walks, to learn whether a merge brings
// in a real `<<` key. That lookup reads the whole merge chain behind the
// mapping, whatever name the selector searches for.
//
// A `.name` after a `[*]` looks up its key in each element, and a `.*`
// looks up each key of its mapping and of the mappings it merges. Each of
// these can make one selector read a chain many times. A resolve stops
// with [ErrExcessiveMerging] when those reads come to many times the
// number of nodes in the document. One lookup alone can read that many,
// so [Resolver.Node] and [Resolver.Token] return the error as well.
//
// # Error Highlighting
//
// Pass a [Path] to [go.jacobcolvin.com/niceyaml.AtPath], which highlights
// the value at the path, or the key of the entry for a path from
// [Path.Key]. The error carries the path, and
// [go.jacobcolvin.com/niceyaml.Node.Bind] resolves it against the
// document. Bind reads an `@` path from the Node that binds the error:
//
//	err := niceyaml.NewError(
//		"invalid value",
//		niceyaml.AtPath(paths.Current().Child("replicas")),
//	)
//	fmt.Printf("%+v\n", specNode.Bind(err)) // $.spec.replicas
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
// An expression starts with `$` or `@`, so "@.name" parses to a path that
// starts at the current node, as [Current] does.
//
// [Path.String] returns the expression, so Parse(p.String()) yields an
// equal path.
//
// # Building Paths
//
// Use [Doc] to start at the document root, or [Current] to start at the
// current node, and chain selectors:
//
//	paths.Doc().Child("items").Index(0).Child("name")  // $.items[0].name
//	paths.Doc().Child("spec").IndexAll()               // $.spec[*]
//	paths.Doc().Child("jobs").ChildAll()               // $.jobs.*
//	paths.Doc().Recursive("name")                      // $..name
//	paths.Doc().RecursiveAll()                         // $..*
//	paths.Doc().Child("spec").Key()                    // $.spec~
//	paths.Current().Child("name")                      // @.name
//
// [Path.Index] panics on a negative index, such as the -1 that a search
// returns when it does not find a match.
//
// A Path is a value that never changes, so callers can share a common
// prefix safely:
//
//	spec := paths.Doc().Child("spec")
//	replicas := spec.Child("replicas") // $.spec.replicas
//	image := spec.Child("image")       // $.spec.image
//
// [Path.Join] appends one or more paths to another. Appending an `@` path
// written from a node of the document to the path of that node gives a
// path that resolves from the root. Checks on decoded values report such
// `@` paths. A `$` path reads from the root already, so joining one
// replaces the path before it:
//
//	spec.Join(paths.Current().Child("replicas")) // $.spec.replicas
//	spec.Join(paths.Doc().Child("kind"))         // $.kind
//
// [Path.Parent] drops the last selector, so a caller can walk from a path
// up to its `$` or `@`. [Path.CutPrefix] drops the selectors of a prefix
// and returns the rest as an `@` path:
//
//	replicas.Parent()        // $.spec, true
//	replicas.CutPrefix(spec) // @.replicas, true
//
// # Reading Selectors
//
// [Path.Selectors] yields each selector of a path as a [Selector]. A
// caller can read the kind, name, and index of each without parsing
// [Path.String]. From them, a caller can build another form of the path,
// such as a JSON Pointer or a breadcrumb. [Path.Last] gives the last
// selector alone. For a match of `.*` or `[*]`, that selector names the
// matched entry or element:
//
//	for _, m := range matches { // $.jobs.*
//		sel, _ := m.Path.Last()
//		fmt.Println(sel.Name) // build, test, ...
//	}
//
// The name of a selector is the text that it matches a key by. In a path
// that the library resolves, the name is the text that [Resolver.KeyName]
// gives the key. The entry under the key `0x10` thus has the name 0x10,
// but a decoded map and the JSON form of the document hold the key 16.
//
// # Comparing and Encoding
//
// A Path holds its selectors in a slice, so the `==` operator does not
// compile for it and it cannot key a map. [Path.Equal] compares where two
// paths start and their selectors one by one, and [Path.String] gives the
// key for a map:
//
//	if p.Equal(replicas) {
//		// ...
//	}
//
//	seen := map[string]bool{p.String(): true}
//
// A Path implements [encoding.TextMarshaler] and
// [encoding.TextUnmarshaler] with the same expression. A report in JSON
// thus writes a path as a string, and a struct field of type Path decodes
// from one:
//
//	type Rule struct {
//		Path paths.Path `json:"path"`
//	}
//
// For the goccy/go-yaml API, [Path.YAMLPath] converts the selectors to a
// [*yaml.Path]. That syntax does not support `@`, so YAMLPath writes `$`
// in its place. It does not support the `.*` or `..*` selectors either,
// so YAMLPath returns an error wrapping [ErrNoYAMLPath] for a path that
// holds one.
package paths
