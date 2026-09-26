// Package segment splits go-yaml tokens at line boundaries.
//
// A YAML token can span several lines, as block scalars and multiline
// strings do, while rendering, diffing, and searching work one line at a
// time. [Split] cuts a [token.Tokens] stream into one [Line] per source
// line, and each [Segment] on a line pairs the part that sits on that line
// with the original token it was cut from.
//
// Consider this YAML input with a block scalar:
//
//	┌───────────────────────────┐
//	│foo: |-                    │
//	│  hello                    │
//	│  world                    │
//	└───────────────────────────┘
//
// The go-yaml lexer produces a [token.Tokens] stream where the block scalar
// content is a single token spanning multiple lines:
//
//	┌──────┬────────────┬───────┐
//	│String│MappingValue│Literal│
//	├──────┴────────────┴───────┤
//	│String                     │
//	│                           │
//	└───────────────────────────┘
//
// [Split] cuts the tokens at line boundaries while every part keeps a
// reference to its source token:
//
//	┌──────┬────────────┬───────┐
//	│String│MappingValue│Literal│
//	├──────┴────────────┴───────┤
//	│String                     │
//	├───────────────────────────┤
//	│String                     │
//	└───────────────────────────┘
//
// The line package builds its own Line type from each [Line], with the
// same segments and number. The flag, overlays, and annotations that
// rendering attaches to a line live on the line package's View.
package segment
