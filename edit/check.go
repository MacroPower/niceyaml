package edit

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"maps"
	"math"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/goccy/go-yaml"
	"github.com/goccy/go-yaml/ast"
	"github.com/goccy/go-yaml/lexer"
	"github.com/goccy/go-yaml/parser"
	"github.com/goccy/go-yaml/token"

	"go.jacobcolvin.com/niceyaml"
	"go.jacobcolvin.com/niceyaml/internal/aliasing"
	"go.jacobcolvin.com/niceyaml/internal/astnode"
	"go.jacobcolvin.com/niceyaml/internal/docparse"
	"go.jacobcolvin.com/niceyaml/internal/hook"
	"go.jacobcolvin.com/niceyaml/internal/mapkey"
	"go.jacobcolvin.com/niceyaml/internal/quantity"
	"go.jacobcolvin.com/niceyaml/paths"
	"go.jacobcolvin.com/niceyaml/position"
)

// begin starts the calls of an Apply that messages name label, or the
// edit of a type field. [Doc.endCalls] ends them.
func (d *Doc) begin(label string) {
	d.label = label

	d.plain = mapkey.NewPlain()
	if t := d.index; t != nil && t.keys != nil {
		t.keys.Share(d.plain)
	}

	d.beginZones()
}

// endCalls ends the calls that [Doc.begin] started. It drops the state that
// the calls kept for the calls after them, such as the offsets of the runes
// of the lines of the text. It drops the indexes of the parses too, but the
// reader of the keys of the current parse. The calls of
// a later Apply read none of that state, and the state holds the parses of
// the texts that the calls left.
func (d *Doc) endCalls() {
	d.callErr, d.stuck = nil, nil
	d.plain = nil
	d.lastEdit = nil
	d.model, d.zones, d.zoneKeys, d.groups = nil, nil, nil, nil
	d.firsts, d.runes = originFirsts{}, runeOffsets{}
	d.callStart = state{}

	t := d.index
	d.index, d.other = nil, nil

	if t != nil && t.node == d.node && t.keys != nil {
		d.index = &parseIndex{node: t.node, root: t.root, keys: t.keys}
	}
}

// endCall ends one call of a [Func] on the node n, and takes err, the
// error that the Func returned. It returns the problem that fails the
// Apply. A read can record a problem that matches [ErrEngine], such as
// the error of a difference. That problem came before err, so it fails
// the Apply whatever the Func returns. Otherwise endCall binds err at n
// and returns it, or else returns the problem of a method that could not
// make its edit. Without either, it returns nil.
func (d *Doc) endCall(n *Node, err error) error {
	callErr := d.callErr
	d.callErr = nil

	if err != nil && !errors.Is(callErr, ErrEngine) {
		return n.bind(err)
	}

	return callErr
}

// check checks the text after the calls of an Apply return. The text must
// keep the limits of the Source of the user's text, as [Doc.limitProblem]
// describes. The text must decode, as [Doc.decodeProblem]
// describes, and go-yaml's parser must take it with and without its comments
// and read it as the edits do, as [Doc.parseProblem] and
// [Doc.misreadProblem] describe. Go-yaml must read it alike with and without
// its comments, as [Doc.commentsProblem] describes. It must hold the values
// of the model of the calls, as [Doc.checkModel] describes, and go-yaml's
// decoder alone must read those values too, as [Doc.ownDecodeProblem]
// describes. The text of start is the text that the Apply found. The parses
// of [Doc.parseFinal] run beside the checks that do not read them, the check
// of the model among them. The problem that check returns is the first in
// the order of this description.
func (d *Doc) check(ctx context.Context, start state) error {
	d.checking = true
	defer func() { d.checking = false }()

	parsed := d.parseFinal(start.cur)

	err := d.limitProblem(start.cur)

	var (
		read     any
		modelErr error
	)

	if err == nil {
		read, err = d.decodeProblem(ctx)
	}

	if err == nil {
		modelErr = d.checkModel()
	}

	final := parsed()
	if err != nil {
		return err
	}

	err = d.parseProblem(final)
	if err != nil {
		return err
	}

	err = d.misreadProblem(final)
	if err != nil {
		return err
	}

	err = d.commentsProblem(ctx, final, start)
	if err != nil {
		return err
	}

	if modelErr != nil {
		return modelErr
	}

	return d.ownDecodeProblem(ctx, final, read, start)
}

// finalParse holds go-yaml's parses of the current text that
// [Doc.parseFinal] makes.
type finalParse struct {
	// The problems of the parse without the comments of the text, as
	// yaml.Unmarshal parses it, and of the parse with them.
	plain, comments error
	// The parse without the comments, or nil when it fails, and the parse
	// with them, or nil when it fails. A text without comments has one
	// parse for both.
	file, commented *ast.File
	// The tokens of the one pass of go-yaml's lexer that both parses read.
	tokens token.Tokens
}

// parseFinal starts go-yaml's parses of the current text when it differs
// from start, the text before the calls. Each parse reads the text with its
// tail, as the parser reads the document in its file. One parse leaves out
// the comments of the text, as yaml.Unmarshal does, and one keeps them, as
// [parser.ParseComments] does. Both read the tokens of one pass of
// go-yaml's lexer, one parse after the other. The parser changes only the
// links between the tokens, which it does not read, so the second parse
// reads the tokens as the lexer made them. The two parses differ only in
// the comment tokens that they read, so a text without comments takes the
// result of the first parse for the second. The parses run beside the
// checks of [Doc.check]. The function that parseFinal returns waits for
// them. A panic of the lexer or the parser comes back as the problem of the
// parse, as [parseTokens] gives it. The parses take a key that the text
// repeats. The parse of the edits already reads the keys as the Source
// does, and a refusal of a repeated key does not depend on the comments.
func (d *Doc) parseFinal(start string) func() finalParse {
	if d.cur == start {
		return func() finalParse { return finalParse{} }
	}

	done := make(chan finalParse, 1)
	text := d.cur + d.tail

	go func() {
		var r finalParse

		defer func() {
			if p := recover(); p != nil {
				r.plain = &parserPanicError{value: p}
			}

			done <- r
		}()

		r.tokens = lexer.Tokenize(text)
		r.file, r.plain = parseTokens(r.tokens, 0)

		// The mode of the parser decides only whether it reads the comment
		// tokens, so it reads tokens without a comment the same in both.
		r.commented, r.comments = r.file, r.plain
		if slices.ContainsFunc(r.tokens, func(tk *token.Token) bool { return tk.Type == token.CommentType }) {
			r.commented, r.comments = parseTokens(r.tokens, parser.ParseComments)
		}
	}()

	return func() finalParse { return <-done }
}

// parseTokens returns go-yaml's parse of tks in mode, or the problem of
// that parse. A panic of the parser comes back as a [*parserPanicError].
func parseTokens(tks token.Tokens, mode parser.Mode) (*ast.File, error) {
	var (
		f   *ast.File
		err error
	)

	func() {
		defer func() {
			if p := recover(); p != nil {
				f, err = nil, &parserPanicError{value: p}
			}
		}()

		f, err = parser.Parse(tks, mode, parser.AllowDuplicateMapKey())
	}()

	return f, err //nolint:wrapcheck // The problem of the check names the parser.
}

// parserPanicError is the error of a panic of go-yaml's lexer or parser on
// the current text.
type parserPanicError struct {
	value any
}

// Error names the parser and the value of the panic.
func (e *parserPanicError) Error() string {
	return fmt.Sprintf("parser panicked: %v", e.value)
}

