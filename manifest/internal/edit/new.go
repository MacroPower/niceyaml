package edit

// NewNode wraps a handle in the type of the migrate package, which sets
// it when it loads. The manifest package hands each step the result.
var NewNode func(h *Handle) any
