package niceyaml

import (
	"strconv"
	"sync"

	"github.com/goccy/go-yaml/ast"
	"github.com/goccy/go-yaml/token"

	"go.jacobcolvin.com/niceyaml/internal/astnode"
	"go.jacobcolvin.com/niceyaml/internal/datapath"
	"go.jacobcolvin.com/niceyaml/paths"
)

// DataLocator finds where a location in decoded data lies in the document
// the data came from. A check of decoded data names what it finds by the
// member names and indices of that data, as an adapter for CUE, OPA, or
// another JSON Schema library does. A path matches a key by its source
// text instead. [AtPath] of those names therefore misses a key the
// decoder respells, such as the key 0x10 of the member 16, and it reads
// the index of an element as the name of a member. A DataLocator reads
// the names as the decoder does, and its options give an error the
// location [go.jacobcolvin.com/niceyaml/schema.Schema] gives a violation
// of the same value:
//
//	func (c *check) Validate(ctx context.Context, n *niceyaml.Node) error {
//		data, err := n.Decode[any](ctx)
//		if err != nil {
//			return err
//		}
//
//		loc := n.DataLocator()
//
//		var errs []error
//
//		for _, f := range c.findings(data) {
//			errs = append(errs, niceyaml.NewError(f.Msg, loc.At(f.Path...)))
//		}
//
//		return n.Bind(errors.Join(errs...))
//	}
//
// In this document, a finding at ports, 16, name reports the key as the
// source spells it, and one at items, 0, name reports an element:
//
//	ports:
//	  0x10:
//	    name: ""
//	items:
//	  - name: ""
//
//	cfg.yaml:3:11: $.ports.0x10.name: must not be empty
//	cfg.yaml:5:11: $.items[0].name: must not be empty
//
// The names are those of the data from Decode[any] of the Node, as
// [Node.Decode] yields it. A member has the name the decoder gives its
// key. A string key gives its text, and any other key gives its Go value
// as [fmt.Sprint] prints it. The key 0x10 is 16, 3.10 is 3.1, True is
// true, 1234567.0 is 1.234567e+06, and !!binary aGk= is [104 105]. A null
// key is null. An element has its index as its name, in decimal digits
// with no sign and no leading zero, as [strconv.Itoa] writes it. Such a
// name is an index where the names before it lead to a sequence, and the
// name of a member anywhere else.
//
// A decode into another type can name a key another way. The key 1e3
// decodes to the string 1e3 in a decode into any and to the key 1000 of
// a map[int]string. The key ~ decodes to null in a decode into any and
// to the empty key of a map[string]string. A name from such a decode
// names no member of the data, so it leads nowhere, as [DataLocator.At]
// describes.
//
// A check that reports JSON Pointers splits each pointer into its names
// and unescapes `~1` and `~0` in each:
//
//	func names(pointer string) []string {
//		if pointer == "" {
//			return nil
//		}
//
//		unescape := strings.NewReplacer("~1", "/", "~0", "~")
//
//		parts := strings.Split(strings.TrimPrefix(pointer, "/"), "/")
//		for i, part := range parts {
//			parts[i] = unescape.Replace(part)
//		}
//
//		return parts
//	}
//
//	niceyaml.NewError(f.Msg, loc.At(names(f.Pointer)...))
//
// A DataLocator is safe for concurrent use. It reads each mapping once
// however many locations pass through it, so a check creates one for all
// the findings of a run. To tell which key a spelling selects, it reads
// the sources of each `<<` merge key that brings the key in or stands
// after it. The locations of one DataLocator may read as many nodes that
// way as [paths.ErrExcessiveMerging] allows one path selector. Past that
// limit it still finds each location, and the path through such a key
// names the member and each key below it as the decoder does. A check
// therefore creates a DataLocator for each run and keeps none for the
// life of the document.
//
// Create instances with [Node.DataLocator].
type DataLocator struct {
	// The node the names read from.
	root ast.Node
	// Finds the members of each mapping by the names a decode gives them.
	idx *datapath.Index
	// The `$` path to root.
	base paths.Path
	// Guards idx, which no two walks may read at once.
	mu sync.Mutex
}

