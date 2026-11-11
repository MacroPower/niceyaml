package input

import (
	"context"

	"go.jacobcolvin.com/niceyaml"
	"go.jacobcolvin.com/niceyaml/internal/inputhook"
)

// init sets what the input package hands to the manifest package through
// [inputhook], as that package describes.
//
//nolint:gochecknoinits // The hooks reach no API, so only init can set them.
func init() {
	inputhook.Read = func(ctx context.Context, in any, us []niceyaml.Unmarshaler) (*inputhook.Result, error) {
		i, _ := in.(Input) //nolint:errcheck // The hook takes an Input.

		rd, err := readInputs(ctx, i, us)
		if err != nil {
			return nil, err
		}

		res := &inputhook.Result{Heading: i.heading(), Empty: rd.empty}
		for _, f := range rd.files {
			res.Files = append(res.Files, *f)
		}

		return res, nil
	}
}
