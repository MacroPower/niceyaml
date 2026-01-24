package yamltest

// MockSchemaValidator implements [niceyaml.SchemaValidator] for testing.
//
// It wraps a validation function that can be configured to pass, fail, or
// implement custom logic.
//
// Create instances with [NewPassingSchemaValidator],
// [NewFailingSchemaValidator], or [NewCustomSchemaValidator].
type MockSchemaValidator struct {
	fn func(data any) error
}

// NewPassingSchemaValidator creates a new [*MockSchemaValidator] that always
// passes validation by returning nil.
func NewPassingSchemaValidator() *MockSchemaValidator {
	return &MockSchemaValidator{
		fn: func(_ any) error { return nil },
	}
}

// NewFailingSchemaValidator creates a new [*MockSchemaValidator] that always
// fails validation with the given error.
func NewFailingSchemaValidator(err error) *MockSchemaValidator {
	return &MockSchemaValidator{
		fn: func(_ any) error { return err },
	}
}

// NewCustomSchemaValidator creates a new [*MockSchemaValidator] that uses the
// given function for validation.
func NewCustomSchemaValidator(fn func(data any) error) *MockSchemaValidator {
	return &MockSchemaValidator{
		fn: fn,
	}
}

// ValidateSchema calls the wrapped validation function.
func (m *MockSchemaValidator) ValidateSchema(data any) error {
	return m.fn(data)
}

// MockNormalizer implements [niceyaml.Normalizer] for testing.
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
