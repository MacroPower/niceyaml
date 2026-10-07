package niceyaml

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"slices"
	"strings"
	"sync"

	"github.com/goccy/go-yaml"
	"github.com/goccy/go-yaml/ast"
	"github.com/goccy/go-yaml/parser"
	"github.com/goccy/go-yaml/token"

	"go.jacobcolvin.com/niceyaml/internal/astnode"
	"go.jacobcolvin.com/niceyaml/internal/escape"
	"go.jacobcolvin.com/niceyaml/internal/preamble"
	"go.jacobcolvin.com/niceyaml/line"
	"go.jacobcolvin.com/niceyaml/tokens"
)

// Source is a YAML file, one stream of text that holds one or more YAML
// documents. It holds the tokens lexed from the text, the [*ast.File]
// they parse into, and the settings for parsing and for reporting
// errors. [Source.Documents] returns the root [*Node] of each document in
// the file, and [Source.Document] returns the root of the one document of
// a file that holds one, which is where decoding and validation live.
//
// Source separates two concerns. Parsing lives on Source itself, where [Source.File]
// lazily parses the AST and [Source.Documents] builds the documents. Every error they
// and their Nodes produce comes back bound to the Source as a [SourceError].
// [Node.Bind] binds errors built elsewhere to the document the caller checked them
// against, and [Source.Bind] binds one to the document its location falls in. Rendering
// lives in a [line.View], which carries the overlays, annotations, and flags that a
// [go.jacobcolvin.com/niceyaml/printer.Printer] renders over the [line.Lines] the
// Source holds. [Source.Lines] returns those lines, which the
// [go.jacobcolvin.com/niceyaml/finder.Finder] and
// [go.jacobcolvin.com/niceyaml/diff.Differ] read, and [Source.View] returns a fresh
// view over them for the [go.jacobcolvin.com/niceyaml/printer.Printer].
//
// Typical use creates a Source and renders a view of it:
//
//	source := niceyaml.NewSourceFromString(yamlContent)
//	p := printer.New()
//	lipgloss.Println(p.Print(source.View()))
//
// A Source never changes after creation. Overlays and annotations go on the
// view, and a fresh view renders the document as parsed:
//
//	view := source.View()
//	view.AddOverlay(kind.GenericHighlight, ranges...)
//	lipgloss.Println(p.Print(view))
//
// Since nothing mutates a Source, it is safe for concurrent use. Every view
// taken from it shares its lines and owns its decoration, so creating one
// costs an index of the lines and no copy of their content.
//
// Create instances with [NewSourceFromFile], [NewSourceFromFS],
// [NewSourceFromReader], [NewSourceFromBytes], [NewSourceFromString], or
// [NewSourceFromTokens].
type Source struct {
	name     string
	filePath string
	lines    line.Lines
	// Holds the stream that [Source.Tokens] rebuilds from lines on its
	// first call.
	stream token.Tokens
	// Holds the documents of the runs that parsed, and the stand-ins parse
	// makes for the runs that did not, in file order.
	file *ast.File
	// Holds the set of copies of the tokens parse hands the parser. Every
	// token the parser takes from the stream is one of these copies, and
	// holdsToken looks a token up in the set by pointer. The implicit null
	// tokens the parser makes for missing values are not copies, so
	// holdsToken finds them among the tokens of the nodes of the document
	// instead.
	fileTokens map[*token.Token]struct{}
	// Holds each stand-in among the Docs of file.
	standIns map[*ast.DocumentNode]standIn
	// Holds the error of the one run that did not parse, or the join of
	// the errors of several.
	fileErr error
	// A second parse of the tokens, which decodeParse makes the first time
	// a document changes its tree for the decoder, as decodeTree
	// describes, and the set of copies of the tokens that parse hands the
	// parser.
	decodeFile       *ast.File
	decodeFileTokens map[*token.Token]struct{}
	// Indexes the counts in brackets the reference documents spell, which
	// referenceSpellings fills on its first call.
	refSpellings *spellings
	docs         []*Node
	parserOpts   []parser.Option
	decodeOpts   []yaml.DecodeOption
	// Holds the text of each reference document from WithReferences, in
	// the order every decode reads them.
	references       [][]byte
	streamOnce       sync.Once
	fileOnce         sync.Once
	docsOnce         sync.Once
	decodeFileOnce   sync.Once
	refSpellingsOnce sync.Once
	// Accepts a mapping with the same key twice when parsing and decoding.
	allowDuplicateKeys bool
	// Turns off the alias limit for every reader of the documents.
	skipAliasLimit bool
}

// SourceOption configures [Source] creation.
//
// Available options:
//   - [WithName]
//   - [WithFilePath]
//   - [WithAllowDuplicateKeys]
//   - [WithAliasLimit]
//   - [WithReferences]
//   - [WithYAMLParserOptions]
//
// [WithAllowDuplicateKeys] and [WithReferences] change what the documents
// mean, and [WithAliasLimit] says whether the program trusts their
// aliases. All three describe the documents themselves, so the Source
// applies them to every decode and every validation of its documents.
// Settings of one decode, such as [WithDisallowUnknownFields], are
// [DecodeOption] values. A caller passes them to [Node.Decode], or to
// [NewDecoder] for a [Decoder] that decodes every document with them.
type SourceOption func(*Source)

// WithName is a [SourceOption] that sets the name for the [Source], which
// [SourceError.Error] puts in front of the position of every error bound
// to it, as "name:line:col: msg". Without it, [Source.Name] returns the
// file path.
func WithName(name string) SourceOption {
	return func(s *Source) {
		s.name = name
	}
}

// WithFilePath is a [SourceOption] that sets the file path for the [Source].
// Each document of the Source reports it from [Node.FilePath], which
// schema matchers route on.
//
// For file-based sources, [NewSourceFromFile] and [NewSourceFromFS] set
// this automatically.
func WithFilePath(path string) SourceOption {
	return func(s *Source) {
		s.filePath = path
	}
}

// WithAllowDuplicateKeys is a [SourceOption] that sets whether a mapping may
// hold the same key twice, both when [Source.File] parses the document and
// when a [Node] decodes it. When allowed, the last value wins. The default
// is false, and a duplicate key is then an error.
func WithAllowDuplicateKeys(allow bool) SourceOption {
	return func(s *Source) {
		s.allowDuplicateKeys = allow
	}
}

// WithAliasLimit is a [SourceOption] that sets whether the alias limit
// applies to the documents of the [Source]. The default is true.
//
// The limit refuses a document whose aliases would make a reader read
// far more than the document holds, as [ErrExcessiveAliasing] describes.
// With the limit on, three readers refuse a node of such a document
// that holds an alias:
//
//   - A decode, as [Node.DecodeInto] describes.
//   - A [go.jacobcolvin.com/niceyaml/schema.Schema] that validates the
//     node.
//   - A [go.jacobcolvin.com/niceyaml/schema/matcher.Content] matcher that
//     reads the node to route its document.
//
// Each returns an error matching ErrExcessiveAliasing. The aliases of
// the document are the cause wherever the count runs, so [IsInvalid]
// reports the error of all three.
//
// Turn the limit off only for input the program trusts, such as a file
// it ships:
//
//	source, err := niceyaml.NewSourceFromFile(path, niceyaml.WithAliasLimit(false))
//	if err != nil {
//		return err
//	}
//
//	config, err := source.Decode[Config](ctx, niceyaml.WithValidator(v))
//
// The decode, the schema, and the matcher then read the document
// whatever its aliases hold. The go-yaml decoder can take minutes on a
// few hundred bytes of nested aliases and never checks the context. A
// schema reads every use of every alias. For a 369-byte document with
// six levels of nested aliases, a validation against {"type": "object"}
// held about 160 MiB for a tenth of a second. One against a schema whose
// items keyword refers back to itself held about 300 MiB for five
// seconds.
//
// Two limits stay on whatever the option says:
//
//   - [go.jacobcolvin.com/niceyaml/schema.Schema.ValidateValue] takes a
//     Go value and holds no Source. It refuses a value that shares its
//     maps and slices past the limit, including one a Source with the
//     limit off decoded.
//   - [Node.Nodes] refuses a path whose selectors reach far more nodes
//     through aliases than the document holds.
//
// The option covers the documents of the Source alone. [WithReferences]
// takes the text of a reference Source and none of its settings, so the
// option on a reference Source changes nothing. The count behind the
// limit reads the documents of the Source and no reference document.
// Every decode reads each reference document in full, so nested `<<`
// merge keys there cost every decode of the Source what they expand to,
// with the limit on or off. Pass WithReferences trusted files only.
func WithAliasLimit(enabled bool) SourceOption {
	return func(s *Source) {
		s.skipAliasLimit = !enabled
	}
}

