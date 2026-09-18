package schema

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"time"

	"go.jacobcolvin.com/x/jsonschema"

	"go.jacobcolvin.com/niceyaml"
	"go.jacobcolvin.com/niceyaml/paths"
)

var (
	// ErrValidate indicates an unexpected, non-validation failure while
	// validating against a schema, such as a reference resolution problem.
	// Schema constraint violations are reported as [*niceyaml.Error] values
	// with path information instead of wrapping this sentinel.
	ErrValidate = errors.New("validate schema")

	// ErrCompile indicates a schema document that does not compile.
	// [Compile] and [Registry.Lookup] return it.
	ErrCompile = errors.New("compile schema")
)

// CompileOption configures [Compile] and [MustCompile], and
// [WithCompileOptions] hands the same options to a [Registry].
//
// Available options:
//   - [WithJSONSchemaOptions]
type CompileOption func(*compileConfig)

// compileConfig holds the settings a [CompileOption] configures.
type compileConfig struct {
	jsonOpts []jsonschema.ValidateOption
}

// newCompileConfig applies opts over the defaults.
func newCompileConfig(opts []CompileOption) compileConfig {
	var cfg compileConfig

	for _, opt := range opts {
		opt(&cfg)
	}

	return cfg
}

// WithJSONSchemaOptions is a [CompileOption] that passes
// [jsonschema.ValidateOption] values to the underlying
// [jsonschema.CompileJSON]. It is the escape hatch for settings of the JSON
// Schema library that have no option of their own, such as format
// assertions or a custom format validator:
//
//	v, err := schema.Compile(ctx, data, schema.WithJSONSchemaOptions(jsonschema.WithFormats(true)))
func WithJSONSchemaOptions(opts ...jsonschema.ValidateOption) CompileOption {
	return func(cfg *compileConfig) {
		cfg.jsonOpts = append(cfg.jsonOpts, opts...)
	}
}

// Compile creates a new [*Validator] from a JSON schema document. The
// context reaches the reference resolver for references resolved while
// compiling. An error wraps [ErrCompile].
//
// It is the path for a program that holds one schema, such as one embedded
// in the binary:
//
//	//go:embed config.schema.json
//	var schemaJSON []byte
//
//	v, err := schema.Compile(ctx, schemaJSON)
//
// A schema known valid at build time compiles with [MustCompile] at package
// scope. A registry compiles the schemas its resolvers name the same way,
// with the options [WithCompileOptions] gives it.
func Compile(ctx context.Context, data []byte, opts ...CompileOption) (*Validator, error) {
	cfg := newCompileConfig(opts)

	compiled, err := jsonschema.CompileJSON(ctx, data, cfg.jsonOpts...)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrCompile, err)
	}

	return NewValidator(compiled), nil
}

// MustCompile is [Compile] with a background context that panics when the
// schema does not compile. Use it for a package-scope validator compiled
// from a schema brought in with go:embed:
//
//	var Schema = schema.MustCompile(schemaJSON)
func MustCompile(data []byte, opts ...CompileOption) *Validator {
	v, err := Compile(context.Background(), data, opts...)
	if err != nil {
		panic(err)
	}

	return v
}

// NewValidator creates a new [*Validator] from a [*jsonschema.Validator]
// compiled elsewhere, such as one built from a Go type with
// [jsonschema.Compile]. A schema held as JSON compiles with [Compile] or
// [MustCompile] instead.
func NewValidator(v *jsonschema.Validator) *Validator {
	return &Validator{schema: v}
}

// Validator is a [niceyaml.Validator] that checks a document against one
// JSON schema, reporting constraint violations as [*niceyaml.Error] values
// that carry the YAML path to each failing location for display by
// [printer.Printer]. [Validator.Validate] checks a whole document, and
// [Validator.ValidateSchema] checks decoded data, such as one value taken
// from a document with [niceyaml.Document.Get].
//
// A Validator is safe for concurrent use. Create instances with [Compile],
// [MustCompile], or [NewValidator].
type Validator struct {
	schema *jsonschema.Validator
}

