package main

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"

	"github.com/spf13/cobra"

	"go.jacobcolvin.com/niceyaml"
	"go.jacobcolvin.com/niceyaml/internal/escape"
	"go.jacobcolvin.com/niceyaml/schema"
	"go.jacobcolvin.com/niceyaml/schema/schemastore"
)

func validateCmd() *cobra.Command {
	var schemaRef string

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
			"A document with neither only has to parse. The SchemaStore lookup " +
			"downloads the catalog from schemastore.org, so while that site is " +
			"unreachable, every document without such a comment fails. A " +
			"$schema=none comment in the same place turns validation off for its " +
			"document and skips the lookup. A $ref in such a schema to a file or " +
			"URL that does not load fails only the documents that reach it.\n\n" +
			"Supports glob patterns like *.yaml.\n\n" +
			"Exits 0 when every document is valid. Exits 1 when the documents " +
			"themselves are at fault for every error, such as a syntax error or " +
			"a schema violation. Exits 2 when any error is not a fault of a " +
			"document, such as a file that does not read, a schema file or URL " +
			"that does not load, or a canceled run, even when other documents " +
			"are invalid too.",
		Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			// Expand glob patterns.
			yamlPaths, err := expandPaths(args...)
			if err != nil {
				return err
			}

			// Build the registry once, so every file shares its schema cache.
			reg, err := buildRegistry(cmd.Context(), schemaRef)
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

				err := validateFile(cmd.Context(), yamlPath, reg)

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

				if err != nil {
					errs = append(errs, err)

					continue
				}

				// A glob match takes its name from the file system, so
				// escape it here and in the error, as validateFile does.
				name := escape.Control(yamlPath)

				_, err = fmt.Fprintf(cmd.OutOrStdout(), "%s: valid\n", name)
				if err != nil {
					errs = append(errs, fmt.Errorf("write the result of %s: %w", name, err))
				}
			}

			return errors.Join(errs...)
		},
	}

	cmd.Flags().StringVarP(&schemaRef, "schema", "s", "", "JSON schema file path or URL")

	return cmd
}

// validateFile validates every document of the file at yamlPath against the
// registry with [niceyaml.Source.ValidateDocuments], which joins what every
// document reports, so one run names each invalid document. A document
// that does not parse reports its syntax error, and the documents around
// it validate as they do in a file that parses. Once ctx is canceled, no
// further document validates, and the error reports the cancellation
// once.
//
// Each error it returns is bound to the source, and the source takes
// yamlPath as the user typed it for its name, with its control characters
// escaped. Each message then opens with "path:line:col:" when the error
// has a position in the file, and the error handler in main renders the
// excerpt with the terminal width. A document with no tokens, such as an
// empty or comment-only file, has no position, so its messages open with
// "path:" alone. The error handler keeps each line break in a message as
// a row break, so a glob match whose name holds a newline would otherwise
// split its message into rows that look like other branches of the tree.
//
// The file path of the source is the [physicalAbs] form of yamlPath.
// SchemaStore patterns that name parent directories, such as
// "**/.github/workflows/*.yml", then match whatever the working directory
// is. Schema directives resolve against the directory of the file the read
// opens. When the read fails, the error from [readSource] already names
// the file, so validateFile adds no name of its own.
func validateFile(ctx context.Context, yamlPath string, reg *schema.Registry) error {
	absPath := physicalAbs(yamlPath)

	// Resolvers route on the absolute path, and WithName keeps messages
	// naming the file as the user typed it. The read uses the typed path,
	// so a read error names the file that way too.
	opts := []niceyaml.SourceOption{
		niceyaml.WithName(escape.Control(yamlPath)),
		niceyaml.WithFilePath(absPath),
	}

	source, err := readSource(yamlPath, opts...)
	if err != nil {
		return err
	}

	return source.ValidateDocuments(ctx, reg)
}

// readSource reads the file at path into a [*niceyaml.Source] with opts, as
// [niceyaml.NewSourceFromFile] does, except that a read error names path
// with its control characters escaped. A glob match takes its name from the
// file system. The error handler in main keeps each line break in a message
// as a row break, so a raw newline in the name would start a row of its
// own.
func readSource(path string, opts ...niceyaml.SourceOption) (*niceyaml.Source, error) {
	data, err := os.ReadFile(path) //nolint:gosec // User-provided file paths are intentional.
	if err != nil {
		// The read made this error, so no other caller holds it.
		if pathErr, ok := errors.AsType[*fs.PathError](err); ok {
			pathErr.Path = escape.Control(pathErr.Path)
		}

		return nil, fmt.Errorf("read file: %w", err)
	}

	// The file path goes first, so opts can override it.
	opts = append([]niceyaml.SourceOption{niceyaml.WithFilePath(path)}, opts...)

	return niceyaml.NewSourceFromBytes(data, opts...), nil
}

