// Package paths locates nodes and tokens in a YAML document.
//
// A [Path] is a sequence of selectors that starts at `$` or `@`, as in
// `$.metadata.name` or `$.items[0]`. The syntax is close to the YAMLPath
// syntax of goccy/go-yaml, but each of the two reads text that the other
// rejects. The section "go-yaml Syntax" lists where the two differ.
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
// the path binds to that token. A scalar is its own token, and a value that
// holds a tag alone, as in "a: !!null", points at its tag. A mapping or a
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
// A `~` selects a key or nothing. A sequence element and the root do not
// have a key. The path `$..*~` thus selects the key of each entry that
// `..*` selects and none of the sequence elements. [Path.RecursiveAll]
// names the entries that `..*` leaves out, such as the entry of a `<<`
// merge key. A path that ends in a `~` after an element or the root still
// points where the path before the `~` does. An error at that path thus
// has a place in the source. No selector after a `~` selects anything.
//
// A path holds at most one `~` in a row. [Path.Key] returns a path that
// ends in `~` as it is, and [Parse] rejects `~~`. [Path.IsKey] reports
// whether a path ends in `~`, and [Path.Value] drops that `~`. A caller
// that gets the path of a key from the library, such as the path of a
// position in a key, reads the name of its entry from Path.Value:
//
//	if p.IsKey() { // $.jobs.build~
//		sel, _ := p.Value().Last()      // .build
//		holder, _ := p.Value().Parent() // $.jobs
//	}
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
// A caller that visits every value of a document does not need a walk of
// its own. [go.jacobcolvin.com/niceyaml.Node.Nodes] with
// `paths.Doc().RecursiveAll()` lists each value with its path. A caller
// that holds one node, such as one that [Resolver.MergeSources] returns,
// gets its path from [go.jacobcolvin.com/niceyaml.Node.PathOf]. The
// position of the token of a node does not name every node. The parser
// gives a block mapping the token of its first entry, such as its ":",
// and a block sequence the "-" of its first element. The path at that position
// thus names the first entry or element.
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
// An `@` path needs a current node to read from. [Resolver.NodeFrom] and
// [Resolver.MatchesFrom] take that node. The other [Resolver] methods
// that resolve a path start at the document root. They return an error
// wrapping [ErrNoCurrentNode] for an `@` path. To resolve an `@` path
// below a node with them, join the `$` path of the node with it:
//
//	spec := paths.Doc().Child("spec")
//	tk, err := r.Token(spec.Join(paths.Current().Child("replicas")))
//
// [Doc] joined with an `@` path reads it from the root, and the join
// leaves a `$` path as it is. The paths that [Resolver.Matches] returns
// start at `$`, and those of MatchesFrom start at `@` for an `@` path.
//
// # Resolution
//
// Selectors apply to the content of a node. An anchor (`&name`) on a value
// is transparent, and an alias (`*name`) resolves to the anchor it names.
// A tag is transparent when the decoder reads the collection under it as
// a collection, as it does for a local tag such as `!t` and for `!!map`. A
// tag that the decoder reads as converting a collection into a value of
// another kind, such as `!!str` or `!!null`, holds a scalar. A selector
// thus selects nothing below it, on the collection itself or on an alias
// of one, as in `!!str *k`. A `<<` merge key reads its mappings through
// every tag. A mapping key lookup sees the entries a `<<` merge key brings
// in. Where the mapping and a merge both define a key, the
// later entry in document order wins, as it does when the decoder fills a
// map.
//
// A caller that walks the document itself reads the node that selectors
// apply to with [Resolver.Content]. It returns a mapping or a sequence
// exactly where a selector applies below the node, even when the
// collection is empty. Where a tag converts a collection, as `!!str *k`
// does, it returns that tag, because the decoder reads a scalar there. A
// tag on a scalar, such as `!!str 5`, gives the scalar.
//
// A child selector matches an alias key by the content of its anchor, so
// `*k` after `&k name` matches `.name`. It matches a block scalar key (`|`
// or `>`) by its content. [Resolver.KeyName] gives the text that a child
// selector matches a key node by. A caller can use it to build a path from
// the keys it walks. Some keys do not have a name, such as an alias key
// that reads no anchor. A child selector never matches such a key. A
// `..name` selector thus skips its entry and everything below that entry.
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
// before it. An anchor counts where it starts and again where its content
// ends, so for the aliases after it, an anchor wins over an anchor of its
// name inside its content. An anchor on the value of a `<<` merge key, or
// on an element of a sequence there, counts for the aliases that other
// merge keys name. An alias elsewhere refers to such an anchor only when
// the document does not define another anchor of its name before the
// alias. At each `<<` merge key, the anchors inside each mapping that the
// merge key brings in count again. Each alias keeps the anchor that this
// rule binds it to, wherever a decode reads it, as at a merge key that
// brings in the mapping that holds the alias.
//
// The decoder rejects a merge key that names an alias inside the content
// of the anchor it refers to, as in "a: &a [{<<: *a}]". Such a merge key
// brings in nothing, and the anchors behind it do not count again. The
// decoder also rejects a cycle of merges, as in
// "m: {<<: [&p {<<: *q}, &q {<<: *p}]}". In such a cycle the merges of a
// mapping lead back to that mapping. The merge key that closes the cycle
// brings in nothing, also where a later merge key brings in the mapping
// that holds it.
//
// The decodes of go.jacobcolvin.com/niceyaml read each alias at the anchor
// that a Resolver binds it to, and so do the keys that a decode names and
// the edits of go.jacobcolvin.com/niceyaml/edit. The decoder of
// goccy/go-yaml binds some aliases of reused anchor names otherwise, so
// yaml.Unmarshal reads some documents apart from niceyaml:
//
//   - An alias after an anchor on a merge value reads the last other anchor
//     of its name in the document, even one after the alias. In
//     "m: {<<: &x {p: 1}}", "b: *x", and "c: &x 2", go-yaml reads b as 2,
//     and niceyaml reads b as {p: 1}.
//   - At a merge key, go-yaml looks up again each alias inside the mapping
//     that the merge key brings in. In "k1: &k 1", "b: &b {v: *k}",
//     "k2: &k 2", and "m: {<<: *b}", go-yaml reads m.v as 2, and niceyaml
//     reads m.v as 1.
//   - An alias inside the content of an anchor of its name reads null in
//     go-yaml, even after an inner anchor of that name. In
//     "r1: &x0 {z1: [&x0 d, *x0]}", go-yaml reads z1 as [d, null], and
//     niceyaml reads z1 as [d, d].
//   - An alias key inside the content of its anchor reads that anchor
//     where a merge key brings it in. In "b: &b {*b : 1}" and
//     "m: {<<: *b}", go-yaml reads m as {"map[null:1]": 1}, and niceyaml
//     reads m as {null: 1}.
//
// When the decoder fills a struct, it resolves aliases in the order of the
// struct's fields. A path does not follow that order.
//
// An alias does not resolve when it leads back to itself or when the document
// does not define an anchor of its name before it. A resolve through such an
// alias returns an [*AliasError] wrapping [ErrAlias], which reads as in
// "resolve $.a.b: alias does not resolve: *x forms a cycle". The AliasError
// carries the alias, and [AliasError.Position] gives its position. The
// error wraps the reason too, [ErrAliasCycle] or [ErrAnchorMissing]. An
// alias inside the content of the anchor it refers to leads back to
// itself, unless a `<<` merge key names it. The decoder reads such an
// alias as null. A path that ends at the alias thus selects it, or the
// tag on it, as it selects an alias that reads a reference document. So
// does a path that ends at an alias of an anchor
// that holds such an alias, as "&x !t *x" does. A `..*` selector lists
// such an alias and does not descend below it. A `.*`, `[*]`, or `..`
// selector at the alias selects nothing, as on any other null. Any other
// selector below the alias returns an error wrapping ErrAlias.
// [Resolver.Token] finds the token of every alias at the end of a path.
//
// A document can read the anchors of reference documents that the Resolver
// does not hold, such as those of niceyaml.WithReferences.
// [WithReferenceAnchors] gives those anchors. A path that ends at an alias
// that reads one selects the alias, as a path that ends at a tag selects
// the tag. A resolve through such an alias returns an error that wraps
// [ErrReferenceAlias] as well as ErrAlias. An alias key that reads one
// takes its name from the anchor, so `*k` matches `.name` after `&k name`
// in a reference document.
//
// A `<<` merge key that reads such an alias brings in the keys of the
// mapping of the anchor, as a merge key of the document does. The Resolver
// reads the keys of that mapping, and of the mappings that its own merge
// keys bring in, through WithReferenceAnchors. A path through a key of the
// mapping that holds the merge key has one of three outcomes:
//
//   - The mapping of the reference document holds the key, and its entry
//     wins. The path returns an error that wraps ErrReferenceAlias, which
//     carries the alias of the merge key, because the value lies in the
//     reference document.
//   - The mapping of the reference document does not hold the key, and the
//     document writes an entry of it. The path selects that entry, whether
//     it comes before or after the merge key.
//   - Neither holds the key. The path does not select anything, and the
//     error wraps ErrNotFound.
//
// A path also returns an error that wraps ErrReferenceAlias when the
// Resolver cannot read the keys of the reference document, such as when
// its mapping merges an alias that no reference document defines. A `.*`
// selector on the mapping returns that error when it would list an entry
// of the reference document. So does a `..name` or `..*` selector at an
// entry that an entry of the reference document hides. At the last merge
// key of a mapping, a real `<<` key that the reference document brings in
// wins over the merge key. A `..name` or `..*` selector then returns the
// error when it lists the merge key, or when it reaches an entry or an
// element in the value of the merge key.
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
// with [ErrWildcard]. [Path.HasWildcard] reports whether a path holds one.
// Use [Resolver.Nodes] to list every match.
// [ErrNotFound] means that the document does not hold a node at the path.
// [ErrAlias] means an alias on the path forms a cycle or does not have an
// anchor of its name before it, and [ErrReferenceAlias] means that the
// value at the path lies in a reference document.
//
// When the document does not have content to resolve in, Resolver.Token
// and Resolver.Node return an error for a `$` path that wraps both
// [ErrNoDocument] and ErrNotFound. Resolver.Nodes returns that error only
// for a nil document. In any other document without content, it returns
// an empty result, as it does for a path that does not select any node.
// For an `@` path, each of these methods returns an error wrapping
// [ErrNoCurrentNode] instead, whatever the document holds.
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
// [WithAliasLimit] set to false turns off both limits for a document that
// the program trusts.
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
// Parse reads a name without quotes only when Path.String writes that name
// without quotes. A name with whitespace, `:`, or a quote character thus
// needs single quotes, and the error for an unquoted one shows the
// selector to write:
//
//	_, err := paths.Parse("$.spec.name ")
//	// parse path "$.spec.name ": invalid path: whitespace in unquoted
//	// selector "name "; write .'name ' to keep it
//
// An expression that a program reads from a rule file or a flag can hold
// a space at its end or the quotes of another path syntax. Such an
// expression returns an error. It does not parse into a path that selects
// nothing.
//
// # go-yaml Syntax
//
// A path expression and a goccy/go-yaml YAMLPath read most text the same
// way. The differences include:
//
//   - `@` starts a path at the current node. go-yaml accepts only `$`.
//   - `~` selects the key of an entry and ends an unquoted name. go-yaml
//     reads `~` as part of a name, so `$.a~` names the key `a~` there.
//   - `.*` and `..*` select every entry of a mapping and every node at
//     any depth. go-yaml rejects both.
//   - `..'name'` quotes the name of a recursive selector. go-yaml reads
//     those quotes as part of the name.
//   - Empty quotes after `.` name the empty key. go-yaml rejects them.
//   - An index has one spelling, so `[01]` is malformed. go-yaml reads it
//     as `[1]`.
//   - An unquoted name does not hold whitespace, a control character, a
//     bidirectional embedding, override, or isolate, or one of `:`, `'`,
//     `"`, and `\`. go-yaml reads each of them as part of an unquoted
//     name, and it prints such a name without quotes, as in `$.my key`.
//   - Inside quotes, `\n` is a line feed, `\r` is a carriage return, `\t`
//     is a tab, and `\u` with four hexadecimal digits is the character
//     with that code. [Path.String] writes a control character or a
//     bidirectional embedding, override, or isolate as one of these
//     escapes. go-yaml reads the character after a `\` as itself, so `\n`
//     is the letter n there. It prints a control character as it is.
//
// The string of one syntax is thus not always an expression of the other.
// Convert a Path with [Path.YAMLPath], not with [yaml.PathString] on the
// text of Path.String. [Parse] does not read every string that go-yaml
// prints for a path. A go-yaml comment map, for example, writes the path
// of an entry with a space in its key without quotes.
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
// [Path.Append] builds a path from selectors, so a caller can change some
// selectors of a path and build it again. [Path.Prefix] keeps the first
// selectors of a path, and [Path.CutPrefix] gives the rest:
//
//	sels := slices.Collect(p.Selectors()) // $.items[3].name
//	sels[1] = paths.Selector{Kind: paths.SelectorIndexAll}
//	p.Prefix(0).Append(sels...)           // $.items[*].name
//	p.Prefix(2)                           // $.items[3]
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
// in its place. It does not support the `~`, `.*`, or `..*` selectors
// either, so YAMLPath returns an error wrapping [ErrNoYAMLPath] for a path
// that holds one. A caller with the path of a key converts [Path.Value]
// to name the value of the entry.
package paths