// WithReferences is a [SourceOption] that lets an alias in the documents
// of the [Source] name an anchor that the documents of refs define, such
// as a file of shared defaults:
//
//	defaults, err := niceyaml.NewSourceFromFile("defaults.yaml")
//	if err != nil {
//		return err
//	}
//
//	source, err := niceyaml.NewSourceFromFile("app.yaml", niceyaml.WithReferences(defaults))
//	if err != nil {
//		return err
//	}
//
//	config, err := source.Decode[Config](ctx, niceyaml.WithValidator(reg))
//
// The references change what an alias means, so the Source applies them
// wherever one of its documents decodes: [Node.Decode], [Node.Validate],
// [Node.SelfValidate], [Source.ValidateDocuments], every [Decoder], and
// the decode a [Validator] runs on the Node it gets. A schema thus reads
// `server: *base` as the decode does, and a check of the whole file
// reports what a decode of each document reports.
//
// An alias reads an anchor of refs only when no anchor of its name comes
// before it in its own document, as [Node.DecodeInto] describes. Each
// decode reads the documents of refs in the order given, so an anchor of
// a later document overrides one of the same name in an earlier one. A
// Source in refs brings its own references, ahead of its own documents,
// so its aliases resolve as they do in its own decodes. It brings no
// other setting, so [WithAliasLimit] on a Source in refs changes nothing,
// and the alias limit counts no reference document, as WithAliasLimit
// describes. WithReferences skips a nil Source. A reference document
// that does not parse fails every decode of the Source.
//
// An alias inside refs reads the anchors of refs alone, whatever anchors
// the document defines. Both files here define `base`:
//
//	# defaults.yaml
//	base: &base {port: 443}
//	server: &server {<<: *base}
//
//	# app.yaml
//	base: &base {port: 80}
//	server: *server
//
// The server of app.yaml has port 443 in every decode, into an any value
// and into a struct alike, so a schema checks the value the decode
// returns.
//
// One limit remains among the documents of refs themselves. When they
// define an anchor name twice, a value of refs that holds an alias
// between the two reads the first anchor in a decode into an any value,
// which is the decode a schema runs. A decode into a typed value, such as
// a struct or a []int, reads the second anchor. Give each anchor of refs
// a name that no other anchor of refs has.
//
// The references reach the decoder alone, so a decode and a validation
// read through an alias to a reference document. A path resolves in the
// document itself and stops at that alias. [Node.At] and [Node.Nodes]
// return an error wrapping [go.jacobcolvin.com/niceyaml/paths.ErrAlias]
// for a path that reaches the alias, and [Node.Ranges] returns one for a
// path that goes through it. [IsInvalid] does not report that error. A
// [go.jacobcolvin.com/niceyaml/schema/matcher.Content] matcher whose
// path reaches the alias returns the error too, so a registry stops at
// that document and routes it nowhere.
//
// An error whose path leads into a reference document binds at the alias
// the path enters, since the document holds no line for the value:
//
//	app.yaml:2:9: $.server.port: port must be at least 1
//
//	   2 | server: *server
//	     |         ^
//
// [SourceError.Nearest] reports the path of that alias, and
// [SourceError.Unresolved] returns the error wrapping ErrAlias, so a
// caller tells the position from one at the value itself. An error at a
// key of a mapping that merges a reference document under a `<<` key
// still binds with no position, as SourceError.Nearest describes.
//
// A reference that [WithYAMLDecodeOptions] passes to one decode, such as
// [yaml.ReferenceFiles], reaches that decode alone, and an anchor it
// defines wins over one of the same name in refs.
func WithReferences(refs ...*Source) SourceOption {
	return func(s *Source) {
		for _, ref := range refs {
			if ref == nil {
				continue
			}

			s.references = append(s.references, ref.references...)
			s.references = append(s.references, ref.text())
		}
	}
}

// referenceReaders returns a go-yaml option that hands the decoder a new
// reader over each of docs, so every decode reads the documents from the
// start.
func referenceReaders(docs [][]byte) yaml.DecodeOption {
	return func(d *yaml.Decoder) error {
		readers := make([]io.Reader, len(docs))
		for i, doc := range docs {
			readers[i] = bytes.NewReader(doc)
		}

		return yaml.ReferenceReaders(readers...)(d)
	}
}

// WithYAMLParserOptions is a [SourceOption] that passes [parser.Option]
// values to the go-yaml parser when [Source.File] parses the document. It is
// the escape hatch for parser settings that have no option of their own.
// The parser always parses comments.
//
// These options reach the parser only, never the decoder that [Node]
// decodes with. Allow duplicate keys through [WithAllowDuplicateKeys],
// which sets both. [parser.AllowDuplicateMapKey] passed here lets
// [Source.File] accept a duplicate key that a decode into a struct or a
// typed map then rejects. A mapping that decodes into an any value, at
// the top level or in a field, keeps the last value instead.
func WithYAMLParserOptions(opts ...parser.Option) SourceOption {
	return func(s *Source) {
		s.parserOpts = append(s.parserOpts, opts...)
	}
}

// NewSourceFromFile creates a new [*Source] by reading a file from disk.
//
// It sets the file path on the [Source], so each document reports it for
// schema routing, and [Source.Name] returns it unless [WithName] sets a
// name. [NewSourceFromFS] reads a file from an [fs.FS] the same way.
//
// Returns an error when it cannot read the file.
func NewSourceFromFile(path string, opts ...SourceOption) (*Source, error) {
	data, err := os.ReadFile(path) //nolint:gosec // User-provided file paths are intentional.
	if err != nil {
		return nil, fmt.Errorf("read file: %w", err)
	}

	// Prepend file path option so user options can override if needed.
	opts = append([]SourceOption{WithFilePath(path)}, opts...)

	return NewSourceFromString(string(data), opts...), nil
}

// NewSourceFromFS creates a new [*Source] by reading the file at path
// from fsys, such as an [embed.FS] that ships configuration with the
// binary or an [fs.FS] a test builds. It sets the path on the [Source] as
// [NewSourceFromFile] does, so each document reports it for schema
// routing and a schema directive resolves relative to it in the same
// file system, through the registry option
// [go.jacobcolvin.com/niceyaml/schema.WithFS]:
//
//	source, err := niceyaml.NewSourceFromFS(bundle, "configs/app.yaml")
//
// Returns an error when it cannot read the file.
func NewSourceFromFS(fsys fs.FS, path string, opts ...SourceOption) (*Source, error) {
	data, err := fs.ReadFile(fsys, path)
	if err != nil {
		return nil, fmt.Errorf("read file: %w", err)
	}

	return NewSourceFromBytes(data, append([]SourceOption{WithFilePath(path)}, opts...)...), nil
}

