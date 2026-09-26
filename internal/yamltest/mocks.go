package yamltest

// NormalizerFunc adapts a function to the
// [go.jacobcolvin.com/niceyaml/finder.Normalizer] interface.
type NormalizerFunc func(in string) string

// Normalize implements [go.jacobcolvin.com/niceyaml/finder.Normalizer].
func (f NormalizerFunc) Normalize(in string) string {
	return f(in)
}
