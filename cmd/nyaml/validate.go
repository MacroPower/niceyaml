package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/spf13/cobra"

	"go.jacobcolvin.com/niceyaml"
	"go.jacobcolvin.com/niceyaml/escape"
	"go.jacobcolvin.com/niceyaml/schema"
	"go.jacobcolvin.com/niceyaml/schema/schemastore"
)

// The error for a flag that names a directory with an empty value.
var errEmptyPath = errors.New("empty path")

func validateCmd(storeOpts ...schemastore.Option) *cobra.Command {
	var (
		schemaRef   string
		schemaRoot  string
		schemaStore bool
		offline     bool
		strict      bool
		limits      *limitFlags
	)

	cmd := &cobra.Command{
		Use:   "validate file.yaml [file.yaml...]",
		Short: "Validate YAML files",
		Long: "Parse YAML files and validate each document against its JSON schema.\n\n" +
			"Each document parses on its own, so a syntax error fails the document " +
			"that holds it, and the other documents of the file still validate. " +
			"One run thus reports every syntax error and every schema violation " +
			"of a file.\n\n" +
			"With --schema, every document validates against that schema, " +
			"a local file or an http/https URL. A $ref in the schema to a file " +
			"or URL that does not load fails the run with one error, before " +
			"it reads any file.\n\n" +
			"Without --schema, a document validates against the schema named by a " +
			`"# yaml-language-server: $schema=" comment above its content, or else ` +
			"against the SchemaStore schema that matches its file path. A $schema " +
			"comment below a document's content has no effect on that document. " +
			"A document with neither only has to parse. A $schema=none comment " +
			"in the same place turns validation off for its document and skips " +
			"the lookup. A $ref in such a schema to a file or URL that does not " +
			"load fails only the documents that reach it.\n\n" +
			"The SchemaStore lookup downloads the catalog from schemastore.org, " +
			"so while that site is unreachable, every document without a " +
			"$schema comment fails. With --schemastore=false, the run skips the " +
			"lookup, and such a document only has to parse.\n\n" +
			"The --offline and --schema-root flags confine the files and URLs " +
			"that a run reads a schema from, such as for files from a pull " +
			"request. They apply to --schema, to each $schema comment, and to " +
			"each $ref in those schemas, but not to the schemas of the " +
			"SchemaStore catalog. With --offline, the run skips " +
			"the SchemaStore lookup and fetches no URL. A schema or $ref that " +
			"names a URL then fails to load without a request. With " +
			"--schema-root DIR, a schema file or $ref outside DIR fails to " +
			"load. Without --offline, a $schema comment can still name any URL, " +
			"so pair the two flags to keep the run to DIR.\n\n" +
			"A schema file or URL holds JSON or YAML, whatever its name. A " +
			"schema that opens with { reads as JSON, and any other reads as " +
			"YAML. A schema in YAML reads its numbers as a YAML document " +
			"does, so 1e3 is text and 010 is 8.\n\n" +
			"Supports glob patterns like *.yaml.\n\n" +
			"Stdout carries one result line for each file, written when the file " +
			"finishes: \"valid\", \"valid, N warnings\", \"invalid\" when the " +
			"documents are at fault, or \"failed\" for any other error, such as " +
			"a file that does not read. Stderr carries every " +
			"diagnostic. The warnings of a file that passes print there before " +
			"its result line, and the errors of every file that fails print " +
			"there together, with their warnings, after the last file. A " +
			"canceled run writes no result line for the file it stopped in or " +
			"for the files after it.\n\n" +
			"With --strict, a warning fails its file as an error does, so a " +
			"file with a deprecated field reads as invalid.\n\n" +
			"Exits 0 when every document is valid. Exits 1 when the documents " +
			"themselves are at fault for every error, such as a syntax error or " +
			"a schema violation. Exits 2 when any error is not a fault of a " +
			"document, such as a file that does not read, a schema file or URL " +
			"that does not load, or a canceled run, even when other documents " +
			"are invalid too.",
		Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			sourceOpts, err := limits.options()
			if err != nil {
				return err
			}

			// Expand glob patterns.
			yamlPaths, err := expandPaths(args...)
			if err != nil {
				return err
			}

			// The registry reads through the root, so the root stays open
			// for the whole run. The flag decides whether the run has a
			// root, not its value. An empty value, such as from an unset
			// shell variable, then fails the run rather than lift the
			// confinement.
			var root *os.Root

			if cmd.Flags().Changed("schema-root") {
				if schemaRoot == "" {
					return fmt.Errorf("--schema-root: %w", errEmptyPath)
				}

				root, err = os.OpenRoot(schemaRoot)
				if err != nil {
					return fmt.Errorf("--schema-root: %w", err)
				}

				defer root.Close() //nolint:errcheck // A directory has nothing to flush.
			}

			access, err := newAccess(root, offline)
			if err != nil {
				return err
			}

			var store schema.Resolver

			if schemaStore && !offline {
				store = schemastore.New(storeOpts...)
			}

			// Build the registry once, so every file shares its schema cache.
			reg, err := buildRegistry(cmd.Context(), schemaRef, access, store)
			if err != nil {
				return err
			}

			var errs []error

			for _, yamlPath := range yamlPaths {
				// A canceled run reports the cancellation once, rather
				// than once for each file left.
				ctxErr := cmd.Context().Err()
				if ctxErr != nil {
					errs = append(errs, ctxErr)

					break
				}

				// The warnings of a file that passes print on stderr before
				// its result line, and the run still passes. A file that
				// fails reports its warnings among its errors.
				warnings := niceyaml.NewWarnings()

				// The run holds the error of each file until the run ends.
				// A binding keeps its whole source alive, so the run
				// detaches each error, after the report adds the warnings.
				// Detach keeps the default context lines, so the error
				// handler in main renders the error with its own excerpt
				// options up to that many context lines. In strict mode,
				// the report of a file that passes with warnings fails it
				// too.
				err := validateFile(warnings.Context(cmd.Context()), yamlPath, reg, sourceOpts...)
				if err != nil || strict {
					err = warnings.Report(err)
				}

				if strict {
					err = niceyaml.AsErrors(err)
				}

				if err != nil {
					err = niceyaml.Detach(err)
				}

				// A run canceled during the file stops there. The error of
				// the file reports the cancellation once it has read the
				// file, so the bare cancellation joins only when no error of
				// the file wraps it.
				ctxErr = cmd.Context().Err()
				if ctxErr != nil {
					if err != nil {
						errs = append(errs, err)
					}

					if !errors.Is(err, ctxErr) {
						errs = append(errs, ctxErr)
					}

					break
				}

				// A glob match takes its name from the file system, so
				// draw it as the library draws the name of a source.
				name := escape.Line(yamlPath)

				result, err := fileResult(cmd.ErrOrStderr(), name, err, warnings)
				if err != nil {
					errs = append(errs, err)
				}

				_, writeErr := fmt.Fprintf(cmd.OutOrStdout(), "%s: %s\n", name, result)
				if writeErr != nil {
					errs = append(errs, fmt.Errorf("write the result of %s: %w", name, writeErr))
				}
			}

			return errors.Join(errs...)
		},
	}

	cmd.Flags().StringVarP(&schemaRef, "schema", "s", "", "File path or URL of a JSON schema, written in JSON or YAML")
	cmd.Flags().BoolVar(&schemaStore, "schemastore", true,
		"Look up the schema of a document without a $schema comment on SchemaStore")
	cmd.Flags().BoolVar(&offline, "offline", false,
		"Fetch no URL, so skip SchemaStore and fail each schema that names a URL")
	cmd.Flags().StringVar(&schemaRoot, "schema-root", "",
		"Directory that holds every schema file the run reads")
	cmd.Flags().BoolVar(&strict, "strict", false, "Fail a file on a warning, such as a deprecated field")

	limits = addLimitFlags(cmd)

	return cmd
}

