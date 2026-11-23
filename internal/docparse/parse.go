package docparse

import (
	"errors"
	"math"
	"runtime/debug"
	"slices"
	"strings"

	"github.com/goccy/go-yaml"
	"github.com/goccy/go-yaml/ast"
	"github.com/goccy/go-yaml/parser"
	"github.com/goccy/go-yaml/token"

	"go.jacobcolvin.com/niceyaml/internal/astnode"
	"go.jacobcolvin.com/niceyaml/internal/preamble"
	"go.jacobcolvin.com/niceyaml/tokens"
)

// Result is what [Parse] gives of a stream of tokens. It is also what
// [Fail] gives of a stream that the caller refuses before the parse.
type Result struct {
	// File holds the documents of the runs that parsed, with the
	// stand-ins of the runs that did not, in file order. The file of
	// Fail holds one stand-in for the whole stream.
	File *ast.File
	// Index maps each copy of a token that Parse hands the parser to the
	// index of the token in the input. It is empty in a Result of Fail,
	// which does not copy any token.
	Index map[*token.Token]int
	// Each stand-in among the Docs of File. The map is nil when every run
	// parsed.
	standIns map[*ast.DocumentNode]standIn
	// Tokens holds the copies of the input that Parse hands the parser,
	// each at the index of its own token. It is nil in a Result of Fail.
	Tokens token.Tokens
	// Failures holds each run that did not parse, in file order. Fail
	// holds the whole stream as one run.
	Failures []Failure
}

// Failure is a run of tokens that did not parse. The parser rejected the
// run or panicked on it, or the run failed before the parser read it. A
// run past a limit of [ParseLimits] fails that way, and so does the
// stream of [Fail]. A run also fails when it holds a "-" right after an
// anchor or a tag on its line, or a "-" that starts an entry inside a
// flow collection. It fails as well when a plain scalar inside a flow
// collection starts with a "-" right before a flow indicator, as in
// "[-]". So does a plain scalar there that holds a flow indicator, as in
// "[a}]". So does the name of an anchor or an alias that holds a flow
// indicator, as in "[&x} 1]". So does a block scalar in a flow
// collection, as in "[x, |\n  a\n]". The parser refuses the "-" after an
// anchor, but it takes the others. A run with a "-" that starts a line
// right below a plain scalar in a flow collection fails too, when the
// parser takes the run. So does a run where the parser reads more than
// one key into a single pair of a flow sequence, as in "[a: b\n c: d]".
type Failure struct {
	// Err is the error of the run, or nil after a panic. It is the error
	// that the parser returned, or the error that the caller handed
	// [Fail]. A run past the byte limit has a [*SizeError], and a run past
	// the depth limit has a [*DepthError]. Err can also be the
	// [*yaml.SyntaxError] that [misreadError] or [flowPairError] returns.
	// The error of [misreadError] also takes the place of a panic.
	Err error
	// Panic is the value the parser panicked with, or nil.
	Panic any
	// Stack is the stack of the goroutine at the panic, as [debug.Stack]
	// formats it.
	Stack []byte
	// Run holds the tokens of the run.
	Run token.Tokens
}

// standIn describes a node that stands in the file for tokens that did
// not parse, in place of the nodes of those tokens. [Result.fail] puts one
// in the file for each token group of a run that did not parse, and [Fail]
// puts in one for a whole stream.
type standIn struct {
	// The token that gives the stand-in its place in the file. It is nil
	// when the tokens of the stand-in do not carry a position.
	anchor *token.Token
	// The index in Failures of the run, for a stand-in that stands for a
	// document. It is -1 for one that stands for the comments and
	// directives above a header, which the parser gives a node of their
	// own.
	failure int
}

// Parse parses tks, a stream of tokens, with [parser.ParseComments] and
// opts. It hands the parser the copies of the tokens that
// [tokens.ForParser] makes. The caller shares tks, and the copies keep tks
// untouched while the parser relinks comment tokens. After the parser
// returns, each copy takes back the Origin and position of its own token.
// The parser also reads the null that [NullFlowPairs] adds after each
// explicit key of a flow sequence that lacks its value. Neither
// [Result.Tokens] nor [Result.Index] holds such a null, as neither holds a
// null that the parser makes itself. The parser reads each anchor after a
// collection tag before the tag, as [AnchorsFirst] moves it. Result.Tokens
// keeps the order of tks.
//
// Each run of documents parses on its own, so a run the parser rejects
// costs the file only its own documents. [Result.File] holds stand-ins in
// their place, and [Result.Failures] holds the run.
func Parse(tks token.Tokens, opts ...parser.Option) *Result {
	return ParseLimits(tks, Limits{Bytes: math.MaxInt, Depth: math.MaxInt}, opts...)
}

// Limits bounds each run of documents that [ParseLimits] parses. A limit
// of [math.MaxInt] does not bound anything.
type Limits struct {
	// Bytes is the most bytes of text that a run can hold, as [Runs]
	// counts them.
	Bytes int
	// Depth is the most levels that a document can nest, as [TooDeep]
	// counts them.
	Depth int
}

// ParseLimits is [Parse] with limits on each run of documents. A run that
// holds more bytes than limits allows fails with a [*SizeError] in place
// of a parse. The time and memory of the parser grow faster than the text
// of a run, so the parser never reads such a run. A run that holds a
// document past the depth limit fails with a [*DepthError] in place of a
// parse. The memory of the parser grows with the square of the depth, so
// the parser never reads such a run either. The byte limit goes first.
func ParseLimits(tks token.Tokens, limits Limits, opts ...parser.Option) *Result {
	return parse(tks, limits, parser.ParseComments, opts)
}

// ParseWithoutComments is [Parse] without the comments of tks, as
// yaml.Unmarshal parses a text. The mode of the parser decides only
// whether it reads the comment tokens, so a stream without a comment
// parses as Parse parses it.
func ParseWithoutComments(tks token.Tokens, opts ...parser.Option) *Result {
	return parse(tks, Limits{Bytes: math.MaxInt, Depth: math.MaxInt}, 0, opts)
}

