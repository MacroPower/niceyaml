package niceyaml

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"sync"

	"github.com/goccy/go-yaml"
	"github.com/goccy/go-yaml/ast"
	"github.com/goccy/go-yaml/parser"
	"github.com/goccy/go-yaml/token"

	"go.jacobcolvin.com/niceyaml/internal/aliasing"
	"go.jacobcolvin.com/niceyaml/internal/bom"
	"go.jacobcolvin.com/niceyaml/internal/decodetree"
	"go.jacobcolvin.com/niceyaml/internal/docparse"
	"go.jacobcolvin.com/niceyaml/internal/escape"
	"go.jacobcolvin.com/niceyaml/internal/limitread"
	"go.jacobcolvin.com/niceyaml/internal/multierr"
	"go.jacobcolvin.com/niceyaml/internal/pathindex"
	"go.jacobcolvin.com/niceyaml/internal/quantity"
	"go.jacobcolvin.com/niceyaml/internal/reftable"
	"go.jacobcolvin.com/niceyaml/line"
	"go.jacobcolvin.com/niceyaml/paths"
	"go.jacobcolvin.com/niceyaml/position"
	"go.jacobcolvin.com/niceyaml/tokens"
)

// Source is a YAML file, one stream of text that holds one or more YAML
// documents. It holds the text it read, the tokens that the lexer made
// from the text, and the [*ast.File] they parse into. It also holds the
// settings for parsing and for reporting errors. [Source.Documents]
// returns the root [*Node] of each document. [Source.Document] returns
// the root of a file that holds one document, and decoding and
// validation start at that root. The Documents section of the package
// documentation describes how a Source divides a file into documents. A
// Source never changes after creation, so it is safe for concurrent use.
//
// The zero Source holds one empty document and does not hold any
// tokens. A nil Source behaves like a Source without text or options.
// [Source.Name], [Source.FilePath], [Source.FS], [Source.Text],
// [Source.Offset], [Source.Tokens], and [Source.Lines] return zero values
// for it, [Source.View] returns a view without lines, and
// [Source.ShowsExcerpts] reports true. The methods that need the parse
// panic on a nil Source. They are [Source.Documents],
// [Source.AllDocuments], [Source.Document], [Source.File],
// [Source.DecodeInto], [Source.Decode], [Source.SelfValidate],
// [Source.ValidateDocuments], and [Source.Bind] of an error that is not
// nil.
//
// [ErrorTree.Source] returns nil for an error that is not bound to a
// Source and for one that [Detach] detached. A report thus reads the
// lines of the Source of each problem without checking for nil.
// [ErrorTree.Name] returns the name of the source for a live binding and
// a detached one alike.
//
// Create instances with [NewSourceFromFile], [NewSourceFromFS],
// [NewSourceFromReader], [NewSourceFromBytes], [NewSourceFromString],
// [NewSourceFromTokens], or [NewSourceFromLayers].
type Source struct {
	// The error of a text past the byte limit, or nil. The error fails the
	// one document of the Source.
	limitErr error
	// Holds the layers [NewSourceFromLayers] merged into the Source, or nil.
	// An error bound in the merged document binds in a layer instead.
	layers *layering
	// Sends each error bound in the Source to the document that its text
	// stands for, or nil. Only a working copy, such as the one that a load
	// of the manifest package edits, has a relocation.
	relocation *relocation
	// Holds the text the Source read, which [Source.Text] returns. A
	// Source built from tokens did not read any text, and hasInput is
	// false for it.
	input string
	lines line.Lines
	// The parse of the tokens, made on the first call that needs it.
	parsed lazy[*parsedFile]
	// The second parse of the tokens, for the decoder, as reparse
	// describes.
	decodeParse lazy[*docparse.Result]
	// The index that maps a position to an offset in input, built on the
	// first call of [Source.Offset].
	offsets lazy[bom.Index]
	// The stream that [Source.Tokens] rebuilds from lines.
	stream lazy[token.Tokens]
	// The anchors an alias of the documents finds in the reference
	// documents, as referenceAnchors builds them.
	refAnchors lazy[*reftable.Table]
	// The anchors a Source that reads this one through WithReferences
	// finds in it, as exportedAnchors builds them.
	exported lazy[*reftable.Table]
	// The counts in brackets that the text of the Source spells. A new
	// anchor name in a Source that reads this one must not spell any of
	// them.
	spellings lazy[*decodetree.Spellings]
	// The spellings of the reference documents, as referenceSpellings
	// collects them.
	refSpellings lazy[[]*decodetree.Spellings]
	sourceConfig
	hasInput bool
}

// lazy holds a value that get computes on the first call. Its zero value
// is ready, so the zero [Source] works.
type lazy[T any] struct {
	v    T
	once sync.Once
}

// get returns the value, which f computes on the first call.
func (l *lazy[T]) get(f func() T) T {
	l.once.Do(func() { l.v = f() })

	return l.v
}

// SourceOption configures [Source] creation.
//
// Available options:
//   - [WithName]
//   - [WithFilePath]
//   - [WithFS]
//   - [WithAllowDuplicateKeys]
//   - [WithAliasLimit]
//   - [WithReferences]
//   - [WithUnmarshalers]
//   - [WithExcerpts]
//   - [WithByteLimit]
//   - [WithDepthLimit]
//
// [WithAllowDuplicateKeys], [WithReferences], and [WithAliasLimit]
// describe the documents themselves, so the Source applies them to every
// decode and every validation of its documents. [WithUnmarshalers]
// states how the types of the program read the documents of this Source.
// Every decode of those documents applies it too, wherever the decode
// runs. Every setting that depends on the target of one decode, such as
// [WithUnknownFields], stays a [DecodeOption] of each call.
//
// [WithExcerpts] sets whether errors show the text in excerpts, for
// every renderer of the errors of the Source. [ExcerptOption] sets how
// the renderer draws an excerpt.
type SourceOption func(*sourceConfig)

// sourceConfig holds the settings a [SourceOption] configures. An option
// takes it in place of the [Source] that embeds it, so only a constructor
// can apply one.
type sourceConfig struct {
	// The error of the parse of a reference document from WithReferences.
	// The error fails every decode of the Source.
	referenceErr error
	// The file system that filePath names a file in, or nil when the
	// options did not set one. With nil, filePath names a file on disk.
	fsys     fs.FS
	name     string
	filePath string
	// Holds each Source that WithReferences gave, in the order given.
	// Each of them holds its own references.
	references []*Source
	// Holds the unmarshalers of WithUnmarshalers, in the order given.
	// Every decode of the documents applies them ahead of its own
	// options.
	unmarshalers []Unmarshaler
	// The bounds on the text that aliases write out for a Source with
	// noLimits whose text holds the aliases of another document, such as
	// the working copy of a manifest.
	aliasBudget aliasing.Budget
	// The limits from WithByteLimit and WithDepthLimit, each when its
	// option set it. A zero limit turns the check off.
	depthLimit int
	byteLimit  int
	// Whether errors must not show the text. Excerpts then do not hold any
	// line of the Source.
	noExcerpts bool
	// Turns off the alias limit for every reader of the documents.
	skipAliasLimit bool
	// Whether WithByteLimit and WithDepthLimit set their limits.
	byteLimitSet  bool
	depthLimitSet bool
	// Says the Source holds text that niceyaml wrote itself, such as the
	// merged text of layers. Such a Source skips the byte limit and the
	// depth limit, and the bounds they put on aliases.
	noLimits bool
	// Whether a reference Source, or a Source that it reads, has excerpts
	// off. Excerpts then do not hold any line of the text of a reference
	// document. The lines of the Source do not hold that text, but the
	// document that [NewSourceFromLayers] builds does.
	noReferenceExcerpts bool
	// Whether the parser accepts a mapping that holds the same key twice.
	allowDuplicateKeys bool
}