// physicalAbs returns an absolute form of path that names the file the
// OS opens for it. [filepath.Abs] drops a ".." element together with the
// element before it, as text. On Unix, the OS instead steps up from the
// directory a symlink leads to, which can be a different directory. So
// physicalAbs resolves the symlinks in path up to its last ".." element,
// including those in the working directory a relative path starts from.
// It leaves the symlinks after that point alone, so a symlinked name
// such as ".github" stays visible to patterns.
//
// A path with no ".." element, or one whose part up to the last ".."
// does not resolve, comes back as [filepath.Abs] returns it. So does
// every path on Windows, which drops ".." elements as text before it
// follows a symlink.
func physicalAbs(path string) string {
	lexical, err := filepath.Abs(path)
	if err != nil {
		// Abs fails only when the working directory is unreadable.
		return path
	}

	if runtime.GOOS == "windows" {
		return lexical
	}

	// Build the absolute path as text, since cleaning it would drop the
	// ".." elements this function resolves.
	abs := path
	if !filepath.IsAbs(abs) {
		wd, err := os.Getwd()
		if err != nil {
			return lexical
		}

		abs = wd + string(filepath.Separator) + path
	}

	end := lastDotDotEnd(abs)
	if end < 0 {
		return lexical
	}

	resolved, err := filepath.EvalSymlinks(abs[:end])
	if err != nil {
		return lexical
	}

	return filepath.Join(resolved, abs[end:])
}

// readsAsPath reports whether [schema.FileOrURL] reads ref as a path that
// starts with a separator or as a relative path, rather than as a URL or
// a path that opens with a drive letter such as "C:/". It asks FileOrURL
// with an empty base directory, which reports [schema.ErrNoBaseDir] for a
// relative path, so the two agree on every ref. A relative path whose
// first element holds a colon, such as "v1:dir/schema.json", reads as a
// path, and so does a file URL that names no local path, such as one with
// a host other than localhost.
func readsAsPath(ref string) bool {
	if ref != "" && os.IsPathSeparator(ref[0]) {
		return true
	}

	_, err := schema.FileOrURL("", ref)

	return errors.Is(err, schema.ErrNoBaseDir)
}

// lastDotDotEnd returns the index just past the last ".." element of
// path, or -1 when path has none.
func lastDotDotEnd(path string) int {
	end, start := -1, 0

	for i := 0; i <= len(path); i++ {
		if i < len(path) && !os.IsPathSeparator(path[i]) {
			continue
		}

		if path[start:i] == ".." {
			end = i
		}

		start = i + 1
	}

	return end
}

// buildRegistry creates a schema registry based on CLI flags.
//
// When schemaRef is not empty, buildRegistry loads and compiles that schema
// once, before the command reads any file. The compiled schema is then the
// only resolver, so every document validates against it. A schema that
// cannot load or compile fails the command with one "--schema:" error. A
// schema path resolves relative to the current working directory, through
// [physicalAbs], so it names the file the OS opens for it. A $ref inside
// the schema resolves relative to the schema's own file or URL, and one
// that names a file or URL that does not load fails the command the same
// way.
//
// Otherwise the registry matches on schema directives first, and a
// directive's reference resolves relative to its own YAML file. A document
// that no directive claims falls through to SchemaStore's automatic
// discovery by file path. SchemaStore fetches its catalog on the first
// document that reaches it, and a file it cannot fetch the catalog for
// reports that in the file's error. Schema validation is optional here, so
// a document that no resolver claims passes rather than failing the file.
// A catalog schema is not the user's to fix, and a public schema can name
// a document that is gone under a branch few documents take. The registry
// therefore compiles a schema without the documents its $refs fail to
// load, and only a document that reaches such a $ref fails.
func buildRegistry(ctx context.Context, schemaRef string) (*schema.Registry, error) {
	if schemaRef != "" {
		// FileOrURL cleans a path as text, which drops a ".." together
		// with a symlinked directory before it. Any other ref, such as a
		// URL or a drive-letter path, goes to FileOrURL as written.
		if readsAsPath(schemaRef) {
			schemaRef = physicalAbs(schemaRef)
		}

		// A relative ref that physicalAbs could not make absolute resolves
		// relative to the working directory. If cwd fails, use ".".
		cwd, err := os.Getwd()
		if err != nil {
			cwd = "."
		}

		ref, err := schema.FileOrURL(cwd, schemaRef)
		if err != nil {
			return nil, fmt.Errorf("--schema: %w", err)
		}

		// A registry keeps the failure of a schema only when a $ref of the
		// schema does not load. With the Ref as its resolver, it would load
		// any other broken schema again for every document. Compiling
		// through Registry.Schema, rather than Compile on the loaded bytes,
		// lets a $ref in the schema resolve against the schema's own file
		// or URL.
		s, err := schema.NewRegistry().Schema(ctx, ref)
		if err != nil {
			return nil, fmt.Errorf("--schema: %w", err)
		}

		// The compiled schema names itself for every document, so it needs
		// no matcher, and the registry validates with it as it is.
		return schema.NewRegistry(schema.WithResolvers(s)), nil
	}

	return schema.NewRegistry(
		schema.WithResolvers(
			schema.Directive(), // Resolves schemas relative to each YAML file.
			schemastore.New(),  // Automatic discovery by absolute file path.
		),
		schema.WithRequireSchema(false),
		schema.WithCompileOptions(schema.WithRequireRefs(false)),
	), nil
}
