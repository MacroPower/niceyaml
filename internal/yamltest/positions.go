package yamltest

import "go.jacobcolvin.com/niceyaml/position"

// LineRange returns the [position.Range] of the columns from start to end
// on the line at index line. Like every range, it leaves out the column at
// end.
func LineRange(line, start, end int) position.Range {
	return position.NewRange(position.New(line, start), position.New(line, end))
}