// DataLocator creates a new [*DataLocator] for the data the Node decodes
// to. The names it reads start at the Node, as the data from
// [Node.Decode] does, so a [Validator] creates one from the Node it got
// whatever the scope of that Node. Each path it writes starts at `$`, so
// the errors bind through any Node of the document.
//
// A document that did not parse decodes to no data, as [Node.Err]
// describes, so every name then leads nowhere and each error binds with
// no position.
func (n *Node) DataLocator() *DataLocator {
	return &DataLocator{
		root: n.AST(),
		idx:  datapath.NewIndex(n.Resolver()),
		base: n.base,
	}
}

// At returns an [ErrorOption] that locates an error at the value the
// names lead to in the data of the [DataLocator]. No names lead to the
// whole of that data. The option sets the path of the error, as [AtPath]
// does, to the `$` path of the value with each key spelled as the source
// spells it:
//
//	niceyaml.NewError("must not be empty", loc.At("ports", "16", "name"))
//	// cfg.yaml:3:11: $.ports.0x10.name: must not be empty
//
// Where several keys of a mapping decode to one name, the name leads to
// the last of them, whose value the decode keeps. The member 16 of
// `m: {16: aaaa, 0x10: b}` holds b, so the names m, 16 lead to
// `$.m.0x10`. The members a `<<` merge key brings in take the order a
// decode gives them.
//
// A path selects a key by its text, and a later key or a later merge
// source may spell the key of another member with the same text. No path
// then selects the value, so the option also sets its position, as
// [AtPosition] does. The error binds at the position, and the path names
// the member and each key below it as the decoder does:
//
//	user:
//	  <<: {0x10: x}
//	  "0x10": 1
//
//	niceyaml.NewError("too short", loc.At("user", "16"))
//	// cfg.yaml:2:14: $.user.16: too short
//
// The names may lead to a value the data lacks, as a check for a
// required member reports one. The path then keeps the names from there
// on as the caller wrote them, and the error binds where that path
// binds. A member that a mapping lacks binds at the key of the mapping,
// as [SourceError.Nearest] describes. A finding at ports, 16, name thus
// binds at the key 0x10 when the mapping under it holds no name:
//
//	cfg.yaml:2:3: $.ports.0x10.name: name is required
//
// An index past the end of a sequence and a name below a scalar bind
// with no position.
//
// The names may also lead through an alias the document cannot follow,
// as one to an anchor of a document from [WithReferences] is. The
// DataLocator cannot tell what lies behind such an alias, or every member
// of a mapping that merges one. A name it cannot follow keeps the form
// the caller wrote, as does each name below it, and the option sets no
// position. The error then binds where its path binds, as
// [SourceError.Nearest] describes. A path that enters the alias binds at
// the alias, and a path to a key beside a merge of one has no position.
//
// A path the names do not lead down can select another value all the
// same. A key can spell a name and set a member of another name, as the
// key 3.10 sets the member 3.1. Under `python: {3.10: {image: a}}` the
// names python, 3.10 lead nowhere, and the path `$.python.'3.10'` selects
// that entry. The option then sets the position of the last value the
// names lead to, here the key python, and the error binds there. It does
// the same where no path selects that last value.
func (l *DataLocator) At(names ...string) ErrorOption {
	return l.option(names, false)
}

// AtKey returns an [ErrorOption] that locates an error at the key of the
// mapping entry the names lead to, which suits an error about the key
// itself, such as an unknown member. It reads the names as
// [DataLocator.At] does, and the path it sets ends in `~`, as one from
// [paths.Path.Key] does:
//
//	niceyaml.NewError("unknown port", loc.AtKey("ports", "16"))
//	// cfg.yaml:2:3: $.ports.0x10~: unknown port
//
// An element of a sequence and the data as a whole have no key, so the
// error binds at the value there. A member the data lacks has no key
// either, so the option locates it as [DataLocator.At] does.
func (l *DataLocator) AtKey(names ...string) ErrorOption {
	return l.option(names, true)
}

