// Package fangs provides CLI utilities for applications built with [fang], a
// Cobra companion library.
//
// # Error Handling
//
// [fang]'s default error handler wraps the entire error message in a lipgloss
// style, which breaks multi-line output.
//
// This is problematic for niceyaml errors that include source context and
// annotations spanning multiple lines.
//
// [ErrorHandler] solves this by styling only the error header while preserving
// the error message formatting. Pass it to [fang.Execute]:
//
//	err := fang.Execute(ctx, rootCmd,
//	    fang.WithErrorHandler(fangs.ErrorHandler),
//	)
//
// # Profiling
//
// [Profiler] adds runtime profiling capabilities to CLI applications.
// It supports CPU, heap, allocs, goroutine, threadcreate, block, and mutex
// profiles through command-line flags.
//
// Typical usage wraps command execution with profiler lifecycle methods:
//
//	profiler := fangs.NewProfiler()
//
//	rootCmd := &cobra.Command{
//	    PersistentPreRunE: func(_ *cobra.Command, _ []string) error {
//	        return profiler.Start()
//	    },
//	}
//
//	profiler.RegisterFlags(rootCmd.PersistentFlags())
//	err := fang.Execute(ctx, rootCmd, ...)
//	stopErr := profiler.Stop()
//
// Users can then enable profiling via flags like --cpu-profile=cpu.prof.
package fangs
