package edit

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"reflect"
	"slices"
	"strconv"
	"strings"

	"github.com/goccy/go-yaml"
	"github.com/goccy/go-yaml/ast"
	"github.com/goccy/go-yaml/parser"
	"github.com/goccy/go-yaml/token"

	"go.jacobcolvin.com/niceyaml/internal/astnode"
	"go.jacobcolvin.com/niceyaml/internal/docparse"
	"go.jacobcolvin.com/niceyaml/tokens"
)

// goyamlError is a problem of go-yaml with the value of a Set, such as a
// refusal of its parser of the YAML that a marshaler writes. It matches
// errEncoder, so the Set fails with [ErrEngine].
type goyamlError struct {
	err error
}

// goyamlProblem returns the problem of go-yaml that format describes, as a
// [*goyamlError].
func goyamlProblem(format string, args ...any) error {
	return &goyamlError{err: fmt.Errorf(format, args...)}
}

// Error returns the message of e.
func (e *goyamlError) Error() string {
	return e.err.Error()
}

// Unwrap returns the error behind e.
func (e *goyamlError) Unwrap() error {
	return e.err
}

// Is reports whether target is errEncoder.
func (e *goyamlError) Is(target error) bool {
	return target == errEncoder
}

// misreadProblem returns the problem of f, a misread of go-yaml in a text.
// The message names the text as what and then as that, as in "the YAML
// that the MarshalYAML method of T writes" and "that YAML". The problem is
// a [*goyamlError] of a [*misreadError], so its detail says what to write.
func misreadProblem(f misreadFound, what, that string) error {
	return &goyamlError{err: &misreadError{
		msg: fmt.Sprintf("go-yaml misreads %s: in %s, %s", what, that, f.msg),
		fix: f.fix,
	}}
}

// refusal returns the words of err, a refusal of go-yaml's parser of a
// text, with the place where the parser refuses it. The words name the
// text as text, as in "found an invalid key for this map at 2:1 of that
// YAML" for the text "that YAML".
func refusal(err error, text string) string {
	syntax, ok := errors.AsType[*yaml.SyntaxError](err)
	if !ok || syntax.Token == nil || syntax.Token.Position == nil {
		return err.Error()
	}

	pos := syntax.Token.Position

	return fmt.Sprintf("%s at %d:%d of %s", syntax.Message, pos.Line, pos.Column, text)
}