// parse parses tks in mode, as [ParseLimits] describes.
func parse(tks token.Tokens, limits Limits, mode parser.Mode, opts []parser.Option) *Result {
	copies := tokens.ForParser(tks)

	index := make(map[*token.Token]int, len(copies))
	for i, tk := range copies {
		index[tk] = i
	}

	// Tks holds no nil tokens, so each copy sits at the index of its own
	// token.
	defer func() {
		for i, tk := range copies {
			tk.Origin = tks[i].Origin

			// Clone gives each copy a Position of its own, so the write
			// leaves the token of tks alone.
			if tk.Position != nil && tks[i].Position != nil {
				*tk.Position = *tks[i].Position
			}
		}
	}()

	r := &Result{File: &ast.File{Docs: []*ast.DocumentNode{}}, Index: index, Tokens: copies}

	// ForParser trims the Origin of a copy, so the size counts the tokens
	// of tks.
	for _, sized := range sizedRuns(splitDocumentRuns(copies), tks) {
		run := sized.Tokens

		if sized.Size > limits.Bytes {
			r.fail(Failure{Err: &SizeError{Token: sizeToken(run), Limit: limits.Bytes}, Run: run})

			continue
		}

		if at, deep := TooDeep(run, limits.Depth); deep {
			r.fail(Failure{Err: &DepthError{Token: at, Limit: limits.Depth}, Run: run})

			continue
		}

		f, failure := parseRun(NullFlowPairs(dropStrandedComments(AnchorsFirst(run))), mode, opts)
		parsed := failure == nil

		// The run fails with its first error in file order. The error of
		// the parser thus wins when it binds before the token of the misread.
		// Any error of the parser wins over the error of a "-" right below
		// a plain scalar, because the parser names the token that it
		// refuses on the line of the key there.
		entryErr, yields := misreadError(run)

		switch {
		case entryErr == nil, r.bindsBefore(failure, entryErr.Token):
		case yields && failure != nil && failure.Err != nil:
		default:
			failure = &Failure{Err: entryErr}
		}

		// The error of a key that the parser reads into a single pair of a
		// flow sequence comes from a run that the parser took. It wins over
		// the error of a misread only when it binds before the token of the
		// misread.
		if parsed {
			pairErr := flowPairError(f, index)
			if pairErr != nil && (entryErr == nil || index[pairErr.Token] < index[entryErr.Token]) {
				failure = &Failure{Err: pairErr}
			}
		}

		if failure == nil {
			nullEmptyAnchors(f)

			r.File.Docs = append(r.File.Docs, f.Docs...)

			continue
		}

		failure.Run = run

		r.fail(*failure)
	}

	// The parser makes one empty document of an empty file but none of a
	// stream of "..." markers alone. That stream gets one too, which the
	// first marker ends, so it decodes to the zero value as an empty file
	// does, although YAML holds no document there.
	if len(r.File.Docs) == 0 {
		doc := ast.Document(nil, nil)

		if i := slices.IndexFunc(
			copies,
			func(tk *token.Token) bool { return tk.Type == token.DocumentEndType },
		); i >= 0 {
			doc.End = copies[i]
		}

		r.File.Docs = append(r.File.Docs, doc)
	}

	return r
}

// Fail returns the [Result] of tks, a stream that fails whole with err
// before any parse, such as one past a byte limit. The parser does not read
// tks, so the file does not hold a node for each document of the stream.
// The file holds one stand-in, which carries err. The Result does not hold
// any copy of tks.
//
// [Result.Documents] gives the document of that stand-in every token of
// tks when every token has a position, as the tokens of [tokens.Tokenize]
// do. Documents places a document by the positions of its tokens. The
// document might thus lack tokens of a stream that holds a token without
// a position.
func Fail(tks token.Tokens, err error) *Result {
	in := standIn{failure: 0}

	if i := slices.IndexFunc(tks, func(tk *token.Token) bool { return tk != nil && tk.Position != nil }); i >= 0 {
		in.anchor = tks[i]
	}

	node := ast.Document(nil, nil)

	return &Result{
		File:     &ast.File{Docs: []*ast.DocumentNode{node}},
		Index:    map[*token.Token]int{},
		standIns: map[*ast.DocumentNode]standIn{node: in},
		Failures: []Failure{{Err: err, Run: tks}},
	}
}

// fail records f, a run that did not parse. It adds a stand-in to the
// file for each token group of the run, as [SplitGroups] splits it. The
// documents of the run thus keep their places and tokens. The
// stand-in of a group that [isDocumentGroup] reports carries f. Any other
// stand-in folds into a document, as a node that the parser separates
// from its document does. A run without a document group carries f on its
// first group, so f always names a document.
func (r *Result) fail(f Failure) {
	r.Failures = append(r.Failures, f)

	failure := len(r.Failures) - 1

	var groups []token.Tokens

	for _, group := range SplitGroups(f.Run) {
		groups = append(groups, group)
	}

	// A run without tokens still gets one stand-in to carry f.
	if len(groups) == 0 {
		groups = []token.Tokens{nil}
	}

	holdsDocument := slices.ContainsFunc(groups, isDocumentGroup)

	if r.standIns == nil {
		r.standIns = map[*ast.DocumentNode]standIn{}
	}

	for i, group := range groups {
		in := standIn{failure: -1}

		if j := slices.IndexFunc(group, func(tk *token.Token) bool { return tk.Position != nil }); j >= 0 {
			in.anchor = group[j]
		}

		node := ast.Document(nil, nil)

		if isDocumentGroup(group) || !holdsDocument && i == 0 {
			in.failure = failure

			// SplitGroups starts a group at each header, so a header
			// is the first token of its group.
			if len(group) > 0 && group[0].Type == token.DocumentHeaderType {
				node.Start = group[0]
			}
		}

		r.File.Docs = append(r.File.Docs, node)
		r.standIns[node] = in
	}
}

// parseRun parses run, one run of [splitDocumentRuns], in mode with opts. It
// returns the [Failure] of a run the parser rejects or panics on. The
// deferred function that recovers a panic takes the stack, so the stack
// still holds the frames of the parser.
func parseRun(run token.Tokens, mode parser.Mode, opts []parser.Option) (*ast.File, *Failure) {
	var (
		f       *ast.File
		failure *Failure
	)

	func() {
		defer func() {
			p := recover()
			if p == nil {
				return
			}

			failure = &Failure{Panic: p, Stack: debug.Stack()}
		}()

		var err error

		f, err = parser.Parse(run, mode, opts...)
		if err != nil {
			failure = &Failure{Err: err}
		}
	}()

	return f, failure
}

