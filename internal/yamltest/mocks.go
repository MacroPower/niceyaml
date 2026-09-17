package yamltest

// MockNormalizer implements [finder.Normalizer] for testing.
//
// It wraps a normalization function that can be configured to return input
// unchanged, return static output, or implement custom logic.
//
// Create instances with [NewIdentityNormalizer], [NewStaticNormalizer], or
// [NewCustomNormalizer].
type MockNormalizer struct {
	fn func(in string) string
}

// NewIdentityNormalizer creates a new [*MockNormalizer] that returns its input
// unchanged.
func NewIdentityNormalizer() *MockNormalizer {
	return &MockNormalizer{
		fn: func(in string) string { return in },
	}
}

// NewStaticNormalizer creates a new [*MockNormalizer] that always returns the
// given output regardless of input.
func NewStaticNormalizer(output string) *MockNormalizer {
	return &MockNormalizer{
		fn: func(_ string) string { return output },
	}
}

// NewCustomNormalizer creates a new [*MockNormalizer] that uses the given
// function for normalization.
func NewCustomNormalizer(fn func(in string) string) *MockNormalizer {
	return &MockNormalizer{
		fn: fn,
	}
}

// Normalize calls the wrapped normalization function.
func (m *MockNormalizer) Normalize(in string) string {
	return m.fn(in)
}
