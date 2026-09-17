package schema

// FileURLPath exposes fileURLPath to the external test package, so the
// Windows drive-letter handling can run on every platform.
var FileURLPath = fileURLPath