// Validate implements [niceyaml.Validator]. It decodes doc to
// [any] and checks the result with [Validator.ValidateSchema], so
// [niceyaml.WithValidator] runs the schema before a decode and
// [niceyaml.Document.Validate] runs it on its own. A decoding error comes
// back bound to the source, and a violation as the unbound [*niceyaml.Error]
// that ValidateSchema returns, which the document binds.
func (v *Validator) Validate(ctx context.Context, doc *niceyaml.Document) error {
	data, err := doc.Decode[any](ctx)
	if err != nil {
		return err
	}

	return v.ValidateSchema(ctx, data)
}

// ValidateSchema checks data, the decoded form of a YAML value, against the
// schema.
//
// YAML-native values the JSON Schema validator does not accept are first
// converted to the JSON spelling of the same data: a !!binary becomes its
// base64 text and a !!timestamp its RFC 3339 text, anywhere in the value.
//
// Returns nil when data conforms. On a constraint violation, returns a
// [*niceyaml.Error]: a single violation carries its YAML path on the error
// itself, and several violations become a count summary whose nested errors
// each carry the path to one failing location. Any other failure wraps
// [ErrValidate]. The context is passed to the underlying
// [jsonschema.Validator], where remote reference resolution honors its
// cancellation and deadlines.
func (v *Validator) ValidateSchema(ctx context.Context, data any) error {
	err := v.schema.Validate(ctx, normalizeJSON(data))
	if err == nil {
		return nil
	}

	// A structured validation failure carries per-location paths; convert it to
	// a niceyaml.Error. Anything else is an unexpected internal failure.
	if ve, ok := errors.AsType[*jsonschema.ValidationError](err); ok {
		return newValidationError(ve)
	}

	return fmt.Errorf("%w: %w", ErrValidate, err)
}

// newValidationError converts a [*jsonschema.ValidationError] into a
// [*niceyaml.Error].
//
// The error tree is flattened to its concrete failures with
// [jsonschema.ValidationError.Leaves]. A single failure becomes the main
// error, carrying its own path so the printer highlights that location and
// [niceyaml.Error.Path] reports it. Several failures become a count summary
// with no path of its own; each nested error carries the path to one
// failing location.
func newValidationError(ve *jsonschema.ValidationError) *niceyaml.Error {
	leaves := ve.Leaves()

	switch len(leaves) {
	case 0:
		return niceyaml.NewError(ve.Message)
	case 1:
		return leafError(leaves[0])
	}

	causes := make([]*niceyaml.Error, 0, len(leaves))
	for _, leaf := range leaves {
		causes = append(causes, leafError(leaf))
	}

	return niceyaml.NewError(
		fmt.Sprintf("%d schema violations", len(leaves)),
		niceyaml.WithErrors(causes...),
	)
}

// leafError converts one concrete failure into a [*niceyaml.Error] carrying
// the YAML path to the failing location.
func leafError(leaf *jsonschema.ValidationError) *niceyaml.Error {
	return niceyaml.NewError(
		leaf.Message,
		niceyaml.WithPath(buildTargetPath(leaf.InstanceSegments(), leaf.TargetsKey())),
	)
}

// buildTargetPath converts instance-location segments to a [paths.Path],
// pointing at the key when targetsKey is set and the value otherwise. Each
// [jsonschema.Segment] already distinguishes an array index from a property
// name, so no numeric guessing is needed.
func buildTargetPath(segments []jsonschema.Segment, targetsKey bool) paths.Path {
	path := paths.Root()

	for _, seg := range segments {
		if seg.IsIndex {
			path = path.Index(seg.Index)
		} else {
			path = path.Child(seg.Key)
		}
	}

	if targetsKey {
		return path.Key()
	}

	return path
}

// normalizeJSON converts the YAML-native values a decode produces that the
// JSON Schema validator does not accept into the JSON spellings of the
// same data: a !!binary becomes its base64 text and a !!timestamp its RFC
// 3339 text. Maps and slices are walked so a tagged scalar anywhere in a
// document stays validatable. Every other value comes back unchanged,
// non-finite floats included, since the validator treats those as numbers.
func normalizeJSON(data any) any {
	switch v := data.(type) {
	case []byte:
		return base64.StdEncoding.EncodeToString(v)
	case time.Time:
		return v.Format(time.RFC3339Nano)
	case map[string]any:
		out := make(map[string]any, len(v))
		for key, elem := range v {
			out[key] = normalizeJSON(elem)
		}

		return out

	case []any:
		out := make([]any, len(v))
		for i, elem := range v {
			out[i] = normalizeJSON(elem)
		}

		return out

	default:
		return data
	}
}
