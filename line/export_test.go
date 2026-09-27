package line

// NewNumberedEmptyLine creates a [Line] with number n and no tokens.
// [NewLines] never creates such a line, so a test can hand one to a
// validator that must reject it.
func NewNumberedEmptyLine(n int) *Line {
	return &Line{number: n}
}