// NewSourceFromReader creates a new [*Source] by reading r to its end,
// such as standard input or the body of an HTTP request. The Source has
// no file path unless [WithFilePath] sets one, and [WithName] names it in
// output:
//
//	source, err := niceyaml.NewSourceFromReader(os.Stdin, niceyaml.WithName("<stdin>"))
//
// Returns an error when it cannot read r.
func NewSourceFromReader(r io.Reader, opts ...SourceOption) (*Source, error) {
	data, err := io.ReadAll(r)
	if err != nil {
		return nil, fmt.Errorf("read: %w", err)
	}

	return NewSourceFromBytes(data, opts...), nil
}

// NewSourceFromBytes creates a new [*Source] from raw YAML bytes.
func NewSourceFromBytes(data []byte, opts ...SourceOption) *Source {
	return NewSourceFromString(string(data), opts...)
}

// NewSourceFromString creates a new [*Source] from a YAML string using
// [tokens.Tokenize]. The Source holds the text without the byte order
// marks Tokenize drops, so no key reads a mark as part of its text.
func NewSourceFromString(src string, opts ...SourceOption) *Source {
	tks := tokens.Tokenize(src)

	return NewSourceFromTokens(tks, opts...)
}

// NewSourceFromTokens creates a new [*Source] from [token.Tokens].
// See [line.NewLines] for details on token splitting behavior.
//
// The Source holds clones of the tokens, and [tokens.ResetPositions]
// resets their positions, so the text it holds counts its lines from 1 as
// [tokens.Tokenize] does. Line i of [Source.Lines] holds line i+1 of the
// text. Tokens that count from 1 already, as a whole stream does, keep
// their positions unless the lexer swallowed the first text, as
// [tokens.ResetPositions] describes. It renumbers tokens cut from a longer
// stream, as [Node.Tokens] hands out, from the first one. To render one
// document of a file with the file's line numbers, print the file's view
// with [Node.Span] instead.
func NewSourceFromTokens(tks token.Tokens, opts ...SourceOption) *Source {
	t := &Source{}
	for _, opt := range opts {
		opt(t)
	}

	if t.allowDuplicateKeys {
		t.parserOpts = append(t.parserOpts, parser.AllowDuplicateMapKey())
		t.decodeOpts = append(t.decodeOpts, yaml.AllowDuplicateMapKey())
	}

	if len(t.references) > 0 {
		t.decodeOpts = append(t.decodeOpts, referenceReaders(t.references))
	}

	t.lines = line.NewLines(tokens.ResetPositions(tks))

	return t
}

// text returns the YAML text of the Source, as the Origins of its tokens
// spell it.
func (s *Source) text() []byte {
	var b bytes.Buffer

	for _, tk := range s.Tokens() {
		b.WriteString(tk.Origin)
	}

	return b.Bytes()
}

// Name returns the name of the [Source]: the one [WithName] set, or else
// the file path. Returns an empty string when the Source has neither.
//
// A nil Source has no name and no file path. [SourceError.Source] returns
// nil for an error bound to no Source, so a report can print the name of
// the Source of any bound error without a nil check.
func (s *Source) Name() string {
	if s == nil {
		return ""
	}

	if s.name == "" {
		return s.filePath
	}

	return s.name
}

// FilePath returns the file path of the [Source].
//
// Returns an empty string unless [WithFilePath], [NewSourceFromFile], or
// [NewSourceFromFS] sets it. A nil Source has no file path, as
// [Source.Name] describes.
func (s *Source) FilePath() string {
	if s == nil {
		return ""
	}

	return s.filePath
}

// Tokens returns the full [token.Tokens] stream of the [Source]. The first
// call rebuilds the stream from every [line.Line], and each call returns a
// new slice that holds the same tokens. See [line.Lines.Tokens] for how a
// token cut across lines comes back whole.
//
// The tokens are the clones [NewSourceFromTokens] made through
// [tokens.ResetPositions], not the tokens the caller passed. The Source keeps
// using them, so treat them as read-only. When the input came from
// [tokens.Tokenize], as it does for every constructor that reads text, the
// Line and Column of each token name the rune where its text starts.
//
// A nil Source holds no tokens, so Tokens returns nil for it.
func (s *Source) Tokens() token.Tokens {
	if s == nil {
		return nil
	}

	s.streamOnce.Do(func() {
		s.stream = s.lines.Tokens()
	})

	return slices.Clone(s.stream)
}

// Documents returns the root [*Node] of each YAML document of this
// [Source], in file order, for a caller that needs the whole file to
// parse:
//
//	docs, err := source.Documents()
//	if err != nil {
//		return err
//	}
//
// For a file with a YAML syntax error, Documents returns no documents and
// the error [Source.File] returns, which names each syntax error of the
// file. [Source.AllDocuments] returns the documents of such a file, for a
// caller that reports on each document.
//
// The parser cuts the comments and %YAML or %TAG directives above a "---"
// header, and the comments after a "..." marker, into a node of their own
// with no header and no content. Documents folds each such node into the
// document below it, or into the last document when no document follows,
// and [Node.Preamble] returns the tokens it put above the content. A
// document that opens with a "---" header and holds only comments is an
// explicit empty document and stays one.
//
// Every Source holds at least one document. An empty file, a file of
// whitespace or comments alone, and a stream of "..." markers alone each
// hold one empty document, which decodes to the zero value. The YAML spec
// finds no document in a stream of markers alone. Documents departs from
// it on purpose, so such a file reads as an empty file does.
// [Node.IsEmpty] reports each empty document, an explicit one included.
//
// It parses the source and builds each Node once, so every call returns
// the same pointers. The slice itself is a copy, so reordering it reaches
// nothing.
func (s *Source) Documents() ([]*Node, error) {
	_, err := s.File()
	if err != nil {
		return nil, err
	}

	return slices.Clone(s.documents()), nil
}

// AllDocuments returns the root [*Node] of each YAML document of this
// [Source], in file order, whether the document parsed or not. It serves
// a caller that renders, diffs, or reports each document, and it returns
// no error:
//
//	for _, doc := range source.AllDocuments() {
//		if err := doc.Err(); err != nil {
//			log.Print(niceyaml.FormatError(err, 2))
//
//			continue
//		}
//
//		lipgloss.Println(p.Print(doc.View()))
//	}
//
// Each document parses on its own, so a YAML syntax error fails the
// document that holds it and no other. The Node of a document that did
// not parse sits in the slice at the index of the document. [Node.Err]
// returns its syntax error, and so do the [Node] methods that read the
// tree, such as [Node.Validate] and [Node.Decode]. A loop over the
// documents thus meets each syntax error beside the errors of the
// documents that parsed. [Source.File] returns those syntax errors as
// one error.
//
// A "---" header that directly follows an anchor with no value parses
// together with the document above it. A syntax error in either of those
// two documents fails both, and both return that error, so a loop that
// collects the error of each document holds it twice. A caller that
// validates every document calls [Source.ValidateDocuments], which runs
// that loop and reports a shared error once:
//
//	err := source.ValidateDocuments(ctx, reg)
//
// AllDocuments divides the file into documents as [Source.Documents]
// describes, and it returns the Nodes Documents returns for a file with
// no syntax error. The slice itself is a copy, so reordering it reaches
// nothing.
func (s *Source) AllDocuments() []*Node {
	return slices.Clone(s.documents())
}

