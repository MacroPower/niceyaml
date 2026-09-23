package schema

// Hooks into the package internals, so the Windows drive-letter handling
// can run on every platform and a test can hand the error path walk a
// tree the parser never builds.
var (
	// FileURLPath exposes fileURLPath to the external test package.
	FileURLPath = fileURLPath

	// HasDriveLetter exposes hasDriveLetter to the external test package.
	HasDriveLetter = hasDriveLetter

	// SourcePath exposes sourcePath to the external test package.
	SourcePath = sourcePath

	// ReadFile exposes readFile to the external test package.
	ReadFile = readFile
)
