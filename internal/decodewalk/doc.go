// Package decodewalk walks a Go type beside the YAML node a go-yaml
// decode of that type reads, in the order the decoder reads them.
//
// A caller that wants to know what the decoder reads for each value of a
// type walks the type with [Walk], such as to find the keys that a decode
// reads. The walk pairs each field with the entry the decoder reads for
// it, each element with its node, and each map entry with its key and
// value. It skips each value that the decoder does not read anything for,
// such as a null. It reads below a mapping only when the decoder decodes a
// field from it.
//
// [Walk] goes below a node once for each type it reads the node as, so it
// reads a node that several aliases reach once.
//
// The visit function of a walk decides, at each [Value], whether the
// walk goes below it.
package decodewalk