// ValidateDocuments validates every document of the Source in file order
// and joins what they return, so one call reports each syntax error and
// each violation of a file that holds several documents:
//
//	err := source.ValidateDocuments(ctx, reg)
//	if err != nil {
//		log.Print(niceyaml.FormatError(err, 2))
//	}
//
// Each document runs the validators as [Node.Validate] runs them. Every
// document validates, an explicit empty one included, such as the one
// below the header of `name: x\n---\n`. A caller whose stream may hold
// empty documents, such as the output of a Helm chart, wraps each
// validator in [SkipEmpty]. The validator then runs on the documents
// with content and not on the ones [Node.IsEmpty] reports:
//
//	err := source.ValidateDocuments(ctx, niceyaml.SkipEmpty(reg))
//
// A document that did not parse reports its syntax error, as
// Node.Validate returns it. Two documents that parse together share one
// syntax error, as [Source.AllDocuments] describes, and ValidateDocuments
// reports it once. Given no validators and a ctx that has not ended, it
// thus returns the error [Source.File] returns.
//
// ValidateDocuments checks ctx before each document and once every
// document has run. Once ctx has ended, no further document validates,
// and the result includes the error of ctx bound to the Source, unless an
// error in the result already wraps it. A ctx that ended before the call
// thus returns its error. Only the ctx passed in stops the walk, so a
// validator that reports a deadline of its own fails its document alone.
//
// [Source.Document] and [Source.Decode] need a Source of one document,
// where ValidateDocuments takes any number, so a file it passes can still
// fail Source.Decode.
func (s *Source) ValidateDocuments(ctx context.Context, validators ...Validator) error {
	var (
		errs []error
		// The syntax error of the document before, which the next document
		// shares when the two parsed together.
		prev error
	)

	// Each document that did not parse reports its own syntax error.
	for _, doc := range s.documents() {
		if ctx.Err() != nil {
			break
		}

		err := doc.Validate(ctx, validators...)

		shared := doc.doc.err != nil && errors.Is(doc.doc.err, prev)
		prev = doc.doc.err

		if err != nil && !shared {
			errs = append(errs, err)
		}
	}

	err := ctx.Err()
	if err != nil && !slices.ContainsFunc(errs, func(e error) bool { return errors.Is(e, err) }) {
		errs = append(errs, s.Bind(err))
	}

	switch len(errs) {
	case 0:
		return nil
	case 1:
		return errs[0]
	default:
		return errors.Join(errs...)
	}
}

// documents returns the root [*Node] of each YAML document, as
// [Source.AllDocuments] does, in the slice the Source keeps rather than a
// copy, so a caller must not change it.
func (s *Source) documents() []*Node {
	s.parseOnce()

	s.docsOnce.Do(func() {
		s.docs = newDocuments(s)
	})

	return s.docs
}

// Document returns the root [*Node] of a [Source] that holds a single
// YAML document, for a caller that reads a configuration file in more
// than one step, such as one that scopes a Node with [Node.At] or binds
// a check with [Node.Bind]. [Source.Decode] decodes that document in
// one step:
//
//	doc, err := niceyaml.NewSourceFromString(yamlContent).Document()
//	if err != nil {
//		return err
//	}
//
//	config, err := doc.Decode[Config](ctx, niceyaml.WithValidator(validator))
//
// The comments above the first "---" are the preamble of the document
// below them rather than a document of their own. A file that opens with
// a license header therefore holds a single document. A file of comments
// alone holds one that decodes to the zero value as an empty file does,
// and so does a file of "..." markers alone, as [Source.Documents]
// describes.
//
// When the file holds more than one document, it returns an error wrapping
// [ErrMultipleDocuments], bound to the Source. The error points at the
// header of the second document, or at the first token of its content
// when a "..." marker rather than a header opens it. A file with a
// document that does not parse returns the error [Source.File] returns,
// however many of its other documents parse. Use [Source.Documents] for a
// file that may hold several.
func (s *Source) Document() (*Node, error) {
	// The parse bound each syntax error already, and binding the join of
	// several anew would return another error than File does.
	_, err := s.File()
	if err != nil {
		return nil, err
	}

	doc, err := s.single()
	if err != nil {
		return nil, s.Bind(err)
	}

	return doc, nil
}

// DecodeInto validates and decodes the one document of the [Source] into
// v, as [Node.DecodeInto] decodes the root Node [Source.Document]
// returns. A Source that holds more than one document returns the error
// Source.Document returns, so a configuration file that must hold one
// document decodes in one step and reports a second document as the
// error it is.
func (s *Source) DecodeInto(ctx context.Context, v any, opts ...DecodeOption) error {
	doc, err := s.Document()
	if err != nil {
		return err
	}

	return doc.DecodeInto(ctx, v, opts...)
}

// Decode validates and decodes the one document of the [Source] into a
// new T, as [Node.Decode] decodes the root Node [Source.Document]
// returns, which is the direct path for a configuration file:
//
//	source, err := niceyaml.NewSourceFromFile(path)
//	if err != nil {
//		return err
//	}
//
//	config, err := source.Decode[Config](ctx, niceyaml.WithValidator(schema))
//
// A Source that holds more than one document returns the error
// Source.Document returns. On error, the returned T is the zero value.
func (s *Source) Decode[T any](ctx context.Context, opts ...DecodeOption) (T, error) {
	var v T

	err := s.DecodeInto(ctx, &v, opts...)
	if err != nil {
		var zero T

		return zero, err
	}

	return v, nil
}

// single returns the one document of the Source, or the reason it has
// no single one, unbound: the error [Source.File] returns, or
// [ErrMultipleDocuments] at the anchor of the second document. Every
// Source holds at least one document, as [Source.Documents] describes.
func (s *Source) single() (*Node, error) {
	_, err := s.File()
	if err != nil {
		return nil, err
	}

	docs := s.documents()
	if len(docs) > 1 {
		return nil, WrapError(
			fmt.Errorf("%w: %d documents", ErrMultipleDocuments, len(docs)),
			atToken(docs[1].doc.anchorToken()),
		)
	}

	return docs[0], nil
}

// anchorToken returns the token that locates the document: its header, or
// the first token of its content when it has no header, as a document after
// a "..." marker has none, past the comments folded above it. It is nil
// when the document has neither.
func (d *document) anchorToken() *token.Token {
	if d.root.Start != nil {
		return d.root.Start
	}

	if d.preamble < len(d.tokens) {
		return d.tokens[d.preamble]
	}

	if len(d.tokens) > 0 {
		return d.tokens[0]
	}

	return nil
}

