package normalizer_test

import (
	"sync"
	"testing"
	"unicode"
	"unicode/utf8"

	"github.com/stretchr/testify/assert"
	"golang.org/x/text/runes"
	"golang.org/x/text/transform"

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
		"default keeps kana voicing mark": {
			in:   "ガ",
			want: "ガ",
		},
		"default keeps kana semi-voiced mark": {
			in:   "パ",
			want: "パ",
		},
		"default keeps Thai vowel sign": {
			in:   "กิน",
			want: "กิน",
		},
		"default keeps Devanagari vowel sign": {
			in:   "कुम",
			want: "कुम",
		},
		"default removes Vietnamese stacked diacritics": {
			in:   "Việt",
			want: "viet",
		},
		"default keeps keycap emoji": {
			in:   "1\ufe0f\u20e3",
			want: "1\ufe0f\u20e3",
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
		"case fold cherokee uppercase": {
			in:   "\u13a0\u13f0",
			want: "\uab70\u13f8",
		},
		"case fold cherokee lowercase": {
			in:   "\uab70\u13f8",
			want: "\uab70\u13f8",
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
				normalizer.WithTransformer(func() transform.Transformer {
					return runes.Map(func(r rune) rune {
						if r == 'a' {
							return 'x'
						}

						return r
					})
				}),
			},
			in:   "abc",
			want: "xbc",
		},
		"multiple custom transformers": {
			opts: []normalizer.Option{
				normalizer.WithCaseFold(false),
				normalizer.WithDiacriticFold(false),
				normalizer.WithTransformer(func() transform.Transformer {
					return runes.Map(func(r rune) rune {
						if r == 'a' {
							return 'b'
						}

						return r
					})
				}),
				normalizer.WithTransformer(func() transform.Transformer {
					return runes.Remove(runes.In(unicode.Zs))
				}),
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

func TestNormalize_Idempotent(t *testing.T) {
	t.Parallel()

	n := normalizer.New()

	for r := range rune(0x30000) {
		if !utf8.ValidRune(r) {
			continue
		}

		once := n.Normalize(string(r))
		if twice := n.Normalize(once); twice != once {
			assert.Failf(t, "not idempotent", "%U: %q then %q", r, once, twice)
		}
	}
}

func TestNormalize_NilNormalizer(t *testing.T) {
	t.Parallel()

	var n *normalizer.Normalizer

	assert.Equal(t, "Café", n.Normalize("Café"))
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

func TestWithTransformer_Nil(t *testing.T) {
	t.Parallel()

	nop := func() transform.Transformer {
		return transform.Nop
	}
	returnsNil := func() transform.Transformer {
		return nil
	}
	returnsNilPointer := func() transform.Transformer {
		return (*runes.Transformer)(nil)
	}

	tcs := map[string]struct {
		call func()
		want string
	}{
		"nil constructor": {
			call: func() {
				normalizer.WithTransformer(nil)
			},
			want: "normalizer.WithTransformer: constructor at index 0 is nil",
		},
		"nil constructor after a valid one": {
			call: func() {
				normalizer.WithTransformer(nop, nil)
			},
			want: "normalizer.WithTransformer: constructor at index 1 is nil",
		},
		"constructor returns nil with default stages": {
			call: func() {
				normalizer.New(normalizer.WithTransformer(returnsNil))
			},
			want: "normalizer.WithTransformer: constructor returned nil",
		},
		"constructor returns nil as the only stage": {
			call: func() {
				normalizer.New(
					normalizer.WithCaseFold(false),
					normalizer.WithDiacriticFold(false),
					normalizer.WithTransformer(returnsNil),
				)
			},
			want: "normalizer.WithTransformer: constructor returned nil",
		},
		"constructor returns nil pointer": {
			call: func() {
				normalizer.New(normalizer.WithTransformer(returnsNilPointer))
			},
			want: "normalizer.WithTransformer: constructor returned nil",
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			assert.PanicsWithValue(t, tc.want, tc.call)
		})
	}
}