// fileResult returns the result that the result line of a file reports,
// and the error that the file adds to the run. The err argument is the
// error of the file, with its warnings already reported, and warnings
// holds the warnings of the file.
//
// For a file that fails, fileResult returns "invalid" when
// [niceyaml.IsInvalid] reports err, and "failed" otherwise. It returns err
// as the error of the run, which reports the warnings among the errors.
//
// For a file that passes, fileResult writes the warnings to diag and
// returns "valid", with a count of the warnings when the file has any. A
// write to diag that fails returns an error that names the file as name.
func fileResult(diag io.Writer, name string, err error, warnings *niceyaml.Warnings) (string, error) {
	if err != nil {
		if niceyaml.IsInvalid(err) {
			return "invalid", err
		}

		return "failed", err
	}

	warned := warnings.Err()
	if warned == nil {
		return "valid", nil
	}

	count := 0
	for range niceyaml.Problems(warned) {
		count++
	}

	result := fmt.Sprintf("valid, %d warnings", count)
	if count == 1 {
		result = "valid, 1 warning"
	}

	_, err = fmt.Fprintf(diag, "%s\n\n", niceyaml.FormatError(warned))
	if err != nil {
		return result, fmt.Errorf("write the warnings of %s: %w", name, err)
	}

	return result, nil
}