// The limits a [Source] applies when its options do not set them.
const (
	// DefaultByteLimit is the most bytes of text a [Source] holds unless
	// [WithByteLimit] sets another limit.
	DefaultByteLimit = 256 << 10
	// DefaultDepthLimit is the most levels a document of a [Source] nests
	// unless [WithDepthLimit] sets another limit.
	DefaultDepthLimit = 100
)

// bytes returns the byte limit of c, or zero when the check is off.
func (c *sourceConfig) bytes() int {
	switch {
	case c.noLimits:
		return 0
	case c.byteLimitSet:
		return c.byteLimit
	default:
		return DefaultByteLimit
	}
}

// depth returns the depth limit of c, or zero when the check is off.
func (c *sourceConfig) depth() int {
	switch {
	case c.noLimits:
		return 0
	case c.depthLimitSet:
		return c.depthLimit
	default:
		return DefaultDepthLimit
	}
}

// budget returns the bounds that the limits of c put on the text that
// the aliases of a document write out, as [aliasing.Budget] describes.
func (c *sourceConfig) budget() aliasing.Budget {
	if c.noLimits {
		return c.aliasBudget
	}

	return aliasing.Budget{Bytes: c.bytes(), Depth: c.depth()}
}

// WithByteLimit is a [SourceOption] that sets the most bytes of text the
// [Source] holds. Without the option, the limit is [DefaultByteLimit]. A
// limit of 0 turns the check off, and a negative limit counts as 0.
//
// The go-yaml parser takes time and memory that grow faster than the text
// of one document. The limit thus bounds the slowest file that a program
// reads. A program that reads large files, such as Kubernetes custom
// resource definitions, raises the limit, and its slowest file then takes
// longer:
//
//	source, err := niceyaml.NewSourceFromFile(path, niceyaml.WithByteLimit(1<<20))
//
// [NewSourceFromFile], [NewSourceFromFS], and [NewSourceFromReader] read
// at most one byte past the limit. When the text holds more, they return a
// nil Source and an error that matches [ErrLimit]. The error is a
// [*SourceError] that names the Source as [WithName] or the constructor
// names it, as in "app.yaml: file is larger than 256 KiB". For a text past
// the limit, [NewSourceFromString] and [NewSourceFromBytes] keep the text
// and skip the lexer. Such a Source does not hold any lines, and its one
// document fails with the same error, as a document that does not parse
// does. A reference document past a limit fails every decode of the Source
// that reads it through [WithReferences].
//
// The limit also bounds the text that the aliases of a document repeat,
// for a decode into a type that reads text anywhere and for
// [NewSourceFromLayers]. Such a call checks the aliases of the whole
// document before it writes any text, as the Limits section of the package
// documentation describes. The merged text of NewSourceFromLayers skips the
// limit, because it holds at most its layers and the text that the limit
// lets their aliases repeat.
func WithByteLimit(n int) SourceOption {
	return func(c *sourceConfig) {
		c.byteLimit, c.byteLimitSet = max(0, n), true
	}
}

// WithDepthLimit is a [SourceOption] that sets the most levels a document
// of the [Source] nests. Without the option, the limit is
// [DefaultDepthLimit]. A limit of 0 turns the check off, and a negative
// limit counts as 0.
//
// The depth counts each mapping and each sequence on the way from the root
// of the document to its deepest value. A flow collection opens a level
// at its "[" or "{", and a block collection opens one at its first key or
// "-". An alias does not open a level of the document. The memory that
// the go-yaml parser keeps grows with the square of the depth, so the
// check reads the tokens of each document before the parser runs.
//
// A document past the limit fails as a document that does not parse
// does. [Node.Err] returns its error, [Node.AST] returns nil, and the
// other documents of the Source still parse. Its error matches [ErrLimit]
// and not [ErrSyntax], because the check runs before the parser. The
// error binds at the token that opens the first level past the limit, as
// in "document nests more than 100 levels". A reference document past the
// limit fails every decode of the Source that reads it through
// [WithReferences].
//
// The limit also bounds the levels that the aliases of a document add, for
// a decode into a type that reads text anywhere and for
// [NewSourceFromLayers]. Such a call checks the aliases of the whole
// document before it writes any text, as the Limits section of the package
// documentation describes. The merged text of NewSourceFromLayers skips the
// limit, because each layer passed it with its aliases written out.
func WithDepthLimit(n int) SourceOption {
	return func(c *sourceConfig) {
		c.depthLimit, c.depthLimitSet = max(0, n), true
	}
}

// newSourceConfig returns the settings of opts applied in order.
func newSourceConfig(opts []SourceOption) sourceConfig {
	var c sourceConfig

	for _, opt := range opts {
		opt(&c)
	}

	return c
}

// limitError returns the error of a text past the byte limit n. The error
// matches [ErrLimit] and declares the document at fault.
func limitError(n int) error {
	return Invalid(&markedError{err: errors.New("file is larger than " + quantity.Bytes(n)), marks: []error{ErrLimit}})
}

// readLimited reads r to its end, or to one byte past limit when limit is
// greater than zero. It returns the error of [limitError] for a text past
// the limit.
func readLimited(r io.Reader, limit int) ([]byte, error) {
	if limit <= 0 {
		return io.ReadAll(r) //nolint:wrapcheck // The caller wraps the error.
	}

	data, err := limitread.ReadAll(r, limit)
	if err != nil {
		return nil, err //nolint:wrapcheck // The caller wraps the error.
	}

	if len(data) > limit {
		return nil, limitError(limit)
	}

	return data, nil
}

// WithName is a [SourceOption] that sets the name of the [Source].
// [SourceError.Error] puts the name in front of the position of every
// error bound to the Source, as in "name:line:col: msg". Without the
// option, [Source.Name] returns the path [NewSourceFromFile] took, or
// else the file path. A message draws each control character of the name
// as its picture, so a file name that holds a line feed stays on one
// line. Source.Name returns the name as given.
func WithName(name string) SourceOption {
	return func(c *sourceConfig) {
		c.name = name
	}
}

// WithFilePath is a [SourceOption] that sets the file path for the [Source].
// Each document of the Source reports the path from [Node.FilePath].
// Schema matchers route on the path, and a schema directive resolves
// beside it.
//
// The Source keeps path as given. A relative path suits a document that
// is not a file on disk, such as a request body or a blob of a
// repository. A file path pattern matches such a path as written. For a
// file on disk, pass an absolute path, because each later reader resolves
// a relative one against the working directory it finds.
//
// A schema directive resolves a relative reference beside a relative
// path only in the file system [WithFS] gives the Source. Without one,
// the reference fails with
// [go.jacobcolvin.com/niceyaml/schema.ErrNoBaseDir], whatever the
// working directory holds. A program that validates a document from
// another trust domain confines its schema registry, as the Untrusted
// Documents section of the [go.jacobcolvin.com/niceyaml/schema] package
// documentation describes.
//
// [NewSourceFromFile] sets the absolute path of the file it reads, and
// [NewSourceFromFS] sets the path in its file system, so neither needs
// the option.
func WithFilePath(path string) SourceOption {
	return func(c *sourceConfig) {
		c.filePath = path
	}
}

