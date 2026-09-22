package yamltest

// RewriteError models a wrapper that discards the text of the error it
// wraps. Its message is "rewritten".
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