// marshaledYAML returns the problem of doc, the YAML that the method of v
// named method writes, or nil when go-yaml reads one value from it as the text says.
// Err is the error of the marshaler, which returns no problem, because the
// encode then fails with that error.
//
// The check parses doc as go-yaml's encoder parses it, without its
// comments. Text of more than one document is the problem of the
// marshaler, because a value is one node, and the problem names it. So are
// a mapping that repeats a key, a "-" right after an anchor or a tag on
// its line, and a "-" that starts an entry inside a flow collection. So
// are a plain scalar there that starts with a "-" right before a flow
// indicator and one that holds a flow indicator, and a "-" at a line of a
// flow collection that does not pass the indent of the block node that
// holds the collection. So is the name of an anchor or an alias that holds
// a flow indicator, because YAML does not allow any of them. The finder can
// also refuse text that go-yaml's parser took. Such a refusal
// is the problem of the marshaler when it binds at a key on a line that
// continues a plain scalar in a flow collection, as [refusesCutKey] finds
// it. So is one of a key that go-yaml's parser reads into a single pair of
// a flow sequence, as [refusesFlowPair] finds it. Each other problem is
// go-yaml's, and it matches errEncoder. One of them is a "-" that
// continues a plain scalar in a flow collection, which the parser
// refuses. Go-yaml's encoder writes a directive alone for text with one
// before its value, as [directiveProblem] tells. Go-yaml's parser can
// refuse the text without its comments, refuse a text with comments that
// it took without them, or panic on it. It can also refuse the text as the
// finder reads it after it took the text. The text can hold no value, as a
// comment alone does. Go-yaml also reads a byte order mark of the text as
// text, as [Doc.valueMisread] finds it. After those checks, marshaledYAML
// returns the instances that the finder finds in the text, as
// [Doc.valueInstances] finds them, such as an anchor or an alias.
func marshaledYAML(v reflect.Value, method string, doc []byte, err error) ([]instance, error) {
	if err != nil {
		return nil, nil //nolint:nilerr // The encode fails with the error of the marshaler.
	}

	who := fmt.Sprintf("the %s method of %T", method, v.Interface())

	file, err := encoderParse(doc)
	panicked, ok := errors.AsType[*parserPanicError](err)

	switch {
	case ok:
		return nil, goyamlProblem("go-yaml's parser panicked on the YAML that %s writes: %v", who, panicked.value)
	case err != nil:
		// YAML does not allow a mapping that repeats a key, so a refusal of
		// such a text is the problem of the marshaler. The finder takes a
		// repeated key but refuses a "-" right after an anchor or a tag on
		// its line and a "-" or a plain scalar inside a flow collection that
		// YAML refuses. It also refuses the name of an anchor or an alias
		// that holds a flow indicator. Those refusals are also the problem
		// of the marshaler.
		enc, derr := encodedDoc(string(doc))
		problem := refusedProblem(derr, who)
		if problem != nil {
			return nil, problem
		}

		if derr == nil {
			if key, repeats := enc.repeatedKey(); repeats {
				return nil, fmt.Errorf("%s writes a mapping that repeats the key %s, which YAML does not allow",
					who, cut(key))
			}
		}

		return nil, goyamlProblem("go-yaml's parser refuses the YAML that %s writes: %s", who,
			refusal(err, "that YAML"))
	}

	// Go-yaml's parse reads a directive as a document of its own, though it
	// belongs to the document after it. Go-yaml's encoder writes the first
	// document of the parse, so for YAML with a directive before its value
	// it writes the directive alone, though go-yaml reads that YAML as the
	// value after it. The engine does not drop or rewrite the directive.
	var bodies []ast.Node

	for _, d := range file.Docs {
		dir, directive := d.Body.(*ast.DirectiveNode)
		if directive && len(bodies) == 0 {
			return nil, directiveProblem(dir, who, fmt.Sprintf("%T", v.Interface()))
		}

		if d.Body != nil && !directive {
			bodies = append(bodies, d.Body)
		}
	}

	switch {
	case len(bodies) > 1:
		return nil, fmt.Errorf("%s writes %d documents, but a value is one node of YAML", who, len(bodies))
	case len(bodies) == 0:
		return nil, goyamlProblem("go-yaml's encoder reads no value from the YAML that %s writes: %q", who,
			cut(string(doc)))
	}

	// The finder reads the text as it reads the user's text, so its words
	// name the places of the text of the marshaler.
	enc, err := encodedDoc(string(doc))
	if err != nil {
		problem := refusedProblem(err, who)
		if problem != nil {
			return nil, problem
		}

		// The tokenizer cuts a plain scalar of a flow collection at a line
		// that holds a key where YAML refuses the scalar as that key. The
		// finder also refuses a key that go-yaml's parser reads into a
		// single pair of a flow sequence. A refusal of either is the
		// problem of the marshaler. Go-yaml's parser took the text without
		// its comments, so another refusal of a text that holds a comment
		// is go-yaml's, and so is each other refusal.
		switch {
		case refusesCutKey(doc, err) || refusesFlowPair(file, err):
			return nil, fmt.Errorf("%s writes YAML that YAML does not allow: %s", who, refusal(err, "that YAML"))
		case holdsComment(doc):
			return nil, goyamlProblem("go-yaml's parser refuses the YAML that %s writes, with its comments: %s",
				who, refusal(err, "that YAML"))
		}

		return nil, goyamlProblem("go-yaml's parser refuses the YAML that %s writes: %s",
			who, refusal(err, "that YAML"))
	}

	if f, ok := enc.valueMisread(); ok {
		return nil, misreadProblem(f, "the YAML that "+who+" writes", "that YAML")
	}

	return enc.valueInstances(fmt.Sprintf("%T", v.Interface())), nil
}

// refusedProblem returns the problem of the marshaler that who names, or
// nil. It returns the problem when err is the refusal of the finder of a
// token in the YAML of the marshaler that YAML does not allow. The finder
// refuses a "-" right after an anchor or a tag on its line and a "-" that
// starts an entry inside a flow collection. It also refuses a plain scalar
// there that starts with a "-" right before a flow indicator or that holds
// a flow indicator, and a "-" at a line of a flow collection that does not
// pass the indent of the block node that holds the collection, and the
// name of an anchor or an alias that holds a flow indicator. YAML does
// not allow any of them, so the refusal is the problem of the marshaler,
// also where go-yaml's parser refuses the token.
func refusedProblem(err error, who string) error {
	syntax, ok := errors.AsType[*yaml.SyntaxError](err)
	if !ok || syntax.Token == nil || syntax.Token.Position == nil {
		return nil
	}

	var what string

	switch syntax.Message {
	case docparse.AnchorSequenceMessage:
		what = `a "-" on the line of an anchor`
	case docparse.TagSequenceMessage:
		what = `a "-" on the line of a tag`
	case docparse.FlowSequenceMessage:
		what = `a "-" in a flow collection`
	case docparse.FlowIndicatorMessage:
		what = `a "-" before a flow indicator`
	case docparse.FlowScalarMessage:
		what = "a plain scalar that holds a flow indicator in a flow collection"
	case docparse.NameIndicatorMessage:
		what = "the name of an anchor or an alias that holds a flow indicator"
	case docparse.FlowIndentMessage:
		what = `a "-" at a line of a flow collection that does not pass the indent of the block node of the collection`
	default:
		return nil
	}

	pos := syntax.Token.Position

	return fmt.Errorf("%s writes %s at %d:%d of that YAML, which YAML does not allow",
		who, what, pos.Line, pos.Column)
}