// validateFile validates every document of the file at yamlPath against the
// registry with [niceyaml.Source.ValidateDocuments]. That method returns
// what every document reports in one error, so one run names each invalid
// document. A document that does not parse reports its syntax error, and
// the documents around it validate as they do in a file that parses.
// After ctx is canceled, the remaining documents do not validate, and the
// error reports the cancellation once.
//
// Each error it returns is bound to the source. The source takes yamlPath
// as the user typed it for its name. A message draws the name as
// [escape.Line] draws it, as the result line of the command does. Each
// message then opens with "path:line:col:" when the error has a position
// in the file. The error handler in main renders the excerpt with the
// terminal width. A document without tokens, such as an empty or
// comment-only file, does not have a position, so its messages open with
// "path:" alone.
//
// Each call hands the warnings of the file to the collector of ctx,
// whether the file passes or fails, and returns only its errors. A
// deprecation that the schema of the file marks is one such warning.
//
// The source takes its file path from [niceyaml.NewSourceFromFile], the
// absolute path of the file that the read opened. SchemaStore patterns
// that name parent directories, such as "**/.github/workflows/*.yml",
// then match whatever the working directory is. Schema directives
// resolve against the directory of that file. When the read fails, the
// error of NewSourceFromFile names the file already, as [escape.Line]
// draws it, so validateFile does not add a name of its own. A glob match
// takes its name from the file system. The escape keeps a line feed in
// that name from starting a row of its own in the output of the error
// handler in main. The error of a file past the byte limit ends with a
// hint that names --byte-limit.
func validateFile(ctx context.Context, yamlPath string, reg *schema.Registry, extra ...niceyaml.SourceOption) error {
	// WithName keeps messages naming the file as the user typed it. The
	// read uses the typed path, so a read error names the file that way
	// too. The library draws the name as escape.Line draws it, and so does
	// every line this command writes. Every line of a run thus spells the
	// file one way.
	opts := append([]niceyaml.SourceOption{niceyaml.WithName(yamlPath)}, extra...)

	source, err := niceyaml.NewSourceFromFile(yamlPath, opts...)

	// The read checks the byte limit alone, so a limit error here names
	// the flag that raises it. The other limits fail a document later.
	if errors.Is(err, niceyaml.ErrLimit) {
		return fmt.Errorf("%w (raise it with --byte-limit)", err)
	}

	if err != nil {
		return err //nolint:wrapcheck // The error names the file that did not read.
	}

	return source.ValidateDocuments(ctx, reg)
}