// decodeProblem returns the problem of the current text when it does not
// decode, or nil. The decode of the user's text can fail too, as on an
// alias of an anchor that the alias lies inside. The user's text then
// holds the problem. Otherwise go-yaml's decoder refuses the current text,
// or it panics on it. The problem names go-yaml's decoder, and it binds at
// the token where the decoder refused the text, under the path of that
// place in the user's text. Without such a token, it binds at the root.
// When go-yaml's parser drops text of the current text from its tree, the
// problem names that text too. Without a problem, decodeProblem returns
// the value that the decode reads.
func (d *Doc) decodeProblem(ctx context.Context) (any, error) {
	held := d.capture
	d.capture = &capture{}

	read, err := d.node.Decode[any](ctx, niceyaml.WithSelfValidation(false))

	placed := *d.capture
	d.capture = held

	if err == nil {
		return read, nil
	}

	userErr := d.userDecode(ctx)
	if userErr != nil {
		return nil, userErr
	}

	cause := decoderPanicked(err)
	if !holdsPanic(err) {
		cause = fmt.Errorf("%w: %w", errDecoderRefuses, err)
	}

	if bound, ok := errors.AsType[*niceyaml.SourceError](err); ok {
		if rng, ok := bound.Range(); ok {
			s := site{at: rng, writer: placed.writerAt(rng)}
			opts := []niceyaml.ErrorOption{niceyaml.AtRange(rng)}

			if path, _, ok := hook.SitePath(d.user, rng.Start, false); ok {
				s.path = path
				opts = append(opts, niceyaml.AtPath(path))
			}

			opts = append(opts, niceyaml.WithDetails(d.callDetails(s)...))
			problem := d.user.Bind(d.engineProblem(cause, opts...))
			d.record(problem)

			return nil, problem //nolint:wrapcheck // The binding is the problem of the edit.
		}
	}

	if refusal, ok := errors.AsType[yaml.Error](err); ok {
		if tk := refusal.GetToken(); tk != nil && tk.Position != nil {
			return nil, d.engineAtToken(tk, cause)
		}
	}

	return nil, d.bug(d.rootNode(), cause)
}

// ownDecodeProblem returns the problem of the current text when go-yaml's
// decoder alone, as yaml.Unmarshal decodes the text, refuses it or reads
// other values than read, the decode of the current text. That decode
// binds each alias as the resolver binds it, and go-yaml's decoder binds
// some aliases of reused anchor names otherwise. The two read one tree
// alike unless the decode reads a tree that differs from the text, as
// [hook.DecodeChanges] tells, so only such a text takes the decode alone.
// Go-yaml's comment-free parse, which yaml.Unmarshal reads, can still read
// another text apart from the parse of the edits, as [Doc.misreadProblem]
// finds it. The decoder parses the current text as yaml.Unmarshal does,
// and final, the parse of the current text without its comments, places a
// refusal.
// A refusal counts only when go-yaml's decoder alone takes start, the text
// that the Apply found, and other values count only when it reads start as
// the decode reads it, as [Doc.readsAlike] tells. Where the two read start
// apart, the edits follow the decode. The problem matches [ErrEngine] and
// names go-yaml's decoder.
func (d *Doc) ownDecodeProblem(ctx context.Context, final finalParse, read any, start state) error {
	if final.file == nil || !hook.DecodeChanges(d.node) {
		return nil
	}

	own, err := hook.OwnDecode(ctx, d.node, d.cur+d.tail, false)
	if err == nil && sameReading(own, read) {
		return nil
	}

	takes, alike := d.readsAlike(ctx, start)
	if err != nil {
		if takes {
			return d.ownRefusal(final, err)
		}

		return nil //nolint:nilerr // Go-yaml's decoder alone refuses the text that the Apply found too.
	}

	if !alike {
		return nil
	}

	a := d.apartAt(own, read)
	reads, meant := shownApart(a.own, a.read)
	cause := fmt.Errorf("go-yaml's decoder reads %s as %s when yaml.Unmarshal decodes the edited text, not %s",
		a.path, reads, meant)

	if a.alias == nil {
		return d.engineAtToken(astnode.StartToken(a.node), cause)
	}

	return d.engineAtToken(a.alias.Start, fmt.Errorf("%w, because %s", cause, apartRule(a)))
}

// readsAlike reports whether go-yaml's decoder alone, as yaml.Unmarshal
// decodes the text, takes start, the text that the Apply found, and whether
// it reads start as the decode of that text reads it. The text reads alike
// only when both decodes take it.
func (d *Doc) readsAlike(ctx context.Context, start state) (bool, bool) {
	own, err := hook.OwnDecode(ctx, start.node, start.cur+d.tail, false)
	if err != nil {
		return false, false
	}

	read, err := start.node.Decode[any](ctx, niceyaml.WithSelfValidation(false))

	return true, err == nil && sameReading(own, read)
}

// ownApart reports whether go-yaml's decoder alone, as yaml.Unmarshal
// decodes the text, refuses the current text or reads other values than
// the decode of the text, where [Doc.ownDecodeProblem] counts that after the
// last call. A refusal counts when the decoder alone takes the text that the
// Apply found, and other values count when it reads that text alike, as
// [Doc.readsAlike] tells.
func (d *Doc) ownApart(ctx context.Context) bool {
	if !hook.DecodeChanges(d.node) {
		return false
	}

	read, err := d.node.Decode[any](ctx, niceyaml.WithSelfValidation(false))
	if err != nil {
		return false
	}

	own, err := hook.OwnDecode(ctx, d.node, d.cur+d.tail, false)
	if err == nil && sameReading(own, read) {
		return false
	}

	start := d.callStart
	if d.model != nil {
		start = d.model.start
	}

	takes, alike := d.readsAlike(ctx, start)

	return takes && (err != nil || alike)
}

// fileBody returns the body of the first document of f that holds
// content, or nil.
func fileBody(f *ast.File) ast.Node {
	for _, doc := range f.Docs {
		if astnode.HasContent(doc.Body) {
			return doc.Body
		}
	}

	return nil
}

// sameReading reports whether a and b, two decodes into any, hold equal
// values, as [sameValue] compares them, with each integer compared by its
// value. Go-yaml's decoder reads an integer under an !!int tag as an int,
// and the decode of the text reads it as an integer without a tag.
func sameReading(a, b any) bool {
	return sameValue(byValue(a), byValue(b))
}

// byValue returns v, a decode into any, with each integer as an int64, or
// as a uint64 past the range of an int64.
func byValue(v any) any {
	switch x := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(x))
		for k, e := range x {
			out[k] = byValue(e)
		}

		return out

	case []any:
		out := make([]any, len(x))
		for i, e := range x {
			out[i] = byValue(e)
		}

		return out

	case int:
		return int64(x)

	case uint64:
		if x <= math.MaxInt64 {
			return int64(x)
		}
	}

	return v
}

// ownRefusal returns the problem of err, the refusal of go-yaml's decoder
// alone of the current text, whose parse without comments final holds. It
// binds at the token that the decoder names, where the parse of the edits
// holds its text, as [Doc.lexedAt] finds it. A refusal without a token,
// such as "exceeded max depth", binds at the first alias inside the value
// of the anchor that it reads, as [Doc.changedAlias] finds it.
func (d *Doc) ownRefusal(final finalParse, err error) error {
	msg := err.Error()
	tk := (*token.Token)(nil)

	if refusal, ok := errors.AsType[yaml.Error](err); ok {
		msg, tk = refusal.GetMessage(), refusal.GetToken()
	}

	if p, ok := errors.AsType[*niceyaml.PanicError](err); ok {
		msg = fmt.Sprintf("it panics: %v", p.Value)
	}

	cause := fmt.Errorf("%w: %s", errOwnRefuses, msg)

	if at, ok := d.lexedAt(final.tokens, tk); ok {
		return d.engineAtToken(at, cause)
	}

	if alias, ok := d.changedAlias(); ok {
		return d.engineAtToken(alias.Start, cause)
	}

	return d.bug(d.rootNode(), cause)
}

// changedAlias returns the first alias of the current text that lies
// inside the value of the anchor that it reads, which the decode reads as
// null.
func (d *Doc) changedAlias() (*ast.AliasNode, bool) {
	facts := aliasing.StateOf(d.node).Facts()

	for _, a := range facts.Aliases {
		if facts.Enclosed[a.Node] && a.Node.Start != nil && a.Node.Start.Position != nil {
			return a.Node, true
		}
	}

	return nil, false
}

// apart is the first place of the current text that go-yaml's decoder
// alone reads otherwise than the decode of the text, as [Doc.apartAt]
// finds it.
type apart struct {
	// The values of the place in the two decodes.
	own, read any
	// The node of the text where the walk to the place stopped.
	node ast.Node
	// The last alias on the way to the place, or nil, which the two decodes
	// bind apart.
	alias *ast.AliasNode
	// The path of the place.
	path paths.Path
	// Whether the way to the alias passes an entry that a `<<` merge key
	// brings in, and whether the alias is a key.
	merged, key bool
}

