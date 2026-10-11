// Package docparse parses a stream of YAML tokens into documents.
//
// The go-yaml parser rejects or misreads some valid YAML. It drops the
// rest of a stream after a "---" header that directly follows another,
// it rejects a comment on a line of its own in several places, and one
// bad document costs it the whole stream. [Parse] works around each of
// these. It cuts the stream into runs that each parse on their own,
// drops the comments the parser would choke on, and puts a stand-in in
// the file for each document of a run the parser rejects or panics on.
// The [Result] holds the file, the copies of the tokens the parser read,
// and a [Failure] for each run that did not parse.
//
// The parser cuts the comments and directives above a header, and the
// comments after a "..." marker, into nodes of their own.
// [Result.Documents] folds those nodes into the documents they belong to
// and pairs each [Document] with its tokens and the lines it covers.
package docparse
