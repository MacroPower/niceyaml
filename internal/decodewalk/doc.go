// Package decodewalk walks a Go type beside the YAML node a go-yaml
// decode of that type reads, in the order the decoder reads them.
//
// The decoder reports one problem of a decode, and an error of a value
// that decodes itself carries no token of the source. A caller that
// wants every problem, or the value an error came from, walks the type
// with [Walk] and decodes the values it reaches one at a time. The walk
// pairs each field with the entry the decoder reads for it, each element
// with its node, and each map entry with its key and value. It reaches
// no value the decoder reads nothing for, such as a null, and it reads
// nothing below a mapping the decoder decodes no field from.
//
// The visit function of a walk decides, at each [Value], whether the
// walk goes below it. [UnknownFields] walks a type that way to find the
// keys a decoder that rejects unknown fields rejects, which the decoder
// reports one at a time.
//
// The decoder starts each value it creates from the zero value of its
// type. [Fill] reads the type, the node, and the value the decoder gave
// by the same rules, and builds each of those values again from a start
// of its own.
package decodewalk
