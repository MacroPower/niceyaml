package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
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
		Long:  "Validate YAML files.\nOptionally validate against a JSON schema (local file or http/https URL).\nSupports glob patterns like *.yaml.",
		Args:  cobra.MinimumNArgs(1),
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
				// the document the cancellation hit already reports it, with
				// an excerpt, so the bare cancellation joins only when no
				// error of the file wraps it.
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
				// escape it the way the error handler renders one.
				_, err = fmt.Fprintf(cmd.OutOrStdout(), "%s: valid\n", escape.Control(yamlPath))
				if err != nil {
					errs = append(errs, fmt.Errorf("write the result of %s: %w", yamlPath, err))
				}
			}

			return errors.Join(errs...)
		},
	}

	cmd.Flags().StringVarP(&schemaRef, "schema", "s", "", "JSON schema file path or URL")

	return cmd
}

// validateFile validates every document of the file at yamlPath against the
// registry and joins what every document reports, so one run names each
// invalid document. Once ctx is canceled, validateFile validates no
// further document, since each would report the cancellation again.
//
// Each error it returns is bound to the source, and the source takes
// yamlPath as the user typed it for its name. Each message then opens
// with "path:line:col:", and the error handler in main renders the
// excerpt with the terminal width.
//
// The file path of the source is the [physicalAbs] form of yamlPath.
// SchemaStore patterns that name parent directories, such as
// "**/.github/workflows/*.yml", then match whatever the working directory
// is. Schema directives resolve against the directory of the file the read
// opens. When the read fails, the read error already names the file, so
// validateFile returns it as is.
func validateFile(ctx context.Context, yamlPath string, reg *schema.Registry) error {
	absPath := physicalAbs(yamlPath)

	// Resolvers route on the absolute path, and WithName keeps messages
	// naming the file as the user typed it. The read uses the typed path,
	// so a read error names the file that way too.
	source, err := niceyaml.NewSourceFromFile(yamlPath,
		niceyaml.WithName(yamlPath),
		niceyaml.WithFilePath(absPath),
	)
	if err != nil {
		return err
	}

	docs, err := source.Documents()
	if err != nil {
		return err
	}

	var errs []error

	for _, doc := range docs {
		if ctx.Err() != nil {
			break
		}

		err = doc.Validate(ctx, reg)
		if err != nil {
			errs = append(errs, err)
		}
	}

	return errors.Join(errs...)
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

// schemePrefix matches a ref that opens with a URL scheme, such as
// "https:" or "file:". It also matches a drive letter such as "C:".
var schemePrefix = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9+.-]*:`)

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
// the schema resolves relative to the schema's own file or URL.
//
// Otherwise the registry matches on schema directives first, and a
// directive's reference resolves relative to its own YAML file. A document
// that no directive claims falls through to SchemaStore's automatic
// discovery by file path. SchemaStore fetches its catalog on the first
// document that reaches it, and a file it cannot fetch the catalog for
// reports that in the file's error. Schema validation is optional here, so
// a document that no resolver claims passes rather than failing the file.
func buildRegistry(ctx context.Context, schemaRef string) (*schema.Registry, error) {
	if schemaRef != "" {
		// FileOrURL cleans a path as text, which drops a ".." together
		// with a symlinked directory before it. A URL or drive-letter
		// path opens with a scheme and goes to FileOrURL as written.
		if !schemePrefix.MatchString(schemaRef) {
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

		// A registry caches only the schemas it compiles, so with the Ref as
		// its resolver, the registry would load a broken schema again for
		// every document. Compiling through Registry.Schema, rather than
		// Compile on the loaded bytes, lets a $ref in the schema resolve
		// against the schema's own file or URL.
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
	), nil
}