// refusesCutKey reports whether err, the refusal of the finder of doc,
// YAML text, binds at a key that the tokenizer cut from a plain scalar of
// a flow collection. The tokenizer cuts the scalar where YAML refuses it
// as the key of a ":", as in "{x: y\n    z: w}". The tokens of the cut
// lines start each line right below a plain scalar, and the last of them
// is the key, which a "-" or "?" can open. The refusal binds at the key
// when it falls at a token from the first cut line up to the ":".
func refusesCutKey(doc []byte, err error) bool {
	syntax, ok := errors.AsType[*yaml.SyntaxError](err)
	if !ok || syntax.Token == nil || syntax.Token.Position == nil {
		return false
	}

	at := syntax.Token.Position.Offset
	tks := tokens.Tokenize(string(doc))
	depth := 0

	for i, tk := range tks {
		switch tk.Type {
		case token.SequenceStartType, token.MappingStartType:
			depth++
		case token.SequenceEndType, token.MappingEndType:
			depth--
		default:
		}

		if depth <= 0 || !plainToken(tk) {
			continue
		}

		// Walk the cut lines below the scalar to the ":" of the key.
		j := i
		for j+1 < len(tks) && tks[j+1].Position.Line > tks[j].Position.Line {
			k := j + 1
			if t := tks[k].Type; (t == token.SequenceEntryType || t == token.MappingKeyType) && k+1 < len(tks) &&
				tks[k+1].Position.Line == tks[k].Position.Line {
				k++
			}

			if !plainToken(tks[k]) {
				break
			}

			j = k
		}

		if j > i && j+1 < len(tks) && tks[j+1].Type == token.MappingValueType &&
			at >= tks[i+1].Position.Offset && at <= tks[j+1].Position.Offset {
			return true
		}
	}

	return false
}

// refusesFlowPair reports whether err, the refusal of the finder of a
// text, is the refusal of a key that go-yaml's parser reads into a single
// pair of a flow sequence. File is go-yaml's parse of the text. YAML holds
// one key in such a pair, as in "[a: b\n c: d]", but go-yaml's parser
// takes the text.
func refusesFlowPair(file *ast.File, err error) bool {
	syntax, ok := errors.AsType[*yaml.SyntaxError](err)

	return ok && syntax.Message == docparse.FlowPairMessage && len(docparse.FlowPairKeys(file)) > 0
}

// plainToken reports whether tk is the token of a plain scalar.
func plainToken(tk *token.Token) bool {
	return tk.Position != nil && (tk.Type == token.StringType || typedScalar(tk.Type))
}

// holdsComment reports whether doc, YAML text, holds a comment.
func holdsComment(doc []byte) bool {
	return slices.ContainsFunc(tokens.Tokenize(string(doc)), func(tk *token.Token) bool {
		return tk.Type == token.CommentType
	})
}

// convertedJSON returns the problem of doc, the YAML that go-yaml converts
// js to, when go-yaml reads another value from doc than encoding/json reads
// from js. Js is the JSON of the MarshalJSON method of v. Go-yaml's
// conversion writes some values as YAML that reads as another value. For
// example, it writes the JSON string "\n" as a literal block that reads as
// "", and the JSON number 1e3 as a plain scalar that reads as a string. The
// problem matches errEncoder. A doc that go-yaml's decoder refuses returns
// no problem, because the check of the encoded text names that refusal.
func convertedJSON(v reflect.Value, js, doc []byte) error {
	dec := json.NewDecoder(bytes.NewReader(js))
	dec.UseNumber()

	var want, got any

	if dec.Decode(&want) != nil || yaml.Unmarshal(doc, &got) != nil {
		return nil //nolint:nilerr // The encode or the check of its text names the refusal.
	}

	if sameJSON(want, got) {
		return nil
	}

	return goyamlProblem("go-yaml converts the JSON %s that the MarshalJSON method of %T writes to the YAML %s, "+
		"which go-yaml reads as %s", cut(strings.TrimSpace(string(js))), v.Interface(),
		cut(strconv.Quote(string(doc))), shownCut(got))
}

