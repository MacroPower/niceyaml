package normalizer

import (
	"log/slog"
	"unicode"

	"golang.org/x/text/cases"
	"golang.org/x/text/runes"
	"golang.org/x/text/transform"
	"golang.org/x/text/unicode/norm"
	"golang.org/x/text/width"
)

// Normalizer transforms strings by applying a configurable pipeline of Unicode
// transformations. The pipeline is built once at construction time from the
// provided [Option] values.
//
// Create instances with [New].
type Normalizer struct {
	transformer transform.Transformer
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
	transformers []transform.Transformer
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

	var transformers []transform.Transformer

	if cfg.widthFold {
		transformers = append(transformers, width.Fold)
	}

	if cfg.diacritics {
		transformers = append(transformers,
			norm.NFD,
			runes.Remove(runes.In(unicode.Mn)),
			norm.NFC,
		)
	}

	if cfg.caseFold {
		transformers = append(transformers, cases.Fold())
	}

	transformers = append(transformers, cfg.transformers...)

	var t transform.Transformer
	switch len(transformers) {
	case 0:
		t = transform.Nop
	case 1:
		t = transformers[0]
	default:
		t = transform.Chain(transformers...)
	}

	return &Normalizer{transformer: t}
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
// of the pipeline. Can be called multiple times to add additional transformers.
func WithTransformer(t ...transform.Transformer) Option {
	return func(c *config) {
		c.transformers = append(c.transformers, t...)
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
	n.transformer.Reset()

	out, _, err := transform.String(n.transformer, in)
	if err != nil {
		slog.Debug("normalize string", slog.Any("error", err))
		return in
	}

	return out
}
