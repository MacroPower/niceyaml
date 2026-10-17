// Package docparse parses a stream of YAML tokens into documents.
//
// The go-yaml parser rejects or misreads some valid YAML. It drops the
// rest of a stream after a "---" header that directly follows another.
// It rejects a comment on a line of its own in several places. One bad
// document also costs it the whole stream.
//
// [Parse] avoids each of these faults. It cuts the stream into runs that
// each parse on their own, and drops the comments that make the parser
// reject or misread valid YAML. For each document of a run that the
// parser rejects or panics on, it puts a stand-in into the file. The
// [Result] holds the file, the copies of the tokens the parser read, and a
// [Failure] for each run that did not parse.
//
// The parser cuts the comments and directives above a header, and the
// comments after a "..." marker, into nodes of their own.
// [Result.Documents] folds those nodes into the documents they belong to
// and pairs each [Document] with its tokens and the lines it covers.
package docparse
