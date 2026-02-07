package theme_test

import (
	"slices"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"go.jacobcolvin.com/niceyaml/style"
	"go.jacobcolvin.com/niceyaml/style/theme"
)

func TestRegister(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		setup func()
		check func(t *testing.T)
	}{
		"retrieve registered theme via Styles": {
			setup: func() {
				theme.Register("test-custom", func() style.Styles {
					return style.Styles{style.Comment: {}}
				}, style.Dark)
			},
			check: func(t *testing.T) {
				t.Helper()

				got, ok := theme.Styles("test-custom")
				require.True(t, ok)
				assert.Contains(t, got, style.Comment)
			},
		},
		"registered theme appears in List": {
			setup: func() {
				theme.Register("test-listed", func() style.Styles {
					return style.Styles{}
				}, style.Dark)
			},
			check: func(t *testing.T) {
				t.Helper()

				names := theme.List(style.Dark)
				assert.True(t, slices.Contains(names, "test-listed"))
			},
		},
		"replace existing custom theme": {
			setup: func() {
				theme.Register("test-replace", func() style.Styles {
					return style.Styles{style.Comment: {}}
				}, style.Dark)
				theme.Register("test-replace", func() style.Styles {
					return style.Styles{style.NameTag: {}}
				}, style.Dark)
			},
			check: func(t *testing.T) {
				t.Helper()

				got, ok := theme.Styles("test-replace")
				require.True(t, ok)
				assert.Contains(t, got, style.NameTag)
				assert.NotContains(t, got, style.Comment)
			},
		},
		"dark theme not in light list": {
			setup: func() {
				theme.Register("test-dark-only", func() style.Styles {
					return style.Styles{}
				}, style.Dark)
			},
			check: func(t *testing.T) {
				t.Helper()

				dark := theme.List(style.Dark)
				light := theme.List(style.Light)

				assert.True(t, slices.Contains(dark, "test-dark-only"))
				assert.False(t, slices.Contains(light, "test-dark-only"))
			},
		},
	}

	for name, tt := range tests {
		tt.setup()
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			tt.check(t)
		})
	}
}

func TestRegisterConcurrent(t *testing.T) {
	t.Parallel()

	var wg sync.WaitGroup

	for i := range 50 {
		wg.Go(func() {
			name := "concurrent-" + string(rune('a'+i%26))

			theme.Register(name, func() style.Styles {
				return style.Styles{}
			}, style.Dark)
			theme.Styles(name)
			theme.List(style.Dark)
		})
	}

	wg.Wait()
}
