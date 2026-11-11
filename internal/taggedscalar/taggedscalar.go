// Package taggedscalar holds the values that niceyaml.Node.MarshalYAML
// writes for the scalars of a document that carry a !!binary or a
// !!timestamp tag. A decode into any reads such a scalar as a []byte or a
// [time.Time], and go-yaml's encoder writes neither one back under its
// tag. Each type here writes its scalar under the tag.
//
// A [Binary] is the one byte slice that the encoder writes as the base64
// text of its bytes. The schema validator tells it apart from other byte
// slices for that reason.
package taggedscalar

import (
	"encoding/base64"
	"time"
)

// Binary is the value of a !!binary scalar.
type Binary []byte

// MarshalYAML returns the bytes as a !!binary scalar, in base64 and in
// double quotes, which also hold the empty text of no bytes.
func (b Binary) MarshalYAML() ([]byte, error) {
	return []byte(`!!binary "` + base64.StdEncoding.EncodeToString(b) + `"`), nil
}

// Timestamp is the value of a !!timestamp scalar.
type Timestamp time.Time

// MarshalYAML returns the time as a !!timestamp scalar, in the layout of
// [time.RFC3339Nano] and in double quotes. Inside a flow mapping,
// go-yaml's parser does not read a plain scalar that follows a tag and
// holds a colon as one scalar. It reads the scalar in double quotes as the
// time.
func (t Timestamp) MarshalYAML() ([]byte, error) {
	return []byte(`!!timestamp "` + time.Time(t).Format(time.RFC3339Nano) + `"`), nil
}
