package yamltest

// RewriteError wraps an error behind a message of its own, the way a wrapper
// that does not keep the text of the error it wraps does. Its message is
// "rewritten".
type RewriteError struct {
	Err error
}

// Error returns "rewritten".
func (e RewriteError) Error() string {
	return "rewritten"
}

// Unwrap returns the wrapped error.
func (e RewriteError) Unwrap() error {
	return e.Err
}