// newAccess returns the [*schema.Access] of the --schema-root and
// --offline flags. Each schema that the run reads from disk must lie under
// the directory of root, or anywhere when root is nil. Offline, the Access
// allows no URL. Otherwise it allows every HTTP and HTTPS URL. Without
// either flag, the Access allows what the Access from
// [schema.Unrestricted] allows.
func newAccess(root *os.Root, offline bool) (*schema.Access, error) {
	fileOpt := schema.AllowAnyFile()
	if root != nil {
		fileOpt = schema.AllowRoot(root)
	}

	opts := []schema.AccessOption{fileOpt}
	if !offline {
		opts = append(opts, schema.AllowAnyURL())
	}

	access, err := schema.NewAccess(opts...)
	if err != nil {
		return nil, fmt.Errorf("--schema-root: %w", err)
	}

	return access, nil
}

// buildRegistry creates a schema registry from schemaRef, the value of the
// --schema flag. The registry reads and fetches each schema under access,
// except the schemas that store names. A nil store leaves SchemaStore out.
//
// When schemaRef is not empty, buildRegistry loads and compiles that schema
// once, before the command reads any file. The compiled schema is then the
// only resolver, so every document validates against it, and store goes
// unused. A schema that does not load or compile fails the command with
// one "--schema:" error. A schema path resolves relative to the current
// working directory, as [schema.FileOrURL] resolves it, so it names the
// file that the OS opens for it. A $ref inside the schema resolves
// relative to the schema's own file or URL. A $ref that names a file or
// URL that does not load, or that access does not allow, fails the
// command the same way.
//
// Otherwise a registry of its own resolves the schema directives under
// access, as the Untrusted Documents section of the schema package
// describes. The reference of each directive resolves relative to its own
// YAML file. A document that does not have a schema directive goes to
// store, which discovers a schema by file path. The outer registry holds
// store, so access does not confine the schemas of the catalog. Such a
// schema comes from SchemaStore rather than from the files the run checks.
// The store fetches its catalog for the first document that reaches it.
// When it cannot fetch the catalog for a file, the error of that file
// reports the failure.
//
// Schema validation is optional here, so a document that the resolvers do
// not claim passes rather than failing the file. A catalog schema is not
// the user's to fix. A public schema might name a document that is gone
// under a branch few documents take. The registries therefore compile a
// schema without the documents that its $refs fail to load, and only a
// document that reaches such a $ref fails.
func buildRegistry(
	ctx context.Context,
	schemaRef string,
	access *schema.Access,
	store schema.Resolver,
) (*schema.Registry, error) {
	if schemaRef != "" {
		// A relative ref resolves relative to the working directory. If
		// cwd fails, use ".".
		cwd, err := os.Getwd()
		if err != nil {
			cwd = "."
		}

		ref, err := schema.FileOrURL(cwd, schemaRef)
		if err != nil {
			return nil, fmt.Errorf("--schema: %w", err)
		}

		// Compiling the schema here fails the command with one "--schema:"
		// error before it reads any file. A registry with the Ref as its
		// resolver would report a broken schema once for every document
		// instead. Compiling through Registry.Schema, rather than Compile
		// on the loaded bytes, lets a $ref in the schema resolve against
		// the schema's own file or URL.
		s, err := schema.NewRegistry(schema.WithAccess(access)).Schema(ctx, ref)
		if err != nil {
			return nil, fmt.Errorf("--schema: %w", err)
		}

		// The compiled schema names itself for every document, so it needs
		// no matcher, and the registry validates with it as it is.
		return schema.NewRegistry(schema.WithResolvers(s)), nil
	}

	refs := schema.WithCompileOptions(schema.WithRequireRefs(false))

	// The directive registry hands each schema to the outer registry
	// compiled, so the outer registry does not need an Access of its own.
	directives := schema.NewRegistry(
		schema.WithAccess(access),
		schema.WithResolvers(schema.Directive()), // Resolves schemas relative to each YAML file.
		refs,
	)

	resolvers := []schema.Resolver{directives}
	if store != nil {
		resolvers = append(resolvers, store) // Automatic discovery by absolute file path.
	}

	return schema.NewRegistry(
		schema.WithResolvers(resolvers...),
		schema.WithRequireSchema(false),
		refs,
	), nil
}
