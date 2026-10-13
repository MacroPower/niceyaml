package main

import (
	"github.com/spf13/cobra"

	"go.jacobcolvin.com/niceyaml"
)

// limitFlags holds the --byte-limit and --depth-limit flags of a command,
// which hand their values to [niceyaml.WithByteLimit] and
// [niceyaml.WithDepthLimit].
type limitFlags struct {
	cmd   *cobra.Command
	bytes int
	depth int
}

// addLimitFlags adds the --byte-limit and --depth-limit flags to cmd. Each
// defaults to the limit a [niceyaml.Source] applies without the option, and
// 0 turns its check off, so a large dump of "kubectl get -o yaml" reads
// with --byte-limit 0.
func addLimitFlags(cmd *cobra.Command) *limitFlags {
	l := &limitFlags{cmd: cmd}

	cmd.Flags().IntVar(&l.bytes, "byte-limit", niceyaml.DefaultByteLimit,
		"most bytes a file may hold, or 0 for no limit")
	cmd.Flags().IntVar(&l.depth, "depth-limit", niceyaml.DefaultDepthLimit,
		"most levels a document may nest, or 0 for no limit")

	return l
}

// options returns the source options of the flags that the command line
// set, so a flag left at its default leaves the default of the library in
// place.
func (l *limitFlags) options() []niceyaml.SourceOption {
	var opts []niceyaml.SourceOption

	if l.cmd.Flags().Changed("byte-limit") {
		opts = append(opts, niceyaml.WithByteLimit(l.bytes))
	}

	if l.cmd.Flags().Changed("depth-limit") {
		opts = append(opts, niceyaml.WithDepthLimit(l.depth))
	}

	return opts
}
