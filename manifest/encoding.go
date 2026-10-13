package manifest

import (
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"go.jacobcolvin.com/niceyaml"
	"go.jacobcolvin.com/niceyaml/internal/bom"
	"go.jacobcolvin.com/niceyaml/position"
)

// errSaveUTF8 is the detail of each encoding problem.
var errSaveUTF8 = errors.New("save the file as UTF-8")

// checkEncoding returns the problem of src, a file that a load read
// itself, when its text is not UTF-8, or nil. A UTF-8 byte order mark can
// start the text. A text whose byte order mark or leading zero bytes show
// UTF-16 or UTF-32 fails whole, and any other text fails at its first byte
// that UTF-8 does not allow.
func checkEncoding(src *niceyaml.Source) error {
	text := src.Text()

	if enc := wideEncoding(text); enc != "" {
		return src.Bind(newProblem(ErrEncoding, "file is "+enc+", not UTF-8", niceyaml.WithDetails(errSaveUTF8)))
	}

	body := strings.TrimPrefix(text, bom.Mark)

	off := invalidByte(body)
	if off < 0 {
		return nil
	}

	at := positionAt(body, off)

	return src.Bind(newProblem(ErrEncoding, fmt.Sprintf("byte 0x%02x is not UTF-8", body[off]),
		niceyaml.AtRange(position.NewRange(at, position.New(at.Line, at.Col+1))),
		niceyaml.WithDetails(errSaveUTF8)))
}

// wideEncoding returns "UTF-16" or "UTF-32" when the byte order mark or the
// leading zero bytes of text show that encoding, as the YAML specification
// detects it, or "" otherwise.
func wideEncoding(text string) string {
	switch {
	case strings.HasPrefix(text, "\x00\x00\xfe\xff"), strings.HasPrefix(text, "\xff\xfe\x00\x00"):
		return "UTF-32"
	case strings.HasPrefix(text, "\xfe\xff"), strings.HasPrefix(text, "\xff\xfe"):
		return "UTF-16"
	}

	if len(text) >= 4 {
		switch {
		case text[0] == 0 && text[1] == 0 && text[2] == 0 && text[3] != 0,
			text[0] != 0 && text[1] == 0 && text[2] == 0 && text[3] == 0:
			return "UTF-32"
		}
	}

	if len(text) >= 2 && (text[0] == 0) != (text[1] == 0) {
		return "UTF-16"
	}

	return ""
}

// invalidByte returns the offset of the first byte of text that UTF-8 does
// not allow, or -1 when text is valid.
func invalidByte(text string) int {
	for i := 0; i < len(text); {
		r, size := utf8.DecodeRuneInString(text[i:])
		if r == utf8.RuneError && size <= 1 {
			return i
		}

		i += size
	}

	return -1
}

// positionAt returns the position of the byte at off in text, with the
// column counted in runes, as a [position.Position] counts it.
func positionAt(text string, off int) position.Position {
	before := text[:off]
	line := strings.Count(before, "\n")
	start := strings.LastIndexByte(before, '\n') + 1

	return position.New(line, utf8.RuneCountInString(before[start:]))
}
