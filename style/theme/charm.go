package theme

import (
	"go.jacobcolvin.com/niceyaml/style"
)

// Charm returns [style.Styles] using CharmTone colors. It is the palette
// [style.Default] returns, listed here so it can be picked by name.
func Charm() style.Styles {
	return style.Default()
}
