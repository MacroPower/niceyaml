package matcher_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"go.jacobcolvin.com/niceyaml"
	"go.jacobcolvin.com/niceyaml/paths"
	"go.jacobcolvin.com/niceyaml/schema/matcher"
)

// BenchmarkContent_TaggedAliasElements matches a sequence against an
// array, where every element is an alias under a tag. The tag keeps the
// path of an element from following the alias, so the matcher follows
// each one. The resolver it follows them with binds the whole document,
// so each element should reuse the resolver of the first instead of
// binding the document again.
func BenchmarkContent_TaggedAliasElements(b *testing.B) {
	const maxElems = 4000

	for _, elems := range []int{1000, maxElems} {
		var sb strings.Builder

		sb.WriteString("v: &v x\nl:\n")

		for range elems {
			sb.WriteString("  - !t *v\n")
		}

		doc, err := niceyaml.NewSourceFromString(sb.String()).Document()
		require.NoError(b, err)

		// The decoder leaves zero each element past the end of the
		// sequence, so one array type matches both sizes.
		var want [maxElems]string

		for i := range elems {
			want[i] = "x"
		}

		m := matcher.Content(paths.Current().Child("l"), want)

		b.Run(fmt.Sprintf("elems_%d", elems), func(b *testing.B) {
			b.ReportAllocs()

			for b.Loop() {
				ok, err := m.Match(b.Context(), doc)
				if err != nil {
					b.Fatal(err)
				}

				if !ok {
					b.Fatal("want a match")
				}
			}
		})
	}
}
