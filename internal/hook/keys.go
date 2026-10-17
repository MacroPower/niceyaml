package hook

import "go.jacobcolvin.com/niceyaml/internal/mapkey"

// Keys returns new [mapkey.Keys] for the document of n, a *niceyaml.Node.
// They name each key as niceyaml's decode of the document reads it. An
// alias in a key reads the anchor that the resolver of the document binds
// it to, or the anchor of a reference document of the Source.
var Keys func(n any) *mapkey.Keys
