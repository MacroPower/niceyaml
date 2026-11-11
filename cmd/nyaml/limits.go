package main

import (
	"errors"
	"fmt"

	"github.com/spf13/cobra"

	"go.jacobcolvin.com/niceyaml"
)

// errNegativeLimit is the error of a --byte-limit, --document-byte-limit,
// or --depth-limit flag set to a negative number.
var errNegativeLimit = errors.New("limit is negative")

// limitFlags holds the --byte-limit, --document-byte-limit, and
// --depth-limit flags of a command, which hand their values to
// [niceyaml.WithByteLimit], [niceyaml.WithDocumentByteLimit], and
// [niceyaml.WithDepthLimit].
type limitFlags struct {
	cmd      *cobra.Command
	bytes    int
	docBytes int
	depth    int
}

// addLimitFlags adds the --byte-limit, --document-byte-limit, and
// --depth-limit flags to cmd. The limits bound the cost of the go-yaml
// parser, so only validate, the command that parses, takes the flags.
// Each flag defaults to the limit that a [niceyaml.Source] applies without
// the option. A value of 0 disables the check of that flag. A negative
// value fails the command.
//
// The validate command reads a stream of many small documents, such as
// the output of a Helm render, with a raised --byte-limit alone. A large
// dump of "kubectl get -o yaml" is one document, so validate reads it with
// --byte-limit 0 and --document-byte-limit 0.
func addLimitFlags(cmd *cobra.Command) *limitFlags {
	l := &limitFlags{cmd: cmd}

	cmd.Flags().IntVar(&l.bytes, "byte-limit", niceyaml.DefaultByteLimit,
		"most bytes a file may hold, or 0 for no limit")
	cmd.Flags().IntVar(&l.docBytes, "document-byte-limit", niceyaml.DefaultDocumentByteLimit,
		"most bytes a document may hold, or 0 for no limit")
	cmd.Flags().IntVar(&l.depth, "depth-limit", niceyaml.DefaultDepthLimit,
		"most levels a document may nest, or 0 for no limit")

	return l
}

// options returns the source options of the flags that the command line
// set. A flag left at its default leaves the default of the library in
// place. A flag set to 0 gives [niceyaml.NoLimit], and a flag set to a
// negative number returns an error that wraps [errNegativeLimit].
func (l *limitFlags) options() ([]niceyaml.SourceOption, error) {
	var opts []niceyaml.SourceOption

	flags := []struct {
		option func(int) niceyaml.SourceOption
		name   string
		value  int
	}{
		{name: "byte-limit", value: l.bytes, option: niceyaml.WithByteLimit},
		{name: "document-byte-limit", value: l.docBytes, option: niceyaml.WithDocumentByteLimit},
		{name: "depth-limit", value: l.depth, option: niceyaml.WithDepthLimit},
	}

	for _, f := range flags {
		if !l.cmd.Flags().Changed(f.name) {
			continue
		}

		n, err := flagLimit(f.name, f.value)
		if err != nil {
			return nil, err
		}

		opts = append(opts, f.option(n))
	}

	return opts, nil
}

// flagLimit returns the limit of the flag name set to n. A flag of 0
// turns the check off, so it returns [niceyaml.NoLimit] for 0.
func flagLimit(name string, n int) (int, error) {
	switch {
	case n < 0:
		return 0, fmt.Errorf("--%s %d: %w", name, n, errNegativeLimit)
	case n == 0:
		return niceyaml.NoLimit, nil
	default:
		return n, nil
	}
}