// option returns the [ErrorOption] that sets the path [DataLocator.locate]
// finds for names, and the position of the token it finds, if any.
func (l *DataLocator) option(names []string, key bool) ErrorOption {
	path, tk := l.locate(names, key)
	place := atToken(tk)

	return func(c *errorConfig) {
		c.path, c.hasPath = path, true

		place(c)
	}
}

// locate walks the node of the locator down names and returns the path
// to the location they lead to. With key set, the location is the key of
// the entry there. It also returns the token to bind an error at where
// the path does not bind at the location, and nil where it does or where
// the walk reached nothing to bind at.
//
// A value the walk reaches takes the path and the token
// [datapath.Target.Token] gives it. So does one the walk cannot follow,
// since the walk cannot tell there whether the data holds it, and a
// schema reports a violation of such a value the same way.
//
// A value the data lacks ends the walk. Its location takes the path of
// the last value the walk reached, with each name from there on below it
// as the caller wrote it. That path binds where a path to a key the
// document leaves out binds, so the token is nil. It is the token of
// that last value where the path would bind elsewhere instead. That
// holds where the path to the value does not select it. It also holds
// where a key of the mapping there spells the missing name, as
// [datapath.Index.SelectsEntry] reports, since the member that key sets
// has another name.
func (l *DataLocator) locate(names []string, key bool) (paths.Path, *token.Token) {
	l.mu.Lock()
	defer l.mu.Unlock()

	t := datapath.Target{Path: l.base, Node: l.root}

	for i, name := range names {
		next, lacks := l.step(t, name)
		if !lacks {
			t = next

			continue
		}

		path := next.Path.Child(names[i+1:]...)

		if t.Unspelled || l.idx.SelectsEntry(t.Node, name) {
			return path, t.Start(true)
		}

		return path, nil
	}

	path := t.Path
	if key {
		path = path.Key()
	}

	return path, t.Token(key)
}

// step returns the target one name down from t, and whether the data
// holds no value there that the names could lead to. A name that
// [elementIndex] reads as an index of the sequence at t leads to an
// element, which the data lacks past the end of the sequence. Any other
// name leads to a member. The data lacks that member where
// [datapath.Index.Lacks] reports so. The walk treats the member as
// missing too where a key of the mapping spells the name and sets a
// member of another name, as [datapath.Index.SelectsOther] reports, so
// no path through that key stands for it. The target of a value the data
// lacks holds its path and no node.
func (l *DataLocator) step(t datapath.Target, name string) (datapath.Target, bool) {
	if index, ok := elementIndex(l.idx.Deref(t.Node), name); ok {
		next := l.idx.Element(t, index)

		return next, astnode.IsNil(next.Node)
	}

	// A member the data lacks never reaches [datapath.Index.Member],
	// which asks which entry the name selects. The walk then reads the
	// merge sources that a schema reads for a member it requires.
	if l.idx.Lacks(t.Node, name) || l.idx.SelectsOther(t.Node, name) {
		return datapath.Target{Path: t.Path.Child(name)}, true
	}

	return l.idx.Member(t, name), false
}

// elementIndex returns the index name stands for, and true, when node is
// a sequence and name is an index as [strconv.Itoa] writes one: decimal
// digits with no sign and no leading zero. Any other name is the name of
// a member, and so is an index too large for an int.
func elementIndex(node ast.Node, name string) (int, bool) {
	if _, ok := node.(*ast.SequenceNode); !ok {
		return 0, false
	}

	if name == "" || (len(name) > 1 && name[0] == '0') {
		return 0, false
	}

	for i := range len(name) {
		if name[i] < '0' || name[i] > '9' {
			return 0, false
		}
	}

	index, err := strconv.Atoi(name)

	return index, err == nil
}