// File returns an [*ast.File] for the [Source] tokens.
//
// The first call parses the file with [parser.Parse] and the options
// [WithYAMLParserOptions] provides. Subsequent calls return the cached
// result. A "---" header that directly follows another header starts a
// document of its own, and a "..." end marker ends its document.
// [parser.Parse] alone drops the rest of the stream after such a header,
// and it can reject what follows a marker or join it to the document the
// marker ends.
//
// The tree leaves out a comment on a line of its own below a document whose
// root is a scalar or a flow collection. Below a block mapping or a block
// sequence at the root, it leaves out such a comment that starts left of
// the first key or "-" of the root. It leaves out such a comment between
// a %YAML or %TAG directive and its "---" header, between the
// "?" of an explicit key and the key, and between the key and its ":".
// Inside a flow collection, it also leaves out such a comment before a key
// of a flow mapping or before a ",", "]", or "}". [parser.Parse] rejects or
// misreads valid YAML that holds a comment in any of these places. A
// comment on the last line of a scalar that spans several lines counts as
// one on a line of its own. The parser attaches a comment to the token
// before it only when that token starts on the line of the comment. The
// tokens of the Source and of each [Node] still hold such a comment.
//
// The tree also leaves out the comments on lines of their own between an
// anchor and the node below them where they make the parser reject or
// misread that node. The anchor ends its line and directly follows a "-",
// or a key and its ":", on that line. The key starts on that line too, at
// its "?" when it has one. When the first of the comments starts left of
// the "-" or key, the parser gives the anchor a null value. The tree then
// leaves the comments out above a node that the parser takes as the value
// of the anchor without them. That node starts right of the "-" or key, or
// it is a "-" in the column of the key. When the first of the comments
// starts in the column of the "-" or key or right of it, the parser gives
// the anchor the node below them. The tree then leaves them out above a
// node left of the "-" or key, above a key in the column of the key, and
// above a "-" in the column of the "-".
//
// The parser takes the comments below an anchor with no value that ends its
// document as the value of the anchor. The tree leaves them out and holds a
// null as the value of that anchor. The exception is an anchor that
// directly follows a "-", or a key and its ":", on its line as above. When
// a "---" header or a "..." marker follows the comments, the tree leaves
// out those that start left of the first key or "-" of the root. When the
// first comment that remains starts left of the "-" or key of the anchor,
// the parser itself gives the anchor a null value, and the comments that
// remain stay in the tree. At the end of the source the tree leaves none of
// them out first, since the parser rejects the anchor without the comments.
// When the first comment there starts left of the "-" or key of the anchor,
// the parser gives the anchor a null value too. The comments then stay in
// the tree unless one starts left of the first key or "-" of the root, and
// File returns the parser's error for that one.
//
// The tokens of the file are copies of the Source's own, since the parser
// relinks the tokens it receives. A copy matches the original by its type,
// value, origin, and position, so a token taken from a node finds its
// lines through [line.Lines.TokenRanges] and [line.Lines.ContentRanges] as
// the original does.
//
// Every [Node] of the Source shares the tree, and [Node.At],
// [Node.Ranges], and every error binding resolve against it, so it is
// read-only. A caller that modifies it corrupts the positions those
// resolve to and races with any concurrent use of the Source. A caller
// that edits a document parses a tree of its own, or edits the text and
// builds a new Source from the result.
//
// A file with a YAML syntax error has no tree, so File returns nil and
// the error. Each document parses on its own, so the error covers every
// document that did not parse, not the first alone. One syntax error
// comes back as a [*SourceError] bound to this Source, so [FormatError]
// renders it with the offending token marked. Several come back joined,
// each a SourceError of its own in file order, and FormatError marks them
// all. [Bindings] iterates over them. [Source.Documents] returns that
// error too, and [Source.AllDocuments] returns the documents of such a
// file, for a caller that reads the ones that parsed.
//
// The message of a syntax error names each control character by its
// Unicode Control Picture, so a tab the parser rejects reads as "␉" there
// as it does in the excerpt. Every error of the parse matches
// [ErrSyntax], and so does a panic in the parser, which comes back as a
// [*SourceError] too. Every later call returns the same error.
func (s *Source) File() (*ast.File, error) {
	s.parseOnce()

	if s.fileErr != nil {
		return nil, s.fileErr
	}

	return s.file, nil
}

// parseOnce parses the tokens of the Source on the first call, and keeps
// what [Source.parse] returns.
func (s *Source) parseOnce() {
	s.fileOnce.Do(func() {
		p := s.parse()

		s.file, s.fileTokens, s.standIns = p.file, p.tokens, p.standIns

		switch len(p.errs) {
		case 0:
		case 1:
			s.fileErr = p.errs[0]
		default:
			s.fileErr = errors.Join(p.errs...)
		}
	})
}

// parsed is what one parse of the tokens of a [Source] gives.
type parsed struct {
	// The documents of the runs that parsed, with the stand-ins of the
	// runs that did not, in file order.
	file *ast.File
	// The set of copies of the tokens the parser read.
	tokens map[*token.Token]struct{}
	// Each stand-in among the Docs of file, which is nil when every run
	// parsed.
	standIns map[*ast.DocumentNode]standIn
	// The error of each run that did not parse, in file order, bound to
	// the Source.
	errs []error
}

// standIn describes a node that [parsed.fail] puts in the file in place of
// the nodes the parser would have made of a run it rejected.
type standIn struct {
	// The token that places the stand-in in the file, which is nil when
	// its tokens carry no position.
	anchor *token.Token
	// The error of the run, for a stand-in that stands for a document. It
	// is nil for one that stands for the comments and directives the
	// parser cuts off above a header.
	err error
}

// parse hands the parser the copies of the tokens that
// [tokens.ForParser] makes. The go-yaml parser relinks Next and Prev while
// it moves comment tokens, and the Source's own tokens, which its lines and
// the caller share, stay untouched. It returns the set of copies with the
// file they parsed to.
//
// Each run of [splitDocumentRuns] parses on its own, so a run the parser
// rejects costs the file only the documents of that run. The file holds
// the stand-ins [parsed.fail] makes in their place, and the parse goes on
// with the next run.
//
// ForParser cuts the blank lines in front of the first text down to their
// line breaks, so a blank line that holds a tab does not make the parser
// reject the first key. It also moves the blank content of a block scalar
// to the line after the header, so the parser attaches a comment below
// that content to the next node. Each copy gets the Origin and position
// of its own token back once the parser returns, so it matches the
// Source's token as [Source.File] promises.
func (s *Source) parse() parsed {
	shared := s.Tokens()
	tks := tokens.ForParser(shared)

	set := make(map[*token.Token]struct{}, len(tks))
	for _, tk := range tks {
		set[tk] = struct{}{}
	}

	// The Source holds no nil tokens, so each copy sits at the index of
	// its own token.
	defer func() {
		for i, tk := range tks {
			tk.Origin = shared[i].Origin

			// Clone gives each copy a Position of its own, so the write
			// leaves the Source's token alone.
			if tk.Position != nil && shared[i].Position != nil {
				*tk.Position = *shared[i].Position
			}
		}
	}()

	p := parsed{file: &ast.File{Docs: []*ast.DocumentNode{}}, tokens: set}

	for _, run := range splitDocumentRuns(tks) {
		f, err := s.parseRun(dropStrandedComments(run))
		if err == nil {
			nullEmptyAnchors(f)

			p.file.Docs = append(p.file.Docs, f.Docs...)

			continue
		}

		// The go-yaml scanner spells the tab it rejects as a raw tab, which
		// the renderers would lay out as four spaces, so the message names
		// each control character by its picture.
		yamlErr, ok := errors.AsType[yaml.Error](err)

		switch {
		case ok:
			msg := escape.Control(yamlErr.GetMessage())

			err = WrapError(syntaxError{err: yamlMessageError{err: yamlErr, msg: msg}}, atToken(yamlErr.GetToken()))

		case !errors.Is(err, ErrSyntax):
			err = syntaxError{err: err}
		}

		// The documents come from the file this parse returns, so the error
		// binds to the source alone rather than routing to one of them.
		p.fail(run, bindTree(err, binder{src: s}))
	}

	// The parser makes one empty document of an empty file, and none of a
	// stream of "..." markers alone. That stream holds one empty document
	// too, which the first marker ends, so every file holds a document and
	// a file of markers decodes to the zero value as an empty file does.
	// YAML holds no document there, so this departs from the spec on
	// purpose.
	if len(p.file.Docs) == 0 {
		doc := ast.Document(nil, nil)

		if i := slices.IndexFunc(tks, func(tk *token.Token) bool { return tk.Type == token.DocumentEndType }); i >= 0 {
			doc.End = tks[i]
		}

		p.file.Docs = append(p.file.Docs, doc)
	}

	return p
}