// WithFS is a [SourceOption] that sets the file system the file path of
// the [Source] names a file in. Each document of the Source reports the
// file system from [Node.FS]. A schema reference written beside the
// document, such as a yaml-language-server directive, then resolves in
// that file system rather than as a path on disk. A schema registry reads
// each path that a directive names from fsys, so fsys decides which files
// the directive can read. The
// [go.jacobcolvin.com/niceyaml/schema.Access] of the registry does not
// apply to those reads, but each URL still goes through it. An
// [os.DirFS] serves the files that its symbolic links lead to, wherever
// they are. The FS of an [os.Root] refuses a link that leads out of its
// directory.
//
// [NewSourceFromFS] sets the file system. A program that reads the bytes
// itself passes WithFS beside [WithFilePath], so a Source built from those
// bytes resolves as one that NewSourceFromFS opened:
//
//	source := niceyaml.NewSourceFromBytes(data,
//		niceyaml.WithFilePath("configs/app.yaml"), niceyaml.WithFS(bundle))
//
// A schema registry distinguishes one file system from another by its
// value, so fsys must be a value that Go can compare or a map. Values that
// Go can compare include an [embed.FS] and a pointer, and the registry
// knows a map by the map itself.
// [go.jacobcolvin.com/niceyaml/schema.FileFS] gives the rule. A schema
// beside a document in a file system of any other type does not load.
//
// A registry holds fsys for as long as it keeps an entry from it, as
// FileFS describes. A program that builds a file system per document
// therefore bounds the registry with
// [go.jacobcolvin.com/niceyaml/schema.WithCacheLimit]. It can instead pass
// each file system to [go.jacobcolvin.com/niceyaml/schema.Registry.Forget]
// after it stops using the file system.
//
// Without the option, and with a nil fsys, the file path names a file on
// disk.
func WithFS(fsys fs.FS) SourceOption {
	return func(c *sourceConfig) {
		c.fsys = fsys
	}
}

// WithAllowDuplicateKeys is a [SourceOption] that sets whether the parser
// accepts a mapping that holds the same key twice when [Source.File]
// parses the document. With the option on, the last value wins. The
// default is false, and a duplicate key is then an error that matches
// [ErrSyntax].
//
// The option reaches the parser alone. A decode keeps the later of two
// entries that it reads as one key, whatever the option says. A mapping
// thus overrides a key that a `<<` merge adds:
//
//	defaults: &defaults {host: localhost, port: 80}
//	server:
//	  <<: *defaults
//	  port: 8080
//
// The parser compares the keys of a mapping by their text, so it accepts
// two keys of different text that a decode reads as one. `&k name` beside
// `*k` is such a pair, and so is 1 beside 0x1 in a map with string keys.
// The decode keeps the later entry of each.
//
// A reference document of [WithReferences] parses with the option of its
// own [Source]. A duplicate key there that its Source does not allow
// fails every decode of a Source that reads it.
func WithAllowDuplicateKeys(allow bool) SourceOption {
	return func(c *sourceConfig) {
		c.allowDuplicateKeys = allow
	}
}

// WithAliasLimit is a [SourceOption] that sets whether the alias limit
// applies to the documents of the [Source]. The default is true. The
// limit refuses a document whose aliases make a reader read far more
// than the document holds. With the limit on, a decode, a
// [go.jacobcolvin.com/niceyaml/schema.Schema], and a
// [go.jacobcolvin.com/niceyaml/schema/matcher.Content] or
// [go.jacobcolvin.com/niceyaml/schema/matcher.Text] matcher refuse a
// node of such a document that holds an alias. The error matches
// [ErrExcessiveAliasing], and [IsInvalid] reports it.
//
// The limit follows the rule of gopkg.in/yaml.v3, which weighs the share
// of the nodes that aliases repeat rather than a size, so the option takes
// a bool. [WithByteLimit] and [WithDepthLimit] set the sizes that also
// bound the text that aliases write out.
//
// With the limit off, a few hundred bytes of nested aliases can keep a
// decode running for minutes, because the go-yaml decoder never checks
// the context. They can also keep a schema validation running for
// seconds and using hundreds of MiB. Turn the limit off only for input
// the program trusts, such as a file it ships:
//
//	source, err := niceyaml.NewSourceFromFile(path, niceyaml.WithAliasLimit(false))
//
// The option also turns off the bounds that [WithByteLimit] and
// [WithDepthLimit] put on the text the aliases of a document write out.
// The option covers the documents of the Source alone. The limit does not
// count the reference documents of [WithReferences]. The option on a
// reference Source thus does not change the limit of a Source that reads
// it. [go.jacobcolvin.com/niceyaml/schema.Schema.ValidateValue] and
// [Node.Nodes] keep their limits whatever the option says. The Alias
// Limit section of the package documentation describes each limit.
func WithAliasLimit(enabled bool) SourceOption {
	return func(c *sourceConfig) {
		c.skipAliasLimit = !enabled
	}
}

// WithReferences is a [SourceOption] that lets an alias in the documents
// of the [Source] name an anchor that the documents of refs define, such
// as a file of shared defaults. Every decode and every validation of
// those documents reads the anchors. Those include [Node.Decode],
// [Node.Validate], [Node.SelfValidate], [Source.ValidateDocuments], and
// the decode a [Validator] runs on the Node it gets. Pass each reference
// file as a Source of its own:
//
//	source, err := niceyaml.NewSourceFromFile("app.yaml", niceyaml.WithReferences(defaults))
//
// When two of refs define the same anchor name, their order decides which
// anchor wins. WithReferences skips a nil Source. It parses each Source
// of refs, and a reference document that does not parse fails every
// decode of the Source with the error of that parse.
//
// Every Source that reads a reference Source shares its parse. A decode
// reads the anchors of refs that its aliases reach, so its cost grows
// with those anchors rather than with the size of refs. One call to
// [Node.Decode] or [Node.Validate] can run several decodes. Those include
// one for each validator that decodes and one for each
// [go.jacobcolvin.com/niceyaml/schema/matcher.Content] matcher of a
// schema registry. Each of them reads the anchors it reaches.
//
// The References section of the package documentation describes how an
// alias finds its anchor and what a reference Source brings. It also
// describes how a path and an error treat an alias to a reference
// document.
func WithReferences(refs ...*Source) SourceOption {
	return func(c *sourceConfig) {
		for _, ref := range refs {
			if ref == nil {
				continue
			}

			c.references = append(c.references, ref)

			if c.referenceErr == nil {
				c.referenceErr = ref.referenceErr
			}

			if c.referenceErr == nil {
				_, c.referenceErr = ref.File()
			}

			if ref.noExcerpts || ref.noReferenceExcerpts {
				c.noReferenceExcerpts = true
			}
		}
	}
}

