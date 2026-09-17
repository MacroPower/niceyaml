package schema

// Hooks into the package internals, so the Windows drive-letter handling
// can run on every platform.
var (
	// FileURLPath exposes fileURLPath to the external test package.
	FileURLPath = fileURLPath

	// HasDriveLetter exposes hasDriveLetter to the external test package.
	HasDriveLetter = hasDriveLetter
)
