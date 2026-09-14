package normalizer

import (
	"log/slog"
	"sync"
	"unicode"

	"golang.org/x/text/cases"
	"golang.org/x/text/runes"
	"golang.org/x/text/transform"
	"golang.org/x/text/unicode/norm"
	"golang.org/x/text/width"
)

// Normalizer transforms strings by applying a configurable pipeline of Unicode
// transformations. The pipeline is defined once at construction time from the
// provided [Option] values.
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

type config struct {
	transformers []func() transform.Transformer
	caseFold     bool
	diacritics   bool
	widthFold    bool
}

// New creates a new [*Normalizer].
//
// By default, diacritics are removed and text is case-folded. Use [Option]
// values to customize the pipeline.
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
			runes.Remove(runes.In(unicode.Mn)),
			norm.NFC,
		)
	}

	if c.caseFold {
		transformers = append(transformers, cases.Fold())
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
// When true (the default), text is case-folded for case-insensitive
// comparison.
func WithCaseFold(enabled bool) Option {
	return func(c *config) {
		c.caseFold = enabled
	}
}

// WithDiacriticFold is an [Option] that toggles diacritics removal.
// When true (the default), diacritics are folded away. For example, "Ö"
// becomes "O" (before any case folding).
func WithDiacriticFold(enabled bool) Option {
	return func(c *config) {
		c.diacritics = enabled
	}
}

// WithTransformer is an [Option] that appends custom transformers to the end
// of the pipeline. Can be called multiple times to add additional
// transformers.
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
// When true, fullwidth and halfwidth characters are normalized to their
// canonical forms. For example, "ａｂｃ" becomes "abc".
func WithWidthFold(enabled bool) Option {
	return func(c *config) {
		c.widthFold = enabled
	}
}

// Normalize applies the configured transformations to the input string.
// If the transformation fails, the original string is returned unchanged.
func (n *Normalizer) Normalize(in string) string {
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