// TagSequenceMessage is the message of the [*yaml.SyntaxError] of a "-"
// right after a tag on its line, which fails the run of the "-".
const TagSequenceMessage = "sequence entries are not allowed after tag on the same line"

// AnchorSequenceMessage is the message of the [*yaml.SyntaxError] of a
// "-" right after an anchor on its line, which fails the run of the "-".
// It matches the message of the go-yaml parser for that "-".
const AnchorSequenceMessage = "sequence entries are not allowed after anchor on the same line"

// FlowSequenceMessage is the message of the [*yaml.SyntaxError] of a "-"
// that starts a sequence entry inside a flow collection, which fails the
// run of the "-".
const FlowSequenceMessage = "sequence entries are not allowed in a flow collection"

// FlowIndicatorMessage is the message of the [*yaml.SyntaxError] of a
// plain scalar that starts with a "-" right before a flow indicator, as
// in "[-]", which fails the run of the "-".
const FlowIndicatorMessage = "a plain scalar cannot start with a \"-\" before a flow indicator"

// FlowScalarMessage is the message of the [*yaml.SyntaxError] of a plain
// scalar inside a flow collection that holds a flow indicator, as in
// "[a}]", which fails the run of the scalar.
const FlowScalarMessage = "a plain scalar in a flow collection cannot hold a flow indicator"

// NameIndicatorMessage is the message of the [*yaml.SyntaxError] of the
// name of an anchor or an alias that holds a flow indicator, as in
// "[&x} 1]", which fails the run of the name.
const NameIndicatorMessage = "the name of an anchor or an alias cannot hold a flow indicator"

// FlowBlockScalarMessage is the message of the [*yaml.SyntaxError] of a
// literal or a folded block scalar inside a flow collection, as in
// "[x, |\n  a\n]", which fails the run of the scalar.
const FlowBlockScalarMessage = "a flow collection cannot hold a block scalar"

// FlowIndentMessage is the message of the [*yaml.SyntaxError] of a line of
// a flow collection that does not pass the indent of the block node that
// holds the collection. YAML requires each line of the collection to pass
// that indent. [tokens.Tokenize] reports the first character of such a
// line as an invalid token with this message, or the quoted scalar that
// spans the line.
const FlowIndentMessage = "a line of a flow collection must be indented past the block node that holds it"

// FlowPairMessage is the message of the [*yaml.SyntaxError] of a key that
// follows a single pair in a flow sequence without a "," between them, as
// in "[a: b\n c: d]". It matches the message of the go-yaml parser for
// another entry that follows an entry without a ",".
const FlowPairMessage = "',' or ']' must be specified"

// flowPairError returns the syntax error of the first key in file order
// that the go-yaml parser reads into a single pair of a flow sequence of
// f, or nil. Index gives the order of the tokens. The error has
// [FlowPairMessage], and it binds at the first key that [FlowPairKeys]
// returns.
func flowPairError(f *ast.File, index map[*token.Token]int) *yaml.SyntaxError {
	var (
		first *token.Token
		at    int
	)

	for _, tk := range FlowPairKeys(f) {
		if i, ok := index[tk]; ok && (first == nil || i < at) {
			first, at = tk, i
		}
	}

	if first == nil {
		return nil
	}

	return &yaml.SyntaxError{Message: FlowPairMessage, Token: first}
}

// FlowPairKeys returns the first token of each key in f that the go-yaml
// parser reads into a single pair of a flow sequence after the one key of
// the pair. YAML refuses each such key. FlowPairKeys gives the first such
// key of each pair, and the token of an explicit key is its "?".
//
// A single pair in a flow sequence holds one key and its value. So
// "[a: b, c: d]" holds [{a: b}, {c: d}]. YAML ignores the indent of a line in a
// flow collection past the indent of the block node that holds the collection,
// and it ends the plain value of a pair at the ":" of a key on the next line.
// So a "," must come before that key. The go-yaml parser reads the pair as a
// block mapping. It takes each key at the column of the first key as another
// key of that mapping, so it reads "[a: b\n c: d]" as [{a: b, c: d}]. It reads
// a key that is indented past the first key, below a key without a value, as a
// block mapping in the value of the pair. So it reads "[a:\n  c: d]" as
// [{a: {c: d}}]. Each mapping in a flow sequence that is not in flow style is
// such a pair.
func FlowPairKeys(f *ast.File) []*token.Token {
	var keys []*token.Token

	for _, doc := range f.Docs {
		astnode.Inspect(doc, func(node ast.Node) bool {
			seq, ok := node.(*ast.SequenceNode)
			if !ok || !seq.IsFlowStyle {
				return true
			}

			for _, value := range seq.Values {
				pair, ok := astnode.Content(value).(*ast.MappingNode)
				if !ok || pair.IsFlowStyle || len(pair.Values) == 0 {
					continue
				}

				if key := flowPairKey(pair); key != nil {
					keys = append(keys, key)
				}
			}

			return true
		})
	}

	return keys
}

// flowPairKey returns the first token of the first key that the go-yaml
// parser reads into pair after its one key, or nil. Pair is a single pair
// of a flow sequence. Such a key is the first key of a block mapping in
// the value of the pair, or the second key of pair.
func flowPairKey(pair *ast.MappingNode) *token.Token {
	nested, ok := astnode.Content(pair.Values[0].Value).(*ast.MappingNode)
	if ok && !nested.IsFlowStyle && len(nested.Values) > 0 {
		return astnode.StartToken(nested.Values[0].Key)
	}

	if len(pair.Values) > 1 {
		return astnode.StartToken(pair.Values[1].Key)
	}

	return nil
}

