package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"go.jacobcolvin.com/niceyaml/manifest"
)

func TestRegistry(t *testing.T) {
	t.Parallel()

	set, err := registry.Load(t.Context(), manifest.FS(files, "config"))
	require.NoError(t, err)

	cafe, ok := set.One[Cafe]()
	require.True(t, ok)
	assert.Equal(t, "yamull", cafe.Name)

	var items []string

	for item := range set.All[MenuItem]() {
		items = append(items, item.Name)
	}

	assert.Equal(t, []string{"kanelbulle", "semla"}, items)

	hours, ok := set.Get[Hours]("weekday")
	require.True(t, ok)
	assert.Equal(t, "weekday", hours.Name)
}