// apartAt returns the first place of the current text where own, go-yaml's
// decoder alone, reads otherwise than read, the decode of the text. It
// walks the text from the root as the decode reads it, through each alias
// to the anchor that the resolver binds it to, and through each `<<`
// merge key to the entry that the decode reads, as [Doc.entryRead]
// finds it. A mapping that the two read with other keys stops the
// walk. A key that only the decode reads names the alias in that key.
func (d *Doc) apartAt(own, read any) apart {
	res := d.node.Resolver()
	a := apart{own: own, read: read, node: d.node.AST(), path: paths.Doc()}

	for node, seen := a.node, map[ast.Node]bool{}; !astnode.IsNil(node) && !seen[node]; {
		seen[node] = true

		switch x := node.(type) {
		case *ast.AnchorNode:
			node = x.Value

			continue

		case *ast.TagNode:
			node = x.Value

			continue

		case *ast.AliasNode:
			target, err := res.Deref(x)
			a.alias, a.key = x, false

			if err != nil {
				return a
			}

			node = target

			continue
		}

		a.node = node

		next, ok := d.apartStep(&a, node)
		if !ok {
			return a
		}

		node = next
	}

	return a
}

// apartStep moves a from node, the content of its place, to the first
// value inside node that the two decodes of a read apart. It reports false
// when the walk stops at node.
func (d *Doc) apartStep(a *apart, node ast.Node) (ast.Node, bool) {
	switch own := a.own.(type) {
	case map[string]any:
		read, ok := a.read.(map[string]any)
		if !ok {
			return nil, false
		}

		return d.apartEntry(a, node, own, read)

	case []any:
		read, ok := a.read.([]any)
		seq, isSeq := node.(*ast.SequenceNode)

		if !ok || !isSeq || len(read) != len(own) || len(seq.Values) != len(own) {
			return nil, false
		}

		for i := range own {
			if !sameReading(own[i], read[i]) {
				a.path, a.own, a.read = a.path.Index(i), own[i], read[i]

				return seq.Values[i], true
			}
		}
	}

	return nil, false
}

// apartEntry moves a from node, a mapping, to the value of the first key
// that the two decodes of a read apart, own and read, as [Doc.apartAt]
// describes. It reports false when the walk stops at node.
func (d *Doc) apartEntry(a *apart, node ast.Node, own, read map[string]any) (ast.Node, bool) {
	keys := slices.Sorted(maps.Keys(read))

	for _, k := range keys {
		ov, inOwn := own[k]
		if inOwn && sameReading(ov, read[k]) {
			continue
		}

		e, merged, ok := d.entryRead(node, k)
		if !ok {
			return nil, false
		}

		a.merged = a.merged || merged

		// The decode reads the entry under the key, so an alias in the key
		// can explain why the two read it apart.
		if alias, ok := keyAlias(e); ok {
			a.alias, a.key = alias, true
		}

		if !inOwn {
			return nil, false
		}

		name, _ := d.node.Resolver().KeyName(e.Key)
		a.path, a.own, a.read = a.path.Child(name), ov, read[k]

		return e.Value, true
	}

	return nil, false
}

// keyAlias returns the alias that the key of e holds under its tags and
// anchors.
func keyAlias(e *ast.MappingValueNode) (*ast.AliasNode, bool) {
	key := ast.Node(e.Key)
	if explicit, ok := key.(*ast.MappingKeyNode); ok {
		key = explicit.Value
	}

	for {
		switch x := key.(type) {
		case *ast.TagNode:
			key = x.Value
		case *ast.AnchorNode:
			key = x.Value
		case *ast.AliasNode:
			return x, x != nil && x.Start != nil
		default:
			return nil, false
		}
	}
}

// entryRead returns the entry of node, a mapping, that the decode of the
// text reads for the key name, and whether a `<<` merge key brings it in.
// It asks the resolver of the text for a mapping that holds a merge key,
// as [paths.Resolver.Entry] reads it. It reports false when the decode
// reads no such entry.
func (d *Doc) entryRead(node ast.Node, name string) (*ast.MappingValueNode, bool, bool) {
	entries, _ := astnode.Entries(node)
	names := d.decodedKeys()

	var last *ast.MappingValueNode

	merges := false

	for _, e := range entries {
		if astnode.IsMergeKey(e.Key) {
			merges = true

			continue
		}

		if k, ok := names(e.Key); ok && k == name {
			last = e
		}
	}

	if !merges {
		return last, false, last != nil
	}

	found, err := d.node.Resolver().Entry(node, name)
	e, ok := found.(*ast.MappingValueNode)

	if err != nil || !ok {
		return nil, false, false
	}

	return e, !slices.Contains(entries, e), true
}

// apartRule returns the rule of go-yaml's decoder that a breaks, in words,
// for an apart whose alias is set. An alias key that a `<<` merge key
// brings in reads the anchor whose value holds it. At a merge key, the
// decoder looks up each alias of the mapping that the merge key brings in
// again.
func apartRule(a apart) string {
	name := aliasText(a.alias)

	switch {
	case a.key && a.merged:
		return fmt.Sprintf("it reads %s, an alias key inside the value of its anchor, "+
			"as that anchor where a merge key brings in the key", name)

	case a.merged:
		return fmt.Sprintf("it looks up %s again at the merge key that brings in its mapping", name)
	}

	return fmt.Sprintf("it binds %s to another anchor of its name", name)
}

// engineAtToken returns err as the problem of a bug in the engine, bound at
// the bytes of tk, a token of the current text, as [Doc.engineAtOffset]
// binds them. A token without bytes, such as the null of an entry without
// a value, binds at the byte where the text places it. The Apply records
// the problem.
func (d *Doc) engineAtToken(tk *token.Token, err error) error {
	s, ok := d.tokenSpan(tk)
	if !ok {
		off := d.offset(position.NewFromToken(tk))
		s = span{start: off, end: off}
	}

	return d.engineAtOffset(s.start, s.end-s.start, err)
}

// parseProblem returns the problem of the current text when go-yaml's
// parser refuses it, or panics on it, in one of the parses of final. The
// parse without comments comes first. The edits read a parse that keeps
// the comments, and that parse leaves out each comment that makes the
// parser refuse or misread valid YAML. The parser refuses some texts
// without their comments, such as one with an anchor without a value
// before a comment at its end. It refuses some texts with their comments,
// such as a flow collection at the root before a comment line. The problem
// matches [ErrEngine], and it names go-yaml's parser. It binds at the
// token where the parser refused the text, as [Doc.lexedAt] places it, or
// at the root for a panic.
func (d *Doc) parseProblem(final finalParse) error {
	refusals := []struct{ err, why error }{
		{final.plain, errRefusedWithoutComments},
		{final.comments, errRefusedWithComments},
	}

	for _, r := range refusals {
		if r.err == nil {
			continue
		}

		cause := fmt.Errorf("%w: %w", r.why, r.err)

		var tk *token.Token

		if refusal, ok := errors.AsType[yaml.Error](r.err); ok {
			cause, tk = fmt.Errorf("%w: %s", r.why, refusal.GetMessage()), refusal.GetToken()
		}

		if at, ok := d.lexedAt(final.tokens, tk); ok {
			return d.engineAtToken(at, cause)
		}

		return d.bug(d.rootNode(), cause)
	}

	return nil
}

// misreadProblem returns the problem of the current text when go-yaml's
// parse of final without the comments reads it otherwise than the parse of
// the edits does. That parse keeps the comments, as the Source does. The
// two read the text the same when their trees hold the same keys, values,
// tags, anchors, and aliases at the same places, so each alias reads the
// same anchor. Go-yaml misreads some texts without their comments, such as
// one where a comment line follows a mapping under a "!!null" tag and an
// anchor. The problem matches [ErrEngine], and it names go-yaml's parser.
// It binds at the first place of the text that the parses read otherwise,
// under the path of that place, which is the key of an entry that differs
// whole. For a value that the parse without the comments holds and the
// parse of the edits does not, that place is the collection around it.
func (d *Doc) misreadProblem(final finalParse) error {
	if final.file == nil {
		return nil
	}

	at, _, ok := misreadAt(d.node.AST(), fileBody(final.file))
	if !ok {
		return nil
	}

	tk := astnode.StartToken(at)
	if tk == nil {
		return d.bug(d.rootNode(), errMisread)
	}

	return d.engineAtToken(tk, errMisread)
}