// misreadError returns the syntax error of the first token of run that
// the go-yaml parser can misread, or nil. Such a token is a "-", a plain
// scalar, an anchor or an alias, or the header of a block scalar. The "-"
// starts a block sequence where YAML does not hold one, starts a plain
// scalar that YAML refuses, or starts a line right below a plain scalar in
// a flow collection. The plain scalar holds a flow indicator inside a flow
// collection. The name of the anchor or the alias holds a flow indicator.
// The block scalar sits inside a flow collection. It reports whether
// the token is a "-" that starts a line right below a plain scalar.
//
// YAML puts a line break between the properties of a block sequence and
// its first "-". The parser refuses a "-" right after an anchor on its
// line, as in "a: &x - 1". It takes one right after a tag, and it reads
// "a: !t - 1" as {a: [1]}. The error of the "-" after the tag has
// [TagSequenceMessage]. The error of the "-" after the anchor has
// [AnchorSequenceMessage], the message of the parser.
//
// A flow collection does not hold a block sequence, and a plain scalar
// does not start with a "-" that a space follows. The parser reads
// "[- 1]" as [[1]] and "{a: - 1}" as {a: [1]}. The error of that "-" has
// [FlowSequenceMessage]. A "-" right after an anchor or a tag on its line
// inside a flow collection keeps the message of the anchor or the tag. So
// "[&x - 1]" fails as the parser fails it.
//
// Inside a flow collection, a plain scalar starts with a "-" only when a
// character other than a space, a line break, or a flow indicator follows
// the "-". The go-yaml lexer reads a "-" right before a ",", a "]", or a
// "}" as a plain scalar, so the parser reads "[-]" as ["-"] and "{a: -}"
// as {a: "-"}. The error of that "-" has [FlowIndicatorMessage], also
// after a tag. A "-" right before a ":" starts a plain scalar, so "{-: 1}"
// holds the key "-". The name "-" of an anchor or an alias is not a plain
// scalar.
//
// YAML ends a plain scalar inside a flow collection at each flow
// indicator, so such a scalar does not hold one. The go-yaml lexer ends
// the scalar at a "]" only inside a flow sequence and at a "}" only
// inside a flow mapping, as its counts of the open collections tell. So
// the parser reads "[a}]" as ["a}"] and "{a: b]}" as {a: "b]"}. The error
// of that scalar has [FlowScalarMessage], and it binds at the start of the
// scalar. The lexer reads a "-" right before such an indicator into the
// scalar too, as in "[-}]". The name of an anchor or an alias is not a
// plain scalar.
//
// YAML does not allow a flow indicator in the name of an anchor or an
// alias, inside a flow collection or outside one. The go-yaml lexer ends
// such a name at a ",", a "[", or a "{" only inside a flow collection, and
// at a "]" or a "}" as it ends a plain scalar there. Outside a flow
// collection it reads each flow indicator into the name. So the parser
// reads "[&x} 1, *x}]" as [1, 1] and "a: &x, 1" as {a: 1}. The error of
// such a name has [NameIndicatorMessage], and it binds at the "&" of the
// anchor or the "*" of the alias.
//
// A "-" that starts a line right below a plain scalar in a flow
// collection continues that scalar when the line passes the indent of the
// block node that holds the collection. [tokens.Tokenize] then reads the
// "-" into the scalar. The tokenizer reports a "-" at a line that does not
// pass that indent as an invalid token, as in "key: [a\n- c]". So the
// "-" gets a token of its own below a plain scalar only at a line that
// holds the key of a ":", as in "{a: b\n - c: d}", because YAML refuses a
// scalar on more than one line as such a key. YAML refuses the text, and
// so does the parser. The error of such a "-" has [FlowSequenceMessage],
// and the run takes the error of the parser in its place when the parser
// refuses the run.
//
// YAML allows a literal or a folded block scalar only in block context,
// so a flow collection does not hold one. The go-yaml parser takes one as
// an entry of a flow sequence or as a value of a flow mapping, so it reads
// "[x, >\n  a\n]" as ["x", "a\n"]. The error of such a scalar has
// [FlowBlockScalarMessage], and it binds at the header of the scalar.
//
// The run fails with this error unless the parser refuses a token before
// the token of the error. The error of a "-" binds at the "-".
func misreadError(run token.Tokens) (*yaml.SyntaxError, bool) {
	// The number of flow collections open at the token.
	depth := 0

	for i, tk := range run {
		switch tk.Type {
		case token.SequenceStartType, token.MappingStartType:
			depth++

		case token.SequenceEndType, token.MappingEndType:
			depth--

		case token.SequenceEntryType:
			if i > 0 && run[i-1].Type == token.TagType && sameLine(run[i-1], tk) {
				return &yaml.SyntaxError{Message: TagSequenceMessage, Token: tk}, false
			}

			// The name of the anchor sits between the "&" and the "-".
			if i > 1 && run[i-2].Type == token.AnchorType && sameLine(run[i-2], tk) {
				return &yaml.SyntaxError{Message: AnchorSequenceMessage, Token: tk}, false
			}

			if depth > 0 && belowPlain(run, i) {
				return &yaml.SyntaxError{Message: FlowSequenceMessage, Token: tk}, true
			}

			if depth > 0 {
				return &yaml.SyntaxError{Message: FlowSequenceMessage, Token: tk}, false
			}

		case token.LiteralType, token.FoldedType:
			if depth > 0 {
				return &yaml.SyntaxError{Message: FlowBlockScalarMessage, Token: tk}, false
			}

		case token.AnchorType, token.AliasType:
			if i+1 < len(run) && run[i+1].Type == token.StringType &&
				strings.ContainsAny(run[i+1].Value, flowIndicators) {
				return &yaml.SyntaxError{Message: NameIndicatorMessage, Token: tk}, false
			}

		case token.StringType:
			// The string is the name of the anchor or the alias.
			if depth <= 0 || i > 0 && (run[i-1].Type == token.AnchorType || run[i-1].Type == token.AliasType) {
				continue
			}

			if strings.ContainsAny(tk.Value, flowIndicators) {
				return &yaml.SyntaxError{Message: FlowScalarMessage, Token: tk}, false
			}

			if tk.Value != "-" || !beforeFlowIndicator(run, i) {
				continue
			}

			return &yaml.SyntaxError{Message: FlowIndicatorMessage, Token: tk}, false

		default:
		}
	}

	return nil, false
}

// flowIndicators holds the flow indicators of YAML. A plain scalar inside
// a flow collection does not hold any of them.
const flowIndicators = ",[]{}"

// beforeFlowIndicator reports whether a flow indicator follows the plain
// scalar "-" at run[i] inside a flow collection. The go-yaml lexer ends
// that scalar only at a flow indicator or at a ":" right after the "-".
// It gives a "-" that a space, a line break, or the end of the text
// follows a token of the sequence entry type.
func beforeFlowIndicator(run token.Tokens, i int) bool {
	return i+1 >= len(run) || run[i+1].Type != token.MappingValueType
}

