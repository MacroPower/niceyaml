package line_test

import "testing"

func BenchmarkView_Segments(b *testing.B) {
	cases := []struct {
		name      string
		entries   int
		highlight bool
	}{
		{"200_entries", 200, false},
		{"200_entries_highlighted", 200, true},
		{"2000_entries", 2000, false},
		{"2000_entries_highlighted", 2000, true},
	}

	for _, bc := range cases {
		b.Run(bc.name, func(b *testing.B) {
			view := flowMappingView(bc.entries, bc.highlight)

			b.ReportAllocs()

			for b.Loop() {
				for range view.Segments(0) {
				}
			}
		})
	}
}