// fail records that the parser rejected run with err. It adds a stand-in
// to the file for each token group [tokens.SplitDocuments] cuts run into,
// so the documents of run keep their places among the documents of the
// file and their tokens. A group with a "---" header or with content
// stands for a document, and its stand-in carries err. Any other group
// holds what the parser cuts off as a node of its own, the comments and
// directives above a header or the comments after a "..." marker, and its
// stand-in folds into a document as that node does. A run without a
// document group, such as a directive that no document follows, takes its
// first group as the document, so err always has a document to name it.
func (p *parsed) fail(run token.Tokens, err error) {
	p.errs = append(p.errs, err)

	var groups []token.Tokens

	for _, group := range tokens.SplitDocuments(run) {
		groups = append(groups, group)
	}

	// A run without tokens still gets one stand-in to carry err.
	if len(groups) == 0 {
		groups = []token.Tokens{nil}
	}

	holdsDocument := slices.ContainsFunc(groups, isDocumentGroup)

	if p.standIns == nil {
		p.standIns = map[*ast.DocumentNode]standIn{}
	}

	for i, group := range groups {
		var in standIn

		if j := slices.IndexFunc(group, func(tk *token.Token) bool { return tk.Position != nil }); j >= 0 {
			in.anchor = group[j]
		}

		node := ast.Document(nil, nil)

		if isDocumentGroup(group) || !holdsDocument && i == 0 {
			in.err = err

			// SplitDocuments starts a group at each header, so a header
			// is the first token of its group.
			if len(group) > 0 && group[0].Type == token.DocumentHeaderType {
				node.Start = group[0]
			}
		}

		p.file.Docs = append(p.file.Docs, node)
		p.standIns[node] = in
	}
}

// isDocumentGroup reports whether group, a token group of
// [tokens.SplitDocuments], holds a YAML document, which it does when it
// opens with a "---" header or holds content. A group of comments,
// directives, and "..." markers alone holds none.
func isDocumentGroup(group token.Tokens) bool {
	if len(group) > 0 && group[0].Type == token.DocumentHeaderType {
		return true
	}

	return preamble.Len(group) < len(group)
}

// syntaxError is an error of the parse, which matches [ErrSyntax] and
// [errInvalid]. It reads as the error it holds and unwraps to it.
type syntaxError struct {
	err error
}

func (e syntaxError) Error() string {
	return e.err.Error()
}

func (e syntaxError) Unwrap() error {
	return e.err
}

// Is reports whether target is [ErrSyntax] or [errInvalid].
func (e syntaxError) Is(target error) bool {
	return target == ErrSyntax || target == errInvalid
}

// parseRun parses run, one run of [splitDocumentRuns]. It turns a panic
// in the parser into a [*SourceError] bound to the Source that matches
// [ErrSyntax], located at the first token of run that carries a
// position. The Error around the panic declares nothing, so the binding
// matches [errInvalid] only as every [syntaxError] does.
func (s *Source) parseRun(run token.Tokens) (*ast.File, error) {
	var (
		f   *ast.File
		err error
	)

	func() {
		defer func() {
			p := recover()
			if p == nil {
				return
			}

			var at *token.Token

			if i := slices.IndexFunc(run, func(tk *token.Token) bool { return tk.Position != nil }); i >= 0 {
				at = run[i]
			}

			panicked := syntaxError{err: fmt.Errorf("parser rejected the tokens: panic: %v", p)}

			err = bindTree(Place(panicked, atToken(at)), binder{src: s})
		}()

		f, err = parser.Parse(run, parser.ParseComments, s.parserOpts...)
	}()

	return f, err //nolint:wrapcheck // The caller binds the error.
}

// decodeParse returns a second parse of the tokens of the Source, with the
// set of copies of the tokens that parse hands the parser, and makes it on
// the first call. Nothing but the decoder reads its tree, so a document
// changes its own part of that tree as [decodeTree] describes, and the
// tree [Source.File] returns stays as the parser built it. The parse
// holds its own copies of the tokens, so the go-yaml formatter, which
// reads the text of each token through the links between them, finds a
// renamed anchor or a verbatim !!int tag with the text of the Source.
// The formatter reads the tokens of every document, so the first call
// writes the nulls of all the documents into the tokens, as
// [nullEnclosedAliases] describes, before it returns the parse to any of
// them. A run the first parse rejected fails the second parse too, so the
// file holds a stand-in at each index where the first file holds one, and
// the documents that parsed sit at the same indexes in both.
func (s *Source) decodeParse() (*ast.File, map[*token.Token]struct{}) {
	s.decodeFileOnce.Do(func() {
		p := s.parse()

		nullEnclosedAliases(s.docs, p.file)

		s.decodeFile, s.decodeFileTokens = p.file, p.tokens
	})

	return s.decodeFile, s.decodeFileTokens
}

// referenceSpellings returns the [*spellings] of the reference documents
// of the Source, and builds it on the first call.
func (s *Source) referenceSpellings() *spellings {
	s.refSpellingsOnce.Do(func() {
		var tks token.Tokens

		for _, ref := range s.references {
			tks = append(tks, tokens.Tokenize(string(ref))...)
		}

		s.refSpellings = newSpellings(tks)
	})

	return s.refSpellings
}

// splitDocumentRuns cuts tks into runs that each parse on its own. It cuts
// before each "---" header that follows a token other than a comment, and
// after each "..." end marker that a token on a later line follows.
//
// The go-yaml parser (v1.19.3-0.20260407131736-edee2f91616c) groups the
// documents of a stream in a recursion that copies the rest of the list
// at each header (parser/token.go:662), so one run of many documents takes
// time quadratic in their number. It also mishandles some sequences of
// headers and markers. It stops at a header that directly follows another
// and drops every token after it (parser/token.go:637). It drops a "..."
// marker that directly follows a header and merges the header into what
// comes after the marker (parser/token.go:661). A second header there
// gives the merged document two headers, which fails with "unexpected
// scalar value type". A %YAML or %TAG directive or a document without a
// header there joins the empty document rather than starting the next
// one, and a %YAML directive there fails when another opens the empty
// document. The parser also rejects a scalar document below any marker
// (parser/token.go:682).
//
// A header stays in the run above it in two cases. A header that a
// directive precedes stays in the run of the directive, since the parser
// rejects a run that ends in a directive. YAML allows a directive only
// before the first document or after a marker, where a run starts anyway,
// and the parser rejects one anywhere else only while the document above
// it shares its run. A header that follows an anchor with no value stays in
// the run of the anchor. The parser takes the two tokens after the "&" as
// the name and the value of the anchor, and rejects a run that ends before
// them (parser/token.go:311).
//
// Each run parses with a parser of its own, so a %TAG or %YAML directive
// holds for the documents of its run. That is the one document the
// directive heads, as YAML defines, unless that document ends in an
// anchor with no value. The parser alone carries a %TAG directive for
// "!!" into every later document of its input.
//
// The look-back skips comments. The parser folds a comment on the line of
// a header or a marker into that token, so such a comment stays in the
// run of its token. A comment on a line of its own above a header parses
// to the same documents whether or not a run ends there. One below a
// marker starts the next run, as it starts a token group of
// [tokens.SplitDocuments]. Left in the run above, it would parse to a node
// that shares that group with the node below, and both would take the
// tokens of the group. A token on the line of a marker stays in its run,
// since YAML allows only a comment there and the parser rejects a scalar
// there.
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

