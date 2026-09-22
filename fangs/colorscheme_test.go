package fangs_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"go.jacobcolvin.com/niceyaml/fangs"
	"go.jacobcolvin.com/niceyaml/style/theme"
)

func TestColorScheme(t *testing.T) {
	t.Parallel()

	styles := theme.Charm.Styles()

	cs := fangs.ColorScheme(styles)

	// Verify ColorScheme populates every field it maps; Help and
	// ErrorDetails stay unset.
	assert.NotNil(t, cs.Base)
	assert.NotNil(t, cs.Title)
	assert.NotNil(t, cs.Description)
	assert.NotNil(t, cs.Codeblock)
	assert.NotNil(t, cs.Program)
	assert.NotNil(t, cs.Command)
	assert.NotNil(t, cs.DimmedArgument)
	assert.NotNil(t, cs.Comment)
	assert.NotNil(t, cs.Flag)
	assert.NotNil(t, cs.FlagDefault)
	assert.NotNil(t, cs.QuotedString)
	assert.NotNil(t, cs.Argument)
	assert.NotNil(t, cs.Dash)
	assert.NotNil(t, cs.ErrorHeader[0])
	assert.NotNil(t, cs.ErrorHeader[1])
}

func TestColorSchemeFunc(t *testing.T) {
	t.Parallel()

	styles := theme.Charm.Styles()

	csFunc := fangs.ColorSchemeFunc(styles)

	// The returned function ignores the LightDarkFunc parameter.
	cs := csFunc(nil)

	assert.NotNil(t, cs.Base)
	assert.NotNil(t, cs.Title)
}
