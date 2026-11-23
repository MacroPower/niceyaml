package yamlscan

import (
	"strings"

	"github.com/goccy/go-yaml/token"
)

// setPositions gives each token of tks the line, column, and offset of the
// rune where its text starts, counted from 1 over the joined Origins. The
// Origins join to the source, so the count needs nothing else. A token
// without text sits where the next text starts, or at the end of the last
// line when no text follows it. IndentNum and IndentLevel stay as the
// scanner set them.
func setPositions(tks token.Tokens) {
	c := cursor{line: 1, col: 1}

	var pending token.Tokens

	for _, tk := range tks {
		text := strings.TrimLeft(tk.Origin, " \t\r\n")
		if text == "" {
			pending = append(pending, tk)
			c.over(tk.Origin)

			continue
		}

		c.over(tk.Origin[:len(tk.Origin)-len(text)])

		for _, p := range append(pending, tk) {
			c.set(p)
		}

		pending = pending[:0]

		c.over(text)
	}

	if len(pending) == 0 {
		return
	}

	var sb strings.Builder

	for _, tk := range tks {
		sb.WriteString(tk.Origin)
	}

	c = cursor{line: 1, col: 1}
	c.over(strings.TrimSuffix(strings.TrimSuffix(sb.String(), "\n"), "\r"))

	for _, p := range pending {
		c.set(p)
	}
}

// cursor counts the line, the column, and the rune offset of a place in
// the joined Origins.
type cursor struct {
	line, col, off int
	// Whether the last rune was a "\r", which a "\n" joins into one CRLF.
	cr bool
}

// over moves the cursor across s. A "\n", a "\r\n", and a bare "\r" each
// move it to column 1 of the next line, and any other rune moves it one
// column to the right. A "\r" that ends one call and a "\n" that opens the
// next are one CRLF.
func (c *cursor) over(s string) {
	for _, r := range s {
		c.off++

		switch {
		case r == '\n' && c.cr:
			// The "\r" before it moved the cursor to the next line.
		case r == '\n', r == '\r':
			c.line, c.col = c.line+1, 1
		default:
			c.col++
		}

		c.cr = r == '\r'
	}
}

// set gives tk the place of the cursor.
func (c *cursor) set(tk *token.Token) {
	tk.Position.Line, tk.Position.Column, tk.Position.Offset = c.line, c.col, c.off+1
}