// belowPlain reports whether the "-" at run[i] starts a line right below
// a plain scalar.
func belowPlain(run token.Tokens, i int) bool {
	if i == 0 {
		return false
	}

	prev := run[i-1]
	if !plainScalar(prev.Type) || prev.Position == nil || run[i].Position == nil ||
		prev.Position.Line >= run[i].Position.Line {
		return false
	}

	// The name of an anchor or an alias is not a plain scalar.
	return i == 1 || run[i-2].Type != token.AnchorType && run[i-2].Type != token.AliasType
}

// plainScalar reports whether t is the type of a token of a plain scalar.
func plainScalar(t token.Type) bool {
	switch t {
	case token.StringType, token.NullType, token.BoolType, token.IntegerType,
		token.BinaryIntegerType, token.OctetIntegerType, token.HexIntegerType,
		token.FloatType, token.InfinityType, token.NanType:
		return true
	default:
		return false
	}
}

// isDocumentGroup reports whether group, a token group of [SplitGroups],
// holds a YAML document. A group holds one when it opens with a "---"
// header or holds content. A group of comments, directives, and "..."
// markers alone does not hold one.
func isDocumentGroup(group token.Tokens) bool {
	if len(group) > 0 && group[0].Type == token.DocumentHeaderType {
		return true
	}

	return preamble.Len(group) < len(group)
}

// splitDocumentRuns cuts tks into runs that each parse on their own. The
// createDocumentTokens step of the go-yaml parser
// (v1.19.3-0.20260407131736-edee2f91616c) takes quadratic time over the
// documents of one run. It also mishandles a scalar document below a
// "..." marker, and a "---" header right after a header or a marker. A run
// ends after each marker that a token on a later line follows, and before
// each header that follows a token other than a comment. A header stays in
// the run of a directive or an anchor without a value before it, because
// the parser rejects a run that ends in either.
func splitDocumentRuns(tks token.Tokens) []token.Tokens {
	var (
		runs  []token.Tokens
		start int
		// Whether a directive follows the last header.
		directive bool
	)

	// The indexes of the last two tokens that are not comments.
	prev, prevprev := -1, -1

	for i, tk := range tks {
		if tk.Type == token.CommentType {
			continue
		}

		if prev >= 0 {
			last := tks[prev]

			switch {
			case last.Type == token.DocumentEndType && startsBelow(tk, last):
				// The comments on lines below the marker open the next run.
				cut := i
				for cut > prev+1 && startsBelow(tks[cut-1], last) {
					cut--
				}

				runs = append(runs, tks[start:cut])
				start = cut

			case tk.Type == token.DocumentHeaderType && last.Type != token.DocumentEndType:
				anchored := last.Type == token.AnchorType ||
					prevprev >= 0 && tks[prevprev].Type == token.AnchorType

				if last.Type == token.DocumentHeaderType || !directive && !anchored {
					runs = append(runs, tks[start:i])
					start = i
				}
			}
		}

		switch tk.Type {
		case token.DirectiveType:
			directive = true

		case token.DocumentHeaderType:
			directive = false

		default:
		}

		prevprev, prev = prev, i
	}

	return append(runs, tks[start:])
}

// dropStrandedComments returns run without the whole-line comments that
// make the go-yaml parser reject or misread valid YAML. The decoder reads
// YAML without its comments.
//
// It drops the comments after a directive or a "?", before a ":", ",",
// "]", or "}", and where a flow mapping expects a key. It drops the
// comments that close a document below a scalar or flow collection, or
// left of the first key or "-" of a block root. Below a "&" without a
// name, it drops only the comments after a directive. Outside a flow
// collection, [breaksAnchor] and [takesNull] decide below an anchor
// without a value. The dropped comments stay among the tokens of the
// input, where [SplitGroups] still hands them to their documents.
func dropStrandedComments(run token.Tokens) token.Tokens {
	kept := make(token.Tokens, 0, len(run))

	s := scan{run: run, rootCol: -1}

	for i := 0; i < len(run); {
		if run[i].Type != token.CommentType {
			s.read(i)

			kept = append(kept, run[i])

			i++

			continue
		}

		next := i
		for next < len(run) && run[next].Type == token.CommentType {
			next++
		}

		stranded, leftDrops := s.drops(i, next)

		for j := i; j < next; j++ {
			left := leftDrops && run[j].Position != nil && run[j].Position.Column < s.rootCol

			if (stranded || left) && startsBelow(run[j], s.last) {
				continue
			}

			kept = append(kept, run[j])
		}

		i = next
	}

	return kept
}

// scan holds what [dropStrandedComments] has read of run so far.
type scan struct {
	// The last two tokens other than comments that hold text.
	last, beforeLast *token.Token
	run              token.Tokens
	// The types of the "[" and "{" tokens of the flow collections that
	// hold the scan, the innermost last.
	flows []token.Type
	// The column where the first key or "-" of the block mapping or block
	// sequence at the root of the document starts, or -1.
	rootCol int
	// The index of the last anchor in run.
	anchor int
	// What the document the scan is in has shown so far. The content flag
	// records a token of its content. The block flag records an indicator
	// of a block mapping or a block sequence at its top level. The
	// directive flag records a directive that awaits its header.
	content, block, directive bool
}

// read reads run[i], a token other than a comment.
func (s *scan) read(i int) {
	tk := s.run[i]

	switch tk.Type {
	case token.DocumentHeaderType, token.DocumentEndType:
		s.content, s.block, s.directive, s.flows = false, false, false, s.flows[:0]
		s.rootCol = -1

	case token.DirectiveType:
		s.directive = true

	case token.SequenceStartType, token.MappingStartType:
		s.content = true

		s.flows = append(s.flows, tk.Type)

	case token.SequenceEndType, token.MappingEndType:
		if len(s.flows) > 0 {
			s.flows = s.flows[:len(s.flows)-1]
		}

	case token.MappingKeyType, token.MappingValueType, token.SequenceEntryType:
		if !s.block && len(s.flows) == 0 {
			s.rootCol = rootColumn(s.run, i)
		}

		s.content = true
		s.block = s.block || len(s.flows) == 0

	case token.AnchorType:
		s.content = true
		s.anchor = i

	default:
		s.content = true
	}

	if strings.Trim(tk.Origin, " \t\r\n") != "" {
		s.beforeLast, s.last = s.last, tk
	}
}

