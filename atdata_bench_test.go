package niceyaml_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"go.jacobcolvin.com/niceyaml"
)

// BenchmarkAtData_Decode decodes a map whose parent check reports one
// [niceyaml.AtData] error for each entry. The decode thus translates one
// location for each entry of the map. The time per entry should stay flat
// as the map grows.
func BenchmarkAtData_Decode(b *testing.B) {
	for _, n := range []int{100, 1000, 4000} {
		b.Run(fmt.Sprintf("entries=%d", n), func(b *testing.B) {
			var sb strings.Builder

			sb.WriteString("ports:\n")

			for i := range n {
				fmt.Fprintf(&sb, "  0x%x:\n    name: \"\"\n", i+1)
			}

			src := niceyaml.NewSourceFromString(sb.String(), niceyaml.WithName("f.yaml"))

			doc, err := src.Document()
			require.NoError(b, err)

			b.ReportAllocs()

			for b.Loop() {
				_, err := doc.Decode[dataPorts](b.Context())
				require.Error(b, err)
			}
		})
	}
}
