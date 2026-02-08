package normalizer_test

import (
	"sync"
	"testing"
	"unicode"

	"github.com/stretchr/testify/assert"
	"golang.org/x/text/runes"

	"go.jacobcolvin.com/niceyaml/normalizer"
)

func TestNormalize(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		opts []normalizer.Option
		in   string
		want string
	}{
		"default removes diacritics and lowercases": {
			in:   "Café",
			want: "cafe",
		},
		"default handles umlaut": {
			in:   "Ö",
			want: "o",
		},
		"default handles uppercase with diacritics": {
			in:   "ÜBER",
			want: "uber",
		},
		"case fold disabled preserves case": {
			opts: []normalizer.Option{normalizer.WithCaseFold(false)},
			in:   "Café",
			want: "Cafe",
		},
		"case fold eszett": {
			in:   "Straße",
			want: "strasse",
		},
		"diacritics disabled preserves diacritics": {
			opts: []normalizer.Option{normalizer.WithDiacriticFold(false)},
			in:   "Café",
			want: "café",
		},
		"both disabled returns input unchanged": {
			opts: []normalizer.Option{
				normalizer.WithCaseFold(false),
				normalizer.WithDiacriticFold(false),
			},
			in:   "Café",
			want: "Café",
		},
		"empty string": {
			in:   "",
			want: "",
		},
		"ascii only": {
			in:   "Hello World",
			want: "hello world",
		},
		"cjk characters": {
			in:   "日本語",
			want: "日本語",
		},
		"emoji": {
			in:   "hello 🌍",
			want: "hello 🌍",
		},
		"width fold fullwidth latin": {
			opts: []normalizer.Option{normalizer.WithWidthFold(true)},
			in:   "ａｂｃ",
			want: "abc",
		},
		"width fold with case fold": {
			opts: []normalizer.Option{normalizer.WithWidthFold(true)},
			in:   "ＡＢＣ",
			want: "abc",
		},
		"custom transformer": {
			opts: []normalizer.Option{
				normalizer.WithCaseFold(false),
				normalizer.WithDiacriticFold(false),
				normalizer.WithTransformer(runes.Map(func(r rune) rune {
					if r == 'a' {
						return 'x'
					}

					return r
				})),
			},
			in:   "abc",
			want: "xbc",
		},
		"multiple custom transformers": {
			opts: []normalizer.Option{
				normalizer.WithCaseFold(false),
				normalizer.WithDiacriticFold(false),
				normalizer.WithTransformer(runes.Map(func(r rune) rune {
					if r == 'a' {
						return 'b'
					}

					return r
				})),
				normalizer.WithTransformer(runes.Remove(runes.In(unicode.Zs))),
			},
			in:   "a b c",
			want: "bbc",
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			n := normalizer.New(tc.opts...)
			got := n.Normalize(tc.in)
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestNormalize_Concurrent(t *testing.T) {
	t.Parallel()

	n := normalizer.New()

	var wg sync.WaitGroup

	for range 100 {
		wg.Go(func() {
			got := n.Normalize("Café")
			assert.Equal(t, "cafe", got)
		})
	}

	wg.Wait()
}
