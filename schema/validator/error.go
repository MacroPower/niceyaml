package validator

import (
	"fmt"
	"path/filepath"
	"strconv"
	"strings"

	"go.jacobcolvin.com/x/jsonschema"

	"go.jacobcolvin.com/niceyaml"
	"go.jacobcolvin.com/niceyaml/paths"
)

// newValidationError converts a [*jsonschema.ValidationError] into a
// [*niceyaml.Error] whose nested errors each carry the YAML path to a failing
// location.
//
// The error tree is flattened to its concrete failures with
// [jsonschema.ValidationError.Leaves]. A single failure becomes the main
// message; several become a summary, naming the schema (url) when known.
func newValidationError(ve *jsonschema.ValidationError, url string) *niceyaml.Error {
	leaves := ve.Leaves()

	var mainMsg string

	switch len(leaves) {
	case 0:
		mainMsg = ve.Message
	case 1:
		mainMsg = leaves[0].Message
	default:
		if url != "" {
			mainMsg = fmt.Sprintf("validation failed at %d locations with %q",
				len(leaves), filepath.Base(url))
		} else {
			mainMsg = fmt.Sprintf("validation failed at %d locations", len(leaves))
		}
	}

	causes := make([]*niceyaml.Error, 0, len(leaves))
	for _, leaf := range leaves {
		causes = append(causes, niceyaml.NewError(
			leaf.Message,
			niceyaml.WithPath(buildTargetPath(parseJSONPointer(leaf.InstancePath), leaf.TargetsKey())),
		))
	}

	// Don't set a main path; the nested errors handle highlighting.
	return niceyaml.NewError(mainMsg, niceyaml.WithErrors(causes...))
}

// parseJSONPointer splits a JSON Pointer into its unescaped reference tokens.
// The empty pointer (the document root) yields no tokens.
func parseJSONPointer(ptr string) []string {
	if ptr == "" {
		return nil
	}

	tokens := strings.Split(strings.TrimPrefix(ptr, "/"), "/")
	for i, tok := range tokens {
		tok = strings.ReplaceAll(tok, "~1", "/")
		tok = strings.ReplaceAll(tok, "~0", "~")
		tokens[i] = tok
	}

	return tokens
}

// buildTargetPath converts instance-location tokens to a [*paths.Path], pointing
// at the key when targetsKey is set and the value otherwise.
func buildTargetPath(location []string, targetsKey bool) *paths.Path {
	builder := paths.Root()

	for _, part := range location {
		// A token that is wholly numeric addresses an array index; anything
		// else is a property name.
		index, err := strconv.Atoi(part)
		if err == nil {
			builder = builder.Index(index)
		} else {
			builder = builder.Child(part)
		}
	}

	if targetsKey {
		return builder.Key()
	}

	return builder.Value()
}