// commentsProblem returns the problem of the current text when go-yaml
// reads it otherwise with its comments than without them, where it read
// the text of start, the text that the Apply found, alike. Go-yaml's
// decoder parses a text with its comments when a program asks for them,
// as with yaml.CommentToMap, and its parser misreads some comments. A
// comment line after an anchor without a value can make the anchor hold
// the lines after the comment, as in "r0: &x0", "# c", and "b: 1", where
// the parse with the comment reads b inside r0. Go-yaml reads a text
// alike when its decoder refuses either parse or reads one value from
// both. The problem matches [ErrEngine], and it names go-yaml's parser
// and the two readings. It binds at the first place of the text that the
// parses read otherwise, as [misreadAt] finds it in the parse without
// comments.
func (d *Doc) commentsProblem(ctx context.Context, final finalParse, start state) error {
	if final.file == nil || final.commented == nil || final.commented == final.file {
		return nil
	}

	plain, commented := fileBody(final.file), fileBody(final.commented)

	at, in, ok := misreadAt(plain, commented)
	if !ok || !d.readsApart(ctx, d.cur+d.tail) || d.readsApart(ctx, start.cur+d.tail) {
		return nil
	}

	cause := fmt.Errorf("%w, so go-yaml reads %s there with the comments and %s without them",
		errCommentsMisread, readingOf(in), readingOf(at))

	tk, ok := d.lexedAt(final.tokens, astnode.StartToken(at))
	if !ok {
		return d.bug(d.rootNode(), cause)
	}

	return d.engineAtToken(tk, cause)
}

// readsApart reports whether go-yaml's decoder alone reads text otherwise
// with its comments than without them, where it takes both parses.
func (d *Doc) readsApart(ctx context.Context, text string) bool {
	plain, err := hook.OwnDecode(ctx, d.node, text, false)
	if err != nil {
		return false
	}

	commented, err := hook.OwnDecode(ctx, d.node, text, true)

	return err == nil && !sameReading(plain, commented)
}

// readingOf returns what go-yaml reads for node, a node of one of its
// parses of the current text, as a message shows it. The decoder reads node
// alone, so an alias in node does not find its anchor. The lines of the
// parse stand for node where that decode fails, and "nothing" stands for a
// nil node, which the parse lacks.
func readingOf(node ast.Node) string {
	if astnode.IsNil(node) {
		return "nothing"
	}

	var v any

	err := astnode.NodeToValue(node, &v)
	if err != nil {
		return fmt.Sprintf("the lines %q", strings.TrimSpace(node.String()))
	}

	return shownCut(v)
}

// misreadAt returns the first node of a, in the order of the text, that b
// reads otherwise, with the node of b in its place, or nil when b lacks
// it. The tree a is the parse of the edits, or go-yaml's parse without
// comments, and b is a parse of the same text without comments or with
// them. An entry whose key reads otherwise differs as a whole. A node of b
// that a lacks differs at the collection around it in a. It reports false
// when b reads each node of a as a does. A comment does not take part, and
// neither do the styles and the places of the nodes.
func misreadAt(a, b ast.Node) (ast.Node, ast.Node, bool) {
	if !astnode.HasContent(a) || !astnode.HasContent(b) {
		return a, b, astnode.HasContent(a) != astnode.HasContent(b)
	}

	if ea, ok := astnode.Entries(a); ok && !isProps(a) {
		eb, ok := astnode.Entries(b)
		if !ok || isProps(b) {
			return a, b, true
		}

		for i, e := range ea {
			if i == len(eb) {
				return e, nil, true
			}

			// An entry whose key reads otherwise differs whole.
			if _, _, ok := misreadAt(e.Key, eb[i].Key); ok {
				return e, eb[i], true
			}

			if at, in, ok := misreadAt(e.Value, eb[i].Value); ok {
				return at, in, true
			}
		}

		return a, b, len(eb) > len(ea)
	}

	if reflect.TypeOf(a) != reflect.TypeOf(b) {
		return a, b, true
	}

	switch x := a.(type) {
	case *ast.SequenceNode:
		y, _ := b.(*ast.SequenceNode) //nolint:errcheck // The types are equal.

		for i, v := range x.Values {
			if i == len(y.Values) {
				return v, nil, true
			}

			if at, in, ok := misreadAt(v, y.Values[i]); ok {
				return at, in, true
			}
		}

		return a, b, len(y.Values) > len(x.Values)

	case *ast.AnchorNode:
		y, _ := b.(*ast.AnchorNode) //nolint:errcheck // The types are equal.

		return misreadProps(a, b, x.Name, y.Name, x.Value, y.Value)

	case *ast.TagNode:
		y, _ := b.(*ast.TagNode) //nolint:errcheck // The types are equal.

		return misreadProps(a, b, x, y, x.Value, y.Value)

	case *ast.AliasNode:
		y, _ := b.(*ast.AliasNode) //nolint:errcheck // The types are equal.

		return misreadProps(a, b, x.Value, y.Value, nil, nil)

	case *ast.MappingKeyNode:
		y, _ := b.(*ast.MappingKeyNode) //nolint:errcheck // The types are equal.

		return misreadAt(x.Value, y.Value)

	case *ast.LiteralNode:
		// The value of the node is its text, header and comment included,
		// and the string that it holds is the value of its content.
		y, _ := b.(*ast.LiteralNode) //nolint:errcheck // The types are equal.
		if x.Value == nil || y.Value == nil {
			return a, b, x.Value != y.Value
		}

		return a, b, x.Value.Value != y.Value.Value

	case ast.ScalarNode:
		y, _ := b.(ast.ScalarNode) //nolint:errcheck // The types are equal.

		return a, b, !sameValue(x.GetValue(), y.GetValue())

	default:
		return a, b, true
	}
}

// misreadProps compares a, an anchor, a tag, or an alias, with b, its node
// in the other parse. It returns a and b when na, the name of a, differs
// from nb, the name of b. Otherwise it returns the first node of va, the
// value of a, that vb reads otherwise, as [misreadAt] does. An anchor and
// an alias take the name of the anchor, and a tag takes its text.
func misreadProps(a, b, na, nb, va, vb ast.Node) (ast.Node, ast.Node, bool) {
	x, _ := astnode.Name(na)
	y, _ := astnode.Name(nb)

	if x != y {
		return a, b, true
	}

	return misreadAt(va, vb)
}

// lexedAt returns the token of the parse of the edits that holds the text
// of tk. Go-yaml's parser names tk in a refusal of tks, the tokens of a
// pass of go-yaml's lexer over the current text. The lexer misplaces some
// tokens, as after a comment in a text with CRLF line breaks, and the
// tokens of the parse of the edits sit where the text holds them. That
// parse reads the tokens of another pass of the lexer over the same text,
// so its tokens come in the same order. The token there with the type and
// the value of tk, after as many such tokens as tks holds before tk, thus
// holds its text. A token that the parser made, such as the null of an
// empty value, has the place of the last token of tks that starts at or
// before it. It reports false when it does not find such a token.
func (d *Doc) lexedAt(tks token.Tokens, tk *token.Token) (*token.Token, bool) {
	if tk == nil || tk.Position == nil {
		return nil, false
	}

	i := slices.Index(tks, tk)
	for j := len(tks) - 1; i < 0 && j >= 0; j-- {
		if tks[j].Position != nil && tks[j].Position.Offset <= tk.Position.Offset {
			i = j
		}
	}

	if i < 0 {
		return nil, false
	}

	same := func(t *token.Token) bool { return t.Type == tks[i].Type && t.Value == tks[i].Value }
	n := 0

	for _, t := range tks[:i] {
		if same(t) {
			n++
		}
	}

	for _, t := range d.tokens() {
		if !same(t) {
			continue
		}

		if n == 0 {
			return t, t.Position != nil
		}

		n--
	}

	return nil, false
}