// sameJSON reports whether y, a value that go-yaml's decoder reads, holds
// the value j that encoding/json reads with numbers as [json.Number]. Each
// number of y matches the number of j that it parses to.
func sameJSON(j, y any) bool {
	switch jv := j.(type) {
	case nil:
		return y == nil
	case bool, string:
		return j == y
	case json.Number:
		return sameNumber(jv, y)
	case []any:
		ys, ok := y.([]any)
		if !ok || len(ys) != len(jv) {
			return false
		}

		for i, e := range jv {
			if !sameJSON(e, ys[i]) {
				return false
			}
		}

		return true

	case map[string]any:
		ym, ok := y.(map[string]any)
		if !ok || len(ym) != len(jv) {
			return false
		}

		for k, e := range jv {
			ye, found := ym[k]
			if !found || !sameJSON(e, ye) {
				return false
			}
		}

		return true
	}

	return false
}

// sameNumber reports whether y, a number that go-yaml's decoder reads,
// is the number n. An integer y matches the integer n of the same value,
// so the JSON number -0 matches the 0 that go-yaml converts it to.
func sameNumber(n json.Number, y any) bool {
	i, isInt := new(big.Int).SetString(n.String(), 10)

	switch yv := y.(type) {
	case int64:
		return isInt && i.Cmp(big.NewInt(yv)) == 0

	case uint64:
		return isInt && i.Cmp(new(big.Int).SetUint64(yv)) == 0

	case float64:
		f, err := n.Float64()

		return err == nil && f == yv
	}

	return false
}

// directiveProblem returns the problem of dir, a directive before the value
// of the YAML that who, the marshaler of a value of the type typ, writes.
// Go-yaml's encoder writes the directive alone for such YAML. The problem
// is a [*goyamlError] of a [*misreadError], so its detail tells the author
// of the program to return the YAML without directives.
func directiveProblem(dir *ast.DirectiveNode, who, typ string) error {
	name, _ := astnode.Name(dir.Name)
	at := ""

	if tk := dir.Start; tk != nil && tk.Position != nil {
		at = fmt.Sprintf(" at %d:%d of that YAML", tk.Position.Line, tk.Position.Column)
	}

	return &goyamlError{err: &misreadError{
		msg: fmt.Sprintf("the YAML that %s writes holds the directive %%%s%s before its value, and go-yaml's "+
			"encoder writes only the directive for that YAML", who, name, at),
		fix: fmt.Sprintf("return the YAML of %s without directives", typ),
	}}
}

// repeatedKey returns the text of the first key of the current text that
// repeats a key before it in its mapping, as go-yaml's decoder reads each
// key alone into any. It reports false when no mapping repeats a key, as
// for the keys 1.0 and "1.0", which the decoder reads as a float and a
// string.
func (d *Doc) repeatedKey() (string, bool) {
	var key string

	plain := d.keyReader().Plain()

	d.inspect(func(n ast.Node) bool {
		m, ok := n.(*ast.MappingNode)
		if !ok || key != "" {
			return key == ""
		}

		var seen []any

		for _, e := range m.Values {
			node := ast.Node(e.Key)
			if explicit, ok := node.(*ast.MappingKeyNode); ok {
				node = explicit.Value
			}

			if astnode.IsNil(node) {
				continue
			}

			k, err := plain.Value(node)
			if err != nil {
				continue
			}

			if slices.ContainsFunc(seen, func(s any) bool { return reflect.DeepEqual(s, k) }) {
				key, _ = astnode.ScalarText(e.Key)

				return false
			}

			seen = append(seen, k)
		}

		return true
	})

	return key, key != ""
}

// encoderParse returns go-yaml's parse of doc, the YAML of a marshaler,
// as go-yaml's encoder parses it. A panic of the parser comes back as a
// [*parserPanicError].
func encoderParse(doc []byte) (*ast.File, error) {
	var (
		file *ast.File
		err  error
	)

	func() {
		defer func() {
			if p := recover(); p != nil {
				file, err = nil, &parserPanicError{value: p}
			}
		}()

		file, err = parser.ParseBytes(doc, 0)
	}()

	return file, err //nolint:wrapcheck // The caller names the parser.
}
