// Package genfield holds the rules that find where the schema generator
// of go.jacobcolvin.com/x/jsonschema and the go-yaml decoder disagree on
// the fields of a struct. Messages call the generator gen, after the
// name of its go:generate tool.
//
// Gen follows encoding/json. It names the property of a field by the
// json tag, or by the Go name of the field when the tag does not name
// one. It leaves out a field whose json tag is "-". It reads inline each
// embedded struct without a JSON name, and each struct field with the
// embed option of encoding/json/v2. It merges the entries of a map field
// with the embed option into the parent. The decoder follows
// [yamlfield.Name]. It reads the yaml tag, or the json tag when the field
// does not have a yaml tag. It lowercases the Go name when the tag it
// reads does not name one. It reads a struct inline only for a field
// with an inline option.
//
// Where the two disagree, a schema that gen writes for a type rejects
// documents that the decoder reads, or passes documents whose values the
// decoder leaves zero. [Inline] reports the fields where they disagree on
// whether a field is inline. [Check] reports those and the fields where
// they disagree on the name.
package genfield

import (
	"fmt"
	"reflect"
	"slices"
	"strings"

	"go.jacobcolvin.com/x/jsonschema"

	"go.jacobcolvin.com/niceyaml/internal/unmarshaler"
	"go.jacobcolvin.com/niceyaml/internal/yamlfield"
)

// Inline returns a message for each field where gen and the decoder
// disagree on whether the field is inline. It reads the fields of each
// struct that a decode into t reads field by field. Each message names
// the struct and the field, as in "Spec.Common: ...", and says how gen
// and the decoder read the field.
func Inline(t reflect.Type) []string {
	return check(t, false)
}

// Check is [Inline] with the rules for names too. It also returns a
// message for each field where the property name that gen gives differs
// from the key that the decoder reads. Each field that only one of the
// two reads gets a message too. It skips each type that supplies its own
// schema through a JSONSchema method, because gen does not read the
// fields of such a type.
func Check(t reflect.Type) []string {
	return check(t, true)
}

// check returns the messages of [Check] when names is true, and those of
// [Inline] otherwise.
func check(t reflect.Type, names bool) []string {
	var out []string

	yamlfield.Reaches(t, func(t reflect.Type) (bool, bool) {
		if unmarshaler.DecodesItself(t) ||
			names && unmarshaler.Declares(t, reflect.TypeFor[jsonschema.JSONSchemaProvider]()) {
			return false, true
		}

		if t.Kind() != reflect.Struct {
			return false, false
		}

		for f := range t.Fields() {
			if problem := fieldProblem(f, names); problem != "" {
				out = append(out, typeName(t)+"."+f.Name+": "+problem)
			}
		}

		return false, false
	})

	return out
}

// fieldProblem returns how gen and the decoder disagree on f, or ""
// when they agree. It checks the names only when names is true.
func fieldProblem(f reflect.StructField, names bool) string {
	name, inline, skip := yamlfield.Name(f)

	switch {
	case skip && names:
		return skipProblem(f)
	case skip:
		return ""
	}

	if problem := inlineProblem(f, inline); problem != "" {
		return problem
	}

	if !names || inline {
		return ""
	}

	return nameProblem(f, name)
}

// inlineProblem returns how gen and the decoder disagree on whether f is
// inline, or "" when they agree. The decoder reads f inline when inline
// is true.
func inlineProblem(f reflect.StructField, inline bool) string {
	_, genSkips := jsonName(f)

	switch {
	case promotes(f) && inline:
		return ""

	case promotes(f) && f.Anonymous:
		return `embedded struct needs yaml:",inline", because gen reads it inline and the decode nests it`

	case promotes(f):
		return `field with the embed option needs yaml:",inline", because gen reads it inline and the decode nests it`

	case inline && genSkips:
		return `field has json:"-" and an inline option, which the decode reads inline and gen leaves out`

	case inline && !f.Anonymous:
		return "named field has an inline option, which the decode reads inline and gen nests"

	case inline:
		return "embedded field has a JSON name and an inline option, which the decode reads inline and gen nests"
	}

	return ""
}