// WithUnmarshalers is a [SourceOption] that gives every decode of the
// documents of the [Source] the unmarshalers us. The unmarshalers set how
// the types of the program read YAML. With the cidr [Unmarshaler] that
// [CustomUnmarshaler] shows, every decode of the file reads a
// [net.IPNet] from its CIDR text:
//
//	source, err := niceyaml.NewSourceFromFile(path, niceyaml.WithUnmarshalers(cidr))
//
// Every decode of the documents applies them, wherever it runs. Those
// decodes include [Node.Decode], [Node.DecodeInto], [Node.DecodeAt],
// [Node.DecodeIfPresent], [Source.Decode], [Source.DecodeInto], the
// decode a [Validator] runs on the Node it gets, and the decode function
// that a CustomUnmarshaler function gets. [Node.SelfValidate] and
// [Source.SelfValidate] spell each map key with them. The unmarshalers
// also reach a decode in code that did not build the Source, such as a
// library that the program passes the Source to. [SelfValidateValue]
// does not read a Source, so the unmarshalers do not reach it.
//
// Each decode applies the unmarshalers us first, and the unmarshalers of
// [WithUnmarshaler] in the call apply over them. A call can thus replace
// the function the Source gives a type, or remove it with a nil
// function. It can also turn the UnmarshalJSON methods off with
// JSONUnmarshalers(false). Each such change holds for that call alone.
//
// Each WithUnmarshalers adds to the ones before it, as the options of one
// call do. A later function for a type thus replaces an earlier one, and
// the last [JSONUnmarshalers] applies. WithUnmarshalers copies the slice
// us, so the option keeps its unmarshalers when the caller changes the
// slice it passed.
//
// A Source from [NewSourceFromLayers] applies the unmarshalers of
// [MergeUnmarshalers]. A merge without that option takes the unmarshalers
// of the layer [MergeAs] names, or else of the lowest layer.
func WithUnmarshalers(us ...Unmarshaler) SourceOption {
	us = slices.Clone(us)

	return func(c *sourceConfig) {
		c.unmarshalers = slices.Concat(c.unmarshalers, us)
	}
}

// WithExcerpts is a [SourceOption] that sets whether errors show the text
// of the [Source] in excerpts. The default is true. Pass false for a
// Source whose text holds secrets, such as the values a program read
// from its environment.
//
// An error bound in a Source with excerpts off keeps its position, its
// path, and its message, as in "environment:3:9: $.db.port: port must be
// at least 1". [SourceError.Excerpt] reports false for it, and its
// [ErrorReport] does not hold an excerpt of the Source. [FormatError],
// the %+v verb, and
// [go.jacobcolvin.com/niceyaml/printer.Printer.PrintError] print its tree
// and leave out the excerpt. An error with bindings in several sources
// keeps the excerpt of each other source. A renderer that builds an
// excerpt of its own checks [Source.ShowsExcerpts] first.
//
// The option covers the excerpts this module builds, but the text still
// reaches a reader in other ways. [Annotate], [Source.View],
// [Source.Lines], [Source.Tokens], and the methods of each [Node] hand
// the text to the caller as they do for any Source. A message quotes the
// value it rejects, as in `time: invalid duration "sk-live-abcdef"`. The
// path of an error spells each key above it, and the message for a
// missing anchor names the alias. The go-yaml error that [errors.As]
// finds behind a syntax error or a decode error holds an excerpt that
// go-yaml builds. That excerpt prints when a program prints the go-yaml
// error, and an UnmarshalYAML method that returns such an error puts the
// excerpt in the message.
//
// The Source that [NewSourceFromLayers] builds has excerpts off when the
// Source of any layer, or of a reference document a layer reads through
// [WithReferences], has them off.
func WithExcerpts(enabled bool) SourceOption {
	return func(c *sourceConfig) {
		c.noExcerpts = !enabled
	}
}

// NewSourceFromFile creates a new [*Source] by reading a file from disk.
//
// It names the [Source] by path as given, so [Source.Name] and every
// message spell the file as the caller did. It sets the file path of the
// Source to the absolute form of path that [filepath.Abs] builds from the
// working directory at the time of the read. Schema routing reads that
// file path, and a schema directive resolves beside it on disk. A later
// change of working directory thus moves neither the name nor the file
// path. [WithName] and [WithFilePath] replace them.
//
// Abs removes a ".." element together with the element before it, as
// text. When the element before a ".." element is a symbolic link, the
// file path then names a directory that the read did not open. In that
// case, pass WithFilePath with the path that the program resolved itself.
//
// NewSourceFromFile returns a nil Source and an error when it cannot read
// the file. A file past the byte limit gives an error that names the
// Source, as [WithByteLimit] describes.
func NewSourceFromFile(path string, opts ...SourceOption) (*Source, error) {
	// Abs fails only when the working directory has no name, and the path
	// as given is then the closest one left.
	filePath, err := filepath.Abs(path)
	if err != nil {
		filePath = path
	}

	// Prepend the name and file path options so user options can
	// override them if needed.
	cfg := newSourceConfig(append([]SourceOption{WithName(path), WithFilePath(filePath)}, opts...))

	data, err := readFile(path, cfg.bytes())
	if errors.Is(err, ErrLimit) {
		return nil, limitSourceError(cfg, err)
	}

	if err != nil {
		return nil, err
	}

	return newStringSource(string(data), cfg), nil
}

// NewSourceFromFS creates a new [*Source] by reading the file at path
// from fsys, such as an [embed.FS] that ships configuration with the
// binary or an [fs.FS] a test builds. It sets the file path of the
// [Source] to path as given, the path from the root of fsys. Each
// document reports that path for schema routing. It sets the file system
// too, as [WithFS] does, so a schema directive in a document resolves
// beside the document in fsys, whatever registry validates it:
//
//	source, err := niceyaml.NewSourceFromFS(bundle, "configs/app.yaml")
//
// A directive there that names ./schema.json reads configs/schema.json
// from bundle. A document that [NewSourceFromFile] opens resolves its
// directive on disk, so one registry validates both. The registry holds
// fsys for as long as it keeps an entry from it, as [WithFS] describes.
//
// NewSourceFromFS returns a nil Source and an error when it cannot read
// the file. A file past the byte limit gives an error that names the
// Source, as [WithByteLimit] describes.
func NewSourceFromFS(fsys fs.FS, path string, opts ...SourceOption) (*Source, error) {
	cfg := newSourceConfig(append([]SourceOption{WithFilePath(path), WithFS(fsys)}, opts...))

	f, err := fsys.Open(path)
	if err != nil {
		return nil, fmt.Errorf("read file: %w", err)
	}

	defer f.Close() //nolint:errcheck // A read-only file has nothing to flush.

	data, err := readLimited(f, cfg.bytes())
	if errors.Is(err, ErrLimit) {
		return nil, limitSourceError(cfg, err)
	}

	if err != nil {
		return nil, fmt.Errorf("read file: %w", err)
	}

	return newStringSource(string(data), cfg), nil
}

// NewSourceFromReader creates a new [*Source] by reading all of r, such as
// standard input or the body of an HTTP request. Only [WithFilePath] gives
// the Source a file path, and [WithName] names it in output:
//
//	source, err := niceyaml.NewSourceFromReader(os.Stdin, niceyaml.WithName("<stdin>"))
//
// NewSourceFromReader returns a nil Source and an error when it cannot
// read r. A text past the byte limit gives an error that names the Source,
// as [WithByteLimit] describes.
func NewSourceFromReader(r io.Reader, opts ...SourceOption) (*Source, error) {
	cfg := newSourceConfig(opts)

	data, err := readLimited(r, cfg.bytes())
	if errors.Is(err, ErrLimit) {
		return nil, limitSourceError(cfg, err)
	}

	if err != nil {
		return nil, fmt.Errorf("read: %w", err)
	}

	return newStringSource(string(data), cfg), nil
}

// NewSourceFromBytes creates a new [*Source] from raw YAML bytes.
func NewSourceFromBytes(data []byte, opts ...SourceOption) *Source {
	return NewSourceFromString(string(data), opts...)
}