// dropStrandedComments returns the tokens of run without the comments on
// lines of their own in places where the go-yaml parser can reject or
// misread valid YAML when it keeps comments, or run itself when it holds
// none.
//
// The parser (v1.19.3-0.20260407131736-edee2f91616c) attaches such a
// comment below the content of a document only when the root of the
// document is a block mapping or a block sequence. The comment must also
// start at or right of the column of the first key or "-" of that root
// (parser/parser.go:525, parser/parser.go:1117). Below a scalar or a flow
// collection, or left of that column, it leaves the comment unread and
// fails with "value is not allowed in this context" (parser/parser.go:171).
// It also requires the header to follow the line of a %YAML or %TAG
// directive at once, and fails with "document not started" when a comment
// sits between them (parser/token.go:598). The go-yaml decoder parses
// without comments and accepts both. The comments stay among the tokens of
// the Source, where [tokens.SplitDocuments] still hands them to their
// documents.
//
// A comment sits on a line of its own when it starts on a line below the
// line where the last token before it starts. The parser attaches a
// comment to the token before it only when both start on one line
// (parser/token.go:254), so it reads a comment on the last line of a
// scalar that spans several lines as it reads one on a line of its own.
//
// Below a scalar or a flow collection, only the comments that close the
// document drop: those that the end of run, a "---" header, or a "..."
// marker follows. The parser rejects any other token after them, with or
// without the comments. Below a block mapping or a block sequence, only
// the comments that close the document and start left of its first key
// or "-" drop. The key starts at its anchor or tag when it has one, as
// [keyStart] finds it. A comment left of that column stays when more of
// the root follows it, since the parser looks past it to the next key or
// "-".
//
// Below an anchor with no value, the comments that close the document stay,
// since the parser rejects an anchor that no token follows past its name
// (parser/token.go:311). The parser takes them as the value of the anchor,
// and [nullEmptyAnchors] puts a null in their place once the parser
// returns. An anchor that directly follows a "-", or a key and its ":", on
// its line differs in two ways. The parser reads that anchor by the first
// comment on a line of its own that its input holds. When that comment
// starts left of the "-" or key, the parser itself gives the anchor a null
// value and reads the comments as it reads them below any other node. In a
// block mapping or a block sequence at the root, when a "---" header or a
// "..." marker follows the comments, the parser takes that anchor without
// them, as [takesNull] reports. Those that start left of the first key or
// "-" of the root then drop, as they do below any other node of such a
// root. The drop can change the first comment from one left of the "-" or
// key of the anchor to one at or right of it. The parser then takes the
// comments that stay as the value of the anchor, and [nullEmptyAnchors]
// puts a null in their place. At the end of run, those left of the root
// stay, and the parser rejects them unless it takes them as the value of
// the anchor. Above a node, the comments drop when [breaksAnchor] reports
// that they make the parser give the anchor the wrong value. Inside a flow
// collection, the anchor does not decide whether they drop. In a flow
// sequence, the parser still reads an anchor after the key of a pair by the
// column of the first comment. When that comment starts left of the key,
// the parser gives the anchor a null value and rejects the value below the
// comment.
//
// Below a "&" with no name, the comments stay wherever they sit, except
// between a directive and its header. The parser takes the token after a
// "&" as the name of the anchor (parser/token.go:314), so it rejects a "&"
// above a comment. Without the comments, it would name the anchor after the
// token below them.
//
// In block and flow style alike, the parser takes the token after a "?"
// as the key the "?" introduces, and the token before a ":" as the key of
// that ":" (parser/token.go:493, parser/token.go:513). With a comment in
// either place, it misreads the mapping or fails with "unexpected scalar
// value type" (parser/parser.go:326). Those comments drop.
//
// Inside a flow collection, the parser reads the other comments as the
// head comment of the node that follows them. It fails when a ",", "]",
// or "}" follows them instead, and when they sit after a "{" or "," of a
// flow mapping, where it looks for a key (parser/parser.go:349,
// parser/parser.go:407, parser/parser.go:1042). Those comments drop, and
// the comments before any other node stay.
func dropStrandedComments(run token.Tokens) token.Tokens {
	var (
		kept token.Tokens
		// What the document the scan is in has shown so far: a token of
		// its content, an indicator of a block mapping or a block sequence
		// at its top level, and a directive that awaits its header.
		content, block, directive bool
		// The column where the first key or "-" of the block mapping or
		// block sequence at the root of the document starts, or -1.
		rootCol = -1
		// The types of the "[" and "{" tokens of the flow collections
		// that hold the scan, the innermost last.
		flows []token.Type
		// The index of the last anchor in run.
		anchor int
		// The last two tokens other than comments that hold text.
		last, beforeLast *token.Token
	)

	for i := 0; i < len(run); {
		tk := run[i]

		if tk.Type != token.CommentType {
			switch tk.Type {
			case token.DocumentHeaderType, token.DocumentEndType:
				content, block, directive, flows = false, false, false, flows[:0]
				rootCol = -1

			case token.DirectiveType:
				directive = true

			case token.SequenceStartType, token.MappingStartType:
				content = true

				flows = append(flows, tk.Type)

			case token.SequenceEndType, token.MappingEndType:
				if len(flows) > 0 {
					flows = flows[:len(flows)-1]
				}

			case token.MappingKeyType, token.MappingValueType, token.SequenceEntryType:
				if !block && len(flows) == 0 {
					rootCol = rootColumn(run, i)
				}

				content = true
				block = block || len(flows) == 0

			case token.AnchorType:
				content = true
				anchor = i

			default:
				content = true
			}

			if strings.Trim(tk.Origin, " \t\r\n") != "" {
				beforeLast, last = last, tk
			}

			if kept != nil {
				kept = append(kept, tk)
			}

			i++

			continue
		}

		next := i
		for next < len(run) && run[next].Type == token.CommentType {
			next++
		}

		// A "&" with no name ends the content.
		bare := last != nil && last.Type == token.AnchorType

		// The name of an anchor ends the content when the anchor has no
		// value.
		anchored := beforeLast != nil && beforeLast.Type == token.AnchorType

		// Whether the comments close the document.
		closes := next == len(run) ||
			run[next].Type == token.DocumentHeaderType || run[next].Type == token.DocumentEndType

		// The parser takes a comment after a "?" or before a ":" as a key.
		stranded := directive || last != nil && last.Type == token.MappingKeyType ||
			next < len(run) && run[next].Type == token.MappingValueType

		// Whether the comments that start left of rootCol drop.
		leftDrops := false

		switch {
		case bare:
			// Without the comments, the parser would take the token below
			// them as the name of the anchor.
			stranded = directive

		case len(flows) > 0:
			stranded = stranded || next < len(run) && endsFlowEntry(run[next]) ||
				startsFlowKey(last, flows[len(flows)-1])

		case anchored:
			// The first comment on a line of its own, past any comment on
			// the line where the last token starts.
			below := i
			for below < next && !startsBelow(run[below], last) {
				below++
			}

			stranded = stranded ||
				below < next && !closes && breaksAnchor(run, anchor, below, next)
			leftDrops = closes && next < len(run) && rootCol >= 0 && takesNull(run, anchor)

		case content && !block:
			stranded = stranded || closes

		case block:
			leftDrops = closes && rootCol >= 0
		}

		for j := i; j < next; j++ {
			left := leftDrops && run[j].Position != nil && run[j].Position.Column < rootCol

			if (stranded || left) && startsBelow(run[j], last) {
				if kept == nil {
					kept = slices.Clone(run[:j])
				}

				continue
			}

			if kept != nil {
				kept = append(kept, run[j])
			}
		}

		i = next
	}

	if kept == nil {
		return run
	}

	return kept
}

