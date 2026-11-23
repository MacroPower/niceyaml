// Package yamlscan is a patched copy of the scanner of go-yaml.
//
// The copy holds go-yaml's scanner package and the Tokenize function of its
// lexer package at the commit that UPSTREAM_COMMIT names. It emits the
// [github.com/goccy/go-yaml/token.Token] values of the go-yaml module that
// go.mod requires, so the commit of the copy must match that version.
// LICENSE holds the license of go-yaml.
//
// # Patches
//
// The patches make the Origins of the tokens join to the source, and they
// make each position name the rune where the text of its token starts.
// Each patch to a copied file carries a comment that opens with
// "niceyaml:". The copy keeps the spaces and tabs that end a line, the
// whitespace in front of a ":" or "?", and the code of a "\x", "\u", or
// "\U" escape. It ends the last token with the text that ends the input,
// and it gives a source without tokens, such as one of whitespace alone,
// one token that holds the whole text. It reads each line ending once,
// and a CRLF stays whole in one Origin. It does not repeat the end of a
// token as a token of its own. It closes the flow collections that a
// document leaves open at a "---" or "..." marker.
//
// Tokenize counts the positions from the joined Origins, in place of the
// counts of the scanner. It also moves the whitespace that go-yaml drops
// from an Origin to the token where niceyaml's tokens package expects it.
// The files position.go and settle.go hold those passes.
//
// Some patches change what the go-yaml parser reads, so that it reads
// the text as YAML does:
//
//   - The copy emits a tag that ends the input, where go-yaml drops it.
//     The parser then reads "b: !!str" as an empty string, as it reads
//     the same text with a final line break.
//   - The copy keeps the rune after a double-quoted scalar that holds a
//     tab between its quotes, where go-yaml skips one rune for each tab
//     that more text follows on its line.
//   - A tag without a value does not type the first key of the next
//     line, nor the first key of the mapping below the tag. Go-yaml reads
//     that key as a string, so it reads the key 0x10 below "a: !t" as
//     "0x10" and not as 16.
//   - A tab after a tag closes the tag, as a space does. Go-yaml reads the
//     tab and the text after it up to the next space, line break, or ","
//     in a flow collection into the tag. So it reads "!t\t&x 1" as the tag
//     "!t\t&x" on 1, without the anchor x.
//   - A tab after the name of an anchor or an alias closes the name, as a
//     space does. Go-yaml reads the tab and the text after it into the
//     name, up to the next space, line break, or comment. In a flow
//     collection a flow indicator such as "," ends the name too. So
//     go-yaml reads "&x\t1" as the anchor x1 on an empty value.
//   - The token of a tag, or of the name of an anchor or an alias, takes
//     the spaces and tabs after it through the last tab. Go-yaml opens the
//     next token with the tab after a space, and it refuses a key or a ":"
//     after that tab as a tab used as indentation. So go-yaml refuses
//     "&x \ta: 1", and the copy reads the anchor x on the key a.
//   - Inside a flow collection, a line folds into the plain scalar on the
//     line above. YAML folds it only when the spaces that open the line pass
//     the indent of the block node that holds the collection. Go-yaml cuts
//     the scalar at a line that starts at or left of the last ":", "?", or
//     "-" indicator. It reads a "-" there as a sequence entry. It reads a "-"
//     that starts a line right of that indicator as a folded block scalar,
//     which takes the rest of the text. So go-yaml refuses "{a: b\n - c}" and
//     "{a: b\n    - c}", and the copy reads both as {a: "b - c"}. YAML drops
//     the indent of each folded line, tabs included, and so does the copy.
//     Go-yaml reads a tab that opens the line into the scalar. Where YAML
//     refuses the scalar as the key of a ":" on a folded line, the copy cuts
//     the scalar at the start of that line. It reads a "-" or "?" that opens
//     the line as an indicator. So the parser
//     refuses "{a: b\n c: d}" and "{a: b\n    c: d}" as YAML does. Go-yaml
//     reads the second of those as {a: "b c: d"}. A ":" that a character
//     other than a blank or a flow indicator follows stays in a value or in
//     an item of a flow sequence that spans lines, as YAML keeps it. So the
//     copy reads "{a: !t b\n c :d}" as {a: "b c :d"}, where go-yaml refuses
//     the ":" after its cut. The copy reads a value the same way below a
//     comment line after the ":" of its key. So it reads
//     "{a: # c\n b\n c:d}" as {a: "b c:d"}, and it refuses
//     "{a: # c\n b\n c: d}". On one line inside a flow mapping, go-yaml
//     reads such a ":" as an indicator after a tag or an anchor on a value.
//     It also reads that ":" as an indicator on the line below a comment
//     line after the ":" of the key, and in an item of a flow sequence. So
//     go-yaml refuses "{a: # c\n b c:d}", and the copy reads
//     "{a: # c\n b\n c:d}" as {a: "b c:d"}. Go-yaml reads "{a: [b c:d]}"
//     as {a: [{"b c": d}]}, and the copy reads "{a: [b\n c:d]}" as
//     {a: ["b c:d"]}. A ":" before such a character ends a key of a flow
//     mapping that spans lines, as go-yaml reads it in a key on one line.
//     So the copy reads "{a\n b:c}" as {"a b": c}, as go-yaml reads
//     "{a b:c}".
//   - YAML reads a line of a flow collection only when the spaces that
//     open the line pass the indent of the block node that holds the
//     collection. Go-yaml does not check that indent inside a flow
//     collection, so its parser reads "key: [a\n]" as {key: [a]}. The copy
//     reports the first character of such a line as an invalid token, so
//     the decode of "key: [a\n]" and of "flow: [a,\nb]" fails. It reports a
//     quoted scalar with such a line as one invalid token, as for
//     "key: ["a\nb"]". A "]" or "}" there still closes its collection. A
//     comment line can start at any column, and so can each line of a flow
//     collection that is the root node of its document. The check skips a
//     "---" or "..." marker, which ends the document, so a collection that
//     is still open at the marker fails as one that is never closed.
//   - Inside a flow mapping, a ":" that a blank or a flow indicator
//     follows ends the plain value of a key on one line, as YAML ends it.
//     Go-yaml keeps each ":" of that value in the scalar, so it reads
//     "{a: b c: d}" as {a: "b c: d"}. YAML refuses the text, because a ","
//     must come before the next key. The copy reports the value and the
//     second ":" as an invalid token. So the decode of "{a: b c: d}" and of
//     "{a: b c: d, e: f}" fails. A ":" that another character follows
//     stays in the value, as in "{a: 12:30}".
//   - A ":" right before a "," or a "]" ends the plain scalar of an
//     explicit key in a flow sequence, as it ends a key inside a flow
//     mapping. Go-yaml keeps that ":" in the scalar, so it reads "[? a:]"
//     with the key "a:". The copy reads the key a, and the ":" as an
//     indicator, also below a comment line above the key or above its
//     tag or anchor, as in "[? # c\n a:]" and "[? # c\n &x a:]". After a
//     quoted key, an alias, or a comment after the key, as in
//     "[? a # c\n:]", go-yaml reads that ":" as a plain scalar, and the
//     copy reads it as an indicator.
//   - A tab after the text of a plain scalar on its line stays in the
//     scalar, as a space does and as YAML keeps it. Go-yaml drops such a
//     tab from the value and keeps it in the Origin alone. So it reads
//     "a: b\tc" as {a: "bc"} and "a: 1\t2" as {a: 12}, and the copy reads
//     them as {a: "b\tc"} and {a: "1\t2"}. A tab that opens a line stays
//     out of the scalar, as YAML drops it with the indent.
//
// # Sync
//
// The sync.sh script merges a new commit of go-yaml into the copy. It
// rewrites only the package clause of each file and the reference of
// Tokenize to the Scanner. Each patch to the copy is a commit of niceyaml
// that changes the files that sync.sh writes. So the patch series sits on
// top of the unchanged copy. The history of go-yaml holds the unchanged copy
// of each commit, and sync.sh reads it from there. To sync the copy:
//
//  1. Check out the new commit in a clone of go-yaml whose history also
//     holds the commit that UPSTREAM_COMMIT names.
//  2. Require the new commit of go-yaml in go.mod. The copy of go-yaml's
//     decoder in internal/yamldecode builds on the same module, so sync that
//     copy too.
//  3. Run sync.sh with the path of the clone. Its merge takes the unchanged
//     copy of the old commit as the base. One side is the copy in this
//     package, and the other side is the unchanged copy of the new commit.
//     Resolve each conflict that sync.sh reports.
//  4. Run the tests of this package. The corpus test reads each input under
//     testdata/corpus. It checks that the Origins join to the input and
//     that each position names the rune where the text of its token
//     starts. It also checks that the tokens have the types and values
//     that go-yaml's lexer gives them, except on the inputs where a patch
//     changes them. That check skips each input where either stream holds
//     an invalid token and each input where the lexer does not give any
//     token.
package yamlscan
