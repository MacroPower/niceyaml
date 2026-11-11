// Package input finds the YAML files that a program reads and builds a
// [niceyaml.Source] for each. The files come from disk, an [io/fs.FS],
// standard input, an [io.Reader], or Sources that the program built. The
// package does not know what the documents mean, so a linter, a schema
// check, or an editor reads a tree of files with it.
// [go.jacobcolvin.com/niceyaml/manifest.Registry] reads the manifests of
// the same inputs.
//
// # Inputs
//
// An [Input] names where the files come from, and [Input.Files] reads
// them:
//
//	files, err := input.Path(dir, input.WithRecursive(true)).Files(ctx)
//	if err != nil {
//		return err // A bad pattern, a missing dir, or the end of ctx.
//	}
//
//	var errs []error
//
//	for _, f := range files {
//		if f.Err != nil {
//			errs = append(errs, f.Err)
//
//			continue
//		}
//
//		errs = append(errs, f.Source.ValidateDocuments(ctx, v))
//	}
//
//	return errors.Join(errs...)
//
// These functions build an Input:
//
//   - [Path] reads a file or a directory, so a --config flag takes either.
//   - [FS] reads a file or a directory of an [io/fs.FS].
//   - [Stdin] reads standard input.
//   - [Reader] reads an [io.Reader].
//   - [Sources] reads Sources that the program built.
//   - [Join] reads several inputs in order.
//
// Path reads only the file system, so a CLI whose --config flag takes "-"
// for standard input picks Stdin itself. The first read of a Stdin or
// Reader input reads its text, and every later one reuses that text. One
// Input thus serves
// [go.jacobcolvin.com/niceyaml/manifest.Registry.PlanMigration] and the
// load after it.
//
// Each [File] holds the Source of one file, or the problem that kept the
// read from taking the file in. Files returns an error only for what fails
// before it reads a file, so the problem of one file does not hide the
// others.
//
// # Selection
//
// A directory input reads the files that match "*.yaml" or "*.yml", and
// skips each entry whose name starts with ".". [WithRecursive],
// [WithInclude], and [WithExclude] change the selection. [WithHidden]
// keeps the entries whose names start with ".", such as ".github", so the
// patterns select among them too.
//
// A read takes the inputs in order, the files of a directory sorted by
// path. A file reads once, however many inputs and paths reach it, except
// through the links of an [FS] input that the Links section describes.
//
// # Links
//
// A walk reads a file that a symbolic link names only when the link leads
// inside the input directory to a file that the patterns select.
// [WithOutsideLinks] also lets a link lead outside. Any other link to a
// regular file fails with an error that matches [ErrOutsideLink].
//
// The walk of an [FS] input applies that rule to each link that the file
// system reports through [fs.ReadLinkFS], as [FS] describes. A directory
// listing can name a link that the file system does not report. The walk
// follows that link only under WithOutsideLinks. It then reads what Open
// returns for the link, so a file that the link and its own path both
// reach reads twice.
//
// A listing can also describe a link as a regular file or as a
// directory. The walk then reads the entry as that file, and a recursive
// walk enters the entry as that directory. A file reads once for each
// path that reaches it through one of those entries, with or without
// WithOutsideLinks. A file that one entry and its own path both reach
// thus reads twice.
//
// The walk of a [Path] input lists each directory and opens each file
// inside it through an [os.Root] of the input directory. Without
// WithOutsideLinks, the walk thus refuses a link that leads outside even
// when another process changes the directory during the read. The
// refusal covers symbolic links only, so the walk reads a file that a
// hard link names. An [FS] input that reads "." of the FS of an [os.Root]
// opened on the directory keeps the same refusal.
//
// # Sources
//
// Each Source that a read builds takes the options of [WithSourceOptions],
// such as the limits of [niceyaml.WithByteLimit],
// [niceyaml.WithDepthLimit], and [niceyaml.WithAliasLimit]. A file longer
// than the byte limit gets a [File] with an error that matches
// [niceyaml.ErrLimit].
//
// A Path or FS input names each Source by the path of its file. A Path
// input also gives the Source the absolute path of the file, as
// [niceyaml.WithFilePath] does. An FS input gives it the path in the file
// system and the file system, as [niceyaml.WithFS] does. A validator that
// picks a schema by file path thus finds the file.
package input