// nullEmptyAnchors puts a null in place of each comment that the parser
// took as the value of an anchor of file. When a header or another entry
// follows an anchor with no value, the parser gives the anchor an
// implicit null, and this null takes the token type and position of that
// one. The go-yaml decoder reads the null as an empty node, where it
// would reject the comment in a typed target and give a pointer a value
// for it.
func nullEmptyAnchors(file *ast.File) {
	for _, doc := range file.Docs {
		for _, node := range sourceNodes(doc) {
			anchor, ok := node.(*ast.AnchorNode)
			if !ok || astnode.HasContent(anchor.Value) {
				continue
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
		}
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

// breaksAnchor reports whether the comments between the anchor at run[at]
// and the node at run[next] make the parser give the anchor the wrong
// value where it gives the right one without them. The anchor has its
// name, and the comment at run[below] is the first of the comments on a
// line of its own.
//
// The comments change what the parser reads only below an anchor that
// directly follows a "-", or a key and its ":", on its line. The key must
// start on that line too, where [keyStart] finds it, and an explicit key
// starts at its "?" (parser/parser.go:730). The parser gives that anchor a
// null value when the first comment starts left of the "-" or key, and the
// node otherwise (parser/parser.go:787, parser/parser.go:1186). It reads
// any other anchor the same way with the comments as without them, so
// breaksAnchor reports false for one.
//
// Without the comments, the parser takes the node as the value of the
// anchor when the node starts right of the "-" or key, or is a "-" in the
// column of the key, where a block sequence may start. Above such a node,
// breaksAnchor reports whether the first comment starts left of the "-" or
// key, which makes the parser give the anchor a null value.
//
// Above any other node, YAML gives the anchor a null value, so
// breaksAnchor reports false when the first comment starts left of the "-"
// or key. Otherwise it reports true above a node left of the "-" or key, a
// key in the column of the key, and a "-" in the column of the "-". The
// parser gives the anchor a null value there without the comments
// (parser/parser.go:750, parser/parser.go:787, parser/parser.go:1149,
// parser/parser.go:1186). It reports false above any other node. One such
// node is a key in the column of the "-", which the parser takes as the
// value without the comments too.
//
// When a token it compares has no position, breaksAnchor reports false.
func breaksAnchor(run token.Tokens, at, below, next int) bool {
	comment, tk := run[below], run[next]
	if comment.Position == nil || tk.Position == nil {
		return false
	}

	// The "-" or ":" that the anchor directly follows on its line.
	i := at - 1
	if i < 0 || !sameLine(run[i], run[at]) {
		return false
	}

	// The index of the "-" or the start of the key.
	holder := i
	mapping := run[i].Type == token.MappingValueType

	switch {
	case mapping:
		holder = keyStart(run, i)

	case run[i].Type != token.SequenceEntryType:
		return false
	}

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
// the first token on its line. That token is a "?" or a ":", or a ":"
// follows it on its line. A "[" or "{" before that ":" makes the line a
// flow collection, which the parser does not read as a key, so startsKey
// reports false for it.
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

// takesNull reports whether the parser gives the anchor at run[at], which
// has its name, a null value when a token left of its key or "-" follows
// it. It does so for an anchor that directly follows a "-", or a key and
// its ":", on its line (parser/parser.go:787, parser/parser.go:1186). The
// parser rejects any other anchor that no node follows.
func takesNull(run token.Tokens, at int) bool {
	i := at - 1
	if i < 0 || !sameLine(run[i], run[at]) {
		return false
	}

	switch run[i].Type {
	case token.SequenceEntryType:
		return true

	case token.MappingValueType:
		return keyStart(run, i) >= 0

	default:
		return false
	}
}

// rootColumn returns the column where the entry of the "-", "?", or ":"
// at run[at] starts, or -1 when the token there has no position. An
// entry starts at its "-" or "?". For a ":", it starts where [keyStart]
// finds the start of the key, or at the ":" itself when no token of a
// key precedes the ":" on its line.
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

// keyStart returns the index of the token on the line of the ":" at
// run[colon] where the key before that ":" starts. An explicit key starts
// at its "?". Any other key starts past any "-" or ":" on the line, at its
// anchor or tag when it has one. It returns -1 when no token of a key
// precedes the ":" on its line, as with a ":" that starts its line, and
// when the "?" of the key sits on a line above.
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
// false when either token has no position.
func sameLine(a, b *token.Token) bool {
	return a.Position != nil && b.Position != nil && a.Position.Line == b.Position.Line
}

// startsBelow reports whether tk starts on a line below the line mark
// starts on. It reports false when mark is nil or either token has no
// position.
func startsBelow(tk, mark *token.Token) bool {
	return mark != nil && tk.Position != nil && mark.Position != nil &&
		tk.Position.Line > mark.Position.Line
}

// Bind binds err to the [Source] and to the document each location in
// it falls in, so a caller that holds the source binds without picking a
// document. An error that carries a [position.Position] or a
// [position.Range], as a check that runs on [Source.Lines] produces, binds
// to the document whose [Node.Span] holds the line, whatever the
// file holds:
//
//	for i, ln := range source.Lines().All() {
//		if ln.Width() > 120 {
//			rng := position.NewRange(position.New(i, 120), position.New(i, ln.Width()))
//
//			return source.Bind(niceyaml.NewError("line exceeds 120 columns", niceyaml.AtRange(rng)))
//		}
//	}
//
// A path resolves in the one document of the source, the one
// [Source.Document] returns, from its root whether it starts at `$` or
// `@`, so a
// check on a configuration file binds its errors here as it would through
// [Node.Bind] of that root:
//
//	return source.Bind(check(cfg))
//
// A path in a source that holds several documents resolves nowhere. The
// bound error keeps its message and the name of the source,
// [SourceError.Unresolved] returns [ErrPathNeedsDocument] wrapping the
// reason [Source.Document] gives, and [FormatError] names it in place of
// the excerpt. Bind such an error through [Node.Bind] with the document
// the caller checked it against. A Node from [Node.At] binds through
// Node.Bind too and resolves each `@` path from its own scope.
//
// In every other way Bind is [Node.Bind], which describes what comes
// back. [SourceError.Document] returns the document each location fell
// in, whether or not the location resolves there, and nil for an error
// that fell in none, as [SourceError.Node] describes.
func (s *Source) Bind(err error) error {
	return bindTree(err, binder{src: s, route: true})
}

// Lines returns the [line.Lines] of the [Source], which hold its tokens
// in one line per line of text. Line i holds line i+1 of the text, so
// [position.NewFromToken] converts any token of the Source to a position
// in the lines.
//
// The lines never change, so every call returns the same value and the
// [go.jacobcolvin.com/niceyaml/finder.Finder] and
// [go.jacobcolvin.com/niceyaml/diff.Differ] read it as it is. To render the Source,
// take a [line.View] from [Source.View].
//
// A nil Source holds no lines, so Lines returns the zero [line.Lines] for
// it, as it does for a Source of empty text.
func (s *Source) Lines() line.Lines {
	if s == nil {
		return line.Lines{}
	}

	return s.lines
}

// View returns a new [*line.View] over [Source.Lines] with no decoration.
// Each call returns a view of its own, so overlays and annotations added to
// one never reach the Source or another view. Render the view to see them.
// The view of a nil Source holds no lines.
func (s *Source) View() *line.View {
	return line.NewView(s.Lines())
}
