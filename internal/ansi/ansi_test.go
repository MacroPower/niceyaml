package ansi_test

import (
	"testing"
	"unicode/utf8"

	"github.com/stretchr/testify/assert"

	"go.jacobcolvin.com/niceyaml/internal/ansi"
)

func TestEscape(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		input string
		want  string
	}{
		"empty string": {
			input: "",
			want:  "",
		},
		"normal text unchanged": {
			input: "Hello, World!",
			want:  "Hello, World!",
		},
		"NUL replaced": {
			input: "before\x00after",
			want:  "before␀after",
		},
		"ESC replaced": {
			input: "before\x1bafter",
			want:  "before␛after",
		},
		"DEL replaced": {
			input: "before\x7fafter",
			want:  "before␡after",
		},
		"TAB replaced": {
			input: "col1\tcol2",
			want:  "col1␉col2",
		},
		"newline replaced": {
			input: "line1\nline2",
			want:  "line1␊line2",
		},
		"carriage return replaced": {
			input: "line1\rline2",
			want:  "line1␍line2",
		},
		"ANSI red sequence": {
			input: "\x1b[31mRed\x1b[0m",
			want:  "␛[31mRed␛[0m",
		},
		"ANSI bold sequence": {
			input: "\x1b[1mBold\x1b[22m",
			want:  "␛[1mBold␛[22m",
		},
		"multiple control chars": {
			input: "\x00\x01\x02\x1f",
			want:  "␀␁␂␟",
		},
		"mixed content": {
			input: "normal\x1b[31mred\x00null",
			want:  "normal␛[31mred␀null",
		},
		"unicode preserved": {
			input: "日本語\x1bテスト",
			want:  "日本語␛テスト",
		},
		"high ASCII preserved": {
			input: "café\x1btest",
			want:  "café␛test",
		},
		"C1 PAD replaced": {
			input: "before\u0080after",
			want:  "before\uFFFDafter",
		},
		"C1 APC replaced": {
			input: "before\u009Fafter",
			want:  "before\uFFFDafter",
		},
		"C1 CSI replaced": {
			input: "before\u009Bafter",
			want:  "before\uFFFDafter",
		},
		"multiple C1 controls": {
			input: "\u0080\u0085\u009B\u009F",
			want:  "\uFFFD\uFFFD\uFFFD\uFFFD",
		},
		"mixed C0 and C1 controls": {
			input: "\x00\u0080\x1b\u009B",
			want:  "␀\uFFFD␛\uFFFD",
		},
		// Boundary tests.
		"boundary US (0x1F) last C0 control": {
			input: "a\x1fb",
			want:  "a␟b",
		},
		"boundary space (0x20) first printable unchanged": {
			input: "a b",
			want:  "a b",
		},
		"boundary tilde (0x7E) last regular printable unchanged": {
			input: "a~b",
			want:  "a~b",
		},
		"boundary DEL (0x7F) replaced": {
			input: "a\x7fb",
			want:  "a␡b",
		},
		"boundary PAD (0x80) first C1 control replaced": {
			input: "a\u0080b",
			want:  "a\uFFFDb",
		},
		"boundary APC (0x9F) last C1 control replaced": {
			input: "a\u009Fb",
			want:  "a\uFFFDb",
		},
		"boundary NBSP (0xA0) after C1 unchanged": {
			input: "a\u00A0b",
			want:  "a\u00A0b",
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got := ansi.Escape(tt.input)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestEscape_PreservesRuneCount(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		input string
	}{
		"empty string": {
			input: "",
		},
		"normal text": {
			input: "Hello, World!",
		},
		"single control char": {
			input: "\x00",
		},
		"multiple control chars": {
			input: "\x00\x01\x02\x1f\x7f",
		},
		"ANSI escape sequence": {
			input: "\x1b[31mRed\x1b[0m",
		},
		"mixed content": {
			input: "before\x00middle\x1b[31mred\x7fafter",
		},
		"unicode with control chars": {
			input: "日本語\x1bテスト\x00end",
		},
		"all C0 controls": {
			input: "\x00\x01\x02\x03\x04\x05\x06\x07\x08\x09\x0a\x0b\x0c\x0d\x0e\x0f" +
				"\x10\x11\x12\x13\x14\x15\x16\x17\x18\x19\x1a\x1b\x1c\x1d\x1e\x1f",
		},
		"all C1 controls": {
			input: "\u0080\u0081\u0082\u0083\u0084\u0085\u0086\u0087\u0088\u0089\u008A\u008B\u008C\u008D\u008E\u008F" +
				"\u0090\u0091\u0092\u0093\u0094\u0095\u0096\u0097\u0098\u0099\u009A\u009B\u009C\u009D\u009E\u009F",
		},
		"mixed C0 and C1 controls": {
			input: "\x00\u0080\x1b\u009B\x7f\u009F",
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			input := tt.input
			got := ansi.Escape(input)

			inputRuneCount := utf8.RuneCountInString(input)
			gotRuneCount := utf8.RuneCountInString(got)

			assert.Equal(t, inputRuneCount, gotRuneCount,
				"rune count changed: input %q (%d runes) -> output %q (%d runes)",
				input, inputRuneCount, got, gotRuneCount)
		})
	}
}
