// Package yamlfield holds the rules go-yaml follows when it decodes a
// mapping into a struct: the key each field reads, the fields it skips,
// and the fields it decodes inline.
//
// Code that pairs the fields of a decoded struct with the entries of the
// mapping they came from calls [Name] for each field, so it pairs them as
// the decoder does. The fields of an inline struct sit beside the fields
// of its parent, and go-yaml zeroes each one whose name a field of the
// parent that is not inline also uses. [OwnNames] returns the names of
// the parent that zero such a field.
//
// One kind of field reads no entry of the mapping. An inline field that
// carries an alias option gets the value of the anchor a `<<` key names.
// [ReadsAnchor] reports such a field, so the pairing code can leave its
// struct alone.
package yamlfield

import (
	"reflect"
	"slices"
	"strings"
)

// Name returns the name go-yaml decodes field under, whether the field
// is inline so its own fields sit beside its siblings, and whether
// go-yaml skips the field. It skips an unexported field that is not
// embedded, and one whose tag is "-". The name comes from the yaml tag,
// or the json tag when the field has no yaml tag. It is the lowercased
// field name when neither tag names it, as go-yaml spells it.
func Name(field reflect.StructField) (string, bool, bool) {
	tag, skip := fieldTag(field)
	if skip {
		return "", false, true
	}

	name := strings.ToLower(field.Name)

	options := strings.Split(tag, ",")
	if options[0] != "" {
		name = options[0]
	}

	return name, slices.Contains(options[1:], "inline"), false
}

// ReadsAnchor reports whether go-yaml fills field from an anchor in
// place of the entries of the mapping it decodes. It does so for an
// inline field whose tag carries an alias option that names no anchor,
// such as `yaml:",inline,alias"`. The field gets the value of the anchor
// that a `<<` key of the mapping names with an alias, and stays zero
// when the mapping has no such key. Any option that starts with alias
// and holds no = counts as that option, as it does in go-yaml.
func ReadsAnchor(field reflect.StructField) bool {
	// Few tags hold the word, so most fields return before the split.
	tag, skip := fieldTag(field)
	if skip || !strings.Contains(tag, "alias") {
		return false
	}

	options := strings.Split(tag, ",")[1:]

	return slices.Contains(options, "inline") && slices.ContainsFunc(options, func(option string) bool {
		return strings.HasPrefix(option, "alias") && !strings.Contains(option, "=")
	})
}

// fieldTag returns the tag go-yaml reads for field, which is the yaml
// tag, or the json tag when the field has no yaml tag. The second result
// reports whether go-yaml skips the field.
func fieldTag(field reflect.StructField) (string, bool) {
	if field.PkgPath != "" && !field.Anonymous {
		return "", true
	}

	tag := field.Tag.Get("yaml")
	if tag == "" {
		tag = field.Tag.Get("json")
	}

	return tag, tag == "-"
}

// OwnNames returns the names go-yaml decodes the fields of t, a struct
// type, under, leaving out the fields it skips and those that are
// inline.
func OwnNames(t reflect.Type) map[string]bool {
	names := map[string]bool{}

	for field := range t.Fields() {
		if name, inline, skip := Name(field); !skip && !inline {
			names[name] = true
		}
	}

	return names
}