// limitProblem returns the problem of the current text when it differs
// from start, the text before the calls, and breaks a limit of the Source
// of the user's text. The edits read the text through a Source without the
// limits, so the check reads the limits after the last call. The byte
// limit counts the bytes of the whole file, with the current text in place
// of the document. Its problem is a problem of the whole file, so it binds
// where a problem at the path $ binds. The depth limit counts the levels of
// the document. Its problem binds at the token that opens the first level
// past the limit, at the site that [Doc.bindAt] gives the token. Each
// problem has the message of the Source, and it matches [niceyaml.ErrLimit].
// The user's text keeps the limits, so [niceyaml.IsInvalid] does not report
// the problem. The Apply records it.
func (d *Doc) limitProblem(start string) error {
	if d.cur == start {
		return nil
	}

	src := d.user.Source()
	bytes, depth := hook.Limits(src)

	var problem error

	if size := len(src.Text()) - (d.end - d.start) + len(d.cur); bytes > 0 && size > bytes {
		problem = d.bindOwn(niceyaml.Unchecked(&limitError{msg: "file is larger than " + quantity.Bytes(bytes)},
			niceyaml.AtPath(paths.Doc())))
	} else if tk, deep := docparse.TooDeep(d.src.Tokens(), depth); deep && tk.Position != nil {
		msg := (&docparse.DepthError{Token: tk, Limit: depth}).Error()
		problem = d.bindAt(d.offset(position.NewFromToken(tk)), 1, func(opts ...niceyaml.ErrorOption) error {
			return niceyaml.Unchecked(&limitError{msg: msg}, opts...)
		})
	}

	if problem != nil {
		d.record(problem)
	}

	return problem
}

// printableProblem returns the problem of the current text when it differs
// from start, the text before a call, and holds a character that a call
// wrote and that YAML does not allow in a text, as [printable] tells. A
// character of the user's text that a call kept at its place or moved does
// not count. Go-yaml's encoder writes control characters such as U+0001 as
// they are, so the problem names the encoder and the character. It matches
// [ErrEngine], and it binds at the character.
func (d *Doc) printableProblem(start string) error {
	if d.cur == start {
		return nil
	}

	for off := 0; off < len(d.cur); {
		_, size := utf8.DecodeRuneInString(d.cur[off:])

		if what, ok := unprintableAt(d.cur, off); ok && !d.userText(off, size) {
			return d.engineAtOffset(off, size, unprintableError(what))
		}

		off += size
	}

	return nil
}

// unprintableError returns the reason of a text that holds what, the words
// that name a character that YAML does not allow in a text, as
// [unprintableAt] names it. Go-yaml's encoder writes such a character as it
// is.
func unprintableError(what string) error {
	return fmt.Errorf("go-yaml's encoder writes %s as it is, and YAML does not allow that in a text", what)
}

// unprintableIn returns the words that name the first character of text
// that YAML does not allow in a text, as [unprintableAt] names it.
func unprintableIn(text string) (string, bool) {
	for off := 0; off < len(text); {
		if what, ok := unprintableAt(text, off); ok {
			return what, true
		}

		_, size := utf8.DecodeRuneInString(text[off:])
		off += size
	}

	return "", false
}

// unprintableAt returns the words that name the character of text at off,
// as in "U+0001", when YAML does not allow it in a text, as [printable]
// tells. A byte that is not UTF-8 counts too.
func unprintableAt(text string, off int) (string, bool) {
	r, size := utf8.DecodeRuneInString(text[off:])
	utf := r != utf8.RuneError || size > 1

	switch {
	case !utf:
		return fmt.Sprintf("the byte %#x, which is not UTF-8,", text[off]), true
	case !printable(r):
		return fmt.Sprintf("%U", r), true
	}

	return "", false
}

// userText reports whether the size bytes of the current text at off hold
// the bytes of the user's text that their origins give.
func (d *Doc) userText(off, size int) bool {
	o := d.origin[off]
	if o < 0 || o+size > len(d.orig) || d.orig[o:o+size] != d.cur[off:off+size] {
		return false
	}

	for i := range size {
		if d.origin[off+i] != o+i {
			return false
		}
	}

	return true
}

// printable reports whether r is a character that YAML allows in a text,
// the c-printable set of YAML 1.2.
func printable(r rune) bool {
	switch {
	case r == '\t', r == '\n', r == '\r', r == 0x85:
		return true
	case r < 0x20, r >= 0x7f && r < 0xa0:
		return false
	default:
		return r <= 0xd7ff || r >= 0xe000 && r <= 0xfffd || r >= 0x10000 && r <= utf8.MaxRune
	}
}

// engineAtOffset returns err as the problem of a bug in the engine, bound
// at the size bytes of the current text at off, under the path of that
// place. The Apply records the problem.
func (d *Doc) engineAtOffset(off, size int, err error) error {
	problem := d.bindAt(off, size, func(opts ...niceyaml.ErrorOption) error {
		return d.engineProblem(err, opts...)
	})
	d.record(problem)

	return problem
}

// bindAt returns the problem that problem makes with the options of the
// site of the size bytes of the current text at off, as [Doc.bindSite]
// binds it. That is the site that [Doc.site] gives the bytes. A key binds
// as [Doc.keyPlace] gives its site, where [niceyaml.Node.PathAt] reads the
// bytes as a part of a key.
func (d *Doc) bindAt(off, size int, problem func(opts ...niceyaml.ErrorOption) error) error {
	s := span{start: off, end: off + max(size, 1)}
	at := d.site(s, false)

	if path, ok := d.node.PathAt(positionIn(d.cur, d.curLines, off)); ok {
		if last, _ := path.Last(); last.Kind == paths.SelectorKey {
			at = d.keyPlace(s)
		}
	}

	return d.bindSite(at, problem)
}

// bindSite returns the problem that problem makes with the options of s, a
// site of the user's text. The problem binds at s under the path of s,
// with the details that name calls, as [Doc.callDetails] gives them for
// s.
func (d *Doc) bindSite(s site, problem func(opts ...niceyaml.ErrorOption) error) error {
	bound := d.user.Bind(problem(
		niceyaml.AtPath(s.path),
		niceyaml.AtRange(s.at),
		niceyaml.WithDetails(d.callDetails(s)...),
	))

	return bound //nolint:wrapcheck // The binding is the problem of the edit.
}

// limitError is the problem of a text past a limit of its Source. It
// matches [niceyaml.ErrLimit].
type limitError struct {
	msg string
}

// Error returns the message of e.
func (e *limitError) Error() string {
	return e.msg
}

// Unwrap returns [niceyaml.ErrLimit].
func (e *limitError) Unwrap() error {
	return niceyaml.ErrLimit
}

// The reasons of the problems of [Doc.parseProblem], of [Doc.parse], and
// of [Doc.decodeProblem].
var (
	errRefusedWithoutComments = errors.New("go-yaml's parser refuses the edited text without its comments")
	errRefusedWithComments    = errors.New("go-yaml's parser refuses the edited text with its comments")
	errParserRefuses          = errors.New("go-yaml's parser refuses the edited text")
	errParserPanicked         = errors.New("go-yaml's parser panicked on the edited text")
	errDecoderRefuses         = errors.New("go-yaml's decoder refuses the edited text")
	errOwnRefuses             = errors.New("go-yaml's decoder refuses the edited text when yaml.Unmarshal decodes it")
	errMisread                = errors.New("go-yaml's parser reads the edited text otherwise without its comments")
	errCommentsMisread        = errors.New("go-yaml's parser reads the edited text otherwise with its comments")
)