// nameProblem returns how gen and the decoder disagree on the name of f,
// or "" when they agree. Neither of them reads f inline. The decoder
// reads f under name.
func nameProblem(f reflect.StructField, name string) string {
	genName, skip := jsonName(f)

	switch {
	case name == "-":
		return `field needs a yaml name other than "-", because the decode reads "-" ` +
			`and gen cannot name a property "-"`

	case embeds(f):
		return fmt.Sprintf("field with the embed option needs json:%[1]q, because gen merges its entries "+
			"into the parent and the decode reads %[1]q", name)

	case skip:
		return fmt.Sprintf("field needs %s, because gen leaves it out and the decode reads %q",
			nameFix(f, name), name)

	case genName != name:
		return fmt.Sprintf("field needs %s, because gen names it %q and the decode reads %q",
			nameFix(f, name), genName, name)
	}

	return ""
}

// skipProblem returns how gen and the decoder disagree on f, a field
// that the decoder skips, or "" when gen leaves it out too.
func skipProblem(f reflect.StructField) string {
	genName, skip := jsonName(f)

	switch {
	case skip:
		return ""

	case promotes(f) && f.Anonymous:
		return `embedded struct needs json:"-", because the decode skips it and gen reads it inline`

	case promotes(f):
		return `field with the embed option needs json:"-", because the decode skips it and gen reads it inline`

	case embeds(f):
		return `field with the embed option needs json:"-", because the decode skips it ` +
			`and gen merges its entries into the parent`
	}

	return fmt.Sprintf(`field needs json:"-", because the decode skips it and gen names it %q`, genName)
}

// promotes reports whether gen reads the fields of f beside the fields
// of the struct that holds f. Gen does so for an embedded field, or one
// with the embed option, whose json tag is not "-" and does not name a
// property. It does so only when the type of f is a struct or a pointer
// to one.
func promotes(f reflect.StructField) bool {
	tag := f.Tag.Get("json")
	if tag == "-" {
		return false
	}

	name, _, _ := strings.Cut(tag, ",")

	return (f.Anonymous || embeds(f)) && name == "" && isStruct(f.Type)
}

// embeds reports whether the json tag of f carries the embed option of
// encoding/json/v2.
func embeds(f reflect.StructField) bool {
	_, options, _ := strings.Cut(f.Tag.Get("json"), ",")

	return slices.Contains(strings.Split(options, ","), "embed")
}

// jsonName returns the name gen gives the property of f, a field it does
// not read inline, and whether gen leaves f out. The name comes from the
// json tag, or is the Go name of f when the tag does not name one. Gen
// leaves out a field whose json tag is "-", and an unexported field that
// is not embedded.
func jsonName(f reflect.StructField) (string, bool) {
	tag := f.Tag.Get("json")
	if tag == "-" || !f.IsExported() && !f.Anonymous {
		return "", true
	}

	if name, _, _ := strings.Cut(tag, ","); name != "" {
		return name, false
	}

	return f.Name, false
}

// nameFix returns the tag that makes gen and the decoder read f under
// the same name, where the decoder reads it under name. A yaml tag that
// holds only options hides a json tag that names f, so the fix moves that
// name into the yaml tag. Every other fix is a json tag that names f as
// the decoder does. It keeps the options of the json tag of f.
func nameFix(f reflect.StructField, name string) string {
	jsonTagName, jsonOptions, _ := strings.Cut(f.Tag.Get("json"), ",")

	yamlTag := f.Tag.Get("yaml")
	if strings.HasPrefix(yamlTag, ",") && jsonTagName != "" && jsonTagName != "-" {
		return fmt.Sprintf("yaml:%q", jsonTagName+yamlTag)
	}

	if jsonOptions != "" {
		return fmt.Sprintf("json:%q", name+","+jsonOptions)
	}

	return fmt.Sprintf("json:%q", name)
}

// isStruct reports whether t is a struct or a pointer to one, the types
// whose fields encoding/json promotes from an embedded field.
func isStruct(t reflect.Type) bool {
	if t.Kind() == reflect.Pointer {
		t = t.Elem()
	}

	return t.Kind() == reflect.Struct
}

// typeName returns the name of t as a message shows it.
func typeName(t reflect.Type) string {
	if t.Name() != "" {
		return t.Name()
	}

	return t.String()
}