// drops reports which of the comments at run[i:next] drop when they sit
// on lines of their own. The first result reports whether all of them
// drop, and the second whether the comments left of rootCol drop.
func (s *scan) drops(i, next int) (bool, bool) {
	run := s.run

	// A "&" with no name ends the content.
	bare := s.last != nil && s.last.Type == token.AnchorType

	// The name of an anchor ends the content when the anchor has no
	// value.
	anchored := s.beforeLast != nil && s.beforeLast.Type == token.AnchorType

	// Whether the comments close the document.
	closes := next == len(run) ||
		run[next].Type == token.DocumentHeaderType || run[next].Type == token.DocumentEndType

	// The parser takes a comment after a "?" or before a ":" as a key.
	stranded := s.directive || s.last != nil && s.last.Type == token.MappingKeyType ||
		next < len(run) && run[next].Type == token.MappingValueType

	switch {
	case bare:
		// Without the comments, the parser would take the token below
		// them as the name of the anchor.
		return s.directive, false

	case len(s.flows) > 0:
		return stranded || next < len(run) && endsFlowEntry(run[next]) ||
			startsFlowKey(s.last, s.flows[len(s.flows)-1]), false

	case anchored:
		// The first comment on a line of its own, past any comment on
		// the line where the last token starts.
		below := i
		for below < next && !startsBelow(run[below], s.last) {
			below++
		}

		return stranded || below < next && !closes && breaksAnchor(run, s.anchor, below, next),
			closes && next < len(run) && s.rootCol >= 0 && takesNull(run, s.anchor)

	case s.content && !s.block:
		return stranded || closes, false

	case s.block:
		return stranded, closes && s.rootCol >= 0

	default:
		return stranded, false
	}
}

// AnchorsFirst returns tks with each anchor that follows a collection tag
// moved before that tag, together with its name. YAML takes the tag and
// the anchor of a node in either order. So "!!map &x {a: b}" holds the
// mapping {a: b} under the anchor x. The go-yaml parser wants the
// collection right after a !!map, !!set, !!seq, or !!omap tag, and it
// refuses the text. It reads "&x !!map {a: b}" as YAML does. [Parse] hands
// the parser each run with the anchors first. The parser thus reads both
// orders the same way. The comments between the tag and the anchor stay
// after the tag.
//
// An anchor stays after the tag when it holds a key on its own line, as in
// "!!map\n&x a: b". There the tag belongs to the mapping and the anchor
// belongs to the key a, and the parser reads them that way. A "&" without
// a name stays after the tag too. The parser takes the token after a "&"
// as its name, even past a comment, so a moved "&" takes the tag as its
// name. Each token keeps its position. AnchorsFirst returns tks itself
// when it does not move an anchor.
func AnchorsFirst(tks token.Tokens) token.Tokens {
	var moved token.Tokens

	for i := 0; i < len(tks); i++ {
		if tks[i].Type != token.TagType || !collectionTag(tks[i]) {
			continue
		}

		at := nextBody(tks, i)

		if at+1 >= len(tks) || tks[at].Type != token.AnchorType ||
			!anchorName(tks[at], tks[at+1]) || holdsKey(tks, at) {
			continue
		}

		if moved == nil {
			moved = slices.Clone(tks)
		}

		// The tag and the comments after it go right after the name.
		copy(moved[i+2:at+2], tks[i:at])

		moved[i], moved[i+1] = tks[at], tks[at+1]

		i = at + 1
	}

	if moved == nil {
		return tks
	}

	return moved
}

// anchorName reports whether name is the name of the anchor at amp. The
// name is not a comment, and it starts on the line of the "&", right after
// it. The parser takes the token after a "&" as its name, even when a
// comment or a line break comes between them.
func anchorName(amp, name *token.Token) bool {
	return name.Type != token.CommentType && sameLine(name, amp) &&
		name.Position.Column == amp.Position.Column+1
}

// collectionTag reports whether tag is a tag that the go-yaml parser
// reserves for a collection: !!map, !!set, !!seq, or !!omap.
func collectionTag(tag *token.Token) bool {
	switch token.ReservedTagKeyword(tag.Value) {
	case token.MappingTag, token.SetTag, token.SequenceTag, token.OrderedMapTag:
		return true

	default:
		return false
	}
}

// holdsKey reports whether the anchor at tks[at] holds a key that starts
// on its line, as in "&x a: 1". The node of the anchor follows its name
// and any other tag or anchor on the line. The key is a scalar, an alias,
// or a flow collection, and a ":" follows it.
func holdsKey(tks token.Tokens, at int) bool {
	// The name of the anchor follows its "&".
	j := nextBody(tks, at+1)

	for j < len(tks) && sameLine(tks[j], tks[at]) &&
		(tks[j].Type == token.TagType || tks[j].Type == token.AnchorType) {
		if tks[j].Type == token.AnchorType {
			j = nextBody(tks, j)
		}

		j = nextBody(tks, j)
	}

	if j >= len(tks) || !sameLine(tks[j], tks[at]) {
		return false
	}

	switch t := tks[j].Type; t {
	case token.SequenceStartType, token.MappingStartType:
		j = flowEnd(tks, j)

	case token.AliasType:
		// The name of the alias follows its "*".
		j = nextBody(tks, j)

	default:
	}

	j = nextBody(tks, j)

	return j < len(tks) && tks[j].Type == token.MappingValueType
}

