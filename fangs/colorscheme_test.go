package fangs_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"go.jacobcolvin.com/niceyaml/fangs"
	"go.jacobcolvin.com/niceyaml/style/theme"
)

func TestColorScheme(t *testing.T) {
	t.Parallel()

	styles, ok := theme.Styles("charm")
	require.True(t, ok, "charm theme should exist")

	cs := fangs.ColorScheme(styles)

	// Verify all fields are populated.
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

	styles, ok := theme.Styles("charm")
	require.True(t, ok, "charm theme should exist")

	csFunc := fangs.ColorSchemeFunc(styles)

	// The LightDarkFunc parameter should be ignored.
	cs := csFunc(nil)

	assert.NotNil(t, cs.Base)
	assert.NotNil(t, cs.Title)
}
