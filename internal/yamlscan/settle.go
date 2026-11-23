package yamlscan

import (
	"strings"
	"unicode/utf8"

	"github.com/goccy/go-yaml/token"
)

// settleWhitespace moves the whitespace between two tokens to where
// niceyaml's tokens.Tokenize puts it. Upstream drops the spaces and tabs
// that end a line outside a block scalar, and all the whitespace in front
// of a ":". The scanner keeps that whitespace, and drops holds the rune
// index of each line break whose line upstream cut that way. Where the
// whitespace between two tokens that hold text holds such a line break,
// the earlier token takes the line breaks and the lines they close. The
// later token keeps the indentation of its own line. A ":" on a later
// line than its key keeps the line breaks, and the key takes only the
// spaces and tabs that end its line. Any other whitespace stays where the
// scanner put it.
func settleWhitespace(tks token.Tokens, drops []int) {
	var (
		prev    *token.Token
		prevEnd int // The rune index past the text of prev.
		off     int // The rune index where the Origin of the token starts.
	)

	for _, cur := range tks {
		text := strings.TrimLeft(cur.Origin, " \t\r\n")
		size := utf8.RuneCountInString(cur.Origin)

		if text == "" {
			// A token of whitespace alone keeps the whitespace around it.
			prev = nil
			off += size

			continue
		}

		lead := cur.Origin[:len(cur.Origin)-len(text)]
		start := off + utf8.RuneCountInString(lead)
		body := strings.TrimRight(text, " \t\r\n")
		end := start + utf8.RuneCountInString(body)

		for len(drops) > 0 && drops[0] < prevEnd {
			drops = drops[1:]
		}

		dropped := len(drops) > 0 && drops[0] < start
		if prev != nil && (dropped || cur.Type == token.MappingValueType && lead != "") {
			settle(prev, cur, lead, text)
		}

		prev, prevEnd = cur, end
		off += size
	}
}

func settle(prev, cur *token.Token, lead, text string) {
	body := strings.TrimRight(prev.Origin, " \t\r\n")
	trail := prev.Origin[len(body):]
	gap := trail + lead

	cut := strings.LastIndexAny(gap, "\r\n") + 1
	if cur.Type == token.MappingValueType && cut > 0 && !strings.ContainsAny(trail, "\r\n") {
		cut = strings.IndexAny(gap, "\r\n")
	}

	prev.Origin = body + gap[:cut]
	cur.Origin = gap[cut:] + text
}
