// Package manifest loads configuration that users write the way they write
// Kubernetes manifests. An app reads every YAML file in a directory that
// the user names. Each document declares what it is with an `apiVersion`
// field and a `kind` field. A load decodes each document into the Go type
// that the app registered for its kind.
//
// # Registries
//
// A [Registry] holds the kinds of an app. Each kind has one Go type, for its
// current version, and lists its versions oldest first:
//
//	var Registry = manifest.MustNewRegistry(
//		manifest.WithNameField(paths.Doc().Child("metadata", "name")),
//		manifest.WithAppName("cafe"),
//		manifest.WithKinds(
//			manifest.NewKind[Cafe]("Cafe",
//				manifest.Required(),
//				manifest.Singleton(),
//				manifest.Version("cafe.jacobcolvin.com/v1",
//					manifest.Schema(schema.FileFS(schemas, "schemas/cafe.v1.json")),
//				),
//			),
//		),
//	)
//
// [NewRegistry] returns an error for a registry that contradicts itself,
// such as a kind that lists a version twice, and [MustNewRegistry] panics on
// one. [Registry.Verify] loads and compiles the schema of each version, so
// a test catches a broken schema before a user meets it. It also catches a
// deprecated field that the user cannot remove.
//
// # Versions
//
// Each version except the oldest can hold steps that bring a document from
// the version before it. [Migrate] adds each step. A load runs the steps of
// each version after the one that a document declares, in order. The app
// thus reads one version of each kind, and the file of the user stays as
// it is. The author of a new version writes its steps against the previous
// version alone:
//
//	manifest.Version("cafe.jacobcolvin.com/v1",
//		manifest.Written(),
//		manifest.Migrate(spec.Child("cost"), func(_ context.Context, n *edit.Node) error {
//			return n.SetKey("price")
//		}),
//	),
//
// A step is a [go.jacobcolvin.com/niceyaml/edit.Func]. Each step of a
// version runs as one [go.jacobcolvin.com/niceyaml/edit.Doc.Apply], which
// calls the step once for each place that its path selects, in document
// order. Each call gets a node that reads the value there and edits the
// document. Each call sees the edits of the calls before it. A document
// that moves into the next version gets the type fields of that version,
// so a step never writes them. An error from a step fails the document,
// and a panic in a step fails it with a [niceyaml.PanicError].
//
// A step must make the same edits for the same document. A load runs it
// at each load of an older document, and a plan runs it again. A step
// that reads the clock, the environment, or a random source thus gives
// each run a different document.
//
// A step should build the values that it sets from types that it declares
// itself and from the standard library. A Go type of the app might change
// in a later version, and the step must still write what it wrote before.
//
// Every error and every warning about a migrated document binds at the
// line that the user wrote, under the path that the file spells. An error
// at a value that a step set carries a detail that names the version of
// the step. A value that a step removes warns, and the warning matches
// [ErrRemoved]. A reason that the step gives with
// [go.jacobcolvin.com/niceyaml/edit.WithReason] appears as a detail of the
// warning.
//
// Versions compare by their place in the list and never by their text.
// [Written] marks the version that a rewrite writes, and [Object.UpToDate]
// reports whether a document declares that version or a newer one.
//
// A load decodes each manifest from the defaults that
// [niceyaml.WithDefaults] gives its Go type, in the options of
// [WithDecodeOptions] and [DecodeOptions]. The defaults belong to the
// current version. A changed default changes the meaning of every file
// that leaves the field out. The author thus ships the change as a new
// version, whose step writes the old default where the field is missing.
//
// # Rewriting Files
//
// A rewrite moves each document to the written version of its kind. A
// user who upgrades thus gets files that the new release reads without
// steps. [Registry.PlanMigration] runs the checks of a load and computes
// the new text of each file, but it does not write a file. [Plan.Apply]
// writes the files that change:
//
//	plan, err := config.Registry.PlanMigration(ctx, manifest.Path(dir))
//	if err != nil {
//		return err
//	}
//
//	for _, f := range plan.Files() {
//		fmt.Println(f.Header())
//		lipgloss.Println(printer.New().Print(f.Diff(3)))
//	}
//
//	res, err := plan.Apply(ctx)
//	if err != nil {
//		return err
//	}
//
//	fmt.Println(res.Summary())
//
//	return res.Err()
//
// A rewrite keeps every byte that a step leaves alone: the comments, the
// blank lines, the key order, the quoting, and the line endings. A
// document that fails a check blocks its file, so a rewrite never writes a
// document that a load then rejects. A value that a step removes appears
// in [PlanFile.Header] and [PlanFile.Removals], so each value that a
// rewrite deletes has a row that says so.
//
// Apply writes each file atomically and keeps its mode, its owner, and its
// group. It writes only the files that a [Path] input read from disk. Each
// other file fails with an error that matches [ErrReadOnly], but its plan
// still holds its new text, so a dry run shows the diff. A file whose text
// changed after the plan read it fails with an error that matches
// [ErrChanged].
//
// # Loading
//
// [Registry.Load] reads an [Input] and returns a [Set]:
//
//	set, err := config.Registry.Load(ctx, manifest.Path(dir))
//	if err != nil {
//		return err
//	}
//
// These functions build an Input:
//
//   - [Path] reads a file or a directory, so a --config flag takes either.
//   - [FS] reads a file or a directory of an [io/fs.FS].
//   - [Stdin] reads standard input.
//   - [Reader] reads an [io.Reader].
//   - [Sources] reads Sources that the app built.
//   - [Inputs] reads several inputs in order.
//
// Path reads only the file system, so a CLI whose --config flag takes "-"
// for standard input picks Stdin itself:
//
//	in := manifest.Path(config)
//	if config == "-" {
//		in = manifest.Stdin()
//	}
//
// The first load or plan that reads a Stdin or Reader input reads its
// text, and every later one reuses that text. One Input thus serves
// [Registry.PlanMigration] and the Load after it.
//
// A directory input reads the files that match "*.yaml" or "*.yml", and
// skips each entry whose name starts with ".". [WithRecursive],
// [WithInclude], and [WithExclude] change the selection. A walk reads a
// file that a symbolic link names only when the link leads inside the
// input directory to a file that the patterns select. [WithOutsideLinks]
// also lets a link lead outside. Any other link to a regular file fails
// with an error that matches [ErrOutsideLink]. A file loads once, however
// many paths reach it.
//
// A load reads the inputs in order, the files of a directory sorted by
// path, and the documents of each file in order. Every report and every
// iteration of a set follows that load order. A load fails each file that
// is not UTF-8 with an error that matches [ErrEncoding]. The text of a
// [Stdin], [Reader], or [Sources] input counts as a file. A load applies
// the limits of each Source, as [niceyaml.WithByteLimit] and
// [niceyaml.WithDepthLimit] describe.
//
// [WithUnmarshalers] sets how the Go types of the kinds read YAML, such
// as a type of another package that a document writes as text. A load
// gives the unmarshalers to each Source that it builds. Every decode that
// the load runs on a document applies them too. The decode of a manifest,
// the decode inside a validator, and the decode of a migration step thus
// read the types alike. The Node of an [Object] from a Source that
// [Sources] reads keeps the unmarshalers of that Source alone.
//
// # Routing
//
// A load reads the type fields of each document and picks the kind and the
// version whose names match the text that the document writes.
// [WithVersionField] and [WithKindField] move the type fields, and
// [WithVersionOnly] gives a registry of one kind a version field alone.
// [WithIgnore] skips the documents of other tools.
//
// A type field must sit in the document itself. A path through an alias or
// a `<<` merge key thus fails with an error that matches [ErrFieldAlias].
// The error about an unknown kind suggests the closest name. The error
// about an unknown version lists the versions that the app reads.
//
// A kind that [Unversioned] gives a layout without type fields reads plain
// documents, so an app that already reads plain YAML can adopt manifests.
// [Claim] picks the kind of a document without a kind field.
//
// # Checks
//
// A load runs these checks on each document, and the first that fails
// ends the checks of that document:
//
//  1. The document must parse.
//  2. The document must write its type fields in place. They must name a
//     kind and a version that the registry holds and that [WithRemoved]
//     does not name.
//  3. The document must pass the schema of the version that it declares.
//  4. For each later version in turn, up to the current version, the steps
//     of the version run. The result must pass the schema of the version.
//  5. The result decodes into the Go type of its kind, and every value
//     that implements [niceyaml.SelfValidator] validates itself.
//  6. The document must hold a name, when [WithNameField] reads names.
//
// The check of each schema sees only the type fields that the schema
// declares under properties, with the names of its version. A closed
// schema that leaves the type fields out thus passes. Only a declaration
// in the schema document itself, outside every branch such as a schema
// under oneOf, counts. [Registry.Verify] reports each schema that requires
// a type field and does not declare it, because such a schema fails every
// document. It also reports each type field that a branch or another
// document might declare.
//
// The decode admits the type fields, and rejects every other key that the
// Go type does not declare. A Go type that reads the type fields, such as
// one that embeds the TypeMeta of Kubernetes or a map[string]any, gets the
// names of the current version. Each validator of [WithDecodeOptions] and
// [DecodeOptions] sees them too. A value that decodes itself does not see
// a type field below it, whether it decodes through a method such as
// UnmarshalYAML or through an unmarshaler of [WithUnmarshalers]. Every
// error and every warning binds in the file of the user.
//
// After each document has run its checks, the load compares the names of
// the manifests of each kind. It counts the manifests of each kind that
// [Required] or [Singleton] marks. Then it calls the functions of
// [WithCheck] with the set. [WithSetChecks] turns these checks off, for a
// load of part of a set.
//
// # Typed Access
//
// A [Set] returns its manifests as [Object] values of the Go type of their
// kind. [Set.One] returns the manifest of a [Singleton] kind, [Set.All]
// yields the manifests of a kind in load order, and [Set.Get] returns one
// by its name:
//
//	cafe, _ := set.One[config.Cafe]()
//	fmt.Printf("%s seats %d\n", cafe.Name, cafe.Value.Spec.Seats)
//
//	for item := range set.All[config.MenuItem]() {
//		fmt.Printf("%s: %d kr\n", item.Name, item.Value.Spec.Price)
//	}
//
// Each of these methods panics on a call that cannot succeed on any set,
// such as a type that is not the Go type of a kind. The first test that
// runs the line thus fails.
//
// # Problems
//
// A load checks every file and every document before it returns. A load that
// fails returns a [*LoadError], which holds every error and every warning in
// load order under one heading:
//
//	config: 2 errors, 1 warning
//	|-- config/hours.yaml:1:13: warning: $.apiVersion: deprecated version "cafe.jacobcolvin.com/v1alpha1"
//	|   `-- cafe 4.0 removes cafe.jacobcolvin.com/v1alpha1
//	|-- config/menu.yaml:7:9: $.spec.price: 0 is less than 1
//	`-- config/specials.yaml:2:7: $.kind: unknown kind "Menuitem"
//	    `-- did you mean "MenuItem"?
//
// A load that passes returns its warnings from [Set.Warnings], which the app
// prints with the renderers that it uses for errors. Each problem matches its
// sentinel with [errors.Is], such as [ErrUnknownKind] or [ErrDeprecated],
// and [niceyaml.IsInvalid] reports each problem that the files of the user
// cause.
//
// [Registry.Check] runs the checks of a load and returns the manifests that
// pass beside the problems, for an editor or a terminal UI that shows both.
// [Checked.Set] leaves out the manifest of each document that an error
// binds in, such as the second manifest of a [Singleton] kind.
// [Checked.Err] returns the error that Load returns, which holds the
// warnings too. A load that passes returns its warnings from
// [Checked.Warnings]:
//
//	checked, err := config.Registry.Check(ctx, manifest.Path(dir))
//	if err != nil {
//		return err
//	}
//
//	for item := range checked.Set().All[config.MenuItem]() {
//		menu.Add(item.Value)
//	}
//
//	problems := checked.Err()
//	if problems == nil {
//		problems = checked.Warnings()
//	}
//
//	diagnostics.Show(problems)
//
// # Warnings
//
// [Deprecated] marks a version deprecated, and a load warns about each
// document that declares it. Each warning that a load reports wraps a
// [*Warning], which names the file, the kind, the version, and the path.
// [WithWarningFilter] decides which warnings a load keeps, so a team can
// accept one deprecation for a while. [WithWarningsAsErrors] turns each
// warning that the filter keeps into an error, so a strict load in CI fails
// on a deprecated file:
//
//	set, err := config.Registry.Load(ctx, manifest.Path(dir),
//		manifest.WithWarningsAsErrors(strict))
//
// A step of a version later than the written one can remove a value. The
// warning about that value stays a warning in strict mode, because a
// rewrite does not delete the value before [Written] moves.
//
// The schema of the current version marks a field or a value deprecated
// with the deprecated keyword, as the Deprecations section of
// [go.jacobcolvin.com/niceyaml/schema] describes. A load warns about each
// value that such a schema applies to. The gen tool of
// [go.jacobcolvin.com/x/jsonschema] sets the keyword on each field whose
// doc comment holds a Deprecated: paragraph:
//
//	// LegacyPricing rounds each price to whole kronor.
//	//
//	// Deprecated: legacyPricing has no effect. Remove it.
//	LegacyPricing bool `json:"legacyPricing,omitzero"`
//
// The omitzero option keeps the field out of required, so the user can
// follow the warning. Only the schema of the current version warns. The
// schema of an older version still checks a document, but a later release
// might drop one of its marks and keep the field.
//
// A load collects the warnings of its checks itself. A [niceyaml.Warnings]
// on the context of the load thus does not receive the warnings that the
// load reports. Inside a call of niceyaml, such as in a
// [niceyaml.Validator], the warnings also reach the collectors of that
// call, as [niceyaml.Warnings.Context] describes.
package manifest
