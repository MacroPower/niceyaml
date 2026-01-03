package yamltest

// MockValidator is a test helper that wraps a validation function.
type MockValidator struct {
	fn func(input any) error
}

// NewPassingValidator creates a new [MockValidator] that always passes validation.
func NewPassingValidator() *MockValidator {
	return &MockValidator{
		fn: func(_ any) error { return nil },
	}
}

// NewFailingValidator creates a new [MockValidator] that always fails validation
// with the given error.
func NewFailingValidator(err error) *MockValidator {
	return &MockValidator{
		fn: func(_ any) error { return err },
	}
}

// NewCustomValidator creates a new [MockValidator] that uses the given function
// for validation.
func NewCustomValidator(fn func(input any) error) *MockValidator {
	return &MockValidator{
		fn: fn,
	}
}

// Validate calls the wrapped validation function.
func (m *MockValidator) Validate(input any) error {
	return m.fn(input)
}

// MockNormalizer is a test helper that wraps a normalization function.
type MockNormalizer struct {
	fn func(in string) string
}

// NewIdentityNormalizer creates a new [MockNormalizer] that returns input unchanged.
func NewIdentityNormalizer() *MockNormalizer {
	return &MockNormalizer{
		fn: func(in string) string { return in },
	}
}

// NewStaticNormalizer creates a new [MockNormalizer] that always returns
// the given output regardless of input.
func NewStaticNormalizer(output string) *MockNormalizer {
	return &MockNormalizer{
		fn: func(_ string) string { return output },
	}
}

// NewCustomNormalizer creates a new [MockNormalizer] that uses the given function
// for normalization.
func NewCustomNormalizer(fn func(in string) string) *MockNormalizer {
	return &MockNormalizer{
		fn: fn,
	}
}

// Normalize calls the wrapped normalization function.
func (m *MockNormalizer) Normalize(in string) string {
	return m.fn(in)
}
