package niceyaml_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.jacobcolvin.com/x/stringtest"

	"go.jacobcolvin.com/niceyaml"
	"go.jacobcolvin.com/niceyaml/internal/yamltest"
	"go.jacobcolvin.com/niceyaml/paths"
)

// logged returns the "err" attribute a JSON handler writes for err.
func logged(t *testing.T, err error) string {
	t.Helper()

	var buf bytes.Buffer

	logger := slog.New(slog.NewJSONHandler(&buf, nil))
	logger.Error("load", slog.Any("err", err))

	var record struct {
		Err string `json:"err"`
	}

	require.NoError(t, json.Unmarshal(buf.Bytes(), &record))

	return record.Err
}

func TestError_LogValue(t *testing.T) {
	t.Parallel()

	t.Run("logs the tree of an unbound error", func(t *testing.T) {
		t.Parallel()

		err := niceyaml.NewError("2 schema violations", niceyaml.WithErrors(
			niceyaml.NewError("bad x", niceyaml.AtPath(paths.Root().Child("x"))),
			niceyaml.NewError("bad y", niceyaml.AtPath(paths.Root().Child("y"))),
		))

		want := stringtest.JoinLF(
			"2 schema violations",
			"|-- $.x: bad x",
			"`-- $.y: bad y",
		)

		assert.Equal(t, want, err.LogValue().String())
		assert.Equal(t, want, logged(t, err))
	})

	t.Run("an error with nothing nested logs its message", func(t *testing.T) {
		t.Parallel()

		err := niceyaml.NewError("bad x", niceyaml.AtPath(paths.Root().Child("x")))

		assert.Equal(t, "$.x: bad x", logged(t, err))
	})

	t.Run("a join of typed-nil errors logs its message", func(t *testing.T) {
		t.Parallel()

		var nilErr *niceyaml.Error

		err := niceyaml.WrapError(errors.Join(nilErr, nilErr))

		assert.Equal(t, "\n", err.LogValue().String())
		assert.Equal(t, niceyaml.FormatError(err, 2), err.LogValue().String())
	})

	t.Run("a nil error logs nothing", func(t *testing.T) {
		t.Parallel()

		var err *niceyaml.Error

		assert.Empty(t, err.LogValue().String())
	})
}

func TestSourceError_LogValue(t *testing.T) {
	t.Parallel()

	source := niceyaml.NewSourceFromString("x: 1\ny: 2\n", niceyaml.WithName("cfg.yaml"))

	t.Run("logs the tree with positions and no excerpt", func(t *testing.T) {
		t.Parallel()

		err := yamltest.Bind(t, source, niceyaml.NewError("2 schema violations", niceyaml.WithErrors(
			niceyaml.NewError("bad x", niceyaml.AtPath(paths.Root().Child("x"))),
			niceyaml.NewError("bad y", niceyaml.AtPath(paths.Root().Child("y"))),
		)))

		want := stringtest.JoinLF(
			"cfg.yaml: 2 schema violations",
			"|-- 1:4: $.x: bad x",
			"`-- 2:4: $.y: bad y",
		)

		assert.Equal(t, want, logged(t, err))

		var buf bytes.Buffer

		slog.New(slog.NewTextHandler(&buf, nil)).Error("load", slog.Any("err", err))

		assert.Contains(t, buf.String(), `err="cfg.yaml: 2 schema violations\n|-- 1:4: $.x: bad x\n`)
		assert.Contains(t, buf.String(), "`-- 2:4: $.y: bad y\"")
		assert.NotContains(t, buf.String(), "   1 | x: 1")
	})

	t.Run("a wrapper logs its own message", func(t *testing.T) {
		t.Parallel()

		err := yamltest.Bind(t, source, niceyaml.NewError("bad x", niceyaml.AtPath(paths.Root().Child("x"))))
		wrapped := fmt.Errorf("load config: %w", err)

		assert.Equal(t, "load config: cfg.yaml:1:4: $.x: bad x", logged(t, wrapped))
	})

	t.Run("a bound join of typed-nil errors logs its message", func(t *testing.T) {
		t.Parallel()

		var nilErr *niceyaml.Error

		err := source.Bind(errors.Join(nilErr, nilErr))
		require.Error(t, err)

		assert.Equal(t, "cfg.yaml: \n", logged(t, err))
		assert.Equal(t, niceyaml.FormatError(err, 2), logged(t, err))

		var buf bytes.Buffer

		slog.New(slog.NewTextHandler(&buf, nil)).Error("load", slog.Any("err", err))

		assert.Contains(t, buf.String(), `err="cfg.yaml: \n"`)
	})

	t.Run("a nil error logs nothing", func(t *testing.T) {
		t.Parallel()

		var err *niceyaml.SourceError

		assert.Empty(t, err.LogValue().String())
	})
}
