// Package manifest loads configuration that users write the way they write
// Kubernetes manifests. An app reads every YAML file in a directory that
// the user names, and each document declares what it is with an
// `apiVersion` field and a `kind` field. A load decodes each document into
// the Go type that the app registered for its kind.
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
// Each version except the oldest can hold steps, which [Migrate] adds, and
// which bring a document from the version before it. A load runs the steps
// of every later version in order, so the app reads one version of each
// kind and the file of the user stays as it is. The author of a new
// version writes its steps against the previous version alone:
//
//	manifest.Version("cafe.jacobcolvin.com/v1",
//		manifest.Written(),
//		manifest.Migrate(spec.Child("cost"), func(_ context.Context, n *migrate.Node) error {
//			return n.SetKey("price")
//		}),
//	),
//
// A step is a [go.jacobcolvin.com/niceyaml/manifest/migrate.Func]. It runs
// once for each place that its path selects, in document order, and gets a
// handle that reads the value there and edits the document. Each call sees
// the edits of the calls before it. A document that moves into the next
// version gets both type fields of that version, so a step never writes
// them. An error from a step fails the document, and a panic in a step
// fails it with a [niceyaml.PanicError].
//
// Every error and every warning about a migrated document binds at the
// line that the user wrote, under the path that the file spells. An error
// at a value that a step set carries a detail that names the version of
// the step. A value that a step removes warns with the reason that the
// step gave, and the warning matches [ErrRemoved].
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
// A rewrite moves each document to the written version of its kind, so a
// user who upgrades gets files that the new release reads without steps.
// [Registry.PlanMigration] runs the checks of a load and works out the new
// text of each file, and writes nothing. [Plan.Apply] writes the files
// that change:
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
// in [PlanFile.Header] and [PlanFile.Removals], so a rewrite never deletes
// a value without a row that says so.
//
// Apply writes each file atomically and keeps its mode, its owner, and its
// group. It writes only a file that a [Path] input read from disk, and a
// file whose text changed after the plan read it fails with an error that
// matches [ErrChanged]. Every other file fails with an error that matches
// [ErrReadOnly], and its plan still holds its new text, so a dry run shows
// the diff.
//
// # Loading
//
// [Registry.Load] reads an [Input] and returns a [Set]. [Path] reads a
// file, a directory, or standard input, so a --config flag takes a
// directory or a single file. [FS] reads a file or a directory of an
// [io/fs.FS], [Sources] reads Sources that the app built, and [Inputs]
// reads several inputs in order:
//
//	set, err := config.Registry.Load(ctx, manifest.Path(dir))
//	if err != nil {
//		return err
//	}
//
// A directory input reads the files that match "*.yaml" or "*.yml", and
// skips each entry whose name starts with ".". [WithRecursive],
// [WithInclude], and [WithExclude] change the selection. A walk reads a
// file that a symbolic link names only when the link leads inside the
// input directory to a file that the patterns select. Any other link fails
// with an error that matches [ErrOutsideLink], unless [WithOutsideLinks]
// lets it lead outside. A file loads once, however many paths reach it.
//
// A load reads the inputs in order, the files of a directory sorted by
// path, and the documents of each file in order. Every report and every
// iteration of a set follows that load order. A load fails a file that is
// not UTF-8 with an error that matches [ErrEncoding], and applies the
// limits of each Source, as [niceyaml.WithByteLimit] and
// [niceyaml.WithDepthLimit] describe.
//
// [WithUnmarshalers] sets how the Go types of the kinds read YAML, such
// as a type of another package that a document writes as text. A load
// gives the unmarshalers to each Source that it builds. Every decode that
// the load runs on a document applies them too, so the decode of a
// manifest, the decode inside a validator, and the decode of a migration
// step read the types alike. The Node of an [Object] from a Source that
// [Sources] reads keeps the unmarshalers of that Source alone.
//
// # Routing
//
// A load reads the type fields of each document and picks the kind and the
// version whose names match the text the document writes. A type field must
// sit in the document itself, so a path through an alias or a `<<` merge
// key fails with an error that matches [ErrFieldAlias]. An unknown kind
// suggests the closest name, and an unknown version lists the versions that
// the app reads. [WithVersionField] and [WithKindField] move the type
// fields, and [WithVersionOnly] gives a registry of one kind a version field
// alone. [WithIgnore] skips the documents of other tools.
//
// A kind that [Unversioned] gives a layout without type fields reads plain
// documents, so an app that already reads plain YAML adopts manifests.
// [Claim] picks the kind of a document without a kind field.
//
// # Checks
//
// A load runs these checks on each document, and the first that fails
// ends the checks of that document:
//
//  1. The document must parse.
//  2. The document must write its type fields in place, and they must name
//     a kind and a version that the registry holds and that no removal
//     names.
//  3. The document must pass the schema of the version it declares.
//  4. For each later version in turn, up to the current version, the steps
//     of the version run, and the result must pass the schema of the
//     version.
//  5. The result decodes into the Go type of its kind, and every value
//     that implements [niceyaml.SelfValidator] validates itself.
//  6. The document must hold a name, when [WithNameField] reads names.
//
// The check of each schema sees the type fields that the schema declares
// under properties, with the names of its version, and sees no other type
// field. A closed schema that leaves the type fields out thus passes. Only
// a declaration in the schema document itself, outside every branch such
// as a schema under oneOf, counts. [Registry.Verify] reports each schema
// that requires a type field and does not declare it, because such a
// schema fails every document. It also reports each type field that a
// branch or another document might declare.
//
// The decode admits the type fields, and rejects every other key that the
// Go type does not declare. A Go type that reads the type fields, such as
// one that embeds the TypeMeta of Kubernetes or a map[string]any, gets the
// names of the current version. Each validator of [WithDecodeOptions] and
// [DecodeOptions] sees them too. A value that decodes itself, through a
// method such as UnmarshalYAML or through an unmarshaler of
// [WithUnmarshalers], sees no type field below it. Every error and every
// warning binds in the file of the user.
//
// Once each document has run its checks, the load compares the names of
// the manifests of each kind, and counts the manifests of each kind that
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
// Each panics on a call that no set can answer, such as a type that is not
// the Go type of a kind, so the first test that runs the line fails.
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
// prints with the renderers it uses for errors. Each problem matches its
// sentinel with [errors.Is], such as [ErrUnknownKind] or [ErrDeprecated],
// and [niceyaml.IsInvalid] reports each problem that the files of the user
// cause.
//
// # Warnings
//
// [Deprecated] marks a version deprecated, and a load warns about each
// document that declares it. Each warning that a load reports wraps a
// [*Warning], which names the file, the kind, the version, and the path.
// [WithWarningFilter] decides which warnings a load keeps, so a team
// accepts one deprecation for a while. [WithWarningsAsErrors] turns each
// warning that the filter keeps into an error, so a strict load in CI fails
// on a deprecated file. The warning about a value that a step of a version
// newer than the written one removes stays a warning, because no rewrite
// deletes the value before [Written] moves:
//
//	set, err := config.Registry.Load(ctx, manifest.Path(dir),
//		manifest.WithWarningsAsErrors(strict))
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
// A load collects the warnings of its checks itself, so a
// [niceyaml.Warnings] on the context of the load receives none of the
// warnings that the load reports. Inside a call of niceyaml, such as in a
// [niceyaml.Validator], the warnings also reach the collectors of that
// call, as [niceyaml.Warnings.Context] describes.
package manifest
