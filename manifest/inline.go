package manifest

import "go.jacobcolvin.com/niceyaml/internal/genfield"

// checkInline reports each field where the decode of the kind of entry
// and the schema generator of go.jacobcolvin.com/x/jsonschema disagree on
// whether the field is inline, as [genfield.Inline] finds them.
func (b *registryBuilder) checkInline(entry *kindEntry) {
	for _, problem := range genfield.Inline(entry.kind.typ) {
		b.failf("kind %s: %s", entry.kind.name, problem)
	}
}
