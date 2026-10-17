package edit

import (
	"errors"
	"fmt"
	"reflect"
	"slices"

	"github.com/goccy/go-yaml"
	"github.com/goccy/go-yaml/ast"
	"github.com/goccy/go-yaml/parser"

	"go.jacobcolvin.com/niceyaml/internal/astnode"
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

// marshaledYAML returns the problem of doc, the YAML that the marshaler of
// v writes, or nil when go-yaml reads one value from it as the text says.
// Err is the error of the marshaler, which returns no problem, because the
// encode then fails with that error.
//
// The check parses doc as go-yaml's encoder parses it, without its
// comments. Text of more than one document is the problem of the
// marshaler, because a value is one node, and the problem names it. Each
// other problem is go-yaml's, and it matches errEncoder. Go-yaml's encoder
// writes a directive alone for text with one before its value, as
// [directiveProblem] tells. Go-yaml's parser can refuse the text, with its
// comments or without them, or panic on it. The text can hold no value, as
// a comment alone does. Go-yaml also reads a byte order mark of the text as
// text, as [Doc.valueMisread] finds it. After those checks, marshaledYAML
// returns the instances that the finder finds in the text, as
// [Doc.valueInstances] finds them, such as an anchor or an alias.
func marshaledYAML(v reflect.Value, doc []byte, err error) ([]instance, error) {
	if err != nil {
		return nil, nil //nolint:nilerr // The encode fails with the error of the marshaler.
	}

	who := fmt.Sprintf("the MarshalYAML method of %T", v.Interface())

	file, err := encoderParse(doc)
	panicked, ok := errors.AsType[*parserPanicError](err)

	switch {
	case ok:
		return nil, goyamlProblem("go-yaml's parser panicked on the YAML that %s writes: %v", who, panicked.value)
	case err != nil:
		// YAML does not allow a mapping that repeats a key, so a refusal of
		// such a text is the problem of the marshaler.
		enc, derr := encodedDoc(string(doc))
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
		return nil, goyamlProblem("go-yaml's parser refuses the YAML that %s writes, with its comments: %s",
			who, refusal(err, "that YAML"))
	}

	if f, ok := enc.valueMisread(); ok {
		return nil, misreadProblem(f, "the YAML that "+who+" writes", "that YAML")
	}

	return enc.valueInstances(fmt.Sprintf("%T", v.Interface())), nil
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
