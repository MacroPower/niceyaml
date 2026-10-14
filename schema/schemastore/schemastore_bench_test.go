package schemastore_test

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"

	"go.jacobcolvin.com/niceyaml/schema/schemastore"
)

func BenchmarkStore_FindMatch(b *testing.B) {
	// A path no entry matches makes a scan try every pattern, as a document
	// outside the catalog does.
	catalog := schemastore.Catalog{}
	for i := range 1000 {
		catalog.Schemas = append(catalog.Schemas, schemastore.CatalogEntry{
			Name: fmt.Sprintf("Schema %d", i),
			URL:  fmt.Sprintf("https://example.com/schema-%d.json", i),
			FileMatch: []string{
				fmt.Sprintf("schema-%d.yaml", i),
				fmt.Sprintf("/.config/schema-%d/*.{yml,yaml}", i),
			},
		})
	}

	store := newCatalogStore(b, catalog)

	_, err := store.FindMatch(b.Context(), "/repo/unmatched.yaml")
	require.ErrorIs(b, err, schemastore.ErrNoCatalogMatch)

	bms := map[string]struct {
		paths []string
	}{
		// Each lookup names a different path from the one before, so each
		// one scans the catalog.
		"new path": {
			paths: []string{"/repo/unmatched.yaml", "/repo/other.yaml"},
		},
		// Every lookup names the same path, as the documents of one file
		// do, so each one reuses the last scan.
		"repeated path": {
			paths: []string{"/repo/unmatched.yaml"},
		},
	}

	for name, bm := range bms {
		b.Run(name, func(b *testing.B) {
			b.ReportAllocs()

			var i int

			for b.Loop() {
				_, err = store.FindMatch(b.Context(), bm.paths[i%len(bm.paths)])
				i++
			}

			require.ErrorIs(b, err, schemastore.ErrNoCatalogMatch)
		})
	}
}
