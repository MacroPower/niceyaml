package niceyaml

import (
	"errors"
	"io"

	"github.com/goccy/go-yaml"
)

// Decoder wraps [yaml.Decoder] with custom error handling.
// It converts any [yaml.Error]s to niceyaml's [Error] type.
type Decoder struct {
	d *yaml.Decoder
}

// NewDecoder wraps [yaml.NewDecoder] to create a new [Decoder].
// Any provided [yaml.DecodeOption]s are passed to the underlying [yaml.Decoder].
func NewDecoder(r io.Reader, opts ...yaml.DecodeOption) *Decoder {
	return &Decoder{
		d: yaml.NewDecoder(r, opts...),
	}
}

// Decode calls [yaml.Decoder.Decode].
// Any [yaml.Error]s are converted niceyaml's [*Error].
func (d *Decoder) Decode(v any) error {
	err := d.d.Decode(v)
	if err == nil {
		return nil
	}

	var yamlErr yaml.Error
	if errors.As(err, &yamlErr) {
		return &Error{
			err:   errors.New(yamlErr.GetMessage()),
			token: yamlErr.GetToken(),
		}
	}

	//nolint:wrapcheck // Return the original error if it's not a [yaml.Error].
	return err
}