// NullFlowPairs returns tks with a null after each explicit key of a flow
// sequence that lacks its value. YAML reads such a key as a pair, so
// "[? A]" and "[? A: ]" each hold [{A: null}]. The go-yaml parser gives a
// null to an explicit key without a value in a flow mapping, as in
// "{? A}", but it refuses the key in a flow sequence. [Parse] hands the
// parser each run with these nulls, and so the parser reads the pair as
// YAML does. NullFlowPairs adds a null only after a key that holds one
// node, so the parser still refuses a key without a node, as in "[? ]".
//
// The null goes after the last token of the key, or after its ":". It
// takes the position right after the start of the last token of the pair
// on the line of the "?". In a flow mapping, the parser places the null
// of an explicit key right after the start of the last token of the pair.
// That token can be a ":" on a line below the "?", but the parser refuses
// the flow sequence when the null sits on such a line. It reads a null on
// the line of the "?" as the value of the key at any indent.
// NullFlowPairs returns tks itself when it does not add a null.
func NullFlowPairs(tks token.Tokens) token.Tokens {
	type null struct {
		tk    *token.Token
		after int
	}

	var (
		nulls []null
		// The types of the "[" and "{" tokens of the flow collections that
		// hold the token, the innermost last.
		flows []token.Type
	)

	for i, tk := range tks {
		switch tk.Type {
		case token.SequenceStartType, token.MappingStartType:
			flows = append(flows, tk.Type)

		case token.SequenceEndType, token.MappingEndType:
			if len(flows) > 0 {
				flows = flows[:len(flows)-1]
			}

		case token.MappingKeyType:
			if len(flows) == 0 || flows[len(flows)-1] != token.SequenceStartType {
				continue
			}

			if at := pairEnd(tks, i); at >= 0 {
				nulls = append(nulls, null{after: at, tk: implicitNull(tks, i, at)})
			}

		default:
		}
	}

	if len(nulls) == 0 {
		return tks
	}

	// The pair of a key inside another key ends before the pair of the
	// outer key, so the nulls go in the order of the tokens they follow.
	slices.SortStableFunc(nulls, func(a, b null) int { return a.after - b.after })

	filled := make(token.Tokens, 0, len(tks)+len(nulls))
	from := 0

	for _, n := range nulls {
		filled = append(append(filled, tks[from:n.after+1]...), n.tk)
		from = n.after + 1
	}

	return append(filled, tks[from:]...)
}

// pairEnd returns the index of the last token of the pair of the explicit
// key at tks[key] in a flow sequence when the pair lacks its value. That
// token is the last token of the key, or the ":" after it. The key holds
// one node: the properties of the node, an anchor or a tag or both, and
// then a scalar, an alias, or a flow collection. A "," or a "]" ends the
// pair, right after the key or after its ":". PairEnd returns -1 for any
// other pair, such as one that holds a value, one whose key does not hold
// a node, as in "[? ]", one whose key holds two scalars, as in
// "[? \"A\" B]", or one whose key has a tag on a line above its node, as
// in "[? !T\n A]".
func pairEnd(tks token.Tokens, key int) int {
	j := nextBody(tks, key)

	// The properties of the node. The name of an anchor follows its "&".
	var tag *token.Token

	for j < len(tks) && (tks[j].Type == token.AnchorType || tks[j].Type == token.TagType) {
		if tks[j].Type == token.AnchorType {
			j = nextBody(tks, j)
		} else {
			tag = tks[j]
		}

		j = nextBody(tks, j)
	}

	// The parser reads a tag at the end of a line of a flow collection as
	// the tag of a null, so it does not read the node below as the key.
	if j >= len(tks) || startsBelow(tks[j], tag) {
		return -1
	}

	switch t := tks[j].Type; {
	case t == token.AliasType:
		// The name of the alias follows its "*".
		j = nextBody(tks, j)

	case t == token.SequenceStartType || t == token.MappingStartType:
		j = flowEnd(tks, j)

	case plainScalar(t) || t == token.MergeKeyType || t == token.SingleQuoteType || t == token.DoubleQuoteType:

	default:
		return -1
	}

	if j >= len(tks) {
		return -1
	}

	last := j
	j = nextBody(tks, j)

	if j < len(tks) && tks[j].Type == token.MappingValueType {
		last = j
		j = nextBody(tks, j)
	}

	if j >= len(tks) || tks[j].Type != token.CollectEntryType && tks[j].Type != token.SequenceEndType {
		return -1
	}

	return last
}

// nextBody returns the index of the first token after tks[i] that is not a
// comment, or len(tks) when every token after tks[i] is a comment.
func nextBody(tks token.Tokens, i int) int {
	i++
	for i < len(tks) && tks[i].Type == token.CommentType {
		i++
	}

	return i
}

// flowEnd returns the index of the "]" or "}" that closes the flow
// collection that the "[" or "{" at tks[start] opens, or len(tks) when the
// collection does not close.
func flowEnd(tks token.Tokens, start int) int {
	depth := 0

	for j := start; j < len(tks); j++ {
		switch tks[j].Type {
		case token.SequenceStartType, token.MappingStartType:
			depth++

		case token.SequenceEndType, token.MappingEndType:
			depth--
			if depth == 0 {
				return j
			}

		default:
		}
	}

	return len(tks)
}

// implicitNull returns the null that [NullFlowPairs] adds after tks[at],
// the last token of the pair of the explicit key at tks[key]. The null
// starts right after the start of the last token of the pair on the line
// of the "?". The parser refuses the flow sequence when the null sits on
// a line below the "?". The null does not have a position when the "?"
// does not have one.
func implicitNull(tks token.Tokens, key, at int) *token.Token {
	base := tks[key]

	for j := key + 1; j <= at; j++ {
		if tks[j].Type != token.CommentType && sameLine(tks[j], base) {
			base = tks[j]
		}
	}

	var pos *token.Position

	if base.Position != nil {
		p := *base.Position
		p.Column++
		pos = &p
	}

	tk := token.New("null", " null", pos)
	tk.Type = token.ImplicitNullType

	return tk
}

// nullEmptyAnchors puts a null in place of each comment that the parser
// took as the value of an anchor of file. The null has the token type and
// position of the implicit null that the parser gives an anchor without a
// value. Without the null, the go-yaml decoder rejects the comment in a
// typed target and gives a pointer a value for it.
func nullEmptyAnchors(file *ast.File) {
	for _, doc := range file.Docs {
		astnode.Inspect(doc, func(node ast.Node) bool {
			anchor, ok := node.(*ast.AnchorNode)
			if !ok || astnode.HasContent(anchor.Value) {
				return node.Type() != ast.CommentType
			}

			var pos *token.Position

			if anchor.Start != nil && anchor.Start.Position != nil {
				p := *anchor.Start.Position
				p.Column++
				pos = &p
			}

			tk := token.New("null", " null", pos)
			tk.Type = token.ImplicitNullType
			anchor.Value = ast.Null(tk)

			return true
		})
	}
}