// userDecode returns the error of a decode of the user's text, or nil when
// that text decodes. The error binds in the user's text, and the Apply
// records it. The user's text is not at fault for a panic of the decoder
// of go-yaml, so the problem of such a panic matches [ErrEngine]. Either
// takes the details that name calls at its site, as [Doc.callDetails]
// gives them.
func (d *Doc) userDecode(ctx context.Context) error {
	_, err := d.user.Decode[any](ctx, niceyaml.WithSelfValidation(false),
		niceyaml.WithUnmarshaler(d.cfg.unmarshalers...))
	if err == nil {
		return nil
	}

	var details []error

	if rng, ok := boundRange(err); ok {
		bound, _ := errors.AsType[*niceyaml.SourceError](err)
		path, _ := bound.Path()
		details = d.callDetails(site{path: path, at: rng})
	}

	opts := append(boundAt(err), niceyaml.WithDetails(details...))
	problem := err

	switch {
	case holdsPanic(err):
		problem = d.user.Bind(d.engineProblem(decoderPanicked(err), opts...))
	case len(details) > 0:
		problem = d.user.Bind(niceyaml.Locate(err, opts...))
	}

	d.record(problem)

	return problem //nolint:wrapcheck // The error binds in the user's text.
}

// holdsPanic reports whether err holds a panic that a decode of niceyaml
// recovered.
func holdsPanic(err error) bool {
	p, ok := errors.AsType[*niceyaml.PanicError](err)

	return ok && p != nil
}

// decoderPanicked returns err, the error of a decode, with a message that
// names the decoder of go-yaml when err holds a panic of the decode.
func decoderPanicked(err error) error {
	if p, ok := errors.AsType[*niceyaml.PanicError](err); ok {
		return &decodePanicError{value: p.Value, err: err}
	}

	return err
}

// decodePanicError is the error of a decode on which the decoder of go-yaml
// panicked.
type decodePanicError struct {
	value any
	err   error
}

// Error names the decoder of go-yaml and the value of the panic.
func (e *decodePanicError) Error() string {
	return fmt.Sprintf("go-yaml's decoder panicked: %v", e.value)
}

// Unwrap returns the error of the decode.
func (e *decodePanicError) Unwrap() error {
	return e.err
}

// boundAt returns the options that bind a problem at the path and the
// range of err, a bound error of a decode, or none.
func boundAt(err error) []niceyaml.ErrorOption {
	rng, ok := boundRange(err)
	if !ok {
		return nil
	}

	bound, _ := errors.AsType[*niceyaml.SourceError](err)
	path, _ := bound.Path()

	return []niceyaml.ErrorOption{niceyaml.AtPath(path), niceyaml.AtRange(rng)}
}

// boundRange returns the range of err, a bound error of a decode. It
// reports false for an error without one.
func boundRange(err error) (position.Range, bool) {
	bound, ok := errors.AsType[*niceyaml.SourceError](err)
	if !ok {
		return position.Range{}, false
	}

	return bound.Range()
}

// sameValue reports whether a and b, two values of a decode into any, are
// deeply equal. Unlike [reflect.DeepEqual], it takes a NaN as equal to a
// NaN, because a text that writes .nan reads back as a NaN. It takes -0 apart
// from 0, because a decode names them apart as keys.
func sameValue(a, b any) bool {
	return sameReflected(reflect.ValueOf(a), reflect.ValueOf(b))
}

// sameReflected reports whether a and b are deeply equal, with a NaN equal
// to a NaN.
func sameReflected(a, b reflect.Value) bool {
	if !a.IsValid() || !b.IsValid() {
		return a.IsValid() == b.IsValid()
	}

	if a.Type() != b.Type() {
		return false
	}

	switch a.Kind() {
	case reflect.Float32, reflect.Float64:
		x, y := a.Float(), b.Float()

		return x == y && math.Signbit(x) == math.Signbit(y) || math.IsNaN(x) && math.IsNaN(y)

	case reflect.Interface, reflect.Pointer:
		if a.IsNil() || b.IsNil() {
			return a.IsNil() == b.IsNil()
		}

		return sameReflected(a.Elem(), b.Elem())

	case reflect.Slice, reflect.Array:
		if a.Kind() == reflect.Slice && a.IsNil() != b.IsNil() || a.Len() != b.Len() {
			return false
		}

		for i := range a.Len() {
			if !sameReflected(a.Index(i), b.Index(i)) {
				return false
			}
		}

		return true

	case reflect.Map:
		if a.IsNil() != b.IsNil() || a.Len() != b.Len() {
			return false
		}

		for it := a.MapRange(); it.Next(); {
			v := b.MapIndex(it.Key())
			if !v.IsValid() || !sameReflected(it.Value(), v) {
				return false
			}
		}

		return true

	default:
		return reflect.DeepEqual(a.Interface(), b.Interface())
	}
}

// liesIn reports whether node, a node of the current text, lies inside
// outer.
func (d *Doc) liesIn(node, outer ast.Node) bool {
	t := d.tree()

	for n, ok := t.parent[node]; ok; n, ok = t.parent[n] {
		if n == outer {
			return true
		}
	}

	return false
}

// rebased returns path with the path that to gives for its first k
// selectors in place of them.
func rebased(path paths.Path, k int, to func(head paths.Path) paths.Path) paths.Path {
	sels := slices.Collect(path.Selectors())

	return appendSelectors(to(prefixOf(path, k)), sels[k:])
}

// shown returns v, a value of a decode into any, as a message shows it on
// one line, in the flow style of YAML. A null shows as null, and each
// string comes in double quotes, so its line breaks show as "\n". So does
// each string in a mapping or a sequence, the keys included. A mapping
// shows its entries in the order of their keys.
func shown(v any) string {
	var b strings.Builder

	writeShown(&b, reflect.ValueOf(v))

	return b.String()
}

// shownCut returns v as [shown] shows it, with "..." in place of the bytes
// past maxShown.
func shownCut(v any) string {
	return cut(shown(v))
}

// shownApart returns a and b, two values that a message names as apart,
// as [shownCut] shows them. Where the two show alike, as the integer 5 and
// the float 5 do, each scalar takes the word for its type.
func shownApart(a, b any) (string, string) {
	x, y := shownCut(a), shownCut(b)
	if x == y {
		x, y = typeWord(a)+x, typeWord(b)+y
	}

	return x, y
}

// typeWord returns the words for the type of v, a scalar of a decode into
// any, that come before it in a message, as in "the float 5", or nothing
// for a collection.
func typeWord(v any) string {
	switch reflect.ValueOf(v).Kind() {
	case reflect.Invalid:
		return "the null "
	case reflect.Bool:
		return "the boolean "
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return "the integer "
	case reflect.Float32, reflect.Float64:
		return "the float "
	case reflect.String:
		return "the string "
	default:
		return ""
	}
}

// writeShown writes v to b as [shown] shows it.
func writeShown(b *strings.Builder, v reflect.Value) {
	switch v.Kind() {
	case reflect.Invalid:
		b.WriteString("null")

	case reflect.Interface, reflect.Pointer:
		if v.IsNil() {
			b.WriteString("null")

			return
		}

		writeShown(b, v.Elem())

	case reflect.String:
		b.WriteString(strconv.Quote(v.String()))

	case reflect.Float32, reflect.Float64:
		switch f := v.Float(); {
		case math.IsNaN(f):
			b.WriteString(".nan")
		case math.IsInf(f, 1):
			b.WriteString(".inf")
		case math.IsInf(f, -1):
			b.WriteString("-.inf")
		default:
			b.WriteString(strconv.FormatFloat(f, 'g', -1, 64))
		}

	case reflect.Map:
		keys := v.MapKeys()
		slices.SortFunc(keys, func(x, y reflect.Value) int {
			return strings.Compare(shown(x.Interface()), shown(y.Interface()))
		})

		b.WriteByte('{')

		for i, k := range keys {
			if i > 0 {
				b.WriteString(", ")
			}

			writeShown(b, k)
			b.WriteString(": ")
			writeShown(b, v.MapIndex(k))
		}

		b.WriteByte('}')

	case reflect.Slice, reflect.Array:
		// Go-yaml's decoder reads a !!binary scalar as bytes.
		if bytes, ok := reflect.TypeAssert[[]byte](v); ok {
			b.WriteString("!!binary " + base64.StdEncoding.EncodeToString(bytes))

			return
		}

		b.WriteByte('[')

		for i := range v.Len() {
			if i > 0 {
				b.WriteString(", ")
			}

			writeShown(b, v.Index(i))
		}

		b.WriteByte(']')

	default:
		// Go-yaml's decoder reads a !!timestamp scalar as a time.
		if t, ok := reflect.TypeAssert[time.Time](v); ok {
			b.WriteString(t.Format(time.RFC3339Nano))

			return
		}

		fmt.Fprint(b, v.Interface())
	}
}

