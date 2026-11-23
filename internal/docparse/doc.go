// Package docparse parses a stream of YAML tokens into documents.
//
// The go-yaml parser rejects or misreads some valid YAML. It drops the
// rest of a stream after a "---" header that directly follows another.
// It rejects a comment on a line of its own in several places. One bad
// document also costs it the whole stream. It also takes a "-" on the
// line of a tag, as in "a: !t - 1", and a "-" that starts an entry inside
// a flow collection, as in "[- 1]". It reads a "-" right before a flow
// indicator as a plain scalar, as in "[-]", and it reads a plain scalar
// in a flow collection that holds a flow indicator, as in "[a}]". It
// takes a "-" that starts a line of a flow collection after an explicit
// key, where the line does not pass the indent of the block node that
// holds the collection, as in "key: {? A\n- b}". YAML refuses each of
// them. The parser reads a key on the line below a single pair of a flow
// sequence into the pair, as in "[a: b\n c: d]". YAML holds one key in
// such a pair, and it refuses the text without a "," before the second
// key. The parser also refuses an explicit key without a value in a flow
// sequence, as in "[? A]", which YAML reads as [{A: null}].
//
// [Parse] avoids each of these faults. It cuts the stream into runs that
// each parse on their own, and drops the comments that make the parser
// reject or misread valid YAML. It fails a run with any such "-" or
// plain scalar, unless the parser refuses an earlier token of the run.
// The error of the parser also wins for a "-" that starts a line right
// below a plain scalar whenever the parser refuses the run. It fails a
// run where the parser reads more than one key into a single pair of a
// flow sequence. It hands the parser a null after each explicit key of a
// flow sequence that lacks its value, as [NullFlowPairs] adds it.
// For each document of a run that does not parse, it puts a stand-in
// into the file.
// The [Result] holds the file, the copies of the tokens the parser read,
// and a [Failure] for each run that did not parse. [ParseLimits] also fails
// a run past a byte limit or a depth limit before the parser reads it.
// [Fail] gives the Result of a stream that the caller refuses before the
// parse, with one stand-in for the whole stream.
//
// The parser cuts the comments and directives above a header, and the
// comments after a "..." marker, into nodes of their own.
// [Result.Documents] folds those nodes into the documents they belong to
// and pairs each [Document] with its tokens and the lines it covers.
package docparse
