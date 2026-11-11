// Package inputhook holds what the input package hands to the manifest
// package without adding it to its API.
//
// The input package sets each variable when it loads, so a package that
// imports input finds every variable set. A parameter that names a type of
// the input package, such as an input.Input, has the type any here,
// because the input package imports this package.
package inputhook

import (
	"context"

	"go.jacobcolvin.com/niceyaml"
)

// Kind names the place that a file came from. The place decides whether
// the manifest package can rewrite the file.
type Kind int

// The places that a file comes from.
const (
	// Disk is a file of an input.Path input.
	Disk Kind = iota + 1
	// Stdin is the text of an input.Stdin input.
	Stdin
	// Reader is the text of an input.Reader input.
	Reader
	// FS is a file of an input.FS input.
	FS
	// Source is a Source of an input.Sources input.
	Source
)

// Target is where a file came from, and where a rewrite writes it.
type Target struct {
	// The directory input whose walk found the file, or "" for a file that
	// an input names directly.
	Root string
	// The slash path of the file below Root, or the path of a file that an
	// input names directly.
	Path string
	Kind Kind
	// Whether a walk reached the file through a link that leads outside
	// the input directory.
	Outside bool
}

// File is one file that a read found.
type File struct {
	// The problem of the file as a whole, bound to its name, or nil.
	Err error
	// The Source of the file, or nil when the read of the file failed.
	Source *niceyaml.Source
	// The name of the file, as messages show it.
	Name string
	// The name of the directory input whose walk found the file, or "".
	Dir string
	// Where the file came from.
	Target Target
}

// Result is what a read of an input found.
type Result struct {
	// The name that the heading of a report of the read starts with, or
	// "" when the read has none. It is the path of a lone input.Path or
	// input.FS input, the name of a lone input.Stdin or input.Reader input,
	// or the name of a lone Source.
	Heading string
	// The files of the read, in load order.
	Files []File
	// A sentence for each directory input that selected no file, which
	// names the directory and the patterns.
	Empty []string
}

// Read reads the files of in, an input.Input, in load order, as
// input.Input.Files does. Each Source that it builds takes the
// unmarshalers us ahead of the source options of its input.
var Read func(ctx context.Context, in any, us []niceyaml.Unmarshaler) (*Result, error)
