package yamltest

import "strings"

// Input is a helper to normalize test input strings.
// It dedents the string by removing the common leading whitespace from all lines,
// allowing test inputs to be indented for readability while producing clean output.
// At most one leading newline and one trailing newline are stripped.
func Input(s string) string {
	// Strip at most one leading newline (allows backtick strings to start on next line).
	s = strings.TrimPrefix(s, "\n")

	// Strip trailing spaces/tabs (allows closing backtick to be indented).
	s = strings.TrimRight(s, " \t")

	// Strip at most one trailing newline.
	s = strings.TrimSuffix(s, "\n")

	lines := strings.Split(s, "\n")
	if len(lines) == 0 {
		return ""
	}

	// Find minimum indentation across non-empty lines.
	minIndent := -1
	for _, line := range lines {
		if strings.TrimSpace(line) == "" {
			continue // Skip empty/whitespace-only lines.
		}

		indent := len(line) - len(strings.TrimLeft(line, " \t"))
		if minIndent == -1 || indent < minIndent {
			minIndent = indent
		}
	}

	if minIndent <= 0 {
		return strings.Join(lines, "\n")
	}

	// Remove common indentation from all lines.
	for i, line := range lines {
		if len(line) >= minIndent {
			lines[i] = line[minIndent:]
		}
	}

	return strings.Join(lines, "\n")
}
