package edit_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.jacobcolvin.com/x/stringtest"

	"go.jacobcolvin.com/niceyaml"
	"go.jacobcolvin.com/niceyaml/internal/yamltest"
	"go.jacobcolvin.com/niceyaml/manifest/internal/edit"
	"go.jacobcolvin.com/niceyaml/paths"
)

var (
	spec       = paths.Doc().Child("spec")
	typeFields = []paths.Path{paths.Doc().Child("apiVersion"), paths.Doc().Child("kind")}
)

// newDoc returns a Doc of the first document of text.
func newDoc(t *testing.T, text string) *edit.Doc {
	t.Helper()

	doc := yamltest.FirstDocument(t, text, niceyaml.WithName("test.yaml"))

	d, err := edit.New(doc, edit.Options{App: "cafe", TypeFields: typeFields})
	require.NoError(t, err)

	d.BeginStep("cafe.jacobcolvin.com/v1")

	return d
}

// requireEdit checks the outcome of an edit of d that returned err. With
// wantErr set, the formatted err must hold wantErr. Otherwise the edit
// must succeed and leave d checked and holding the text want.
func requireEdit(t *testing.T, d *edit.Doc, err error, wantErr, want string) {
	t.Helper()

	if wantErr != "" {
		require.Error(t, err)
		assert.Contains(t, niceyaml.FormatError(err), wantErr)

		return
	}

	require.NoError(t, err)
	require.NoError(t, d.Check(t.Context()))
	assert.Equal(t, want, d.Text())
}

func TestDoc_Locate(t *testing.T) {
	t.Parallel()

	d := newDoc(t, stringtest.LinesLF("spec:", "  cost: 45 # kr"))
	require.NoError(t, d.Handle(spec.Child("cost")).SetKey("price"))

	err := d.Node().Bind(niceyaml.NewError("too cheap", niceyaml.AtPath(spec.Child("price"))))
	assert.Equal(t, "test.yaml:2:9: $.spec.cost: too cheap", err.Error())

	err = d.Node().Bind(niceyaml.NewError("deprecated", niceyaml.AtPath(spec.Child("price").Key())))
	assert.Equal(t, "test.yaml:2:3: $.spec.cost~: deprecated", err.Error())

	err = d.Node().Bind(niceyaml.NewError("missing", niceyaml.AtPath(spec.Child("close"))))
	assert.Equal(t, "test.yaml:1:1: $.spec.close: missing", err.Error())
}