// NewSourceFromString creates a new [*Source] from a YAML string using
// [tokens.Tokenize]. Tokenize drops the byte order marks YAML allows, so
// the tokens and lines of the Source do not hold any mark. A key thus
// does not read a mark as part of its text. The Source keeps src as it
// is, and [Source.Text] returns it with every mark.
//
// A text past the byte limit, as [WithByteLimit] sets it, skips the lexer.
// The Source then does not hold any lines, and its one document fails
// with an error that matches [ErrLimit].
func NewSourceFromString(src string, opts ...SourceOption) *Source {
	return newStringSource(src, newSourceConfig(opts))
}

// newStringSource returns the [Source] that [NewSourceFromString] creates
// from src with the settings cfg. Each constructor resolves its options
// once and hands the result here, so an option runs once per Source.
func newStringSource(src string, cfg sourceConfig) *Source {
	if limit := cfg.bytes(); limit > 0 && len(src) > limit {
		return &Source{sourceConfig: cfg, input: src, hasInput: true, limitErr: limitError(limit)}
	}

	s := newTokenSource(tokens.Tokenize(src), cfg)
	s.input, s.hasInput = src, true

	return s
}

// limitSourceError returns err, the error of a text past the byte limit,
// bound to a [Source] of cfg that does not hold the text. The error thus
// names the Source as the error of a Source from [NewSourceFromString]
// names it.
func limitSourceError(cfg sourceConfig, err error) error {
	return (&Source{sourceConfig: cfg, limitErr: err}).Bind(err)
}

// readFile reads the file at path from disk, to one byte past limit when
// limit is greater than zero, as [readLimited] reads it.
func readFile(path string, limit int) ([]byte, error) {
	f, err := os.Open(path) //nolint:gosec // User-provided file paths are intentional.
	if err != nil {
		return nil, fmt.Errorf("read file: %w", err)
	}

	defer f.Close() //nolint:errcheck // A read-only file has nothing to flush.

	data, err := readLimited(f, limit)
	if errors.Is(err, ErrLimit) {
		return nil, err
	}

	if err != nil {
		return nil, fmt.Errorf("read file: %w", err)
	}

	return data, nil
}

// NewSourceFromTokens creates a new [*Source] from [token.Tokens].
// See [line.NewLines] for details on token splitting behavior.
//
// The Source holds clones of the tokens, and [tokens.ResetPositions]
// resets their positions, so the text it holds counts its lines from 1 as
// [tokens.Tokenize] does. Line i of [Source.Lines] holds line i+1 of the
// text. Tokens that count from 1 already, as a whole stream does, keep
// their positions unless the lexer dropped the first text, as
// [tokens.ResetPositions] describes. ResetPositions renumbers tokens cut
// from a longer stream, such as those that [Node.Tokens] returns, from
// the first one. To render one document of a file with the file's line
// numbers, print the file's view with [Node.Span] instead.
//
// The Source does not read any text, so [Source.Text] returns an empty
// string for it and [Source.Offset] reports false.
func NewSourceFromTokens(tks token.Tokens, opts ...SourceOption) *Source {
	return newTokenSource(tks, newSourceConfig(opts))
}

// newTokenSource returns the [Source] that [NewSourceFromTokens] creates
// from tks with the settings cfg.
func newTokenSource(tks token.Tokens, cfg sourceConfig) *Source {
	return &Source{lines: line.NewLines(tokens.ResetPositions(tks)), sourceConfig: cfg}
}

// parserOptions returns the options the Source parses its tokens with.
func (s *Source) parserOptions() []parser.Option {
	if s.allowDuplicateKeys {
		return []parser.Option{parser.AllowDuplicateMapKey()}
	}

	return nil
}

// decodeOptions returns the options of every decode of a document of the
// Source.
func (s *Source) decodeOptions() []yaml.DecodeOption {
	// The decoder counts a key that a `<<` merge brings in beside the
	// keys of the mapping, so its check rejects a mapping that overrides
	// a merged key. The parser rejects a key a mapping holds twice.
	return []yaml.DecodeOption{yaml.AllowDuplicateMapKey()}
}

// referenceAnchors returns the anchors of the reference documents of the
// Source, or nil for a Source without reference documents. An alias of
// its documents reads them when it names an anchor that does not appear
// before it. The first call builds the table over the tables of the
// reference Sources, which each of them builds once.
func (s *Source) referenceAnchors() *reftable.Table {
	if len(s.references) == 0 {
		return nil
	}

	return s.refAnchors.get(func() *reftable.Table {
		tables := make([]*reftable.Table, len(s.references))
		for i, ref := range s.references {
			tables[i] = ref.exportedAnchors()
		}

		return reftable.New(nil, tables...)
	})
}

// exportedAnchors returns the anchors a Source finds in s when it reads s
// through [WithReferences]. Those are the anchors of the documents of s
// over those of the reference documents of s.
func (s *Source) exportedAnchors() *reftable.Table {
	return s.exported.get(func() *reftable.Table {
		var bodies []ast.Node

		for _, doc := range s.documents() {
			if doc.doc.err == nil && doc.doc.root != nil {
				bodies = append(bodies, doc.doc.root.Body)
			}
		}

		return reftable.New(bodies, s.referenceAnchors())
	})
}

// referenceSpellings returns the [decodetree.Spellings] of the reference
// documents, those of each reference Source included, each once.
func (s *Source) referenceSpellings() []*decodetree.Spellings {
	return s.refSpellings.get(func() []*decodetree.Spellings {
		var all []*decodetree.Spellings

		// Two reference Sources can read the same Source, and the list
		// holds its Spellings once.
		seen := map[*decodetree.Spellings]bool{}

		for _, ref := range s.references {
			for _, sp := range slices.Concat(ref.referenceSpellings(), []*decodetree.Spellings{ref.ownSpellings()}) {
				if !seen[sp] {
					seen[sp] = true
					all = append(all, sp)
				}
			}
		}

		return all
	})
}

// ownSpellings returns the [decodetree.Spellings] of the text of the
// Source, which the first call builds.
func (s *Source) ownSpellings() *decodetree.Spellings {
	return s.spellings.get(func() *decodetree.Spellings {
		return decodetree.NewSpellings(spellingTexts(s.Tokens()))
	})
}

// Name returns the name of the [Source]: the one [WithName] set, the
// path [NewSourceFromFile] read as its caller gave it, or else the file
// path. It returns an empty string when the Source does not have any of
// them, and for a nil Source.
func (s *Source) Name() string {
	if s == nil {
		return ""
	}

	if s.name == "" {
		return s.filePath
	}

	return s.name
}

// FilePath returns the file path of the [Source], which schema routing
// reads. It is the absolute path of the file [NewSourceFromFile] read,
// the path in the file system [NewSourceFromFS] read from, or the path
// [WithFilePath] set, as given. A message or a report names the file
// with [Source.Name], which spells it as the caller did.
//
// FilePath returns an empty string when none of those set it, and for a
// nil Source.
func (s *Source) FilePath() string {
	if s == nil {
		return ""
	}

	return s.filePath
}

// FS returns the file system the file path of the [Source] names a file
// in, which [NewSourceFromFS] or [WithFS] sets. It returns nil when
// neither set one, and the file path then names a file on disk. It also
// returns nil for a nil Source. A Source that WithFS gave a file system
// but not a path returns that file system.
func (s *Source) FS() fs.FS {
	if s == nil {
		return nil
	}

	return s.fsys
}

