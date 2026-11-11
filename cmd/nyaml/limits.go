package main

import (
	"errors"
	"fmt"

	"github.com/spf13/cobra"

	"go.jacobcolvin.com/niceyaml"
)

// errNegativeLimit is the error of a --byte-limit or --depth-limit flag
// set to a negative number.
var errNegativeLimit = errors.New("limit is negative")

// limitFlags holds the --byte-limit and --depth-limit flags of a command,
// which hand their values to [niceyaml.WithByteLimit] and
// [niceyaml.WithDepthLimit].
type limitFlags struct {
	cmd   *cobra.Command
	bytes int
	depth int
}

// addLimitFlags adds the --byte-limit and --depth-limit flags to cmd. Each
// flag defaults to the limit that a [niceyaml.Source] applies without the
// option. A value of 0 disables the check of that flag, so a large dump of
// "kubectl get -o yaml" reads with --byte-limit 0. A negative value fails
// the command.
func addLimitFlags(cmd *cobra.Command) *limitFlags {
	l := &limitFlags{cmd: cmd}

	cmd.Flags().IntVar(&l.bytes, "byte-limit", niceyaml.DefaultByteLimit,
		"most bytes a file may hold, or 0 for no limit")
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

	if l.cmd.Flags().Changed("byte-limit") {
		n, err := flagLimit("byte-limit", l.bytes)
		if err != nil {
			return nil, err
		}

		opts = append(opts, niceyaml.WithByteLimit(n))
	}

	if l.cmd.Flags().Changed("depth-limit") {
		n, err := flagLimit("depth-limit", l.depth)
		if err != nil {
			return nil, err
		}

		opts = append(opts, niceyaml.WithDepthLimit(n))
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
