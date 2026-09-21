package schema

// Hooks into the package internals, so the Windows drive-letter handling
// can run on every platform and the error position walk can be handed a
// tree the parser never builds.
var (
	// FileURLPath exposes fileURLPath to the external test package.
	FileURLPath = fileURLPath

	// HasDriveLetter exposes hasDriveLetter to the external test package.
	HasDriveLetter = hasDriveLetter

	// WalkSegments exposes walkSegments to the external test package.
	WalkSegments = walkSegments
)
