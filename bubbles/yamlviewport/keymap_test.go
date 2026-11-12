package yamlviewport_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"go.jacobcolvin.com/niceyaml/bubbles/yamlviewport"
)

func TestDefaultKeyMap(t *testing.T) {
	t.Parallel()

	km := yamlviewport.DefaultKeyMap()
	assert.True(t, km.PageDown.Enabled())
	assert.True(t, km.PageUp.Enabled())
	assert.True(t, km.HalfPageDown.Enabled())
	assert.True(t, km.HalfPageUp.Enabled())
	assert.True(t, km.Down.Enabled())
	assert.True(t, km.Up.Enabled())
	assert.True(t, km.Left.Enabled())
	assert.True(t, km.Right.Enabled())
	assert.True(t, km.GotoTop.Enabled())
	assert.True(t, km.GotoBottom.Enabled())
	assert.True(t, km.SearchNext.Enabled())
	assert.True(t, km.SearchPrevious.Enabled())
	assert.True(t, km.NextRevision.Enabled())
	assert.True(t, km.PreviousRevision.Enabled())
	assert.True(t, km.ToggleDiffMode.Enabled())
}
