package normalizer

import (
	"log/slog"
	"sync"
	"unicode"

	"golang.org/x/text/cases"
	"golang.org/x/text/language"
	"golang.org/x/text/runes"
	"golang.org/x/text/transform"
	"golang.org/x/text/unicode/norm"
	"golang.org/x/text/width"
)

// Normalizer transforms strings by applying a configurable pipeline of Unicode
// transformations. [New] defines the pipeline once at construction time from
// the provided [Option] values.
//
// Normalizer is safe for concurrent use. Each call borrows a pipeline
// instance from a pool, so concurrent calls never wait on each other.
//
// Create instances with [New].
type Normalizer struct {
	pool sync.Pool
}

// Option configures a [Normalizer].
//
// Available options:
//   - [WithCaseFold]
//   - [WithDiacriticFold]
//   - [WithTransformer]
//   - [WithWidthFold]
type Option func(*config)

// diacriticBlocks holds the combining diacritical mark blocks. The
// diacritic stage removes only the nonspacing marks in these blocks. Vowel
// signs, viramas, and kana voicing marks are also nonspacing marks, but they
// belong to the letter, so removing them would make distinct words equal.
// Enclosing marks such as the keycap stay too.
var diacriticBlocks = &unicode.RangeTable{
	R16: []unicode.Range16{
		{Lo: 0x0300, Hi: 0x036f, Stride: 1}, // Combining Diacritical Marks.
		{Lo: 0x1ab0, Hi: 0x1aff, Stride: 1}, // Combining Diacritical Marks Extended.
		{Lo: 0x1dc0, Hi: 0x1dff, Stride: 1}, // Combining Diacritical Marks Supplement.
		{Lo: 0x20d0, Hi: 0x20ff, Stride: 1}, // Combining Diacritical Marks for Symbols.
		{Lo: 0xfe20, Hi: 0xfe2f, Stride: 1}, // Combining Half Marks.
	},
}

func isDiacritic(r rune) bool {
	return unicode.Is(unicode.Mn, r) && unicode.Is(diacriticBlocks, r)
}

type config struct {
	transformers []func() transform.Transformer
	caseFold     bool
	diacritics   bool
	widthFold    bool
}

// New creates a new [*Normalizer].
//
// By default, the pipeline removes diacritics and case-folds text. Use
// [Option] values to customize the pipeline.
func New(opts ...Option) *Normalizer {
	cfg := config{
		caseFold:   true,
		diacritics: true,
	}

	for _, opt := range opts {
		opt(&cfg)
	}

	n := &Normalizer{}
	n.pool.New = func() any {
		return cfg.build()
	}

	return n
}

// build constructs a fresh pipeline. Transformers carry state between calls,
// so every pooled instance gets its own.
func (c *config) build() transform.Transformer {
	var transformers []transform.Transformer

	if c.widthFold {
		transformers = append(transformers, width.Fold)
	}

	if c.diacritics {
		transformers = append(transformers,
			norm.NFD,
			runes.Remove(runes.Predicate(isDiacritic)),
			norm.NFC,
		)
	}

	if c.caseFold {
		// The x/text Fold swaps upper- and lowercase Cherokee instead of
		// mapping both to one form, so Lower follows it to collapse each
		// pair. Fold returns lowercase for every other rune, which Lower
		// keeps as is.
		transformers = append(transformers,
			cases.Fold(),
			cases.Lower(language.Und),
		)
	}

	for _, newTransformer := range c.transformers {
		transformers = append(transformers, newTransformer())
	}

	switch len(transformers) {
	case 0:
		return transform.Nop
	case 1:
		return transformers[0]
	default:
		return transform.Chain(transformers...)
	}
}

// WithCaseFold is an [Option] that toggles Unicode case folding.
// When true (the default), the pipeline case-folds text for case-insensitive
// comparison.
func WithCaseFold(enabled bool) Option {
	return func(c *config) {
		c.caseFold = enabled
	}
}

// WithDiacriticFold is an [Option] that toggles diacritics removal.
// When true (the default), the pipeline decomposes text and removes the
// combining diacritical marks, such as accents, umlauts, and cedillas. For
// example, "Ö" becomes "O" (before any case folding). Marks that form part of
// a letter stay, so "ガ" keeps its voicing mark and Thai and Devanagari vowel
// signs survive.
func WithDiacriticFold(enabled bool) Option {
	return func(c *config) {
		c.diacritics = enabled
	}
}

// WithTransformer is an [Option] that appends custom transformers to the end
// of the pipeline. Call it multiple times to append more transformers.
//
// Each argument is a constructor rather than an instance, because a
// [transform.Transformer] carries state between calls and the Normalizer
// keeps one pipeline per concurrent caller:
//
//	normalizer.WithTransformer(func() transform.Transformer {
//		return runes.Remove(runes.In(unicode.Zs))
//	})
func WithTransformer(newTransformer ...func() transform.Transformer) Option {
	return func(c *config) {
		c.transformers = append(c.transformers, newTransformer...)
	}
}

// WithWidthFold is an [Option] that toggles Unicode width folding.
// When true, the pipeline normalizes fullwidth and halfwidth characters to
// their canonical forms. For example, "ａｂｃ" becomes "abc".
func WithWidthFold(enabled bool) Option {
	return func(c *config) {
		c.widthFold = enabled
	}
}

// Normalize applies the configured transformations to the input string.
// A nil Normalizer returns the input string unchanged, and so does a
// transformation that fails.
func (n *Normalizer) Normalize(in string) string {
	if n == nil {
		return in
	}

	t, ok := n.pool.Get().(transform.Transformer)
	if !ok {
		return in
	}

	defer n.pool.Put(t)

	out, _, err := transform.String(t, in)
	if err != nil {
		slog.Debug("normalize string", slog.Any("error", err))

		return in
	}

	return out
}