// endsFlowEntry reports whether tk is a ",", "]", or "}", which ends an
// entry or a collection in flow style.
func endsFlowEntry(tk *token.Token) bool {
	switch tk.Type {
	case token.CollectEntryType, token.SequenceEndType, token.MappingEndType:
		return true

	default:
		return false
	}
}

// startsFlowKey reports whether tk is the "{" or a "," of a flow mapping,
// which a key follows. A token of type flow opens the innermost flow
// collection around tk. It reports false when tk is nil.
func startsFlowKey(tk *token.Token, flow token.Type) bool {
	if tk == nil || flow != token.MappingStartType {
		return false
	}

	return tk.Type == token.MappingStartType || tk.Type == token.CollectEntryType
}

// breaksAnchor reports whether the comments between the named anchor at
// run[at] and the node at run[next] change the value the parser gives the
// anchor. It reports false when a token it compares does not have a
// position. The parseMapValue and parseSequenceValue steps of the parser
// give a null value to an anchor right after a "-" or a key's ":". They
// do so when the first whole-line comment, at run[below], starts left of
// the "-" or key.
func breaksAnchor(run token.Tokens, at, below, next int) bool {
	comment, tk := run[below], run[next]
	if comment.Position == nil || tk.Position == nil {
		return false
	}

	// The index of the "-" or the start of the key.
	holder, mapping := anchorHolder(run, at)
	if holder < 0 || run[holder].Position == nil {
		return false
	}

	var (
		col   = run[holder].Position.Column
		entry = tk.Type == token.SequenceEntryType
		// Whether the parser gives the anchor a null value with the
		// comments in the tree.
		null = comment.Position.Column < col
	)

	switch {
	case tk.Position.Column > col, mapping && entry && tk.Position.Column == col:
		return null

	case null:
		return false

	case tk.Position.Column < col:
		return true

	case mapping:
		return startsKey(run, next)

	default:
		return entry
	}
}

// startsKey reports whether a key of a block mapping starts at run[at],
// the first token on its line. A key starts there when that token is a
// "?" or a ":", or when a ":" follows it on its line. A "[" or "{" before
// that ":" makes the line a flow collection, which the parser does not
// read as a key, so startsKey reports false for it.
func startsKey(run token.Tokens, at int) bool {
	if run[at].Type == token.MappingKeyType {
		return true
	}

	for j := at; j < len(run) && sameLine(run[j], run[at]); j++ {
		switch run[j].Type {
		case token.MappingValueType:
			return true

		case token.SequenceStartType, token.MappingStartType:
			return false

		default:
		}
	}

	return false
}

// takesNull reports whether the parser gives the anchor at run[at] a null
// value when a token left of its key or "-" follows it. The anchor has its
// name. The parser gives that null in parseMapValue and parseSequenceValue
// for an anchor that directly follows a "-", or a key and its ":", on its
// line. The parser rejects any other anchor that a node does not follow.
func takesNull(run token.Tokens, at int) bool {
	holder, _ := anchorHolder(run, at)

	return holder >= 0
}

// anchorHolder returns the index of the "-" that the anchor at run[at]
// directly follows on its line, or of the start of the key whose ":" it
// directly follows there, as [keyStart] finds it. The boolean reports
// whether the anchor follows a key. It returns -1 when the anchor does
// not follow either.
func anchorHolder(run token.Tokens, at int) (int, bool) {
	i := at - 1
	if i < 0 || !sameLine(run[i], run[at]) {
		return -1, false
	}

	switch run[i].Type {
	case token.SequenceEntryType:
		return i, false

	case token.MappingValueType:
		return keyStart(run, i), true

	default:
		return -1, false
	}
}

// rootColumn returns the column where the entry of the "-", "?", or ":"
// at run[at] starts, or -1 when the token there does not have a position.
// An entry starts at its "-" or "?". For a ":", it starts where
// [keyStart] finds the start of the key, or at the ":" itself when
// [keyStart] does not find one.
func rootColumn(run token.Tokens, at int) int {
	tk := run[at]

	if tk.Type == token.MappingValueType {
		if k := keyStart(run, at); k >= 0 {
			tk = run[k]
		}
	}

	if tk.Position == nil {
		return -1
	}

	return tk.Position.Column
}

// keyStart returns the index of the token where the key of the ":" at
// run[colon] starts on its line. That token is its "?", or else the first
// token past any "-" or ":", which might be an anchor or a tag. It
// returns -1 when the ":" does not follow a token of a key on its line,
// or when the "?" of the key sits on a line above.
func keyStart(run token.Tokens, colon int) int {
	start := -1

	k := colon - 1
	for ; k >= 0 && sameLine(run[k], run[colon]); k-- {
		switch run[k].Type {
		case token.MappingKeyType:
			return k

		case token.SequenceEntryType, token.MappingValueType, token.DocumentHeaderType:
			return start

		default:
		}

		start = k
	}

	// The token before the line of the ":", past any comments.
	for k >= 0 && run[k].Type == token.CommentType {
		k--
	}

	if k >= 0 && run[k].Type == token.MappingKeyType {
		return -1
	}

	return start
}

// sameLine reports whether a and b start on the same line. It reports
// false when either token does not have a position.
func sameLine(a, b *token.Token) bool {
	return a.Position != nil && b.Position != nil && a.Position.Line == b.Position.Line
}

// startsBelow reports whether tk starts on a line below the line mark
// starts on. It reports false when mark is nil or either token does not
// have a position.
func startsBelow(tk, mark *token.Token) bool {
	return mark != nil && tk.Position != nil && mark.Position != nil &&
		tk.Position.Line > mark.Position.Line
}

// bindsBefore reports whether failure holds an error of the parser that
// binds at a token before tk, a token of the run of failure. A failure
// after a panic, and an error without a token of the run, do not bind
// before any token.
func (r *Result) bindsBefore(failure *Failure, tk *token.Token) bool {
	var yamlErr yaml.Error

	if failure == nil || !errors.As(failure.Err, &yamlErr) {
		return false
	}

	at, ok := r.Index[yamlErr.GetToken()]

	return ok && at < r.Index[tk]
}
