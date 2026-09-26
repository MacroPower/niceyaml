package style_test

import (
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/exp/charmtone"
	"github.com/stretchr/testify/assert"

	"go.jacobcolvin.com/niceyaml/style"
	"go.jacobcolvin.com/niceyaml/style/kind"
)

func TestDefault_SharedValueStaysUnchanged(t *testing.T) {
	t.Parallel()

	first := style.Default()
	assert.Equal(t, first, style.Default())

	red := lipgloss.NewStyle().Foreground(lipgloss.Color("#ff0000"))
	extended := style.Default().With(style.Set(kind.Comment, red))
	assert.Equal(t, red, extended.Style(kind.Comment))

	// With copies the shared value, so a later call still sees the default.
	assert.Equal(t, charmtone.Oyster, style.Default().Style(kind.Comment).GetForeground())
}