// rootNode returns a node on the root, which binds a problem of the whole
// text.
func (d *Doc) rootNode() *Node {
	return &Node{d: d, path: paths.Doc()}
}

// placeRange returns the site where an error at p, the place of n, binds,
// as [Doc.placeOfSite] gives it, under the path that the user's text spells
// there. The error binds at the value of a scalar, and at the key or the
// "-" of a collection. An empty value has no token, so an error there binds
// at the place of the value. The path is that of the entry or the item of
// the key or the "-", or $ for the root.
func (d *Doc) placeRange(n *Node, p *place) site {
	s := d.placeOfSite(p)

	if n.path.IsRoot() {
		s.path, s.writer = paths.Doc(), nil
	} else {
		s.path = d.holderSite(p).path
	}

	return s
}

// placeOfSite returns the site where an error at p, a place of the current
// text, binds. That is the site of its value, as [Doc.valueSite] gives it,
// or the site that [Doc.tokenlessSite] gives for a value without a token.
// The root has no key or "-", so an error at the root binds at its first
// key or item, under the path $.
func (d *Doc) placeOfSite(p *place) site {
	s, ok := d.valueSite(p)
	if !ok {
		s, _ = d.tokenlessSite(p.path)
	}

	if p.root {
		s.path, s.writer = paths.Doc(), nil
	}

	return s
}

// holderSite returns the site of the key or the "-" of p, an entry or an
// item, as [Doc.site] gives it, at the token that [Doc.holderToken] gives.
func (d *Doc) holderSite(p *place) site {
	start := d.holderToken(p)

	end := start + 1
	if ks, _, ok := d.keySpan(p); ok {
		end = ks.end
	}

	// A mapping that the user wrote without braces in a flow sequence
	// starts at its first key, and the site of the item names the item.
	bare := p.seq != nil && p.seq.IsFlowStyle && isBareMapping(astnode.Content(p.value))

	s := d.site(span{start: start, end: end}, bare)
	s.inKey = s.inKey && !bare

	return s
}

// holderToken returns the offset of the key or the "-" of p, an entry or
// an item. That token is the text of the key without its anchors, its tags,
// and the "?" of an explicit key, the "-" of an item of a block sequence,
// or the first byte of an item of a flow sequence.
func (d *Doc) holderToken(p *place) int {
	if ks, _, ok := d.keySpan(p); ok {
		return ks.start
	}

	if start, ok := d.startOf(p); ok {
		return start
	}

	return d.valueStart(p)
}

// aliasText returns the text of the alias that node, a value or a key as the
// text writes it, holds under its tags and its anchors, such as "*x".
func aliasText(node ast.Node) string {
	alias := node

	for unwrapped := false; !unwrapped; {
		switch x := alias.(type) {
		case *ast.TagNode:
			alias = x.Value
		case *ast.AnchorNode:
			alias = x.Value
		default:
			unwrapped = true
		}
	}

	name, _ := astnode.Name(alias)
	if a, ok := alias.(*ast.AliasNode); ok {
		name, _ = astnode.Name(a.Value)
	}

	return "*" + name
}

// checkModel returns the problem of the text after the last call when it
// does not hold the values of the model. The problem binds where the text
// differs from the model first, and it names the call after which the text
// first differed there. It shows a bug in the engine or a misread of
// go-yaml, so the problem matches [ErrEngine].
func (d *Doc) checkModel() error {
	x := d.model
	if x == nil {
		return nil
	}

	if x.synced != d.node {
		d.alignModel(x)
	}

	if len(x.diffs) == 0 {
		return nil
	}

	return d.diffProblem(x, d.firstDiff(x))
}

// diffProblem returns the problem of df, a place where the current text
// differs from x, as [Doc.checkModel] describes it. The message names the
// call after which the text first differed there. The model of the calls
// cannot hold the values of a user's text that does not decode. The
// problem of such a text is the error of its decode, as a read gives it.
//
//nolint:contextcheck // The decode of the user's text reads no state that a context carries.
func (d *Doc) diffProblem(x *model, df *diff) error {
	err := d.userDecode(context.Background())
	if err != nil {
		return err
	}

	node, path := d.diffPlace(df)
	msg := d.describeDiff(x, df, path)

	if call := x.since[df.id()]; call > 0 && call <= len(x.calls) {
		msg += fmt.Sprintf(", after %s %s", d.subject(), x.calls[call-1].named)
	}

	problem := errors.New(msg)

	// An extra or a missing entry binds at its key: in the current text for
	// an extra entry, and in the text that the Apply found for a missing
	// one. An extra entry whose key a call wrote binds at the mapping that
	// holds it, as a missing entry that a call created does.
	at := path

	switch df.kind {
	case diffExtra:
		if s, ok := d.nodeSpan(df.e.entry.Key); ok && d.userWrote(s.start) {
			node, at = df.e.entry.Key, path.Child(df.e.pathName())
		}

	case diffMissing:
		at = path.Child(df.e.pathName())
	default:
	}

	var bound error

	v, err := d.node.At(at)
	s, ok := d.nodeSpan(node)

	switch key, held := d.startKey(x, df); {
	case held:
		bound = d.bindSite(key, func(opts ...niceyaml.ErrorOption) error { return d.engineProblem(problem, opts...) })

	case ok && !at.IsRoot() && (err != nil || astnode.Content(v.AST()) != astnode.Content(node)):
		// The path selects another value, as when a later `<<` merge key
		// hides the entry, so the problem binds at the node. So does an
		// extra entry, at its key.
		bound = d.bindOwn(d.engineProblem(problem, niceyaml.AtPath(at), niceyaml.AtRange(
			position.NewRange(positionIn(d.cur, d.curLines, s.start), positionIn(d.cur, d.curLines, s.end)),
		)))

	default:
		bound = d.diffAt(at, problem)
	}

	d.record(bound)

	return bound //nolint:wrapcheck // The binding is the problem of the edit.
}

// diffAt returns problem, the problem of a difference at path, bound in
// the text that the call running found, where an error at the value of
// path binds there. It does so when that text holds the value at path with
// the same first byte, a value that the call left in its place. Go-yaml can
// read such a value as a value of another kind after the call, as it reads
// a tag alone before a key that the call writes. The site of a value
// follows its kind, so the problem binds where the user's text holds the
// value. Any other problem, and a problem of the checks after the last
// call, binds as [Doc.engineAt] binds it.
func (d *Doc) diffAt(path paths.Path, problem error) error {
	if d.calling && !path.IsRoot() {
		now := d.save()
		first, ok := d.valueOrigin(path)

		d.show(d.callStart)

		if found, held := d.valueOrigin(path); ok && held && found == first {
			if bound, ok := d.engineIn(path, problem); ok {
				d.show(now)

				return bound
			}
		}

		d.show(now)
	}

	return d.engineAt(path, problem)
}

// valueOrigin returns the origin of the first byte of the value at path in
// the current text, with its anchors and tags, as [Doc.firstOrigin] gives
// it. It reports false when the text does not hold path or the value has no
// bytes.
func (d *Doc) valueOrigin(path paths.Path) (int, bool) {
	p, err := d.resolve(path)
	if err != nil || !p.exists {
		return 0, false
	}

	o := d.firstOrigin(p.value)

	return o, o != math.MinInt
}

// startKey returns the site of the key of the entry that df finds missing,
// as [Doc.entrySite] gives it in the text that the Apply found. It reports
// false for any other difference, and for an entry that a call created.
func (d *Doc) startKey(x *model, df *diff) (site, bool) {
	e := df.e
	if df.kind != diffMissing || e == nil || e.entry == nil {
		return site{}, false
	}

	// The key lies in the text that the Apply found, so its offsets read
	// that text.
	now := d.save()
	d.show(x.start)

	defer d.show(now)

	if _, ok := d.nodeSpan(e.entry.Key); !ok {
		return site{}, false
	}

	return d.entrySite(e.entry), true
}

