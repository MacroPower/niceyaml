// The functions below are copies of go-yaml's yaml.go, with the decoder of
// this package in place of go-yaml's. YAMLToJSON keeps go-yaml's encoder.

package yamldecode

import (
	"bytes"
	"context"
	"io"

	"github.com/goccy/go-yaml"
	"github.com/goccy/go-yaml/ast"
)

// Unmarshal decodes the first document found within the in byte slice
// and assigns decoded values into the out value.
//
// Struct fields are only unmarshalled if they are exported (have an
// upper case first letter), and are unmarshalled using the field name
// lowercased as the default key. Custom keys may be defined via the
// "yaml" name in the field tag: the content preceding the first comma
// is used as the key, and the following comma-separated options are
// used to tweak the marshaling process (see Marshal).
// Conflicting names result in a runtime error.
//
// For example:
//
//	type T struct {
//	    F int `yaml:"a,omitempty"`
//	    B int
//	}
//	var t T
//	yaml.Unmarshal([]byte("a: 1\nb: 2"), &t)
//
// See the documentation of Marshal for the format of tags and a list of
// supported tag options.
func Unmarshal(data []byte, v interface{}) error {
	return UnmarshalWithOptions(data, v)
}

// UnmarshalWithOptions decodes with DecodeOptions the first document found within the in byte slice
// and assigns decoded values into the out value.
func UnmarshalWithOptions(data []byte, v interface{}, opts ...DecodeOption) error {
	return UnmarshalContext(context.Background(), data, v, opts...)
}

// UnmarshalContext decodes with context.Context and DecodeOptions.
func UnmarshalContext(ctx context.Context, data []byte, v interface{}, opts ...DecodeOption) error {
	dec := NewDecoder(bytes.NewBuffer(data), opts...)
	if err := dec.DecodeContext(ctx, v); err != nil {
		if err == io.EOF {
			return nil
		}
		return err
	}
	return nil
}

// NodeToValue converts node to the value pointed to by v.
func NodeToValue(node ast.Node, v interface{}, opts ...DecodeOption) error {
	var buf bytes.Buffer
	if err := NewDecoder(&buf, opts...).DecodeFromNode(node, v); err != nil {
		return err
	}
	return nil
}

// YAMLToJSON convert YAML bytes to JSON.
func YAMLToJSON(bytes []byte) ([]byte, error) {
	var v interface{}
	if err := UnmarshalWithOptions(bytes, &v, UseOrderedMap()); err != nil {
		return nil, err
	}
	out, err := yaml.MarshalWithOptions(v, yaml.JSON())
	if err != nil {
		return nil, err
	}
	return out, nil
}