// ShowsExcerpts reports whether errors show the text of the [Source] in
// excerpts, as [WithExcerpts] sets. A renderer that builds an excerpt of
// its own, from [Source.View] and [Annotate], checks ShowsExcerpts first.
// A nil Source does not hold any text, so ShowsExcerpts reports true for
// it, as it does for a Source without options.
func (s *Source) ShowsExcerpts() bool {
	return s == nil || !s.noExcerpts
}

// Text returns the text the [Source] read, byte for byte. It keeps the
// bytes that the lines of the Source drop or rewrite. Those are a byte
// order mark, the "\r" of a CRLF line ending, the final line ending, and
// each byte that is not valid UTF-8. A program that edits a file in place
// cuts the text at the offsets that [Source.Offset] returns. The example
// cuts around a scalar on one line:
//
//	ranges, err := doc.Ranges(paths.Doc().Child("spec", "replicas"))
//	if err != nil {
//		return err
//	}
//
//	start, _ := source.Offset(ranges[0].Start)
//	end, _ := source.Offset(ranges[0].End)
//
//	text := source.Text()
//	fixed := text[:start] + "5" + text[end:]
//
// A Source that [NewSourceFromTokens] built did not read any text, so
// Text returns an empty string for it, as it does for a nil Source.
func (s *Source) Text() string {
	if s == nil {
		return ""
	}

	return s.input
}