// firstDiff returns the first difference of the current text from x: of
// the differences that the earliest call made, the first in the text.
func (d *Doc) firstDiff(x *model) *diff {
	first, call, at := x.diffs[0], x.since[x.diffs[0].id()], d.diffOffset(x.diffs[0])

	for _, df := range x.diffs[1:] {
		c, off := x.since[df.id()], d.diffOffset(df)
		if c < call || c == call && off < at {
			first, call, at = df, c, off
		}
	}

	return first
}

// diffOffset returns the offset in the current text where df lies, or
// the length of the text when df does not lie at a node of the text. An
// extra entry lies at its key, and an alias at the alias. A missing entry,
// and a merge key that the text lacks, lie at the end of the mapping.
func (d *Doc) diffOffset(df *diff) int {
	nodes := []ast.Node{df.t.node, d.edgeNode(df.at)}

	switch df.kind {
	case diffExtra:
		nodes = []ast.Node{d.edgeNode(df.e)}
	case diffAlias:
		nodes = []ast.Node{d.edgeNode(df.at)}
	case diffValue, diffTag, diffMissing, diffItems, diffMerges, diffShared:
	}

	for _, n := range nodes {
		if astnode.IsNil(n) {
			continue
		}

		s, ok := d.nodeSpan(n)

		switch {
		case !ok:
		case df.kind == diffMissing || df.kind == diffMerges:
			return s.end
		default:
			return s.start
		}
	}

	return len(d.cur)
}

// edgeNode returns the node of the text that e, an edge of the text,
// holds, or nil.
func (d *Doc) edgeNode(e *edge) ast.Node {
	switch {
	case e == nil:
		return nil
	case e.entry != nil:
		return e.entry
	default:
		return e.at
	}
}

// diffPlace returns the node of the current text where df lies and the
// path that names its place. The path selects the value by names and
// indexes, with a `<<` merge key as a name. An extra or a missing entry
// lies at its mapping, and an alias that reads another value lies at the
// alias. When no such path selects the value, as for a value in a key,
// the path is that of a value that holds it. The node is nil when df does
// not lie in the current text.
func (d *Doc) diffPlace(df *diff) (ast.Node, paths.Path) {
	var at ast.Node

	if df.at != nil {
		at = df.at.at
	}

	nodes := []ast.Node{df.t.node, at}
	if df.kind == diffAlias {
		nodes = []ast.Node{at}
	}

	for _, n := range nodes {
		if path, ok := d.valuePath(n); ok {
			return n, path
		}
	}

	return nil, paths.Doc()
}

// valuePath returns the path that names the place of n, a value of the
// current text, as [Doc.diffPlace] describes it.
func (d *Doc) valuePath(n ast.Node) (paths.Path, bool) {
	if astnode.IsNil(n) {
		return paths.Path{}, false
	}

	path, ok := d.pathIn(d.node.AST(), n)
	if !ok {
		return paths.Path{}, false
	}

	return paths.Doc().Join(path), true
}

// describeDiff returns what go-yaml reads at path, the place of df, in the
// current text, where x holds another value. Go-yaml's parser and decoder
// read the text, so the message names go-yaml.
func (d *Doc) describeDiff(x *model, df *diff, path paths.Path) string {
	switch df.kind {
	case diffMissing:
		return fmt.Sprintf("go-yaml reads no value at %s", path.Child(df.e.pathName()))

	case diffExtra:
		return fmt.Sprintf("go-yaml reads %s as %s, where the edits leave no value",
			path.Child(df.e.pathName()), shownValue(df.e.val))

	case diffItems:
		return fmt.Sprintf("go-yaml reads %s with %s, not %s",
			path, countItems(len(df.t.edges)), countItems(len(df.m.edges)))

	case diffMerges:
		return fmt.Sprintf("go-yaml reads %s with %s, not %s",
			path, countMerges(len(mergesOf(df.t))), countMerges(len(mergesOf(df.m))))

	case diffShared:
		// The value of the model pairs with the text where the text writes
		// it, unless the alignment did not reach that place.
		if own := df.m.own; own != nil && own.seen == x.epoch && own.mate != nil {
			if shared, ok := d.valuePath(own.mate.val.node); ok {
				return fmt.Sprintf("go-yaml reads a copy at %s in place of an alias of %s", path, shared)
			}
		}

		return fmt.Sprintf("go-yaml reads a copy at %s in place of an alias", path)

	case diffAlias:
		name := aliasText(df.at.at)

		return fmt.Sprintf("go-yaml reads another value at %s through %s", path, name)

	case diffTag:
		tag := x.text.tag
		if df.at != nil {
			tag = df.at.tag
		}

		return fmt.Sprintf("go-yaml reads the tag %q at %s, not %q", tag, path, df.e.tag)

	case diffValue:
	}

	read, meant := shownValues(df.t, df.m)

	return fmt.Sprintf("go-yaml reads %s as %s, not %s", path, read, meant)
}

// countMerges returns n merge keys in words, as a message counts them.
func countMerges(n int) string {
	if n == 1 {
		return "1 merge key"
	}

	return fmt.Sprintf("%d merge keys", n)
}

// The most bytes of a value that a message shows. It shows "..." in place
// of the rest.
const maxShown = 200

// shownValue returns v as a message shows it, on one line. A scalar shows
// as [shown] shows the value that a decode reads. A collection shows as
// the text writes it, with the value of each alias in place of the alias,
// and with each `<<` merge key as an entry of its mapping. A mapping shows
// its entries in the order of their keys. The message shows at most
// maxShown bytes of v, so a value that aliases share shows no more than
// that.
func shownValue(v *value) string {
	var b strings.Builder

	writeValue(&b, v)

	return b.String()
}

// writeValue writes v to b as [shownValue] shows it.
func writeValue(b *strings.Builder, v *value) {
	if b.Len() >= maxShown {
		b.WriteString("...")

		return
	}

	switch v.kind {
	case vScalar, vReference:
		s := shown(v.sc.get())
		if room := maxShown - b.Len(); len(s) > room {
			s = strings.ToValidUTF8(s[:room], "") + "..."
		}

		b.WriteString(s)

	case vSequence:
		b.WriteByte('[')

		for i, e := range v.edges {
			if i > 0 {
				b.WriteString(", ")
			}

			writeValue(b, e.val)

			if b.Len() >= maxShown && i < len(v.edges)-1 {
				b.WriteString(", ...")

				break
			}
		}

		b.WriteByte(']')

	case vMapping:
		entries := slices.Clone(v.edges)
		slices.SortStableFunc(entries, func(x, y *edge) int { return strings.Compare(entryLabel(x), entryLabel(y)) })

		b.WriteByte('{')

		for i, e := range entries {
			if i > 0 {
				b.WriteString(", ")
			}

			b.WriteString(entryLabel(e))
			b.WriteString(": ")
			writeValue(b, e.val)

			if b.Len() >= maxShown && i < len(entries)-1 {
				b.WriteString(", ...")

				break
			}
		}

		b.WriteByte('}')
	}
}

// shownValues returns t and m, two values of the models that a message
// names as apart, as [shownValue] shows them. Where two scalars show
// alike, as the integer 5 and the float 5 do, each takes the word for its
// type, as [shownApart] gives it.
func shownValues(t, m *value) (string, string) {
	x, y := shownValue(t), shownValue(m)
	if x == y && t.kind != vSequence && t.kind != vMapping && m.kind != vSequence && m.kind != vMapping {
		x, y = typeWord(t.sc.get())+x, typeWord(m.sc.get())+y
	}

	return x, y
}

// pathName returns the name that a path selects e, an entry of a mapping,
// by, or the key of e when no path selects it.
func (e *edge) pathName() string {
	if e.name != "" {
		return e.name
	}

	return e.key
}

// entryLabel returns the key of e, an entry of a mapping, as a message
// shows it: "<<" for a merge key, and the name in double quotes for any
// other key.
func entryLabel(e *edge) string {
	if e.merge {
		return "<<"
	}

	return keyLabel(e.key)
}

// keyLabel returns k, a key of the model, as a message shows it: the name
// in double quotes, or the text of a key that does not decode, which
// [keyText] marks.
func keyLabel(k string) string {
	return strconv.Quote(strings.TrimPrefix(k, "\x00"))
}