// Offset returns the byte offset in [Source.Text] of the rune at pos, a
// position in the coordinates of [Source.Lines], and true. The offsets
// of the Start and the End of a [position.Range] cut the text the range
// covers out of Source.Text.
//
// A column counts runes, and a line ending takes one column whether it
// is LF, CRLF, or a bare CR, as [line.Lines.Runes] yields it. A byte
// that is not valid UTF-8 takes one column too. A byte order mark that
// [tokens.Tokenize] dropped lies before column 0 of its line, so the
// offset of column 0 is past the mark. Offset counts in the text, so it
// counts text the lexer left out of the lines, as Tokenize describes.
//
// A position can name the end of a line or of the text, as the End of a
// range does. The offset of the column past a line ending is where the
// next line starts. For a text without a final line ending, the offset
// of the column past the last rune is the length of the text. For a text
// with a final line ending, the offset of column 0 of the line after it
// is the length of the text.
//
// Offset reports false for a position outside the text. A position is
// outside the text when it has a negative line or column, a line past
// the last, or a column past the end of its line. Offset also reports
// false for a Source that [NewSourceFromTokens] built and for a nil
// Source, because neither holds any text.
func (s *Source) Offset(pos position.Position) (int, bool) {
	if s == nil || !s.hasInput || pos.Line < 0 || pos.Col < 0 {
		return 0, false
	}

	return s.offsets.get(func() bom.Index { return bom.NewIndex(s.input) }).Offset(pos.Line, pos.Col)
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
// A nil Source does not hold any tokens, so Tokens returns nil for it.
func (s *Source) Tokens() token.Tokens {
	if s == nil {
		return nil
	}

	return slices.Clone(s.stream.get(s.lines.Tokens))
}

// Documents returns the root [*Node] of each YAML document of this
// [Source] that holds content, in file order. It suits a caller that
// decodes or validates each document of a file that holds several:
//
//	for _, doc := range source.Documents() {
//		manifest, err := doc.Decode[Manifest](ctx, strict)
//		if err != nil {
//			return err
//		}
//	}
//
// It leaves out each empty document of a file that holds a document
// with content, such as the one a trailing "---" leaves.
// [Source.AllDocuments] returns every document. For a file without
// content, Documents returns one empty document, which decodes to the
// zero value. The Documents section of the package documentation
// describes which documents are empty.
//
// The Node of a document that did not parse keeps its place in the
// slice. [Node.Err] returns the error of its parse, and so do the methods
// that read its tree, such as [Node.Decode]. The loop in the example thus
// meets each such error where it meets the errors of the documents that
// parsed. [Source.File] returns every such error of the file at once, and
// [Source.ValidateDocuments] reports a single copy of an error that two
// documents share.
//
// The first call parses the Source. Every call returns the same Nodes in
// a new slice, so a caller that reorders the slice does not change the
// Source.
func (s *Source) Documents() []*Node {
	return slices.Clone(s.contentDocuments())
}

// AllDocuments returns the root [*Node] of every YAML document of this
// [Source], in file order, whether the document parsed or not and whether
// it holds content or not. It suits a caller that renders, diffs, or
// reports each document:
//
//	for _, doc := range source.AllDocuments() {
//		if err := doc.Err(); err != nil {
//			log.Print(niceyaml.FormatError(err))
//
//			continue
//		}
//
//		lipgloss.Println(p.Print(doc.View()))
//	}
//
// The Node at index i is the one whose [Node.DocumentIndex] is i. For a
// file in which every document parses, AllDocuments returns the Nodes
// that [Source.Documents] returns when each document of the file holds
// content, or when the file holds one empty document alone.
//
// A "---" header that directly follows an anchor without a value parses
// together with the document before it. A syntax error in either
// document fails both. Both return that error, bound to the one of the
// two that holds its location. A loop that collects the error of each
// document thus holds it twice. [Source.ValidateDocuments] reports it
// once.
//
// Every call returns the same Nodes in a new slice.
func (s *Source) AllDocuments() []*Node {
	return slices.Clone(s.documents())
}

// ValidateDocuments runs v on each document that [Source.Documents]
// returns, in file order, as [Node.Validate] runs it. It joins the errors
// that the documents return. A document that did not parse reports the
// error of its parse, and two documents that parse together report theirs
// once, as [Source.AllDocuments] describes. One call thus reports the
// error of each document that did not parse beside what v reports for
// each document that parsed:
//
//	err := source.ValidateDocuments(ctx, niceyaml.MultiValidator(schema, names))
//	if err != nil {
//		log.Print(niceyaml.FormatError(err))
//	}
//
// It leaves out the empty documents that Source.Documents leaves out. The
// validator v thus runs on the documents with content, or on the one
// empty document of a file without content. A schema that wants a
// mapping then rejects an empty file, unless [SkipEmpty] wraps v. A nil v
// checks only the parse of each document, so with a ctx that has not
// ended, ValidateDocuments returns the error [Source.File] returns.
//
// ValidateDocuments checks ctx before each document and one more time
// after the last. After ctx ends, ValidateDocuments does not validate
// another document. The result then holds the error of ctx bound to the
// Source, unless an error in the result already wraps it. A deadline that
// v reports of its own fails its document alone.
//
// ValidateDocuments takes any number of documents, so a file of two
// documents with content that passes it still fails [Source.Decode] with
// [ErrMultipleDocuments].
func (s *Source) ValidateDocuments(ctx context.Context, v Validator) error {
	var (
		errs []error
		// The error of the parse of the document before, which the next
		// document shares when the two parsed together.
		prev error
	)

	// Each document that did not parse reports the error of its own parse.
	for _, doc := range s.contentDocuments() {
		if ctx.Err() != nil {
			break
		}

		err := doc.Validate(ctx, v)

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

	return multierr.Join(errs)
}

// documents returns the root [*Node] of each YAML document, as
// [Source.AllDocuments] does. It returns the slice that the Source keeps
// rather than a copy, so a caller must not change it.
func (s *Source) documents() []*Node {
	return s.parsed.get(s.parse).docs
}

// contentDocuments returns the roots [Source.Documents] returns, in the
// slice the Source keeps rather than a copy, so a caller must not change it.
func (s *Source) contentDocuments() []*Node {
	return s.parsed.get(s.parse).content
}

// pickContentDocuments returns the documents of docs that are not empty,
// or the first one when all are empty. A file of empty documents thus
// reads as an empty file. An empty document beside content is what a
// separator left.
func pickContentDocuments(docs []*Node) []*Node {
	if !slices.ContainsFunc(docs, func(doc *Node) bool { return !doc.IsEmpty() }) {
		// The result takes none of the capacity of docs, so an append to
		// it replaces no document of docs.
		return docs[:1:1]
	}

	return slices.DeleteFunc(slices.Clone(docs), (*Node).IsEmpty)
}

// Document returns the root [*Node] of a [Source] that holds a single
// YAML document among those that [Source.Documents] returns.
// [Source.Decode] decodes that document in one step. Document suits a
// caller that reads a configuration file in more than one step, such as
// one that scopes a Node with [Node.At] or binds a check with
// [Node.Bind]:
//
//	doc, err := niceyaml.NewSourceFromString(yamlContent).Document()
//	if err != nil {
//		return err
//	}
//
//	config, err := doc.Decode[Config](ctx, niceyaml.WithValidator(validator))
//
// A file that opens with a comment header, ends in "---", or does not
// hold any content still holds a single document, as the Documents
// section of the package documentation describes. [Node.DocumentIndex]
// gives the place of the document among every document of the file.
//
// For a file with a document that does not parse, Document returns the
// error that [Source.File] returns, however many of its other documents
// parse. For a file of more than one document, it returns an error that
// wraps [ErrMultipleDocuments]. The error binds to the Source at the
// header of the second document, or at the first token of its content
// when a "..." marker rather than a header opens it. Use Source.Documents
// for a file that might hold several.
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
// returns. For a Source that holds more than one document, DecodeInto
// returns the error that Source.Document returns. A configuration file
// that must hold one document thus decodes in one step and reports a
// second document as an error.
func (s *Source) DecodeInto(ctx context.Context, v any, opts ...DecodeOption) error {
	doc, err := s.Document()
	if err != nil {
		return err
	}

	return doc.DecodeInto(ctx, v, opts...)
}

// SelfValidate runs the self-validation step of [Source.DecodeInto] on
// its own, on v through the one document of the [Source], as
// [Node.SelfValidate] runs it through the root Node [Source.Document]
// returns. For a Source that holds more than one document, or a document
// that does not parse, SelfValidate returns the error that
// Source.Document returns.
//
// A program that fills v after the decode, such as from its environment,
// runs the step after v is whole:
//
//	var cfg Config
//	if err := source.DecodeInto(ctx, &cfg, niceyaml.WithSelfValidation(false)); err != nil {
//		return err
//	}
//
//	applyEnv(&cfg)
//
//	return source.SelfValidate(ctx, &cfg)
//
// An empty Source, such as one from NewSourceFromString(""), represents
// an optional file that does not exist. Its decode leaves v as it was,
// and each error of the step binds without a position, as in
// "$.servers[1].port: port is required". Another document does not place
// such an error. [SelfValidateValue] validates a value that did not come
// from a document, and it leaves its errors free for a document to place.
func (s *Source) SelfValidate(ctx context.Context, v any, opts ...DecodeOption) error {
	doc, err := s.Document()
	if err != nil {
		return err
	}

	return doc.SelfValidate(ctx, v, opts...)
}

// Decode validates and decodes the one document of the [Source] into a
// new T, as [Node.Decode] decodes the root Node [Source.Document]
// returns. It reads a configuration file in one step:
//
//	source, err := niceyaml.NewSourceFromFile(path)
//	if err != nil {
//		return err
//	}
//
//	config, err := source.Decode[Config](ctx, niceyaml.WithValidator(schema))
//
// For a Source that holds more than one document, Decode returns the
// error that Source.Document returns. On error, the returned T is the
// zero value.
func (s *Source) Decode[T any](ctx context.Context, opts ...DecodeOption) (T, error) {
	doc, err := s.Document()
	if err != nil {
		var zero T

		return zero, err
	}

	return doc.Decode[T](ctx, opts...)
}

// single returns the one document [Source.Documents] returns. For a
// syntax error, it returns the error that [Source.File] returns, which the
// parse bound already. For more than one document, it returns an unbound
// error. Every Source holds at least one document, so docs[0] always
// exists.
func (s *Source) single() (*Node, error) {
	_, err := s.File()
	if err != nil {
		return nil, err
	}

	docs := s.contentDocuments()
	if len(docs) > 1 {
		return nil, Invalid(
			fmt.Errorf("%w: %d documents", ErrMultipleDocuments, len(docs)),
			atToken(docs[1].doc.anchorToken()),
		)
	}

	return docs[0], nil
}

// File returns the [*ast.File] the tokens of the [Source] parse into,
// with the duplicate keys [WithAllowDuplicateKeys] allows. The first call
// parses the Source, and every later call returns the same tree or the
// same error. Every [Node] of the Source shares the tree. [Node.At],
// [Node.Ranges], and every error binding resolve against it, so a caller
// must not modify it. A caller that edits a document parses a tree of
// its own, or edits the text and builds a new Source.
//
// The tokens of the tree are copies of the tokens of the Source. The
// Lines section of the package documentation describes how the copies
// find their lines.
//
// The tree leaves out some comments that sit on lines of their own, in
// places where the go-yaml parser rejects or misreads them. One such
// place is below a document whose root is a scalar or a flow collection.
// Others are before a key or a closing bracket inside a flow collection,
// and between an anchor and the node below it. The tokens of the Source
// and of each Node still hold those comments.
//
// A file with a document that did not parse does not have a tree. For such
// a file, File returns nil and an error that covers every document that
// did not parse, as the Documents section of the package documentation
// describes. One such error comes back as a [*SourceError], and several
// come back joined, in file order, each bound to its own document. Every
// syntax error matches [ErrSyntax], and its message draws each control
// character as its Unicode Control Picture. The error of a document past
// the depth limit, or of a text past the byte limit, matches [ErrLimit]
// and not ErrSyntax. A panic in the parser comes back as a SourceError
// too, bound at the first token with a position among those the parser
// was reading. It holds a [PanicError] and does not match ErrSyntax.
func (s *Source) File() (*ast.File, error) {
	p := s.parsed.get(s.parse)
	if p.err != nil {
		return nil, p.err
	}

	return p.result.File, nil
}

// parsedFile holds the result of the parse of the tokens of a [Source].
type parsedFile struct {
	// The file, the copies of the tokens the parser read, and the runs
	// that did not parse.
	result *docparse.Result
	// The error of the one run that did not parse, or the join of the
	// errors of several, each bound to the Source.
	err error
	// The root of each document of the file, in file order.
	docs []*Node
	// The documents among docs that [Source.Documents] returns, as
	// [pickContentDocuments] picks them.
	content []*Node
}

// parse parses the tokens of the Source and builds its documents. It
// builds them in the same step because [newDocuments] binds each syntax
// error to its document by writing to the error. That write must happen
// before any caller holds the error.
func (s *Source) parse() *parsedFile {
	tks := s.Tokens()

	var r *docparse.Result

	if s.limitErr != nil {
		r = docparse.Failed(s.limitErr)
	} else {
		r = docparse.ParseDepth(tks, s.depth(), s.parserOptions()...)
	}

	errs := make([]error, len(r.Failures))
	for i, f := range r.Failures {
		errs[i] = s.parseError(f)
	}

	docs := newDocuments(s, r.Documents(tks, s.lines.Len()), errs)

	return &parsedFile{result: r, err: multierr.Join(errs), docs: docs, content: pickContentDocuments(docs)}
}

// parseError returns the error of f, a run the parser rejected, bound to
// the Source. A parser panic is not a fault of the text, so its
// [*PanicError] matches neither [ErrSyntax] nor [errInvalid].
func (s *Source) parseError(f docparse.Failure) error {
	var err error

	// The go-yaml scanner spells the tab it rejects as a raw tab, which
	// the renderers would lay out as four spaces, so the message names
	// each control character by its picture.
	yamlErr, ok := errors.AsType[yaml.Error](f.Err)

	depthErr, deep := errors.AsType[*docparse.DepthError](f.Err)

	switch {
	case deep:
		limit := &markedError{err: errors.New(depthErr.Error()), marks: []error{ErrLimit}}
		err = Invalid(limit, atToken(depthErr.Token))

	case errors.Is(f.Err, ErrLimit):
		err = f.Err

	case f.Panic != nil:
		var at *token.Token

		if i := slices.IndexFunc(f.Run, func(tk *token.Token) bool { return tk.Position != nil }); i >= 0 {
			at = f.Run[i]
		}

		err = Unchecked(panicked("parser", &PanicError{Value: f.Panic, Stack: f.Stack}), atToken(at))

	case ok:
		msg := escape.Control(yamlErr.GetMessage())

		err = Invalid(markSyntax(yamlMessage(yamlErr, msg)), atToken(yamlErr.GetToken()))

	default:
		err = markSyntax(f.Err)
	}

	// The documents come from the file this parse returns, so the error
	// binds to the source alone rather than routing to one of them.
	// [bindSyntaxErrors] gives it its document once they exist.
	return bindTree(err, binder{src: s})
}

// markSyntax returns err as an error of the parse, which matches
// [ErrSyntax] and [errInvalid]. It reads as err and unwraps to it.
func markSyntax(err error) *markedError {
	return &markedError{err: err, marks: []error{ErrSyntax, errInvalid}}
}

// reparse parses the tokens again for the decoder, which edits the tree it
// reads, so the tree [Source.File] returns stays as parsed. Both parses
// give each token and document the same index. It writes the null of each
// enclosed alias of every document, because the go-yaml formatter reads
// every document.
func (s *Source) reparse() *docparse.Result {
	r := docparse.ParseDepth(s.Tokens(), s.depth(), s.parserOptions()...)

	first := s.parsed.get(s.parse).result.Index

	for _, doc := range s.documents() {
		for alias := range doc.doc.state.Facts().Enclosed {
			if i, ok := first[alias.Start]; ok {
				decodetree.WriteNull(r.Tokens[i])
			}
		}
	}

	return r
}

// Bind binds err to the [Source] and to the document that each location
// in err falls in. A caller that holds the source thus binds without
// picking a document. In every other way Bind is [Node.Bind], which
// describes the result.
//
// A [position.Position] or a [position.Range], as a check that runs on
// [Source.Lines] produces, binds to the document whose [Node.Span] holds
// its line, even in a file with a YAML syntax error. That document might
// be an empty one that [Source.Documents] leaves out:
//
//	return source.Bind(niceyaml.NewError("line exceeds 120 columns", niceyaml.AtRange(rng)))
//
// A path, whether it starts at `$` or `@`, resolves from the root of the
// one document [Source.Document] returns, as [Node.Bind] of that root
// resolves it. In a source that does not hold a single document, a path
// does not resolve. The bound error keeps its message and the name of
// the source. [SourceError.Unresolved] returns an error that wraps
// [ErrPathNeedsDocument] and the reason Source.Document gives. Bind such
// an error through Node.Bind with the document the caller checked it
// against.
//
// [SourceError.Document] returns the document each location fell in,
// whether or not the location resolves there. It reports false for an
// error that did not fall in any document, as [SourceError.Node]
// describes.
func (s *Source) Bind(err error) error {
	return bindTree(err, binder{src: s, route: true})
}

// Lines returns the [line.Lines] of the [Source], which hold its tokens
// in one line per line of text. Line i holds line i+1 of the text, so
// [position.NewFromToken] converts any token of the Source to a position
// in the lines.
//
// The lines never change, so every call returns the same value. The
// [go.jacobcolvin.com/niceyaml/finder.Finder] and
// [go.jacobcolvin.com/niceyaml/diff.Differ] read that value as it is. To
// render the Source, take a [line.View] from [Source.View].
//
// A nil Source does not hold any lines, so Lines returns the zero
// [line.Lines] for it, as it does for a Source of empty text.
func (s *Source) Lines() line.Lines {
	if s == nil {
		return line.Lines{}
	}

	return s.lines
}

// View returns a new [*line.View] over [Source.Lines] without decoration.
// Each call returns a view of its own, so overlays and annotations added to
// one never reach the Source or another view. Render the view to see them.
// The view of a nil Source does not hold any lines.
func (s *Source) View() *line.View {
	return line.NewView(s.Lines())
}

// newDocuments creates the root [*Node] of each of docs, in file order. A
// document that did not parse gets a Node too, which carries the error of
// its run from errs.
func newDocuments(src *Source, docs []docparse.Document, errs []error) []*Node {
	nodes := make([]*Node, len(docs))

	for i, parsed := range docs {
		doc := &document{
			root:       parsed.Root,
			tokens:     parsed.Tokens,
			positioned: parsed.Positioned,
			index:      i,
			fileIndex:  parsed.FileIndex,
			preamble:   parsed.Preamble,
		}

		if parsed.Failure >= 0 {
			doc.err = errs[parsed.Failure]
		}

		doc.node = &Node{source: src, doc: doc, content: doc.tokens, base: paths.Doc(), span: parsed.Span}
		doc.tree = sync.OnceValue(doc.newDecodeTree)
		doc.pathIndex = sync.OnceValue(func() *pathindex.Index { return pathindex.New(doc.root.Body) })
		doc.resolver = sync.OnceValue(doc.newResolver)
		doc.state = aliasing.NewState(!src.skipAliasLimit, src.budget(), func() (ast.Node, *paths.Resolver) {
			return doc.root.Body, doc.pathResolver()
		})
		nodes[i] = doc.node
	}

	bindSyntaxErrors(nodes)

	return nodes
}

// bindSyntaxErrors binds each syntax error to the document of its run
// whose span holds its line, or to the first document of the run when the
// error lacks a location. The parser bound each such error to the source
// alone.
func bindSyntaxErrors(docs []*Node) {
	for i, doc := range docs {
		if doc.doc.err == nil {
			continue
		}

		for bound := range allBindings(doc.doc.err) {
			// An earlier document of the run bound the error already.
			if bound.node != nil {
				continue
			}

			bound.node = doc

			if bound.locErr != nil {
				continue
			}

			// The parse hands every document of a run the one binding of
			// its error, so the documents that follow with that error are
			// the rest of the run.
			for _, other := range docs[i:] {
				if !errors.Is(other.doc.err, doc.doc.err) {
					break
				}

				if other.span.Contains(bound.loc.pos.Line) {
					bound.node = other

					break
				}
			}
		}
	}
}
