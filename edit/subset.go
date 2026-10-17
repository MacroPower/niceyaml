package edit

import (
	"cmp"
	"errors"
	"fmt"
	"maps"
	"math"
	"slices"
	"strconv"
	"strings"

	"github.com/goccy/go-yaml/ast"
	"github.com/goccy/go-yaml/token"

	"go.jacobcolvin.com/niceyaml"
	"go.jacobcolvin.com/niceyaml/internal/aliasing"
	"go.jacobcolvin.com/niceyaml/internal/astnode"
	"go.jacobcolvin.com/niceyaml/internal/bom"
	"go.jacobcolvin.com/niceyaml/internal/datapath"
	"go.jacobcolvin.com/niceyaml/internal/hook"
	"go.jacobcolvin.com/niceyaml/internal/mapkey"
	"go.jacobcolvin.com/niceyaml/paths"
	"go.jacobcolvin.com/niceyaml/position"
)

// instance is one instance of a construct that the engine does not edit,
// as [Doc.find] finds it in the user's text.
type instance struct {
	// The other sites of the construct, in the order of the text.
	related []relatedSite
	// The paths of the readers of a taken anchor, which [UnsupportedError]
	// holds in Related before those of the other sites. The problem binds
	// at the first of them.
	readers []paths.Path
	// The words of the message that name what the program does not edit,
	// such as "a document that defines the anchor &x twice". The words of
	// the detail say what the user can write instead.
	what, fix string
	// The fix of the instance in the YAML that the encoder writes for a Go
	// value, which names a change to the value, or "" for a fix that names
	// the YAML.
	valueFix string
	// The words of the message of a call that reaches the instance, such as
	// "goes through a merge key that merges a sequence of aliases", and what
	// the program does not do, such as "edit through such a merge key".
	reach, object string
	// The words of the message of a call after the name of the call, such
	// as ", but <<: *s at 4:3 also brings in a", for a refusal whose words
	// take another form, or "".
	but string
	// The words of the message of a call after the subject, with the name
	// of the call in place of its %s, for a refusal whose words do not
	// start with the name of the call, or "".
	call string
	// The site of the instance, the first token of the construct, where its
	// problem binds.
	at site
	// The construct of the instance.
	construct Construct
	// Whether the instance lies in a value or a key that the program passed
	// to the call, rather than in the text of the document.
	inValue bool
}

// newRefusal returns the problem of the first construct of the user's text
// that [New] refuses, in the order of the text, or nil.
func (d *Doc) newRefusal() error {
	found := d.find(false)
	if len(found) == 0 {
		return nil
	}

	return d.refusal(firstInText(found), "")
}

// firstInText returns the first of found, instances of constructs, by their
// sites in the user's text, and by their constructs at one site. Found must
// not be empty.
func firstInText(found []instance) *instance {
	first := &found[0]

	for i := range found {
		if in := &found[i]; cmp.Or(siteOrder(in.at, first.at), cmp.Compare(in.construct, first.construct)) < 0 {
			first = in
		}
	}

	return first
}

// siteOrder orders a and b, two sites, by their places in the user's text.
func siteOrder(a, b site) int {
	return cmp.Or(cmp.Compare(a.at.Start.Line, b.at.Start.Line), cmp.Compare(a.at.Start.Col, b.at.Start.Col))
}

// find returns each instance of a construct that [New] refuses in the
// user's text, or in the current text when edited is true. It reads
// go-yaml's parse of the text, and each detector reports the instances of
// one construct.
func (d *Doc) find(edited bool) []instance {
	var found []instance

	if in, ok := d.lineBreaks(edited); ok {
		found = append(found, in)
	}

	found = append(found, d.propertyInstances()...)
	found = append(found, d.misnestedEmpties()...)
	found = append(found, d.anchorInstances()...)

	return found
}

// valueInstances returns each instance of a construct that a Set does not
// write, as the finder finds it in the current text, the YAML of a Set
// value as [encodedDoc] reads it (C17). An anchor and an alias give
// [ConstructValueAlias], a << merge key gives [ConstructMergeForm], and an
// explicit key gives [ConstructExplicitKey]. So do the instances of
// [Doc.find], and the comments and the pairs without braces of the flow
// collections, which give [ConstructFlowLayout]. The instances come in the
// order of the text, and at one place an anchor, an alias, or a key comes
// first, then an instance of find, then one of a flow collection. Each
// instance holds the words of a Set that writes it. Its fix names typ, the
// type of the marshaler that wrote the YAML, or the value for "", the text
// of the encode.
func (d *Doc) valueInstances(typ string) []instance {
	type found struct {
		in   instance
		at   position.Position
		rank int
	}

	var out []found

	fix := func(object string) string {
		if typ == "" {
			return "set a value without " + object
		}

		return fmt.Sprintf("return the YAML of %s without %s", typ, object)
	}

	add := func(node ast.Node, rank int, in instance) {
		s, ok := d.tokenSpan(astnode.Token(node))
		if !ok {
			return
		}

		in.inValue = true
		out = append(out, found{at: d.userPos(s.start), rank: rank, in: in})
	}

	const aliases = "anchors and aliases"

	// The encoder writes the anchors and the aliases of a Go value from the
	// tags of the fields of its structs.
	aliasFix := fix(aliases)
	if typ == "" {
		aliasFix = "remove the anchor and alias options from the yaml tags of the fields of the value"
	}

	d.inspect(func(n ast.Node) bool {
		switch x := n.(type) {
		case *ast.AnchorNode:
			name, _ := astnode.Name(x.Name)
			add(x, 0, instance{
				construct: ConstructValueAlias, reach: "the anchor &" + name, object: "anchors or aliases",
				fix: aliasFix,
			})

		case *ast.AliasNode:
			name, _ := astnode.Name(x.Value)
			add(x, 0, instance{
				construct: ConstructValueAlias, reach: "the alias *" + name, object: "anchors or aliases",
				fix: aliasFix,
			})

		case *ast.MappingValueNode:
			if _, ok := x.Key.(*ast.MappingKeyNode); ok {
				add(x.Key, 0, instance{
					construct: ConstructExplicitKey, reach: "an explicit key", object: "explicit keys",
					fix: fix("explicit keys"),
				})
			} else if astnode.IsMergeKey(x.Key) {
				add(x.Key, 0, instance{
					construct: ConstructMergeForm, reach: "a merge key", object: "merge keys", fix: fix("merge keys"),
				})
			}
		}

		return true
	})

	// The fix of such an instance tells what to write in YAML, which the
	// program writes only through a marshaler.
	news := d.find(false)
	for i := range news {
		in := news[i]
		in.inValue, in.reach, in.object = true, strings.TrimPrefix(in.what, "a document with "), "such YAML"
		if typ == "" {
			in.fix = cmp.Or(in.valueFix, fix(in.object))
		}

		out = append(out, found{at: in.at.at.Start, rank: 1, in: in})
	}

	for _, z := range d.constructZones(true) {
		if z.in.construct != ConstructFlowLayout {
			continue
		}

		in := z.in
		in.inValue, in.related = true, nil
		in.reach, in.object = "a pair without braces in a flow sequence", "pairs without braces in flow sequences"

		if strings.Contains(z.in.reach, "comment") {
			in.reach, in.object = "a comment inside a flow collection", "comments inside flow collections"
		}

		in.fix = fix(in.object)
		out = append(out, found{at: in.at.at.Start, rank: 2, in: in})
	}

	slices.SortStableFunc(out, func(a, b found) int {
		return cmp.Or(cmp.Compare(a.at.Line, b.at.Line), cmp.Compare(a.at.Col, b.at.Col), cmp.Compare(a.rank, b.rank))
	})

	refused := make([]instance, 0, len(out))
	for i := range out {
		refused = append(refused, out[i].in)
	}

	return refused
}

// valueRefusal returns the problem of the Set of h, which named names,
// whose value holds in, an instance of a construct in its YAML, as
// [Doc.valueInstances] finds it. The Set writes the value with origin. The
// problem binds where every problem of the Set binds before the Set
// writes, as [Doc.setSite] gives the site, and the Apply records it. The
// program wrote the construct, so the document is not at fault.
func (d *Doc) valueRefusal(h *Node, origin int, in *instance, named string) error {
	err := &UnsupportedError{
		msg: fmt.Sprintf("%s %s to a value whose YAML holds %s, and %s does not write %s",
			d.subject(), named, in.reach, d.appName(), in.object),
		Construct: in.construct,
		Related:   []paths.Path{},
		InValue:   true,
	}

	fix := errors.New(in.fix)

	problem := d.bindSite(d.setSite(h, origin), func(opts ...niceyaml.ErrorOption) error {
		return niceyaml.Unchecked(err, append([]niceyaml.ErrorOption{niceyaml.WithDetails(fix)}, opts...)...)
	})
	d.record(problem)

	return problem
}

// lineBreaks returns the instance of [ConstructLineBreaks] in the text of
// the user's Source, or in the current text when edited is true: its first
// "\r" alone, or else its first line break of another kind than its first
// line break. Every document of the Source takes the instance of its text,
// because [FileText] writes them all.
func (d *Doc) lineBreaks(edited bool) (instance, bool) {
	var scan breakScan

	if edited {
		scan = scanBreaks(d.cur)
	} else {
		scan, _ = hook.LineBreaks(d.user.Source(), func(text string) any { //nolint:errcheck // The read gives a breakScan.
			// The positions of the Source count without the byte order marks
			// that the tokenizer drops.
			text, _ = bom.Drop(text)

			return scanBreaks(text)
		}).(breakScan)
	}

	if scan.at < 0 {
		return instance{}, false
	}

	in := instance{
		construct: ConstructLineBreaks,
		what:      scan.what,
		fix:       `write each line break as "\n", or each one as "\r\n"`,
	}

	if edited {
		in.at = d.offsetSite(scan.at, 1)

		return in, true
	}

	text, _ := bom.Drop(d.user.Source().Text())
	pos := positionIn(text, lineStarts(text), scan.at)
	path, _ := d.userSitePath(pos, false)
	in.at = site{path: path, at: position.NewRange(pos, position.New(pos.Line, pos.Col+1))}

	return in, true
}

// breakScan is the first instance of [ConstructLineBreaks] in a text, as
// [scanBreaks] finds it: the offset of its byte, and the words of its
// message. The offset is -1 for a text without one.
type breakScan struct {
	what string
	at   int
}

// scanBreaks returns the first instance of [ConstructLineBreaks] in text:
// its first "\r" alone, or else its first line break of another kind than
// its first line break.
func scanBreaks(text string) breakScan {
	// Both instances hold a "\r", so a text without one holds neither.
	if strings.IndexByte(text, '\r') < 0 {
		return breakScan{at: -1}
	}

	lone, mixed, first := -1, -1, ""

	for i := 0; i < len(text) && lone < 0; i++ {
		brk := ""

		switch {
		case text[i] == '\r' && isBreak(text, i):
			lone = i
		case text[i] == '\r':
			brk = "\r\n"
			i++

		case text[i] == '\n':
			brk = "\n"
		}

		switch {
		case brk == "" || mixed >= 0:
		case first == "":
			first = brk
		case brk != first:
			mixed = i + 1 - len(brk)
		}
	}

	if lone >= 0 {
		return breakScan{at: lone, what: `a text with a "\r" alone as a line break`}
	}

	return breakScan{at: mixed, what: `a text with both "\n" and "\r\n" line breaks`}
}

// propertyInstances returns the instances of the properties and the keys
// of the user's text that [New] refuses. Those are each property on an
// alias, each tag that converts a collection, and each tag that
// [ConstructTagKind] names. They are also each key with properties, each
// alias key, each key that reads as another value than its text, and each
// anchor on an empty value.
func (d *Doc) propertyInstances() []instance {
	var found []instance

	// The anchors on keys, which the key instances cover.
	keyed := make(map[ast.Node]bool)

	d.inspect(func(n ast.Node) bool {
		switch x := n.(type) {
		case *ast.TagNode, *ast.AnchorNode:
			if isAliasValue(x) {
				found = append(found, d.aliasProperties(x))

				return false
			}

			if tag, ok := x.(*ast.TagNode); ok {
				if in, ok := d.tagInstance(tag); ok {
					found = append(found, in)
				}
			}

			if anchor, ok := x.(*ast.AnchorNode); ok && !keyed[anchor] && emptyUnder(anchor.Value) {
				found = append(found, d.anchoredEmpty(anchor))
			}

			if in, ok := d.mergeKeyProperties(x); ok {
				found = append(found, in)

				return false
			}

		case *ast.MappingValueNode:
			var key ast.Node = x.Key

			if explicit, ok := key.(*ast.MappingKeyNode); ok {
				key = explicit.Value
			}

			for k := key; ; {
				a, ok := k.(*ast.AnchorNode)
				if ok {
					keyed[a] = true
					k = a.Value

					continue
				}

				t, ok := k.(*ast.TagNode)
				if !ok {
					break
				}

				k = t.Value
			}

			if in, ok := d.keyInstance(key); ok {
				found = append(found, in)
			}
		}

		return true
	})

	return found
}

// mergeKeyProperties returns the instance of [ConstructKeyProperties] of
// props, the anchors and the tags on a block mapping of the user's text,
// when an anchor among them stands on the line of the first key of the
// mapping, a << merge key, as in "&q <<: *s". YAML reads the properties
// before a key on its line as the properties of the key, but go-yaml's
// parse hangs them on the mapping when the key is a merge key. The
// instance binds at the first of them, and its words name the anchor.
func (d *Doc) mergeKeyProperties(props ast.Node) (instance, bool) {
	held := properties(props)
	at := slices.IndexFunc(held, func(p ast.Node) bool { return anchorText(p) != "" })
	content := astnode.Content(props)

	entries, ok := readEntries(content)
	if !ok || at < 0 || isFlow(content) || len(entries) == 0 || !astnode.IsMergeKey(entries[0].Key) {
		return instance{}, false
	}

	ps, pok := d.tokenSpan(astnode.Token(props))
	ks, kok := d.tokenSpan(astnode.Token(entries[0].Key))

	if !pok || !kok || d.lineOf(ps.start) != d.lineOf(ks.start) {
		return instance{}, false
	}

	return instance{
		construct: ConstructKeyProperties,
		at:        d.nodeSite(props, false),
		what:      "a document with a tag or an anchor on a mapping key, such as " + anchorText(held[at]),
		fix:       d.keyFix([]string{anchorText(held[at])}),
	}, true
}

// aliasProperties returns the instance of [ConstructAliasProperties] of
// props, the tags and the anchors on an alias, as in "!t *x".
func (d *Doc) aliasProperties(props ast.Node) instance {
	alias, _ := astnode.Content(props).(*ast.AliasNode) //nolint:errcheck // isAliasValue found the alias.
	name, _ := astnode.Name(alias.Value)

	var held []string

	for _, p := range properties(props) {
		if tag, ok := p.(*ast.TagNode); ok {
			held = append(held, "the tag "+tagText(tag))
		} else {
			held = append(held, "the anchor "+anchorText(p))
		}
	}

	return instance{
		construct: ConstructAliasProperties,
		at:        d.nodeSite(props, false),
		what:      "a document with a tag or an anchor on an alias, such as " + cut(propertyText(props)),
		fix: fmt.Sprintf(
			"remove %s from *%s, or write the value in place of the alias",
			strings.Join(held, " and "), name,
		),
	}
}

// anchorText returns the text of an anchor, as in "&x", or "" for a node
// of another type.
func anchorText(node ast.Node) string {
	a, ok := node.(*ast.AnchorNode)
	if !ok {
		return ""
	}

	name, _ := astnode.Name(a.Name)

	return "&" + name
}

// properties returns the tags and the anchors of props, in the order of
// the text.
func properties(props ast.Node) []ast.Node {
	var held []ast.Node

	for {
		switch x := props.(type) {
		case *ast.TagNode:
			held, props = append(held, x), x.Value
		case *ast.AnchorNode:
			held, props = append(held, x), x.Value
		default:
			return held
		}
	}
}

// propertyText returns the words of node, the tags and the anchors on an
// alias, and the alias, on one line, as in "&a !t *x".
func propertyText(node ast.Node) string {
	var words []string

	for node != nil {
		switch x := node.(type) {
		case *ast.TagNode:
			words, node = append(words, tagText(x)), x.Value

		case *ast.AnchorNode:
			name, _ := astnode.Name(x.Name)
			words, node = append(words, "&"+name), x.Value

		case *ast.AliasNode:
			name, _ := astnode.Name(x.Value)
			words, node = append(words, "*"+name), nil

		default:
			node = nil
		}
	}

	return strings.Join(words, " ")
}

// coreTag returns the shorthand of text, the text of a tag, when text
// writes a tag of the YAML core schema in its verbatim form. YAML reads
// "!<tag:yaml.org,2002:str>" as "!!str", but go-yaml reads it as a local
// tag, so the finder compares the shorthand.
func coreTag(text string) string {
	name, ok := strings.CutPrefix(text, "!<tag:yaml.org,2002:")
	if name, closed := strings.CutSuffix(name, ">"); ok && closed {
		return "!!" + name
	}

	return text
}

// shorthand returns tag, or a copy of it with the shorthand of a verbatim
// core tag, as [coreTag] reads it, so that go-yaml's decoder reads the tag
// that YAML reads.
func shorthand(tag *ast.TagNode) *ast.TagNode {
	text := coreTag(tagText(tag))
	if text == tagText(tag) {
		return tag
	}

	start := *tag.Start
	start.Value = text
	short := *tag
	short.Start = &start

	return &short
}

// tagKinds are the tags of [ConstructTagKind].
var tagKinds = []string{"!", "!!set", "!!omap", "!!pairs"}

// tagInstance returns the instance of tag, a tag of the user's text, when
// it is a tag of [ConstructTagKind] or a tag that converts the collection
// under it, as [astnode.ConvertsCollection] reads it.
func (d *Doc) tagInstance(tag *ast.TagNode) (instance, bool) {
	text := tagText(tag)

	if slices.Contains(tagKinds, coreTag(text)) {
		return instance{
			construct: ConstructTagKind,
			at:        d.nodeSite(tag, false),
			what:      "a document with the tag " + text,
			fix:       "remove the tag " + text,
		}, true
	}

	switch astnode.Content(tag.Value).(type) {
	case *ast.MappingNode, *ast.SequenceNode:
	default:
		return instance{}, false
	}

	if !astnode.ConvertsCollection(shorthand(tag)) {
		return instance{}, false
	}

	return instance{
		construct: ConstructConvertingTag,
		at:        d.nodeSite(tag, false),
		what:      fmt.Sprintf("a document with a tag that converts a collection, such as %s", text),
		fix:       fmt.Sprintf("remove the tag %s, or write the value as a quoted string", text),
	}, true
}

// keyInstance returns the instance of key, the key of an entry of the
// user's text without its "?". It finds one when key is an alias, has an
// anchor or a tag, or reads as another value than its text. The tag
// !!merge on a << merge key stays a merge key, which a call checks.
func (d *Doc) keyInstance(key ast.Node) (instance, bool) {
	switch k := key.(type) {
	case *ast.AliasNode:
		name, _ := astnode.Name(k.Value)

		return instance{
			construct: ConstructKeyProperties,
			at:        d.nodeSite(k, true),
			what:      "a document with an alias as a mapping key, such as *" + name,
			fix:       fmt.Sprintf("write the key out in place of *%s", name),
		}, true

	case *ast.TagNode:
		if coreTag(tagText(k)) == "!!merge" && astnode.IsMergeKey(k.Value) {
			if _, ok := k.Value.(*ast.MergeKeyNode); ok {
				return instance{}, false
			}
		}

		return d.keyProperties(k), true

	case *ast.AnchorNode:
		return d.keyProperties(k), true

	case nil, *ast.MappingNode, *ast.SequenceNode, *ast.MergeKeyNode:
		return instance{}, false

	default:
		return d.keyText(key)
	}
}

// keyProperties returns the instance of [ConstructKeyProperties] of key, a
// key under a tag or an anchor. The message shows the first of them, and
// the fix names each of them but the tag !!merge on a << merge key, which
// the key keeps.
func (d *Doc) keyProperties(key ast.Node) instance {
	var held []string

	_, merge := astnode.Content(key).(*ast.MergeKeyNode)

	for _, p := range properties(key) {
		tag, ok := p.(*ast.TagNode)

		switch {
		case ok && merge && coreTag(tagText(tag)) == "!!merge":
		case ok:
			held = append(held, tagText(tag))
		default:
			held = append(held, anchorText(p))
		}
	}

	return instance{
		construct: ConstructKeyProperties,
		at:        d.nodeSite(key, true),
		what:      "a document with a tag or an anchor on a mapping key, such as " + held[0],
		fix:       d.keyFix(held),
	}
}

// keyFix returns the fix of a key under the properties in held, which
// asks for the key in place of each alias of an anchor among them.
func (d *Doc) keyFix(held []string) string {
	fix := fmt.Sprintf("remove %s from the key", strings.Join(held, " and "))

	aliases := aliasing.StateOf(d.node).Facts().Aliases
	if slices.ContainsFunc(aliases, func(f aliasing.AliasFact) bool { return slices.Contains(held, "&"+f.Name) }) {
		fix += ", and write the key out in place of each alias of it"
	}

	return fix
}

// keyText returns the instance of [ConstructKeyText] of key, a scalar key
// without properties, when go-yaml's decoder reads it as a value that does
// not print as the text of the key. The decoder reads a mapping of the key
// alone into a map[any]any, as a decode into any reads each key of a
// mapping. A key that the decoder does not read passes. The null value is
// no string, so the edits do not take a key that reads as it, though a
// decode into a map names the key null by its text, as
// [datapath.MemberName] tells. The message names such a key as one that
// reads as the null value.
func (d *Doc) keyText(key ast.Node) (instance, bool) {
	// An empty key has no text, and a decode reads it as null.
	text, _ := astnode.ScalarText(key)

	r := d.readKey(key, text)
	if !r.ok || r.shows {
		return instance{}, false
	}

	read := r.key

	// The reading takes the word for its type where it shows as the text
	// does, as [shownApart] shows two values.
	shown := "the null value"
	if read != nil {
		shown = shownCut(read)
		if shown == cut(text) {
			shown = typeWord(read) + shown
		}
	}

	what := fmt.Sprintf(
		"a key that reads as another value than its text, such as %s, which reads as %s",
		cut(text),
		shown,
	)

	if read == nil && datapath.MemberName(read) == text {
		what = "a key that reads as the null value, such as " + cut(text)
	}

	return instance{
		construct: ConstructKeyText,
		at:        d.nodeSite(key, true),
		what:      what,
		fix:       "quote the key, as in " + cut(strconv.Quote(text)),
		valueFix:  "make the key a Go string, as in " + cut(strconv.Quote(text)),
	}, true
}

// readKey returns what go-yaml's decoder reads from key, whose text
// [astnode.ScalarText] reads as text, as [Doc.keyText] reads it. The Doc
// keeps each reading by the token and the string of the key, so a call
// reads each such key once.
func (d *Doc) readKey(key ast.Node, text string) keyReading {
	tk := astnode.Token(key)
	if tk == nil {
		return keyReading{}
	}

	// The token of a block scalar is its header, so its text tells two
	// such keys apart.
	k := keyRead{strconv.Itoa(int(tk.Type)), tk.Value, tk.Origin, text}
	if r, ok := d.keyReads[k]; ok {
		return r
	}

	mk, ok := key.(ast.MapKeyNode)
	if !ok {
		explicit := ast.MappingKey(token.New("?", "?", &token.Position{}))
		explicit.Value = key
		mk = explicit
	}

	var (
		m map[any]any
		r keyReading
	)

	err := astnode.NodeToValue(ast.MappingValue(token.New(":", ":", &token.Position{}), mk,
		ast.Null(token.New("null", "null", &token.Position{}))), &m)
	if err == nil && len(m) == 1 {
		for read := range m {
			r = keyReading{key: read, ok: true, shows: read != nil && fmt.Sprint(read) == text}
		}
	}

	if d.keyReads == nil {
		d.keyReads = make(map[keyRead]keyReading)
	}

	d.keyReads[k] = r

	return r
}

// keyRead is the token of a key that [Doc.readKey] reads, with the
// string of the key: the type of the token, its value, its origin, and the
// string.
type keyRead [4]string

// keyReading is what go-yaml's decoder reads from a key, as
// [Doc.readKey] returns it, and whether a value other than null reads
// and prints as the text of the key, as [Doc.keyText] tells.
type keyReading struct {
	key       any
	ok, shows bool
}

// mergeName returns the name of key, a key of the current text, under
// which go-yaml's decoder merges its entry: the key that a decode into a
// map gives the entry, as [Doc.decodedKeys] names it. So 0x1 and 1 name one
// entry, and so do an alias and the key of its anchor. A key that does not
// decode takes the name that a path reads, as [paths.Resolver.KeyName]
// gives it.
func (d *Doc) mergeName(key ast.Node) (string, bool) {
	if name, ok := d.decodedKeys()(key); ok {
		return name, true
	}

	return d.node.Resolver().KeyName(key)
}

// emptyUnder reports whether node, the value under an anchor, is the null
// of an empty value under its tags and anchors, as in "a: &x" and
// "a: &x !t".
func emptyUnder(node ast.Node) bool {
	for {
		switch n := node.(type) {
		case *ast.TagNode:
			node = n.Value
		case *ast.AnchorNode:
			node = n.Value
		default:
			return isImplicitNull(node)
		}
	}
}

// anchoredEmpty returns the instance of [ConstructAnchoredEmpty] of anchor,
// an anchor on an empty value or an empty item of the user's text.
func (d *Doc) anchoredEmpty(anchor *ast.AnchorNode) instance {
	name, _ := astnode.Name(anchor.Name)

	return instance{
		construct: ConstructAnchoredEmpty,
		at:        d.nodeSite(anchor, false),
		what:      "a document with an anchor on an empty value, such as &" + name,
		fix:       fmt.Sprintf("write a value after &%s, such as null", name),
	}
}

// misnestedEmpties returns the instances of [ConstructAnchoredEmpty] that
// go-yaml's parse of the user's text does not show as empty. Go-yaml nests
// the lines after an anchored empty value or item in that value, as
// [misnested] finds them. Each anchor on the collection of such a line is
// the anchor of an empty value of the text.
func (d *Doc) misnestedEmpties() []instance {
	seen := make(map[*ast.AnchorNode]bool)

	var found []instance

	for _, m := range d.misnests() {
		d.anchorsAbove(m.props, func(anchor *ast.AnchorNode) {
			if !seen[anchor] {
				seen[anchor] = true
				found = append(found, d.anchoredEmpty(anchor))
			}
		})
	}

	return found
}

// anchorsAbove calls f with each anchor of props, the anchors and the tags
// on a collection of the current text, from props up, as
// [Doc.misnestedEmpties] reads them.
func (d *Doc) anchorsAbove(props ast.Node, f func(*ast.AnchorNode)) {
	if props == nil {
		return
	}

	t := d.tree()

	for p := props; p != nil; p = t.parent[p] {
		switch x := p.(type) {
		case *ast.AnchorNode:
			f(x)
		case *ast.TagNode:
		default:
			return
		}
	}
}

// misnest is a block entry or item of the current text that go-yaml's
// parse nests deeper than its indentation lets YAML nest it, as
// [misnested] finds it. It holds the anchor or the tag on the collection
// that go-yaml nests the entry or the item in.
type misnest struct {
	props ast.Node
}

// misnests returns each [misnest] of the current text in the order of the
// text, which it reads on its first call for the parse. The caller must not
// change the returned slice.
func (d *Doc) misnests() []misnest {
	t := d.parseIndex()
	if t.misnests == nil {
		t.misnests = d.readMisnests()
	}

	return t.misnests
}

// readMisnests reads each [misnest] of the current text, as
// [Doc.misnests] returns them.
func (d *Doc) readMisnests() []misnest {
	out := []misnest{}

	misnested(d.node.AST(), 0, 0, nil, func(_ *token.Token, props ast.Node) bool {
		out = append(out, misnest{props: props})

		return true
	})

	return out
}

// misnested calls found with the token of each block entry or item under
// node that go-yaml's parse nests deeper than its indentation lets YAML
// nest it. The calls come in the order of the text. It also passes the anchor or the tag
// on the collection that holds that entry or item. Misnested stops when
// found returns false, and it reports whether it stopped. An entry of a
// block mapping starts at a column of keyMin or more, and an item of a
// block sequence at a column of seqMin or more. YAML nests a collection
// below an entry or an item only when the collection starts past the
// column of that entry or that "-". A block sequence can also start at the
// column of the key whose value it is. Go-yaml nests the entries after an
// anchored empty item in the item anyway. It reads r1 of
// "r0:\n- &x0\nr1: 1" as an entry of the item. The props are the anchor
// or the tag that holds node. A flow collection does not count, because
// YAML reads it by its brackets.
func misnested(node ast.Node, keyMin, seqMin int, props ast.Node, found func(*token.Token, ast.Node) bool) bool {
	switch n := node.(type) {
	case *ast.AnchorNode:
		return misnested(n.Value, keyMin, seqMin, n, found)

	case *ast.TagNode:
		return misnested(n.Value, keyMin, seqMin, n, found)

	case *ast.MappingValueNode:
		return misnestedEntry(n, keyMin, props, found)

	case *ast.MappingNode:
		if n.IsFlowStyle {
			return false
		}

		for _, e := range n.Values {
			if misnestedEntry(e, keyMin, props, found) {
				return true
			}
		}

	case *ast.SequenceNode:
		if n.IsFlowStyle {
			return false
		}

		for i, v := range n.Values {
			tk := itemMark(n, i)
			if tk == nil || tk.Position == nil {
				continue
			}

			col := tk.Position.Column - 1
			if col < seqMin && !found(tk, props) {
				return true
			}

			if misnested(v, col+1, col+1, nil, found) {
				return true
			}
		}
	}

	return false
}

// misnestedEntry calls found with the token of e, an entry of a block
// mapping, when it starts at a column less than keyMin, as [misnested]
// reads it. It then calls found for the entries and the items in the key
// and the value of e. It reports whether found stopped it.
func misnestedEntry(e *ast.MappingValueNode, keyMin int, props ast.Node, found func(*token.Token, ast.Node) bool) bool {
	// The token of a key is its first token: the "?" of an explicit key,
	// the first property of a key with properties, or the key itself.
	tk := astnode.Token(e.Key)
	if tk == nil || tk.Position == nil {
		return false
	}

	col := tk.Position.Column - 1
	if col < keyMin && !found(tk, props) {
		return true
	}

	if misnested(e.Key, col+1, col+1, nil, found) {
		return true
	}

	return misnested(e.Value, col+1, col, nil, found)
}

// anchorInstances returns the instances of the anchors of the user's text
// that [New] refuses. Those are each name that the text defines twice, and
// each name that it defines and aliases while a reference document defines
// it too. They are also each alias inside the value of the anchor that it
// reads.
func (d *Doc) anchorInstances() []instance {
	facts := aliasing.StateOf(d.node).Facts()

	var found []instance

	defined := make(map[string][]*ast.AnchorNode)
	for _, a := range facts.Anchors {
		defined[a.Name] = append(defined[a.Name], a.Node)
	}

	aliased := make(map[string][]*ast.AliasNode)
	for _, a := range facts.Aliases {
		aliased[a.Name] = append(aliased[a.Name], a.Node)
	}

	for _, a := range facts.Anchors {
		anchors := defined[a.Name]

		switch {
		case len(anchors) > 1 && a.Node == anchors[1]:
			found = append(found, instance{
				construct: ConstructReusedAnchor,
				at:        d.nodeSite(a.Node, false),
				what:      fmt.Sprintf("a document that defines the anchor &%s twice", a.Name),
				fix:       "rename one of the two anchors and the aliases that read it",
				related: []relatedSite{{
					site: d.nodeSite(anchors[0], false),
					note: fmt.Sprintf("&%s is defined here", a.Name),
				}},
			})

		case len(anchors) == 1 && len(aliased[a.Name]) > 0:
			if in, ok := d.referencedAnchor(a.Node, a.Name, aliased[a.Name]); ok {
				found = append(found, in)
			}
		}
	}

	// Only an alias inside its anchor reads the parse with the nulls, so the
	// parse runs for a text with such an alias alone.
	var misnested map[[2]int]bool

	if slices.ContainsFunc(facts.Aliases, func(a aliasing.AliasFact) bool { return a.Inside && a.Anchor != nil }) {
		misnested = d.misnestedAliases()
	}

	for _, a := range facts.Aliases {
		// An alias after the second anchor of its name reads one of the two
		// in YAML, which go-yaml can read otherwise. The name is an instance
		// of its own, whose site at the second anchor comes before the site of
		// any anchor after it.
		if anchors := defined[a.Name]; len(anchors) > 1 && d.offsetOf(a.Node) > d.offsetOf(anchors[1]) {
			continue
		}

		if a.Inside && a.Anchor != nil && !misnested[aliasPlace(a.Node)] {
			found = append(found, instance{
				construct: ConstructRecursiveAlias,
				at:        d.nodeSite(a.Anchor, false),
				what: fmt.Sprintf(
					"a document with an alias inside the value of the anchor that it reads, such as *%s",
					a.Name,
				),
				fix: fmt.Sprintf("remove *%s from the value of &%s", a.Name, a.Name),
				related: []relatedSite{{
					site: d.nodeSite(a.Node, false),
					note: fmt.Sprintf("*%s is here", a.Name),
				}},
			})
		}
	}

	return found
}

// misnestedAliases returns the place of each alias of the current text
// that go-yaml's parse alone nests inside the value of the anchor that it
// reads, as [aliasPlace] gives it. Go-yaml nests the lines after a
// property on an empty value in that value, as [Doc.misnests] finds them,
// and YAML does not. The parse of the text with a null after each such
// property reads those lines as YAML does. An alias inside its anchor in
// go-yaml's parse and outside it in that parse is such an alias. The null
// goes at the end of a line, so each alias keeps its line and its column.
// MisnestedAliases returns nil for a text without such lines, and for one
// whose parse with the nulls fails.
func (d *Doc) misnestedAliases() map[[2]int]bool {
	var ends []int

	for _, m := range d.misnests() {
		prop := m.props
		if a, ok := prop.(*ast.AnchorNode); ok {
			prop = a.Name
		}

		if s, ok := d.tokenSpan(astnode.Token(prop)); ok && prop != nil {
			ends = append(ends, s.end)
		}
	}

	if len(ends) == 0 {
		return nil
	}

	slices.Sort(ends)

	var b strings.Builder

	last := 0
	for _, end := range slices.Compact(ends) {
		b.WriteString(d.cur[last:end] + " null")

		last = end
	}

	b.WriteString(d.cur[last:] + d.tail)

	//nolint:errcheck // The hook gives a *niceyaml.Source.
	src, _ := hook.WorkingSource(b.String(), d.user, d.cfg.unmarshalers, d.locate).(*niceyaml.Source)

	for _, n := range src.AllDocuments() {
		if n.Err() != nil {
			return nil
		}

		if n.IsEmpty() {
			continue
		}

		out := make(map[[2]int]bool)

		for _, a := range aliasing.StateOf(n).Facts().Aliases {
			if !a.Inside {
				out[aliasPlace(a.Node)] = true
			}
		}

		return out
	}

	return nil
}

// aliasPlace returns the line and the column of alias, which are the same
// in the parse of [Doc.misnestedAliases].
func aliasPlace(alias *ast.AliasNode) [2]int {
	tk := alias.GetToken()
	if tk == nil || tk.Position == nil {
		return [2]int{}
	}

	return [2]int{tk.Position.Line, tk.Position.Column}
}

// referenceWords names the reference document that defines the anchor of
// name in the words of a message, as in "the reference document base.yaml",
// or "a reference document" when the Source of the reference document has
// no name.
func (d *Doc) referenceWords(name string) string {
	if n, ok := hook.ReferenceName(d.user, name); ok && n != "" {
		return "the reference document " + n
	}

	return "a reference document"
}

// referencedAnchor returns the instance of [ConstructReusedAnchor] of
// anchor, the one anchor of name in the user's text, when a reference
// document defines name too. The text aliases name with aliases, so an
// alias can read the anchor of the reference document instead of anchor.
func (d *Doc) referencedAnchor(anchor *ast.AnchorNode, name string, aliases []*ast.AliasNode) (instance, bool) {
	_, err := d.node.Resolver().Anchor(mapkey.NewAlias(name))
	if !errors.Is(err, paths.ErrReferenceAlias) {
		return instance{}, false
	}

	ref := d.referenceWords(name)

	in := instance{
		construct: ConstructReusedAnchor,
		at:        d.nodeSite(anchor, false),
		what:      fmt.Sprintf("a document that aliases the anchor &%s, which %s also defines", name, ref),
		fix:       fmt.Sprintf("rename &%s and its aliases", name),
	}

	for _, alias := range aliases {
		in.related = append(in.related, relatedSite{
			site: d.nodeSite(alias, false),
			note: fmt.Sprintf("*%s reads &%s here", name, name),
		})
	}

	return in, true
}

// nodeSite returns the site of node, a node of the current text, at its
// first token, as [Doc.site] gives it with key. The site of an alias or an
// anchor marks its name too.
func (d *Doc) nodeSite(node ast.Node, key bool) site {
	s, ok := d.tokenSpan(astnode.Token(node))
	if !ok {
		return site{path: paths.Doc(), at: position.NewRange(position.New(d.base, 0), position.New(d.base, 0))}
	}

	// The token of an alias or an anchor is its "*" or its "&", and the
	// site marks the name after it too.
	var name ast.Node

	switch x := node.(type) {
	case *ast.AliasNode:
		name = x.Value
	case *ast.AnchorNode:
		name = x.Name
	}

	if ns, ok := d.tokenSpan(astnode.Token(name)); ok && name != nil && ns.start == s.end {
		s.end = ns.end
	}

	return d.site(s, key)
}

// site returns the site of the token whose bytes are s in the current
// text, where every problem at the token binds. A token that the user
// wrote binds at its bytes in the user's text. A token that an edit wrote
// for a token of the user binds where the user wrote that token, as the
// key that a rename writes binds at the old key. Either binds under the
// path of the value that holds that token in the user's text, as the
// SitePath hook spells it with key. The path climbs entries and items
// alike, and with key it is the path of the collection that holds the
// entry or the item. No path names a << merge entry, so inside one the
// path is that of the mapping that holds the outermost such entry. A token
// that an edit created binds as [Doc.writtenSite] binds it. A detail names
// the call that wrote it, as [Doc.callDetails] gives it.
func (d *Doc) site(s span, key bool) site {
	o := d.originAt(s.start)

	if o < 0 && o != math.MinInt {
		return d.writtenSite(s.start, key)
	}

	at := d.userRange(s.start, s.end)
	path, inKey := d.userSitePath(at.Start, key)

	return site{path: path, at: at, origin: o, inKey: inKey}
}

// writtenSite returns the site of the token at off of the current text,
// which an edit wrote, as [Doc.site] gives it with key. A token that stands
// for text of the user binds where [Doc.noteSite] binds its note. The
// user's text holds no token for a token that an edit created. So such a
// token binds at the site of its value in the current text, as
// [Doc.madeSite] gives it. The call that wrote the token is the writer of
// the site, as [Doc.tokenWriter] gives it.
func (d *Doc) writtenSite(off int, key bool) site {
	o := d.origin[off]

	at, ok := d.madeSite(off)
	if !ok {
		at = d.noteSite(d.notes[-1-o], o, key)
	}

	at.writer = d.tokenWriter(off)

	return at
}

// madeSite returns the site of the token at off of the current text, a
// token that an edit created. That is the site of the value of the token
// in the current text. [Doc.tokenlessSite] gives it at the path that
// [niceyaml.Node.PathAt] gives at the token. Where PathAt gives no path,
// as in an entry that no path selects or at the place of a token without
// bytes, the byte takes the site of the root. It reports false for a byte
// that the user wrote or that stands for text of the user. It also reports
// false past the end of the text and when the user's text has no root.
func (d *Doc) madeSite(off int) (site, bool) {
	if !d.madeAt(off) || off >= len(d.origin) {
		return site{}, false
	}

	path, ok := d.node.PathAt(positionIn(d.cur, d.curLines, off))
	if !ok {
		path = paths.Doc()
	}

	s, ok := d.tokenlessSite(path)
	s.origin, s.inKey = d.origin[off], false

	return s, ok
}

// noteSite returns the site of a token that an edit wrote with n, the note
// of origin o, as [Doc.site] gives it with key. The site lacks the detail
// of a token that the edit created. A note that stands for text of the user
// binds where the user wrote that text. So does the note of a value that a
// Set wrote over a value of the user. A note without a place binds at the
// site of the root, as [Doc.rootSite] gives it.
func (d *Doc) noteSite(n note, o int, key bool) site {
	switch {
	case !n.located:
		at, _ := d.rootSite()
		at.origin = o

		return at

	case n.hasPath && n.path.IsRoot():
		return site{path: n.path, at: n.at, origin: o}

	case n.detail != "" && n.hasPath:
		return site{path: d.notePath(n), at: n.at, origin: o}

	case n.detail != "":
		path, _ := d.userSitePath(n.at.Start, false)

		return site{path: path, at: n.at, origin: o}

	// A note that stands for an item of a flow sequence names the item, as
	// [Doc.holderSite] names an item that the user wrote as a mapping
	// without braces. Such an item starts at its first key.
	case n.flowItem && n.hasPath && !key:
		return site{path: n.path, at: n.at, origin: o}
	}

	path, inKey := d.userSitePath(n.at.Start, key)

	return site{path: path, at: n.at, origin: o, inKey: inKey}
}

// notePath returns the path where a problem at a value that an edit wrote
// with n binds, a note with a detail and a path. That path names the value
// of the user's text that the edit replaced, as n holds it. No path names a
// << merge entry. So for a value inside one, it is the path of the mapping
// that holds the outermost such entry, as the SitePath hook gives it at the
// value.
func (d *Doc) notePath(n note) paths.Path {
	path, _, ok := hook.SitePath(d.user, n.at.Start, false)
	if _, inside := n.path.CutPrefix(path.Child("<<")); ok && inside {
		return path
	}

	return n.path
}

// userSitePath returns the path of the value of the user's text that holds
// the token at pos, as [Doc.site] spells it with key, or $ where no token
// lies at pos. It also reports whether pos lies in the key of the entry
// that the path names.
func (d *Doc) userSitePath(pos position.Position, key bool) (paths.Path, bool) {
	path, inKey, ok := hook.SitePath(d.user, pos, key)
	if !ok {
		return paths.Doc(), false
	}

	return path, inKey
}

// userPathAt returns the path of the value of the user's text that holds
// the token at pos, as [Doc.site] spells it. A token in the key of the entry
// that the path names gives the path of that key, which ends in `~`. It
// reports false where no token lies at pos.
func (d *Doc) userPathAt(pos position.Position) (paths.Path, bool) {
	path, inKey, ok := hook.SitePath(d.user, pos, false)
	if inKey {
		path = path.Key()
	}

	return path, ok
}

// originAt returns the origin of the byte at off of the current text, or
// the least int past the end of the text.
func (d *Doc) originAt(off int) int {
	if off < 0 || off >= len(d.origin) {
		return math.MinInt
	}

	return d.origin[off]
}

// zone is a part of the current text that a call must not reach: the part
// of an instance of a construct that a call reaches, as the Supported YAML
// section of the package documentation describes them. [Doc.beginZones]
// finds the zones of an Apply in the text that the Apply finds, and each
// call finds the parts of a zone again by the origins of their bytes, as
// [Doc.liveZone] tells.
type zone struct {
	// The instance of a call that reaches the zone by a change of its keys,
	// whose words differ from those of in, or nil.
	keyed *instance
	// The parts of the text that the zone holds whole, as the Apply found
	// them.
	parts []zonePart
	// The key sets that the zone holds.
	keys []zoneKeys
	// The instance of the construct.
	in instance
	// The offset of the site in the text that the Apply found, which orders
	// the zones.
	at int
}

// zonePart is a part of the text that a zone holds whole: its bytes and
// their origins, as the Apply found them.
type zonePart struct {
	text    string
	origins []int
}

// zoneKeys is a key set that a zone holds: the key texts of a mapping, or
// every key of the mapping when keys is nil. The mapping is the origin of
// its first byte. A Set writes every byte of its value with one origin, so
// the origin names no mapping inside such a value. [Doc.liveZone] finds
// the zone of a << merge key again in each parse. That zone names each
// mapping by the offset of its first byte in the parse instead, as live
// tells.
type zoneKeys struct {
	keys    []string
	mapping int
	start   int
	live    bool
}

// names reports whether k names the mapping whose first byte has the
// origin origin and the offset start in the current text.
func (k zoneKeys) names(origin, start int) bool {
	if k.live {
		return start == k.start
	}

	return origin == k.mapping
}

// beginZones finds the zones of the Apply that starts, in the text that it
// finds, as [Doc.constructZones] finds them.
func (d *Doc) beginZones() {
	d.zones = d.constructZones(false)

	slices.SortStableFunc(d.zones, func(a, b *zone) int { return cmp.Compare(a.at, b.at) })

	d.zoneKeys = make(map[zoneKey]bool, len(d.zones))
	for _, z := range d.zones {
		d.zoneKeys[keyOfZone(z)] = true
	}

	// The parse that the zones come from holds each zone of a merge key as
	// it is, as [Doc.liveZone] finds it.
	t := d.parseIndex()
	t.live = map[*zone]*zone{}

	for _, z := range d.zones {
		if z.in.construct == ConstructMergeForm {
			t.live[z] = z
		}
	}
}

// zonePart returns the part of a zone that holds the bytes s of the current
// text.
func (d *Doc) zonePart(s span) zonePart {
	s.start, s.end = max(s.start, 0), min(s.end, len(d.cur))

	return zonePart{text: d.cur[s.start:s.end], origins: slices.Clone(d.origin[s.start:s.end])}
}

// offsetOf returns the offset of the first byte of node in the current
// text, or [math.MinInt] when the text does not hold the node.
func (d *Doc) offsetOf(node ast.Node) int {
	s, ok := d.nodeSpan(node)
	if !ok {
		return math.MinInt
	}

	return s.start
}

// keyChange returns the change of the key text key in m, a mapping of the
// current text.
func (d *Doc) keyChange(m ast.Node, key string) keyChange {
	return keyChange{node: m, key: key, mapping: d.firstOrigin(m), start: d.offsetOf(m)}
}

// liveKeys returns the key set of every key of m, a mapping of the current
// text, for the zone of a << merge key, which names m by its offset.
func (d *Doc) liveKeys(m ast.Node) zoneKeys {
	return zoneKeys{mapping: d.firstOrigin(m), start: d.offsetOf(m), live: true}
}

// firstOrigin returns the origin of the first byte of node in the current
// text, or the least int for a node without bytes.
func (d *Doc) firstOrigin(node ast.Node) int {
	s, ok := d.nodeSpan(node)
	if !ok || s.start >= len(d.origin) {
		return math.MinInt
	}

	return d.origin[s.start]
}

// partSpan returns the bytes of the current text that hold p, a part of a
// zone, as the Apply found it, with the same origins. It reports false
// when the text does not hold the part so.
func (d *Doc) partSpan(p zonePart) (span, bool) {
	holds := func(at int) bool {
		end := at + len(p.text)

		return at >= 0 && end <= len(d.cur) && d.cur[at:end] == p.text && slices.Equal(d.origin[at:end], p.origins)
	}

	if len(p.origins) == 0 {
		return span{}, false
	}

	o := p.origins[0]

	for at := d.firstOffset(o); at >= 0 && at < len(d.origin); at++ {
		if d.origin[at] == o && holds(at) {
			return span{start: at, end: at + len(p.text)}, true
		}
	}

	return span{}, false
}

// originFirsts holds the first offset of each origin in one slice of the
// origins of a text: of each origin of the user's text by its offset, and
// of each note by its origin. An edit makes a new slice of origins and
// does not change the one before it, so the offsets hold as long as the
// text keeps that slice.
type originFirsts struct {
	notes  map[int]int
	origin []int
	user   []int
}

// firstOffset returns the first offset of the current text whose byte has
// the origin o, or -1 when no byte has it. It reads the first offsets of
// all the origins of the current text in one pass, on its first call for
// them.
func (d *Doc) firstOffset(o int) int {
	f := &d.firsts
	if f.notes == nil || len(f.origin) != len(d.origin) || len(d.origin) > 0 && &f.origin[0] != &d.origin[0] {
		top := -1
		for _, x := range d.origin {
			top = max(top, x)
		}

		*f = originFirsts{origin: d.origin, user: make([]int, top+1), notes: make(map[int]int)}

		for k := range f.user {
			f.user[k] = -1
		}

		for at, x := range d.origin {
			if x >= 0 {
				if f.user[x] < 0 {
					f.user[x] = at
				}
			} else if _, seen := f.notes[x]; !seen {
				f.notes[x] = at
			}
		}
	}

	if o >= 0 {
		if o < len(f.user) {
			return f.user[o]
		}

		return -1
	}

	at, ok := f.notes[o]
	if !ok {
		return -1
	}

	return at
}

// zoneSpans returns the bytes of the current text that hold the parts of
// z, in the order of its parts, without the parts that the text does not
// hold as the Apply found them.
func (d *Doc) zoneSpans(z *zone) []span {
	var out []span

	for _, p := range z.parts {
		if s, ok := d.partSpan(p); ok {
			out = append(out, s)
		}
	}

	return out
}

// callKind is what a call does at its place, as the Reaches rules of the
// package documentation read it.
type callKind int

const (
	// A callSet call sets the value at its place, as Set and a write of a
	// type field do.
	callSet callKind = iota
	// A callKey call renames the key of the entry at its place.
	callKey
	// A callRemove call removes the entry or the item at its place.
	callRemove
	// A callMove call moves the entry or the item at its place to a target.
	callMove
)

// reachCall is a call whose reach [Doc.reachProblem] checks.
type reachCall struct {
	// The new key of a SetKey.
	key string
	// What the call does, as a message names it, such as "sets $.a".
	named string
	// The entries that a later entry of their key hides on the path of the
	// call, as [Doc.walk] reads them.
	hidden []hiddenEntry
	// The path of the call, and the target of a Move.
	path, target paths.Path
	kind         callKind
	// Whether a SetKey writes a key that its mapping already writes, which
	// refuses the call before the rule of reference documents.
	repeats bool
}

// callWalk is the walk of the path of a call through the current text, as
// the Reaches rules read it.
type callWalk struct {
	// The content of the value of the last step, which the next step of the
	// path looks into, or nil when the walk did not read it.
	lookup ast.Node
	// The place of the call.
	p *place
	// The nodes that the walk visits: the root, the key and the value of
	// each entry on the way, and the anchored value of each alias that the
	// walk reads through. A walk through a << merge key visits the key, its
	// value, and the mapping that it brings in.
	steps []ast.Node
	// The step of the path at which the walk visits each node of steps: the
	// number of the selectors of the path that it found then.
	found []int
	// The << merge keys that the walk looks through.
	merges []mergeStep
}

// stepOf returns the step of the path at which w visits its node at the
// index i of its steps, or the step after the last for an index past them.
// A step visits the key and the value of its entry, the anchored value of
// each alias of the value, and the nodes of each << merge key that its
// lookup goes through.
func (w *callWalk) stepOf(i int) int {
	if i < len(w.found) {
		return w.found[i]
	}

	if len(w.found) == 0 {
		return 0
	}

	return w.found[len(w.found)-1] + 1
}

// mergeStep is a lookup of name in the mapping m that a << merge key of m,
// entry, answers from s, the mapping that the merge key brings in. The
// lookup comes before the step of its walk at the index at.
type mergeStep struct {
	m, s  ast.Node
	entry *ast.MappingValueNode
	name  string
	at    int
}

// walkCall returns the walk of path through the current text, where hidden
// names the entries that a later entry of their key hides, as [Doc.walk]
// reads them. It returns the problem of a path that the text does not
// take, with the walk of the steps before the step that fails, whose place
// is nil.
func (d *Doc) walkCall(path paths.Path, hidden []hiddenEntry) (*callWalk, error) {
	var (
		w    callWalk
		prev ast.Node
	)

	res := d.node.Resolver()
	sels := slices.Collect(path.Selectors())

	p, err := d.walk(path, hidden, func(p *place, cur ast.Node) bool {
		if e := p.entry; e != nil {
			if p.merged && prev != nil && p.found > 0 {
				w.addMerges(d.mergeChain(prev, e, sels[p.found-1].Name, nil))
			}

			w.steps = append(w.steps, e.Key)
		}

		w.steps = append(w.steps, cur)

		// The walk reads through each alias on the way to the place.
		w.lookup = nil

		if p.found < len(sels) {
			// The walk itself returns the problem of an alias that the text
			// does not resolve.
			content, aliases, err := d.readValue(cur)
			if err == nil {
				for _, a := range aliases {
					anchor, err := res.Anchor(a)
					if err == nil {
						w.steps = append(w.steps, anchor)
					}
				}

				w.lookup = content
			}

			prev = content
		}

		for len(w.found) < len(w.steps) {
			w.found = append(w.found, p.found)
		}

		return true
	})
	if err != nil {
		return &w, err
	}

	w.p = p

	return &w, nil
}

// addMerges adds the merge steps ms to w, with the nodes that each visits.
func (w *callWalk) addMerges(ms []mergeStep) {
	for _, m := range ms {
		m.at = len(w.steps)
		w.steps = append(w.steps, m.entry.Key, m.entry.Value, m.s)
		w.merges = append(w.merges, m)
	}
}

// zoneStep returns the index of the first step of w that reaches z: the
// first node on its way that lies inside the zone, or the first lookup of a
// key of the zone through a << merge key. A zone that no step of w reaches
// comes after every step, as one that a call reaches at its place or by the
// change of a key.
func (d *Doc) zoneStep(z *zone, w *callWalk) int {
	spans := d.zoneSpans(z)
	at := len(w.steps)

	for i, n := range w.steps {
		if d.nodeIn(n, spans) {
			at = i

			break
		}
	}

	for _, m := range w.merges {
		if m.at < at && slices.ContainsFunc(z.keys, func(k zoneKeys) bool { return d.looksUp(k, []mergeStep{m}) }) {
			at = m.at
		}
	}

	return at
}

// mergeChain returns the << merge keys through which a lookup of name in
// m, a mapping, reaches entry, an entry that m brings in. Each merge key of
// m brings in the mappings that its value reads, and a mapping that holds
// a merge key brings in the entries of its own sources.
func (d *Doc) mergeChain(m ast.Node, entry *ast.MappingValueNode, name string, seen map[ast.Node]bool) []mergeStep {
	if seen == nil {
		seen = make(map[ast.Node]bool)
	}

	if seen[m] {
		return nil
	}

	seen[m] = true

	entries, _ := readEntries(m)

	for _, me := range entries {
		if !astnode.IsMergeKey(me.Key) {
			continue
		}

		for _, src := range d.mergeSources(me) {
			step := mergeStep{m: m, entry: me, s: src, name: name}

			own, _ := readEntries(astnode.Content(src))
			if slices.Contains(own, entry) {
				return []mergeStep{step}
			}

			if more := d.mergeChain(astnode.Content(src), entry, name, seen); more != nil {
				return append([]mergeStep{step}, more...)
			}
		}
	}

	return nil
}

// mergeSources returns the values that the << merge key of me brings in:
// the anchored value of each alias of its value, or of the items of a
// sequence there, and a mapping that its value writes itself.
func (d *Doc) mergeSources(me *ast.MappingValueNode) []ast.Node {
	res := d.node.Resolver()

	var out []ast.Node

	for _, v := range astnode.MergeValues(me) {
		alias, ok := v.(*ast.AliasNode)
		if !ok {
			out = append(out, v)

			continue
		}

		anchor, err := res.Anchor(alias)
		if err == nil {
			out = append(out, anchor)
		}
	}

	return out
}

// walkHolder returns the value that holds the place of w when the text lacks
// it: the deepest value on its path that the text holds.
func (d *Doc) walkHolder(w *callWalk) ast.Node {
	if astnode.IsNil(w.p.container) {
		return d.node.AST()
	}

	return w.p.container
}

// takenSpan returns the bytes of the value that a call of kind takes at
// the place of w, as the Reaches rules define it, and reports false for a
// call that takes no value. A Remove and a Move take the entry, with its
// key, or the item, with its "-". A Set takes the value, without the
// anchor that it keeps.
func (d *Doc) takenSpan(w *callWalk, kind callKind) (span, bool) {
	p := w.p
	if !p.exists {
		return span{}, false
	}

	vs, ok := d.valueSpan(p.value)
	if !ok {
		return span{}, false
	}

	switch kind {
	case callKey:
		return span{}, false

	case callRemove, callMove:
		switch {
		case p.entry != nil:
			if ks, ok := d.tokenSpan(astnode.Token(p.entry.Key)); ok {
				vs.start = min(vs.start, ks.start)
			}

			if es, ok := d.nodeSpan(p.entry); ok {
				vs.end = max(vs.end, es.end)
			}

		case p.seq != nil && p.index >= 0:
			if ms, ok := d.tokenSpan(itemMark(p.seq, p.index)); ok {
				vs.start = min(vs.start, ms.start)
			}
		}

		return vs, true

	case callSet:
		if a, ok := p.value.(*ast.AnchorNode); ok {
			if ps, ok := d.propsSpan(a); ok {
				vs.start = ps.end
			}
		}

		return vs, true
	}

	return span{}, false
}

// within reports whether the bytes inner lie inside outer.
func within(inner, outer span) bool {
	return inner.start >= outer.start && inner.end <= outer.end
}

// nodeIn reports whether node lies inside one of spans. A node without
// bytes at the end of a span lies outside it, unless the span holds no
// bytes either.
func (d *Doc) nodeIn(node ast.Node, spans []span) bool {
	s, ok := d.valueSpan(node)
	if !ok {
		return false
	}

	for _, z := range spans {
		if within(s, z) && (s.start < s.end || s.start < z.end || z.start == z.end) {
			return true
		}
	}

	return false
}

// keyChange is a key text of a mapping that a call creates, removes,
// renames, or moves. The mapping is the origin of its first byte, at the
// offset start of the current text. Step is the step of the path of the
// call that looks the key up in the mapping, as [callWalk.stepOf] tells,
// and target tells that the target of a Move looks it up instead. Node is
// the mapping in the current text.
type keyChange struct {
	node    ast.Node
	key     string
	mapping int
	start   int
	step    int
	target  bool
}

// keyChanges returns the key texts of the mappings that c, whose walk is w
// and whose target walk is tw, creates, removes, renames, or moves. A
// Remove of a << merge entry leaves out its key when skipMerge is true.
func (d *Doc) keyChanges(c reachCall, w, tw *callWalk, skipMerge bool) []keyChange {
	var out []keyChange

	keyOf := func(e *ast.MappingValueNode) string {
		text, _ := astnode.ScalarText(e.Key)

		return text
	}

	// The change of key in m, a mapping that cw, the walk of the call or of
	// the target, visits. The change meets m at the step that looks the key
	// up in m, the step of the node after m, as a zone around the entry of
	// the key does.
	change := func(cw *callWalk, m ast.Node, key string) keyChange {
		ch := d.keyChange(m, key)

		i := slices.IndexFunc(cw.steps, func(n ast.Node) bool { return n == m || astnode.Content(n) == m })
		if i < 0 {
			i = len(cw.steps)
		}

		ch.step, ch.target = cw.stepOf(i+1), cw != w

		return ch
	}

	created := func(cw *callWalk) {
		if cw == nil || cw.p.exists {
			return
		}

		holder := d.walkHolder(cw)
		if _, ok := readEntries(astnode.Content(holder)); !ok {
			return
		}

		sels := slices.Collect(cw.p.path.Selectors())
		if cw.p.found < len(sels) && sels[cw.p.found].Kind == paths.SelectorChild {
			out = append(out, change(cw, holder, sels[cw.p.found].Name))
		}
	}

	p := w.p

	switch c.kind {
	case callSet:
		created(w)

	case callRemove:
		if p.exists && p.entry != nil && (!skipMerge || !astnode.IsMergeKey(p.entry.Key)) {
			out = append(out, change(w, p.container, keyOf(p.entry)))
		}

	case callKey:
		if p.exists && p.entry != nil {
			out = append(out, change(w, p.container, keyOf(p.entry)), change(w, p.container, c.key))
		}

	case callMove:
		if p.exists && p.entry != nil {
			out = append(out, change(w, p.container, keyOf(p.entry)))
		}

		created(tw)
	}

	return out
}

// reaches reports whether c, a call whose walk is w and whose target walk
// is tw, reaches z, as the Reaches rules of the package documentation
// define it. A call reaches a zone when a node on its way, its place, or
// the value that holds a place that the text lacks lies inside the zone,
// and a SetKey reaches it through the key and the values above it. A Move
// of a value that holds a zone reaches it, and so does a Move whose target
// reaches it. A call that takes the whole zone of a construct does not
// reach it. A call reaches the key sets of a zone when it creates,
// removes, renames, or moves an entry of the mapping with such a key, or
// when its walk looks such a key up through a << merge key of the mapping.
// Reaches tells how in a [reach], and it reports false for a call that does
// not reach z.
func (d *Doc) reaches(z *zone, c reachCall, w, tw *callWalk) (reach, bool) {
	spans := d.zoneSpans(z)
	taken, takes := d.takenSpan(w, c.kind)

	whole := takes && len(spans) > 0

	// The zone of a merge key in a refused form holds the merge entries of
	// its sources, which can lie outside its mapping. A call that takes the
	// mapping takes the merge entry, and so the zone whole.
	switch z.in.construct {
	case ConstructMergeForm:
		first, ok := d.partSpan(z.parts[0])
		whole = whole && ok && within(first, taken)

	default:
		for _, s := range spans {
			whole = whole && within(s, taken)
		}
	}

	// A call inside a flow collection or a pair of the zone does not take
	// the zone whole.
	if whole && z.in.construct == ConstructFlowLayout && w.p.exists {
		if cs, ok := d.nodeSpan(w.p.container); ok && within(cs, spans[0]) {
			whole = false
		}
	}

	r := reach{step: -1}
	reached, changed := false, false

	// At notes a step of the path that reaches the zone.
	at := func(step int) {
		if r.step < 0 || step < r.step {
			r.step = step
		}
	}

	if len(spans) > 0 {
		step, onPath, target := d.spansStep(spans, c, w, tw, taken, takes, whole)
		if onPath {
			at(step)
		}

		r.target, reached = target, onPath || target
	}

	changes := d.keyChanges(c, w, tw, z.in.construct == ConstructMergeForm)

	for _, k := range z.keys {
		if whole && c.kind != callMove {
			if ms, ok := d.mappingSpan(k); ok && within(ms, taken) {
				continue
			}
		}

		for _, m := range w.merges {
			if d.looksUp(k, []mergeStep{m}) {
				at(w.stepOf(m.at))

				reached = true
			}
		}

		if tw != nil && d.looksUp(k, tw.merges) {
			r.target, reached = true, true
		}

		for _, ch := range changes {
			if !k.names(ch.mapping, ch.start) || k.keys != nil && !slices.Contains(k.keys, ch.key) {
				continue
			}

			changed = true

			if ch.target {
				r.target = true
			} else {
				at(ch.step)
			}
		}
	}

	r.keyed = changed && !reached

	return r, reached || changed
}

// reach is how a call reaches a zone, as [Doc.reaches] finds it.
type reach struct {
	// The first step of the path of the call that reaches the zone, as
	// [callWalk.stepOf] tells, or -1 when only the target of a Move reaches
	// it. A step reaches the zone when a node that it visits lies inside the
	// zone, when it looks a key of the zone up through a << merge key, and
	// when it looks a key up whose change reaches the zone, as [keyChange]
	// tells. A Move of a value that holds the zone reaches it at its place.
	step int
	// Whether the call reaches the zone by the change of a key alone.
	keyed bool
	// Whether the target of a Move reaches the zone.
	target bool
}

// looksUp reports whether one of merges looks a key of k, a key set of a
// zone, up through a << merge key of its mapping.
func (d *Doc) looksUp(k zoneKeys, merges []mergeStep) bool {
	for _, m := range merges {
		if k.names(d.firstOrigin(m.m), d.offsetOf(m.m)) && (k.keys == nil || slices.Contains(k.keys, m.name)) {
			return true
		}
	}

	return false
}

// spansStep returns the first step of the path of c that reaches spans,
// the parts of a zone as the text holds them, by rule 1 of the Reaches
// rules, as [Doc.reaches] describes. It reports whether the path reaches
// them, and whether the target of a Move does. Taken is the value that c
// takes, when takes is true, and whole tells that it holds every part.
func (d *Doc) spansStep(spans []span, c reachCall, w, tw *callWalk, taken span, takes, whole bool) (int, bool, bool) {
	step, onPath := w.stepOf(len(w.steps)), false

	// The Move writes the value again at its target, so it reaches the zone
	// at its place.
	if c.kind == callMove && takes && slices.ContainsFunc(spans, func(s span) bool { return within(s, taken) }) {
		step, onPath = w.stepOf(len(w.steps)-1), true
	}

	if at, ok := d.walksIn(spans, w, c.kind == callKey); !whole && ok {
		step, onPath = min(step, at), true
	}

	target := false
	if tw != nil {
		_, target = d.walksIn(spans, tw, false)
	}

	return step, onPath, target
}

// walksIn returns the step of the first node on the way of w, its place,
// or the value that holds a place that the text lacks, that lies inside one
// of spans, the parts of a zone, and reports whether one does. A walk of a
// SetKey, as key tells, leaves out the value of the entry at its place.
func (d *Doc) walksIn(spans []span, w *callWalk, key bool) (int, bool) {
	steps := w.steps
	if key && len(steps) > 0 && w.p.exists && steps[len(steps)-1] == w.p.value {
		steps = steps[:len(steps)-1]
	}

	if i := slices.IndexFunc(steps, func(n ast.Node) bool { return d.nodeIn(n, spans) }); i >= 0 {
		return w.stepOf(i), true
	}

	if w.p.exists || !d.nodeIn(d.walkHolder(w), spans) {
		return 0, false
	}

	return w.stepOf(len(w.steps)), true
}

// mappingSpan returns the bytes of the mapping of the current text that k
// names, as [Doc.keysMapping] finds it.
func (d *Doc) mappingSpan(k zoneKeys) (span, bool) {
	m := d.keysMapping(k)
	if m == nil {
		return span{}, false
	}

	return d.nodeSpan(m)
}

// keysMapping returns the mapping of the current text that k names, as
// [zoneKeys.names] tells, or nil.
func (d *Doc) keysMapping(k zoneKeys) ast.Node {
	var found ast.Node

	d.inspect(func(n ast.Node) bool {
		if found != nil {
			return false
		}

		if _, isMapping := n.(*ast.MappingNode); isMapping && k.names(d.firstOrigin(n), d.offsetOf(n)) {
			found = n
		}

		return found == nil
	})

	return found
}

// reachProblem returns the problem of c when it reaches a zone of the Apply, as
// [Doc.reaches] tells, or nil. The path of the call is checked step by
// step from the root, so the zone that the first step reaches decides the
// problem, as [Doc.reaches] tells, and then the order of the sites. The
// target of a Move is checked after the path. A call whose path the text
// does not take reports that problem itself.
func (d *Doc) reachProblem(c reachCall) error {
	w, err := d.walkCall(c.path, c.hidden)
	if err != nil {
		return nil //nolint:nilerr // The call reports the problem of its path.
	}

	var tw *callWalk

	if c.kind == callMove {
		tw, err = d.walkCall(c.target, nil)
		if err != nil {
			tw = nil
		}
	}

	var (
		first, target *zone
		step          int
	)

	ch, refMerge, changes := d.referenceChange(c, w)

	for _, z := range d.zones {
		live, ok := d.liveZone(z)
		if !ok {
			continue
		}

		r, ok := d.reaches(live, c, w, tw)
		if !ok {
			continue
		}

		if r.keyed && live.keyed != nil {
			kz := *live
			kz.in = *live.keyed
			live = &kz
		}

		if r.step >= 0 && (first == nil || r.step < step) {
			first, step = live, r.step
		}

		if r.target && target == nil {
			target = live
		}
	}

	// A change of a name that a merge of a reference document decides meets
	// the mapping at the step that looks the name up there. A zone meets the
	// call at the step that reaches it. The zone refuses the call first at
	// that step or at an earlier one. A key that a SetKey repeats refuses
	// the call at the step of its entry too, after the zones.
	if changes && !c.repeats && (first == nil || ch.step < step) {
		return d.referenceProblem(c, w, refMerge)
	}

	// The path of the call comes first, and the target of a Move after it.
	// The target is a place, not a path that the call checks step by step,
	// so the first zone that it reaches in the order of the sites decides.
	if first == nil {
		first = target
	}

	if first == nil {
		return nil
	}

	return d.zoneProblem(first, c.named)
}

// stepProblem returns the problem of c, a call whose path meets a problem
// of the path or selects a place that the text lacks. A scalar where the
// path selects a key is such a problem. The problem of the call comes back
// when a step of the path reaches a zone of the Apply, or nil. The path of a
// call is checked step by step from the root. The first step that meets a
// refusal, a problem of the path, or an alias of a reference document
// decides the problem of the call. A step reaches a zone when a node that it
// visits lies inside the zone. It also reaches the zone when it looks a key
// of the zone up through a << merge key, as [Doc.reaches] tells for a call.
// A step can look up a key that the text lacks, or fail. It then looks the
// key up through each merge key of its mapping, so it reaches the zone of
// such a merge key. A step that meets an alias of a reference document stops
// at the alias, also at an item of a merge key, before it looks the key up
// through the merge keys of its mapping. A merge key in a refused form then
// refuses the call only when a node of the walk, or the mapping that the
// step looks the key up in, lies inside its zone. Two zones that one step
// reaches come in the order of their sites.
func (d *Doc) stepProblem(c reachCall) error {
	// A path of another selector fails as a call, before its steps.
	if !plain(c.path) {
		return nil
	}

	w, err := d.walkCall(c.path, c.hidden)

	var merges []*ast.MappingValueNode

	// A step that meets an alias of a reference document stops there, before
	// it looks the key up through the merge keys of its mapping.
	sels := slices.Collect(c.path.Selectors())
	if done := len(w.steps) > 0 && (err != nil || !w.p.exists); done && w.lookup != nil &&
		!errors.Is(err, paths.ErrReferenceAlias) {
		at := len(sels)
		if err == nil {
			at = w.p.found
		}

		// A path never selects a << merge key, so a lookup of "<<" names a
		// key that the mapping lacks, and it looks the key up through the
		// merge keys as a lookup of any other key does.
		child := at < len(sels) && sels[at].Kind == paths.SelectorChild

		entries, _ := readEntries(w.lookup)
		for _, e := range entries {
			if astnode.IsMergeKey(e.Key) && (err != nil || child) {
				merges = append(merges, e)
			}
		}
	}

	var (
		first *zone
		step  int
	)

	for _, z := range d.zones {
		live, ok := d.liveZone(z)
		if !ok {
			continue
		}

		// The lookup in the last mapping, and through its merge keys, comes
		// after every step. The mapping holds the place of the call, also
		// under an anchor or a tag of its value, which lie outside the zone
		// as any anchor or tag of a step does.
		spans := d.zoneSpans(live)
		at := d.zoneStep(live, w)
		holds := w.lookup != nil && d.nodeIn(w.lookup, spans)

		if at == len(w.steps) && !holds &&
			!slices.ContainsFunc(merges, func(e *ast.MappingValueNode) bool { return d.nodeIn(e, spans) }) {
			continue
		}

		if at = w.stepOf(at); first == nil || at < step {
			first, step = live, at
		}
	}

	if first == nil {
		return nil
	}

	return d.zoneProblem(first, c.named)
}

// liveZone returns z as the current text holds it, and reports false when
// the text no longer holds its construct. The zone of a << merge key in a
// refused form holds the keys of the mappings that the merge key reads. A
// call can write another mapping in place of a source, as a Set at the
// anchor of the source does. The merge key then reads that mapping. A call
// can also take the merge key away, or leave it in the form that the
// engine edits, as a Set of a source that drops its own merge key does. So
// liveZone finds the zone of a merge key again at its entry, which the zone
// holds whole, in the text that the call finds, once for each parse. The
// zones of the other constructs keep their parts.
func (d *Doc) liveZone(z *zone) (*zone, bool) {
	if z.in.construct != ConstructMergeForm {
		return z, true
	}

	t := d.parseIndex()
	if t.live == nil {
		t.live = map[*zone]*zone{}
	}

	live, found := t.live[z]
	if !found {
		live = d.findZone(z)
		t.live[z] = live
	}

	return live, live != nil
}

// findZone returns the zone in the current text of the << merge key of z,
// a zone of [ConstructMergeForm]. It returns nil when the text no longer
// holds the merge key in a refused form.
func (d *Doc) findZone(z *zone) *zone {
	s, ok := d.partSpan(z.parts[0])
	if !ok {
		return nil
	}

	e := d.mergeKeyStarts()[s.start]
	if e == nil {
		return nil
	}

	m := d.mappingOf(e)
	entries, _ := d.mappingEntries(m)

	var merges []*ast.MappingValueNode

	for _, x := range entries {
		if astnode.IsMergeKey(x.Key) {
			merges = append(merges, x)
		}
	}

	live, ok := d.mergeZone(m, e, firstOfMany(merges), false)
	if !ok {
		return nil
	}

	live.at = z.at

	return live
}

// mergeKeyStarts returns each entry of the current text whose key YAML
// reads as a << merge key, by the start of its key, as [Doc.findZone] looks
// them up. The index of the parse keeps them.
func (d *Doc) mergeKeyStarts() map[int]*ast.MappingValueNode {
	t := d.parseIndex()
	if t.mergeStarts != nil {
		return t.mergeStarts
	}

	out := map[int]*ast.MappingValueNode{}
	t.mergeStarts = out

	d.inspect(func(n ast.Node) bool {
		e, ok := n.(*ast.MappingValueNode)
		if !ok || e.Key == nil || !astnode.IsMergeKey(e.Key) {
			return true
		}

		if s, ok := d.nodeSpan(e.Key); ok {
			out[s.start] = e
		}

		return true
	})

	return out
}

// zoneProblem returns the problem of a call that named names, which
// reaches z. It binds at the site of z in the user's text, and the Apply
// records it.
func (d *Doc) zoneProblem(z *zone, named string) error {
	problem := d.refusal(&z.in, named)
	d.record(problem)

	return problem
}

// offsetPath returns the path of the innermost value of the current text
// whose bytes hold the byte at off, or that lies at off without bytes, as
// [Doc.valueSpan] places such a value, as the null of "b: !!str" lies at
// its tag when go-yaml's lexer drops the tag.
func (d *Doc) offsetPath(off int) paths.Path {
	var (
		at   ast.Node
		size = math.MaxInt
	)

	d.inspect(func(n ast.Node) bool {
		if _, ok := n.(*ast.MappingValueNode); ok {
			return true
		}

		s, ok := d.valueSpan(n)
		if ok && s.start <= off && (off < s.end || s.start == off && s.end == off) && s.end-s.start <= size {
			at, size = n, s.end-s.start
		}

		return true
	})

	if path, ok := d.valuePath(at); ok {
		return path
	}

	return paths.Doc()
}

// offsetSite returns the site of the size bytes at off of the current text,
// in the user's text, under the path of the innermost value that holds
// them. Such bytes, a byte order mark or a line break, are not a token, so
// they bind at their place and not as [Doc.site] binds a token.
func (d *Doc) offsetSite(off, size int) site {
	return site{path: d.offsetPath(off), at: d.userRange(off, off+size), origin: d.originAt(off)}
}

// markPlaces returns the offsets in orig of the byte order marks of the
// user's text that the tokenizer dropped, as [bom.Drop] finds them: first
// the marks in the prefix of the document, then the marks inside the
// document. A mark in the prefix stands before the first token of the
// document and its "---", after comment lines, directives and empty lines
// alone. A mark after a "..." line that ends the document stands in the
// prefix of the next document, so it counts in neither. Each other mark
// stands inside the document, as after its "---".
func (d *Doc) markPlaces() ([]int, []int) {
	const (
		inPrefix = iota
		inDocument
		afterEnd
	)

	var prefix, inside []int

	state, i := inPrefix, 0
	starts := lineStarts(d.orig)

	// Take sorts the marks before the offset end by the state of the lines
	// before them.
	take := func(end int) {
		for ; i < len(d.marks) && d.marks[i] <= end; i++ {
			switch state {
			case inPrefix:
				prefix = append(prefix, d.marks[i])
			case inDocument:
				inside = append(inside, d.marks[i])
			}
		}
	}

	for j, at := range starts {
		end := len(d.orig)
		if j+1 < len(starts) {
			end = starts[j+1]
		}

		take(at)

		body := strings.TrimLeft(strings.TrimRight(d.orig[at:end], "\r\n"), " \t")

		switch {
		case strings.HasPrefix(body, "...") && (len(body) == 3 || strings.ContainsAny(body[3:4], " \t")):
			state = afterEnd
		case state == afterEnd, body == "" || strings.HasPrefix(body, "#") ||
			state == inPrefix && strings.HasPrefix(body, "%"):
		default:
			state = inDocument
		}
	}

	take(len(d.orig))

	return prefix, inside
}

// markRefusal returns the problem of the user's text when it holds a byte
// order mark inside the document, which YAML allows only in the prefix of a
// document, as [Doc.markPlaces] finds it, or nil. The tokenizer of the
// Source drops a mark at the start of a line after the "---" of a
// document, but go-yaml reads such a mark as text. The problem binds at the
// line of the mark, and [niceyaml.IsInvalid] reports it.
func (d *Doc) markRefusal() error {
	_, inside := d.markPlaces()
	if len(inside) == 0 {
		return nil
	}

	s := d.offsetSite(inside[0], 1)

	//nolint:wrapcheck // The binding is the problem of the document.
	return d.user.Bind(niceyaml.Invalid(
		fmt.Errorf("%w at %s inside the document, where YAML does not allow it", errMarkInside, d.userAt(inside[0])),
		niceyaml.AtRange(s.at), niceyaml.AtPath(s.path),
		niceyaml.WithDetails(errors.New(markFix)),
	))
}

// constructZones returns the zones of the constructs of the current text
// that a call reaches: each << merge key in another form than one alias of
// an anchored mapping of the document, each key that a mapping repeats,
// each explicit key, each flow collection with a comment inside it, and
// each pair without braces in a flow sequence. With heads, the zone of a
// merge key holds only the entry of the merge key and the keys of its
// mapping, as [Doc.mergeZone] tells.
func (d *Doc) constructZones(heads bool) []*zone {
	var out []*zone

	w := d.walked()

	// The groups keep their storage for the parses of the calls after.
	if d.groups == nil {
		d.groups = &keyGroups{first: make(map[string]keyFirst)}
	}

	groups := d.groups

	for i, n := range w.nodes {
		switch x := n.(type) {
		case *ast.MappingNode:
			if !x.IsFlowStyle && len(x.Values) > 0 {
				if seq, ok := w.parentAt(i).(*ast.SequenceNode); ok && seq.IsFlowStyle {
					z := d.flowZone(x, false)
					z.in.at = d.entrySite(x.Values[0])
					out = append(out, z)
				}
			}

			out = append(out, d.mappingZones(w, i, x.Values, groups, heads)...)

		case *ast.MappingValueNode:
			if _, ok := w.parentAt(i).(*ast.MappingNode); !ok {
				out = append(out, d.mappingZones(w, i, []*ast.MappingValueNode{x}, groups, heads)...)
			}

		case *ast.SequenceNode:
			if x.IsFlowStyle {
				if z, ok := d.commentZone(w, i); ok {
					out = append(out, z)
				}
			}
		}
	}

	return out
}

// mappingZones returns the zones of the merge keys, the repeated keys, and
// the explicit keys of m, a mapping of the current text with entries. The
// walk w of the parse holds m at idx, and groups finds the repeated keys. A
// mapping with more than one << merge key holds merge keys of a refused
// form, whose zones hold the whole mapping, so its merge keys count as
// no repeated key. A merge key of one alias of a reference document has no
// zone, so where no merge key of m has one, m repeats the key << instead.
// Heads tells what the zone of a merge key holds, as [Doc.mergeZone] reads
// it.
func (d *Doc) mappingZones(
	w *nodeWalk, idx int, entries []*ast.MappingValueNode, groups *keyGroups, heads bool,
) []*zone {
	m := w.nodes[idx]

	var (
		out    []*zone
		merges []*ast.MappingValueNode
	)

	res := d.node.Resolver()

	for _, e := range entries {
		if _, ok := e.Key.(*ast.MappingKeyNode); ok {
			out = append(out, d.explicitZone(e))
		}

		if astnode.IsMergeKey(e.Key) {
			merges = append(merges, e)

			continue
		}

		if name, ok := res.KeyName(e.Key); ok {
			groups.add(e, name)
		}
	}

	groups.repeated(func(name string, entries []*ast.MappingValueNode) {
		out = append(out, d.repeatZone(m, name, entries))
	})

	if mm, ok := m.(*ast.MappingNode); ok && mm.IsFlowStyle {
		if z, ok := d.commentZone(w, idx); ok {
			out = append(out, z)
		}
	}

	zoned := false

	for _, e := range merges {
		if z, ok := d.mergeZone(m, e, firstOfMany(merges), heads); ok {
			out, zoned = append(out, z), true
		}
	}

	if len(merges) > 1 && !zoned {
		out = append(out, d.repeatZone(m, "<<", merges))
	}

	return out
}

// keyGroups groups the entries of one mapping by their keys, as
// [Doc.mappingZones] reads them, to find the keys that the mapping repeats.
// The groups of the mappings of one walk share their storage.
type keyGroups struct {
	first map[string]keyFirst
	names []string
	count []int
	named []namedEntry
	// The number of the mapping that the groups read. An entry of first
	// from an earlier mapping holds another number, so it does not count.
	gen int
}

// keyFirst is the index of a key in the names of the mapping gen of a
// [keyGroups].
type keyFirst struct {
	at, gen int
}

// namedEntry is an entry of a mapping and the index of its key in the
// names of a [keyGroups].
type namedEntry struct {
	e    *ast.MappingValueNode
	name int
}

// add adds e, an entry whose key name names, to the mapping of g.
func (g *keyGroups) add(e *ast.MappingValueNode, name string) {
	f, ok := g.first[name]

	j := f.at
	if !ok || f.gen != g.gen {
		j = len(g.names)
		g.first[name] = keyFirst{at: j, gen: g.gen}
		g.names = append(g.names, name)
		g.count = append(g.count, 0)
	}

	g.count[j]++
	g.named = append(g.named, namedEntry{e: e, name: j})
}

// repeated calls f with each key that the mapping of g repeats, in the
// order of the first entry of each, and with the entries of the key in
// their order. It then empties g for the next mapping.
func (g *keyGroups) repeated(f func(name string, entries []*ast.MappingValueNode)) {
	for j, name := range g.names {
		if g.count[j] < 2 {
			continue
		}

		var entries []*ast.MappingValueNode

		for _, ne := range g.named {
			if ne.name == j {
				entries = append(entries, ne.e)
			}
		}

		f(name, entries)
	}

	g.names, g.count, g.named = g.names[:0], g.count[:0], g.named[:0]
	g.gen++
}

// entrySite returns the site of e, an entry of the current text, at its
// first token, the "?" of an explicit key or the first token of its key, as
// [Doc.site] gives it. The path is that of the entry in the user's text,
// or of the mapping that holds a << merge entry.
func (d *Doc) entrySite(e *ast.MappingValueNode) site {
	return d.nodeSite(e.Key, false)
}

// explicitZone returns the zone of e, an entry with an explicit key. The
// zone holds the entry whole, so a Remove of the entry passes.
func (d *Doc) explicitZone(e *ast.MappingValueNode) *zone {
	es, _ := d.nodeSpan(e)

	return &zone{
		parts: []zonePart{d.zonePart(es)},
		at:    es.start,
		in: instance{
			construct: ConstructExplicitKey,
			at:        d.entrySite(e),
			reach:     "reaches an entry with an explicit key",
			object:    "edit such an entry other than by removing it",
			fix:       `write the key without the "?"`,
		},
	}
}

// repeatZone returns the zone of the entries of m whose key is name, which
// m repeats. The zone holds each entry whole and the key in the keys of m.
// Each entry binds at its site, as [Doc.entrySite] gives it.
func (d *Doc) repeatZone(m ast.Node, name string, entries []*ast.MappingValueNode) *zone {
	z := &zone{
		keys: []zoneKeys{{mapping: d.firstOrigin(m), keys: []string{name}}},
		in: instance{
			construct: ConstructRepeatedKey,
			at:        d.entrySite(entries[0]),
			reach:     fmt.Sprintf("reaches %s, a key that its mapping repeats", cut(strconv.Quote(name))),
			object:    "edit the entries of a repeated key",
			fix:       "remove all but one of the entries of the key",
		},
	}

	for i, e := range entries {
		es, _ := d.nodeSpan(e)
		z.parts = append(z.parts, d.zonePart(es))

		if i == 0 {
			z.at = es.start

			continue
		}

		z.in.related = append(z.in.related, relatedSite{
			site: d.entrySite(e),
			note: fmt.Sprintf("the key %s is written again here", cut(strconv.Quote(name))),
		})
	}

	return z
}

// commentZone returns the zone of flow, a flow collection of the current
// text, when a comment lies inside it and inside no flow collection around
// it. The walk w of the parse holds flow at idx. The zone runs
// from the "[" or "{" of the collection to its end. YAML opens a comment
// with a "#" after white space or at the start of a line, so a "#" right
// after a comma opens none, though go-yaml's lexer reads one there.
func (d *Doc) commentZone(w *nodeWalk, idx int) (*zone, bool) {
	flow := w.nodes[idx]

	for up := w.up[idx]; up >= 0; up = w.up[up] {
		if isFlow(w.nodes[up]) {
			return nil, false
		}
	}

	// The first comment after the "[" or "{" of the collection. A text
	// without such comments has none inside a collection.
	comments := d.openComments()
	if len(comments) == 0 {
		return nil, false
	}

	fs, ok := d.nodeSpan(flow)
	if !ok {
		return nil, false
	}

	i, _ := slices.BinarySearchFunc(comments, fs.start+1, func(c span, at int) int { return cmp.Compare(c.start, at) })
	if i == len(comments) || comments[i].start >= fs.end {
		return nil, false
	}

	z := d.flowZone(flow, true)
	z.in.related = []relatedSite{{site: d.site(comments[i], false), note: "the comment is here"}}

	return z, true
}

// openComments returns the span of each comment of the current text that
// opens with a "#" after white space, in the order of the text, as
// [Doc.commentZone] reads them. It reads them on its first call for a
// parse. The caller must not change the returned slice.
func (d *Doc) openComments() []span {
	t := d.parseIndex()
	if t.comments == nil {
		t.comments = []span{}

		for _, tk := range d.commentTokens() {
			cs, ok := d.tokenSpan(tk)
			if ok && cs.start > 0 && strings.ContainsAny(d.cur[cs.start-1:cs.start], " \t\r\n") {
				t.comments = append(t.comments, cs)
			}
		}
	}

	return t.comments
}

// flowZone returns the zone of flow, a flow collection with a comment
// inside it when comment is true, or a pair without braces in a flow
// sequence. The zone holds the collection or the pair whole.
func (d *Doc) flowZone(flow ast.Node, comment bool) *zone {
	fs, _ := d.nodeSpan(flow)

	in := instance{
		construct: ConstructFlowLayout,
		at:        d.nodeSite(flow, false),
		reach:     "reaches a pair without braces in a flow sequence",
		object:    "edit inside such a pair",
		fix:       "write braces around the pair, as in [{a: 1}]",
	}

	if comment {
		in.reach = "reaches a flow collection with a comment inside it"
		in.object = "edit inside such a collection"
		in.fix = "move the comment out of the flow collection"
	}

	return &zone{parts: []zonePart{d.zonePart(fs)}, at: fs.start, in: in}
}

// mergeForm names the form of the value of a << merge key that a call
// does not edit through, as the words of a message.
type mergeForm struct {
	// The first alias of a reference document that the merge key reads,
	// directly or as an item of its sequence, or nil.
	ref *ast.AliasNode
	// The fix rewrites the merge key into the form that a call edits.
	noun, fix string
	// The mappings of the document that the merge key brings in, in the
	// order in which go-yaml's decoder merges them.
	sources []ast.Node
	// The numbers of sources that come before the first and the last alias
	// of a reference document among the items of the sequence of the merge
	// key, or 0.
	firstRef, beforeRef int
	// Whether the merge key reads one alias of an anchored mapping of the
	// document.
	plain bool
	// Whether a call that goes through the merge key can set the key in a
	// source of the document instead.
	inSource bool
}

// formOf returns the form of e, a << merge key of the current text, as
// [mergeForm] names it. The form depends only on e and the parse, so the
// index of the parse keeps the form of each merge key that it read. The
// caller must not change the slices of the form.
func (d *Doc) formOf(e *ast.MappingValueNode) mergeForm {
	t := d.parseIndex()
	if f, ok := t.forms[e]; ok {
		return f
	}

	f := d.mergeValueForm(e.Value)

	if t.forms == nil {
		t.forms = make(map[*ast.MappingValueNode]mergeForm)
	}

	t.forms[e] = f

	return f
}

// mergeValueForm returns the form of a << merge key whose value is value,
// a node of the current text, as [Doc.formOf] gives it. Go-yaml's decoder
// merges the mappings under a tag on the value, so the form of a tagged
// value brings in the mappings that the value brings in without the tag.
// It merges each mapping of a sequence too, in the order of the sequence,
// also of a sequence that an alias of the value reads.
func (d *Doc) mergeValueForm(value ast.Node) mergeForm {
	const aliasFix = "merge an alias of a mapping"

	res := d.node.Resolver()

	// The mapping that alias brings in, and the form of a merge key whose
	// alias brings in none. The form of an alias of a reference document
	// tells that the merge key reads one, and the engine does not read what
	// it brings in. The mappings of a sequence that the alias reads come in
	// the form, and an alias of an item reads a mapping alone, as item
	// tells.
	var aliasSource func(alias *ast.AliasNode, item bool) (ast.Node, mergeForm)

	// The form of a merge key whose value is the sequence seq, which an
	// alias of the value reads when read is true.
	sequence := func(seq *ast.SequenceNode, read bool) mergeForm {
		// The noun names what the items of the sequence are: aliases alone,
		// or a mapping written in place among them.
		f := mergeForm{noun: "a merge key that merges a sequence", fix: "merge one alias", inSource: true}
		aliases, mappings := len(seq.Values) > 0, 0

		for _, item := range seq.Values {
			alias, ok := item.(*ast.AliasNode)
			if !ok {
				aliases = false

				if _, ok := readEntries(astnode.Content(item)); ok {
					f.noun = "a merge key that merges a sequence that holds a mapping"
					f.sources = append(f.sources, item)
					mappings++
				}

				continue
			}

			src, af := aliasSource(alias, true)
			if f.ref == nil && af.ref != nil {
				f.ref, f.firstRef = af.ref, len(f.sources)
			}

			if af.ref != nil {
				f.beforeRef = len(f.sources)
			}

			if src != nil {
				f.sources = append(f.sources, src)
			}
		}

		switch {
		case read:
			f.noun, f.fix, f.inSource = "a merge key whose alias reads a value other than a mapping", aliasFix, false
		case aliases:
			f.noun = "a merge key that merges a sequence of aliases"
		case mappings > 0 && mappings == len(seq.Values):
			// No path reaches a mapping written in the sequence, so the user
			// writes its entries in place.
			f.fix, f.inSource = "write the entries of the mappings in place of the merge key", false
		}

		return f
	}

	aliasSource = func(alias *ast.AliasNode, item bool) (ast.Node, mergeForm) {
		anchor, err := res.Anchor(alias)

		switch {
		case errors.Is(err, paths.ErrReferenceAlias):
			return nil, mergeForm{ref: alias}

		case err != nil:
			return nil, mergeForm{noun: "a merge key whose alias reads no anchor", fix: aliasFix}
		}

		if _, ok := readEntries(astnode.Content(anchor)); ok {
			return anchor, mergeForm{}
		}

		if seq, ok := astnode.Content(anchor).(*ast.SequenceNode); ok && !item {
			return nil, sequence(seq, true)
		}

		return nil, mergeForm{noun: "a merge key whose alias reads a value other than a mapping", fix: aliasFix}
	}

	switch v := value.(type) {
	case *ast.AliasNode:
		src, f := aliasSource(v, false)
		if src == nil {
			return f
		}

		return mergeForm{plain: true, sources: []ast.Node{src}}

	case *ast.TagNode:
		under := d.mergeValueForm(v.Value)

		return mergeForm{
			noun:    "a merge key whose value has the tag " + tagText(v),
			fix:     "merge an alias of a mapping of the document",
			sources: under.sources, ref: under.ref, firstRef: under.firstRef, beforeRef: under.beforeRef,
		}

	case *ast.AnchorNode:
		if _, ok := readEntries(astnode.Content(v)); ok {
			return mergeForm{
				noun: "a merge key whose value has an anchor", sources: []ast.Node{v},
				fix: "write the mapping apart with its anchor, and merge an alias of it",
			}
		}

	case *ast.MappingNode, *ast.MappingValueNode:
		return mergeForm{
			noun: "a merge key that merges a mapping written in place", sources: []ast.Node{v},
			fix: "write the entries of the mapping in place of the merge key",
		}
	}

	if seq, ok := astnode.Content(value).(*ast.SequenceNode); ok {
		return sequence(seq, false)
	}

	if isImplicitNull(value) {
		return mergeForm{noun: "a merge key without a value", fix: aliasFix + ", or remove the merge key"}
	}

	return mergeForm{noun: "a merge key whose value is a scalar", fix: aliasFix}
}

// heldEntries returns the entries of srcs, the sources of a << merge key,
// under each key, earlier sources first, as [Doc.specEntries] reads them
// with memo.
func (d *Doc) heldEntries(
	srcs []ast.Node,
	memo map[ast.Node]map[string]*ast.MappingValueNode,
) map[string][]*ast.MappingValueNode {
	held := make(map[string][]*ast.MappingValueNode)

	for _, src := range srcs {
		for name, se := range d.specEntries(src, memo) {
			held[name] = append(held[name], se)
		}
	}

	return held
}

// keptForm returns the form of e, a << merge key of the current text, as
// [Doc.formOf] reads it, where a tag on the key, a source that holds a
// merge key of its own, and another merge key of its mapping, as repeated
// tells, refuse the form of one alias of an anchored mapping too.
func (d *Doc) keptForm(e *ast.MappingValueNode, repeated bool) mergeForm {
	f := d.formOf(e)

	if tag, ok := e.Key.(*ast.TagNode); ok {
		f.noun, f.fix, f.plain = "a merge key with the tag "+tagText(tag), "remove the tag "+tagText(tag), false
	}

	if f.plain {
		if entries, _ := readEntries(astnode.Content(f.sources[0])); slices.ContainsFunc(entries,
			func(se *ast.MappingValueNode) bool { return astnode.IsMergeKey(se.Key) }) {
			f.noun, f.fix, f.plain = "a merge key that merges a mapping with a merge key of its own",
				"merge a mapping without a merge key", false
			f.inSource = true
		}
	}

	if f.plain && repeated {
		f.noun, f.fix, f.plain = "a mapping with more than one merge key", "merge one alias", false
	}

	return f
}

// firstOfMany returns the first of merges, the merge entries of a mapping,
// when the mapping holds more than one, or nil.
func firstOfMany(merges []*ast.MappingValueNode) *ast.MappingValueNode {
	if len(merges) < 2 {
		return nil
	}

	return merges[0]
}

// mergeZone returns the zone of e, a << merge key of m, when e is in
// another form than one alias of an anchored mapping of the document that
// holds no merge key, as [Doc.formOf] reads it. First is the first merge
// entry of m when m holds more than one, or nil. A mapping with more than
// one merge key is a construct of all of them, which binds at the first,
// whatever each merge key reads. The zone holds the entry of e whole, and
// every key of m and of each mapping that e brings in, through their merge
// keys too, with each of their merge entries whole. With heads, the zone
// holds only the entry of e and the keys of m. The callers that pass heads
// read no more of a zone than its instance and its first part. A merge of
// an alias of a reference document has no zone, because a path through it
// fails as a path through such an alias does.
func (d *Doc) mergeZone(m ast.Node, e, first *ast.MappingValueNode, heads bool) (*zone, bool) {
	f := d.keptForm(e, first != nil)
	if f.plain || f.ref != nil && f.noun == "" {
		return nil, false
	}

	at := e
	if first != nil {
		at = first
	}

	es, _ := d.nodeSpan(e)
	z := &zone{
		parts: []zonePart{d.zonePart(es)},
		keys:  []zoneKeys{d.liveKeys(m)},
		at:    d.offsetOf(at),
		in: instance{
			construct: ConstructMergeForm,
			at:        d.entrySite(at),
			reach:     "goes through " + f.noun,
			object:    "edit through such a merge key",
			fix:       f.fix,
		},
	}

	if f.inSource {
		z.in.fix += ", or set the key in a source"
	}

	// The anchored mappings that the aliases of the merge key read are the
	// other sites of the construct, each once and in the order of the text.
	aliases := []ast.Node{e.Value}
	if seq, ok := astnode.Content(e.Value).(*ast.SequenceNode); ok {
		aliases = seq.Values
	}

	var sources []site

	for _, n := range aliases {
		alias, ok := n.(*ast.AliasNode)
		if !ok {
			continue
		}

		anchor, err := d.node.Resolver().Anchor(alias)
		if err == nil {
			sources = append(sources, d.nodeSite(anchor, false))
		}
	}

	slices.SortStableFunc(sources, siteOrder)

	for _, s := range sources {
		if !slices.ContainsFunc(z.in.readers, s.path.Equal) {
			z.in.readers = append(z.in.readers, s.path)
		}
	}

	// A call that changes the keys of the merge does not go through it, and
	// a rewrite of the merge key is what the user can write.
	keyed := z.in
	keyed.reach, keyed.object, keyed.fix = "changes the keys of a merge through "+f.noun,
		"change the keys of such a merge", f.fix
	z.keyed = &keyed

	if heads {
		return z, true
	}

	seen := map[ast.Node]bool{m: true}
	stack := slices.Clone(f.sources)

	for len(stack) > 0 {
		src := astnode.Content(stack[len(stack)-1])
		stack = stack[:len(stack)-1]

		entries, ok := readEntries(src)
		if !ok || seen[src] {
			continue
		}

		seen[src] = true
		z.keys = append(z.keys, d.liveKeys(src))

		for _, se := range entries {
			if astnode.IsMergeKey(se.Key) {
				if s, ok := d.nodeSpan(se); ok {
					z.parts = append(z.parts, d.zonePart(s))
				}

				stack = append(stack, d.formOf(se).sources...)
			}
		}
	}

	return z, true
}

// userBlock reports whether node holds a block mapping, a block sequence,
// or a block scalar under its anchors and tags, with bytes of the user's
// text under those anchors and tags. A Move writes such a value inside a
// flow collection from its decode, which drops the comments and the style
// of the user. The engine writes no comment of its own in a value, so a
// block value that a Set wrote under the user's anchor moves as the engine
// writes it.
func (d *Doc) userBlock(node ast.Node) bool {
	content := astnode.Content(node)

	_, literal := content.(*ast.LiteralNode)
	if !literal && !isBlockCollection(node) {
		return false
	}

	s, ok := d.nodeSpan(content)

	return ok && slices.ContainsFunc(d.origin[s.start:s.end], func(o int) bool { return o >= 0 })
}

// flowMoveProblem returns the problem of a Move that named names, which
// puts value, a block value of the current text, inside a flow collection.
// Such a Move writes the value from its decode and drops its comments and
// its style. The problem binds at the first token of the value, under the
// path of the value, and the Apply records it.
func (d *Doc) flowMoveProblem(value ast.Node, named string) error {
	at := d.nodeSite(value, false)

	switch x := value.(type) {
	case *ast.MappingNode:
		at = d.nodeSite(x.Values[0].Key, true)
	case *ast.MappingValueNode:
		at = d.nodeSite(x.Key, true)
	case *ast.SequenceNode:
		at.path, _ = at.path.Parent()
	}

	problem := d.refusal(&instance{
		construct: ConstructFlowLayout,
		at:        at,
		reach:     "puts a block value inside a flow collection",
		object:    "move a block value into a flow collection",
		fix:       "write the value in flow style first, or move it into a block collection",
	}, named)
	d.record(problem)

	return problem
}

// repeatRefusal returns the problem of a rename that named names, which
// writes key, the new key of renamed, while r.other, another entry of its
// mapping in the model of x, reads key. The Source allows a mapping to
// repeat a key, so the rename makes a repeated key. The program passed the
// key, so the problem lies in its value and binds at the renamed entry,
// and the Apply records it.
func (d *Doc) repeatRefusal(x *model, r repeated, renamed *ast.MappingValueNode, key, named string) error {
	in := instance{
		construct: ConstructRepeatedKey,
		at:        d.entrySite(renamed),
		reach:     fmt.Sprintf("writes the key %s a second time in its mapping", cut(strconv.Quote(key))),
		object:    "write a key that its mapping repeats",
		fix:       "rename the key to one that its mapping does not hold",
		inValue:   true,
	}

	if _, ok := d.clashKey(x, r.other); ok {
		in.related = []relatedSite{{
			site: d.entrySite(r.other.mate.entry),
			note: fmt.Sprintf("the key %s is written here", cut(strconv.Quote(key))),
		}}
	}

	problem := d.refusal(&in, named)
	d.record(problem)

	return problem
}

// mappingEntries returns the entries of n, a node of the current text, when
// n is a mapping: a mapping node, or an entry that no mapping node holds,
// which go-yaml's parser writes for a mapping of one entry.
func (d *Doc) mappingEntries(n ast.Node) ([]*ast.MappingValueNode, bool) {
	if _, ok := n.(*ast.MappingValueNode); ok {
		return entriesIn(n, d.tree().parent[n])
	}

	return entriesIn(n, nil)
}

// entriesIn returns the entries of n, a node that up holds, when n is a
// mapping, as [Doc.mappingEntries] reads them.
func entriesIn(n, up ast.Node) ([]*ast.MappingValueNode, bool) {
	switch x := n.(type) {
	case *ast.MappingNode:
		return x.Values, true
	case *ast.MappingValueNode:
		if _, ok := up.(*ast.MappingNode); !ok {
			return []*ast.MappingValueNode{x}, true
		}
	}

	return nil, false
}

// keptMerge is a << merge key of the current text in the kept form, as
// [Doc.keptForm] reads it: one alias of an anchored mapping of the
// document that holds no merge key, alone in its mapping.
type keptMerge struct {
	// The mapping that holds the merge key, and the anchored node that its
	// alias reads.
	m, s  ast.Node
	entry *ast.MappingValueNode
}

// mergeMapping is a mapping of the current text that holds a << merge
// key, with its entries, as [Doc.mappingEntries] reads them.
type mergeMapping struct {
	m       ast.Node
	entries []*ast.MappingValueNode
}

// mergeMappings returns each mapping of the current text that holds a <<
// merge key, in the order of the text. A mapping without a merge key gives
// no result to [Doc.keptMerges], [Doc.mergeEntries] or
// [Doc.precedenceMisreads], so they read these mappings alone. It reads them
// on its first call for a parse. The caller must not change the returned
// slice.
func (d *Doc) mergeMappings() []mergeMapping {
	t := d.parseIndex()
	if t.merges == nil {
		t.merges = []mergeMapping{}
		merge := func(e *ast.MappingValueNode) bool { return astnode.IsMergeKey(e.Key) }

		w := d.walked()

		for i, n := range w.nodes {
			if entries, ok := entriesIn(n, w.parentAt(i)); ok && slices.ContainsFunc(entries, merge) {
				t.merges = append(t.merges, mergeMapping{m: n, entries: entries})
			}
		}
	}

	return t.merges
}

// keptMerges returns the kept merges of the current text in the order of
// the text, which it reads on its first call for the parse. The caller must
// not change the returned slice.
func (d *Doc) keptMerges() []keptMerge {
	t := d.parseIndex()
	if t.kept == nil {
		t.kept = d.readKeptMerges()
	}

	return t.kept
}

// readKeptMerges reads the kept merges of the current text, as
// [Doc.keptMerges] returns them.
func (d *Doc) readKeptMerges() []keptMerge {
	out := []keptMerge{}

	for _, mm := range d.mergeMappings() {
		var merges []*ast.MappingValueNode

		for _, e := range mm.entries {
			if astnode.IsMergeKey(e.Key) {
				merges = append(merges, e)
			}
		}

		if len(merges) == 1 {
			if f := d.keptForm(merges[0], false); f.plain {
				out = append(out, keptMerge{m: mm.m, s: f.sources[0], entry: merges[0]})
			}
		}
	}

	return out
}

// keptMergesOf returns the kept merges of the current text whose mapping
// is m.
func (d *Doc) keptMergesOf(m ast.Node) []keptMerge {
	var out []keptMerge

	for _, k := range d.keptMerges() {
		if k.m == m {
			out = append(out, k)
		}
	}

	return out
}

// ownKeys returns the key of each entry of the mapping that node holds,
// other than its << merge keys, as a decode names the keys. The index of
// the parse keeps the keys of each node. The caller must not change the
// returned slice.
func (d *Doc) ownKeys(node ast.Node) []string {
	t := d.parseIndex()
	if out, ok := t.own[node]; ok {
		return out
	}

	if t.own == nil {
		t.own = make(map[ast.Node][]string)
	}

	out := d.readOwnKeys(node)
	t.own[node] = out

	return out
}

// readOwnKeys reads the keys of the mapping that node holds, as
// [Doc.ownKeys] returns them.
func (d *Doc) readOwnKeys(node ast.Node) []string {
	res := d.node.Resolver()
	entries, _ := readEntries(astnode.Content(node))

	out := make([]string, 0, len(entries))

	for _, e := range entries {
		if astnode.IsMergeKey(e.Key) {
			continue
		}

		if name, ok := res.KeyName(e.Key); ok {
			out = append(out, name)
		}
	}

	return out
}

// mergeText returns the text of the merge key of k, as in "<<: *s" or
// "<<: [*a, *b]", or "the merge key" for another form.
func mergeText(k keptMerge) string {
	name := func(n ast.Node) (string, bool) {
		alias, ok := n.(*ast.AliasNode)
		if !ok || alias.Value == nil {
			return "", false
		}

		return "*" + alias.Value.GetToken().Value, true
	}

	if text, ok := name(k.entry.Value); ok {
		return "<<: " + text
	}

	seq, ok := astnode.Content(k.entry.Value).(*ast.SequenceNode)
	if !ok {
		return "the merge key"
	}

	texts := make([]string, 0, len(seq.Values))

	for _, item := range seq.Values {
		text, ok := name(item)
		if !ok {
			return "the merge key"
		}

		texts = append(texts, text)
	}

	return "<<: [" + strings.Join(texts, ", ") + "]"
}

// mergeReads returns each alias of a << merge key of the current text that
// reads a mapping of the document, in the order of the text. Each one is a
// [keptMerge] whose source is the anchored mapping. The alias is the value
// of the merge key, or an item of the sequence that the merge key merges,
// so a merge key in a refused form counts too.
func (d *Doc) mergeReads() []keptMerge {
	res := d.node.Resolver()

	var out []keptMerge

	// Only a mapping that holds a merge key holds such an alias.
	for _, mm := range d.mergeMappings() {
		n, entries := mm.m, mm.entries

		for _, e := range entries {
			if !astnode.IsMergeKey(e.Key) {
				continue
			}

			aliases := []ast.Node{e.Value}
			if seq, ok := astnode.Content(e.Value).(*ast.SequenceNode); ok {
				aliases = seq.Values
			}

			for _, a := range aliases {
				alias, ok := a.(*ast.AliasNode)
				if !ok {
					continue
				}

				anchor, err := res.Anchor(alias)
				if err != nil {
					continue
				}

				if _, mapping := readEntries(astnode.Content(anchor)); mapping {
					out = append(out, keptMerge{m: n, s: anchor, entry: e})
				}
			}
		}
	}

	return out
}

// mappingSite returns the site that names m, a mapping of the current
// text: the key of the entry that holds m, under the path of m, or the
// first token of m.
func (d *Doc) mappingSite(m ast.Node) site {
	t := d.tree()

	for n := t.parent[m]; n != nil; n = t.parent[n] {
		switch x := n.(type) {
		case *ast.TagNode, *ast.AnchorNode:
			continue
		case *ast.MappingValueNode:
			if !astnode.IsMergeKey(x.Key) {
				return d.nodeSite(x.Key, false)
			}
		}

		break
	}

	if entries, ok := d.mappingEntries(m); ok && len(entries) > 0 && !isFlow(m) {
		return d.nodeSite(entries[0].Key, true)
	}

	return d.nodeSite(m, false)
}

// mergeKeysProblem returns the problem of a call that named names, which
// makes k.m stop writing key, or start writing it when made is true, while
// the source of the kept merge k holds key. The call hides or reveals an
// entry of the source, as rule M2 of the merge rules refuses it. The
// problem binds at at, the site of the entry that the call takes, moves,
// or renames, or at the merge key when at is nil, and the Apply records
// it. The words after the name of the call name the mappings and the key
// in the current text, as the name of the call does.
func (d *Doc) mergeKeysProblem(k keptMerge, at *site, key string, made bool, named string) error {
	merge := d.nodeSite(k.entry.Key, true)
	mapping, source := d.textPath(k.m), d.textPath(k.s)
	pos := merge.at.Start

	in := instance{
		construct: ConstructMergeKeys,
		at:        merge,
		but: fmt.Sprintf(", but %s at %d:%d also brings in %s, so %s still reads %s",
			mergeText(k), pos.Line+1, pos.Col+1, key, mapping, key),
		fix: fmt.Sprintf("set %s to the value it needs, or remove %s from %s too",
			mapping.Child(key), key, source),
	}

	if at != nil {
		in.at = *at
		in.related = []relatedSite{{site: merge, note: fmt.Sprintf("%s brings in %s here", mergeText(k), key)}}
	}

	if made {
		in.but = fmt.Sprintf(", but %s at %d:%d also brings in %s, so %s then hides the %s of %s",
			mergeText(k), pos.Line+1, pos.Col+1, key, mapping, key, source)
		in.fix = fmt.Sprintf("write a key that %s does not hold, or remove %s from %s first", source, key, source)
	}

	return d.recordRefusal(&in, named)
}

// keySite returns the site of the entry that the call through h takes,
// moves, or renames, at the place of h in the text before the call. A call
// that creates the entry of key in m, a mapping of the current text, finds
// no entry at its place, so the site is that of the entry that it wrote.
// KeySite returns nil when neither text holds such an entry.
func (d *Doc) keySite(h *Node, m ast.Node, key string) *site {
	now := d.save()
	d.show(d.callStart)

	var at *site

	p, err := d.nodePlace(h)
	if err == nil && p.exists {
		at = d.siteOf(p.entry)
	}

	d.show(now)

	if at == nil {
		at = d.siteOf(d.entryOf(m, key))
	}

	return at
}

// siteOf returns the site of e, an entry of the current text, as
// [Doc.entrySite] gives it, or nil for a nil e.
func (d *Doc) siteOf(e *ast.MappingValueNode) *site {
	if e == nil {
		return nil
	}

	return new(d.entrySite(e))
}

// entryOf returns the last entry of key in m, a mapping of the current
// text, other than a << merge key, or nil.
func (d *Doc) entryOf(m ast.Node, key string) *ast.MappingValueNode {
	entries, _ := readEntries(astnode.Content(m))
	res := d.node.Resolver()

	var at *ast.MappingValueNode

	for _, e := range entries {
		if name, ok := res.KeyName(e.Key); ok && name == key && !astnode.IsMergeKey(e.Key) {
			at = e
		}
	}

	return at
}

// textPath returns the path of node in the current text, or of the nearest
// value that holds node and that a path selects, as the name of a call
// names its place.
func (d *Doc) textPath(node ast.Node) paths.Path {
	for _, n := range append([]ast.Node{node}, d.ancestors(node)...) {
		if rel, ok := d.pathIn(d.node.AST(), n); ok {
			return paths.Doc().Join(rel)
		}
	}

	return paths.Doc()
}

// ownKeysProblem returns the problem of a call that named names, which
// takes away own, an own entry of a mapping of the current text, or
// renames its key to key when renamed is true, as rule M2 of the merge
// rules refuses it, or nil.
func (d *Doc) ownKeysProblem(own *ast.MappingValueNode, m ast.Node, key string, renamed bool, named string) error {
	name, ok := d.node.Resolver().KeyName(own.Key)
	if !ok || astnode.IsMergeKey(own.Key) {
		return nil
	}

	for _, k := range d.keptMergesOf(m) {
		held := d.ownKeys(k.s)

		switch {
		case slices.Contains(held, name):
			return d.mergeKeysProblem(k, d.siteOf(own), name, false, named)
		case renamed && slices.Contains(held, key):
			return d.mergeKeysProblem(k, d.siteOf(own), key, true, named)
		}
	}

	return nil
}

// mergedTarget returns the kept merge through which w, the walk of the
// target of a Move, reaches an entry that the source of the merge brings
// in, with the key of that entry. A Move there would hide the entry, as
// rule M2 of the merge rules refuses it. It reports false for another
// target.
func (d *Doc) mergedTarget(w *callWalk) (keptMerge, string, bool) {
	if !w.p.exists || len(w.merges) == 0 {
		return keptMerge{}, "", false
	}

	step := w.merges[len(w.merges)-1]

	for _, k := range d.keptMergesOf(step.m) {
		if k.entry == step.entry {
			return k, step.name, true
		}
	}

	return keptMerge{}, "", false
}

// mergeSourceProblem returns the problem of a Set that named names, which
// writes a value other than a mapping, as mapping tells, at the place of
// w. Reads returns the aliases of merge keys of the current text that the
// rule judges. When one of them reads the mapping at the place, rule M5 of
// the merge rules refuses the Set. Otherwise mergeSourceProblem returns
// nil. The problem binds at the merge key, and the Apply records it.
func (d *Doc) mergeSourceProblem(w *callWalk, mapping bool, reads func() []keptMerge, named string) error {
	if mapping || !w.p.exists {
		return nil
	}

	for _, k := range reads() {
		if astnode.Content(k.s) != astnode.Content(w.p.value) {
			continue
		}

		merge := d.nodeSite(k.entry.Key, true)
		pos := merge.at.Start

		return d.recordRefusal(&instance{
			construct: ConstructMergeForm,
			at:        merge,
			reach: fmt.Sprintf("leaves %s at %d:%d reading a value other than a mapping",
				mergeText(k), pos.Line+1, pos.Col+1),
			object:  "merge a value other than a mapping",
			fix:     "set a mapping there, or set the entries of the mapping one at a time",
			related: []relatedSite{{site: d.mappingSite(k.m), note: "this mapping merges the value"}},
		}, named)
	}

	return nil
}

// setRules returns the problem of the Set c, which writes a mapping when
// mapping is true, when the rules M4 and M5 of the merge rules refuse it,
// or when it takes an anchor that an alias outside its place reads, as
// [Doc.aliasCopyProblem] tells, or nil. The Set runs them after its reach
// of the zones, in that order, and rule M5 for a merge key in a refused
// form last. It keeps the anchor on its place, so only the anchors below
// that one count.
func (d *Doc) setRules(c reachCall, mapping bool) error {
	w, err := d.walkCall(c.path, c.hidden)
	if err != nil {
		return nil //nolint:nilerr // The call reports the problem of its path.
	}

	err = d.mergeSourceProblem(w, mapping, d.keptMerges, c.named)
	if err != nil || !w.p.exists {
		return err
	}

	err = d.aliasCopyProblem(d.anchorsIn(underKeptAnchor(w.p.value)), nil, c.named, c.path)
	if err != nil {
		return err
	}

	// A merge key in a refused form reads its sources through aliases too.
	// Go-yaml's decoder refuses a merge of a value other than a mapping, as
	// the merge spec does. So such a Set fails as rule M5 refuses it, after
	// the rules that judge the refused forms.
	return d.mergeSourceProblem(w, mapping, d.mergeReads, c.named)
}

// underKeptAnchor returns the value that a Set of node, a value of the
// current text, writes over: the value under the anchor of node, which the
// Set keeps, or node without one. The tags before the anchor go with the
// old value too.
func underKeptAnchor(node ast.Node) ast.Node {
	if a, ok := keptAnchorOf(node); ok {
		return a.Value
	}

	return node
}

// keptAnchorOf returns the anchor of node, a value of the current text,
// under the tags before it, which a Set of node keeps.
func keptAnchorOf(node ast.Node) (*ast.AnchorNode, bool) {
	for {
		switch x := node.(type) {
		case *ast.AnchorNode:
			return x, true
		case *ast.TagNode:
			node = x.Value
		default:
			return nil, false
		}
	}
}

// aliasCopyProblem returns the problem of a call that named names, whose
// place is at, when an alias or a << merge key outside the value that the
// call takes reads an anchor of k inside it (C21), or nil. The engine does
// not write a copy of a value at its alias. The problem binds at the alias
// of the first such reader in the order of the text, and the other readers
// come as details. A reader that is the value of a merge key, or an item
// of its sequence, binds under the path of the mapping of the merge key,
// and the message names the merge key by its position. Top is the value
// at the place, whose anchor the message names as the value that the call
// takes.
func (d *Doc) aliasCopyProblem(k *keeper, top ast.Node, named string, at paths.Path) error {
	var readers []reader

	anchors := make(map[ast.Node]*keptAnchor)

	for i := range k.kept {
		for _, r := range k.kept[i].readers {
			readers = append(readers, r)
			anchors[r.ref.node] = &k.kept[i]
		}
	}

	if len(readers) == 0 {
		return nil
	}

	slices.SortStableFunc(readers, func(a, b reader) int { return cmp.Compare(a.ref.start, b.ref.start) })

	first := anchors[readers[0].ref.node]
	merge, isMerge := d.mergeReader(readers[0].ref.node)

	reads := "this alias"
	if isMerge {
		pos := d.nodeSite(merge.Key, true)
		reads = fmt.Sprintf("the merge key at %d:%d of %s", pos.at.Start.Line+1, pos.at.Start.Col+1, pos.path)
	}

	words := "which " + reads + " reads"
	if first.anchor != top {
		words = fmt.Sprintf("which holds the anchor &%s that %s reads", first.name, reads)
	}

	in := instance{construct: ConstructAliasCopy}

	for i, r := range readers {
		s := d.nodeSite(r.ref.node, false)
		if i == 0 {
			in.at, in.readers = s, []paths.Path{s.path}

			continue
		}

		name := anchors[r.ref.node].name
		in.related = append(in.related, relatedSite{site: s, note: fmt.Sprintf("*%s reads &%s here", name, name)})
	}

	in.fix = d.aliasCopyFix(readers, at)
	in.but = fmt.Sprintf(", %s, and %s does not write a copy of a value at its alias", words, d.appName())

	return d.recordRefusal(&in, named)
}

// aliasCopyFix returns the fix of a call whose place is at, which takes
// the anchors that readers read. The fix names a fallback for each reader:
// a Set of each alias, and of the mapping of each merge key, to the value
// that it reads. The fix names each reader by its path in the current
// text, as [Doc.readerPath] gives it, because a program sets that path. No
// path selects the root to set, so the fix asks for the merged entries in
// place of a merge key of the root.
func (d *Doc) aliasCopyFix(readers []reader, at paths.Path) string {
	var (
		sets  []string
		merge bool
		root  string
	)

	for _, r := range readers {
		path, _ := d.readerPath(r.ref.node)
		e, isMerge := d.mergeReader(r.ref.node)

		switch {
		case isMerge && path.IsRoot():
			root = d.userAt(d.offsetOf(e.Key))
		case !slices.Contains(sets, path.String()):
			sets, merge = append(sets, path.String()), merge || isMerge
		}
	}

	var fixes []string

	switch {
	case len(sets) == 1 && merge:
		fixes = append(fixes, fmt.Sprintf("set %s to the value it reads first, which writes the merged "+
			"entries in place of the merge key", sets[0]))

	case len(sets) == 1:
		fixes = append(fixes, fmt.Sprintf("set %s to the value it reads first", sets[0]))
	case len(sets) > 1:
		fixes = append(fixes, fmt.Sprintf("set %s and %s to the values that they read first",
			strings.Join(sets[:len(sets)-1], ", "), sets[len(sets)-1]))
	}

	if root != "" {
		fixes = append(fixes, "write the merged entries in place of the merge key at "+root)
	}

	return strings.Join(append(fixes, "or keep "+at.String()), ", ")
}

// appName returns the name of the program, as [WithApp] gives it.
func (d *Doc) appName() string {
	if d.cfg.app == "" {
		return "this program"
	}

	return d.cfg.app
}

// mergeReader returns the << merge entry whose value is alias, an alias of
// the current text, or whose sequence holds alias as an item.
func (d *Doc) mergeReader(alias ast.Node) (*ast.MappingValueNode, bool) {
	t := d.tree()

	up := t.parent[alias]
	if seq, ok := up.(*ast.SequenceNode); ok {
		e, ok := t.parent[seq].(*ast.MappingValueNode)

		return e, ok && e.Value == seq && astnode.IsMergeKey(e.Key)
	}

	e, ok := up.(*ast.MappingValueNode)

	return e, ok && e.Value == alias && astnode.IsMergeKey(e.Key)
}

// precedenceMisreads returns each own entry of a mapping of the current
// text that a << merge key after it overrides: a merge key whose source
// holds its key, or reads it through merge keys of its own, after the last
// own entry of the key. Go-yaml's decoder reads the entries of a mapping in
// order and lets each later one override the earlier ones, so the last
// such merge key gives the value, but the merge spec keeps the own entry.
// The keys compare as the decoder reads them, as [Doc.mergeName] gives
// them. The misread binds at the key of the last own entry and names that
// merge key. It also returns each key that two sources of one merge key
// hold, which the mapping does not write. Go-yaml's decoder lets the later
// source override the earlier one, but the merge spec keeps the earlier
// one. That misread binds at the merge key. The sources are the mappings of
// the document that the merge key brings in, as [Doc.formOf] gives them.
// The engine does not read what an alias of a reference document brings
// in, so a key of a reference document counts for neither kind.
func (d *Doc) precedenceMisreads() []misreadFound {
	var out []misreadFound

	memo := make(map[ast.Node]map[string]*ast.MappingValueNode)
	refs := make(map[ast.Node]bool)

	type merge struct {
		e    *ast.MappingValueNode
		held map[string][]*ast.MappingValueNode
		at   int
	}

	for _, mm := range d.mergeMappings() {
		entries := mm.entries
		owns := make(map[string]int)

		var merges []merge

		// Whether a merge key after the merge key j of the mapping reads a
		// reference document, which can bring in any key again.
		laterReference := func(j int) bool {
			return slices.ContainsFunc(merges[j+1:], func(m merge) bool {
				return d.mergeReadsReference(d.formOf(m.e), 0, refs)
			})
		}

		for i, e := range entries {
			if astnode.IsMergeKey(e.Key) {
				// The entries of the sources of e under each key, earlier
				// sources first, as go-yaml's resolver binds the aliases of e.
				merges = append(merges, merge{e: e, held: d.heldEntries(d.formOf(e).sources, memo), at: i})
			} else if name, ok := d.mergeName(e.Key); ok {
				owns[name] = i
			}
		}

		for i, own := range entries {
			name, ok := d.mergeName(own.Key)
			if !ok || astnode.IsMergeKey(own.Key) || owns[name] != i {
				continue
			}

			other := func(se *ast.MappingValueNode) bool { return se != own }

			for j, m := range slices.Backward(merges) {
				// A mapping that merges itself brings in its own entry, which
				// both readings keep.
				held := m.held[name]
				if m.at < i || !slices.ContainsFunc(held, other) {
					continue
				}

				// Go-yaml's decoder reads the entry of the last source that
				// brings in the key.
				var se *ast.MappingValueNode

				for _, h := range slices.Backward(held) {
					if other(h) {
						se = h

						break
					}
				}

				later := laterReference(j) || d.readsReferenceAfter(m.e, name, se, memo, refs)
				if f, ok := d.precedencePart(m.e, own, later); ok {
					out = append(out, f)
				}

				break
			}
		}

		for j, m := range merges {
			// Two sources that bring in one entry read alike. The keys come in
			// the order of the entries of their last sources in the text, and
			// keys of the same place in the order of their names.
			var order []misreadFound

			for _, name := range slices.Sorted(maps.Keys(m.held)) {
				ses := m.held[name]
				if _, owned := owns[name]; owned || ses[0] == ses[len(ses)-1] {
					continue
				}

				// A later merge key that brings in the key, or a later source
				// that a reference document decides, can be the one that
				// go-yaml's decoder reads. A source before the first one can
				// hold the key in a reference document, which the merge spec
				// then keeps.
				later := laterReference(j) || d.readsReferenceAfter(m.e, name, ses[len(ses)-1], memo, refs) ||
					slices.ContainsFunc(merges[j+1:], func(m merge) bool { return len(m.held[name]) > 0 })
				earlier := d.readsReferenceBefore(m.e, name, ses[0], memo, refs)

				if f, ok := d.orderPart(m.e, name, ses, later, earlier, memo); ok {
					order = append(order, f)
				}
			}

			slices.SortStableFunc(order, func(a, b misreadFound) int {
				return cmp.Or(cmp.Compare(a.order[0], b.order[0]), cmp.Compare(a.order[1], b.order[1]))
			})

			out = append(out, order...)
		}
	}

	return out
}

// orderPart returns the misread of the key that two or more sources of e,
// a << merge key of the current text, hold under held, as
// [Doc.precedenceMisreads] finds it. The entries ses hold the key in those
// sources, earlier sources first, as [Doc.specEntries] reads them with
// memo. The message names the key as the path names the entry of the last
// source. It names that source as the one that go-yaml's decoder reads,
// unless a later source or merge key can bring in the key after it, as
// later tells. It names the first source as the one that the merge spec
// keeps, unless a source before it reads a reference document, as earlier
// tells. The engine does not read a reference document, so the message
// then names no source in its place. A source names its place by the entry
// that holds the key or the merge key that brings it in, as
// [Doc.sourceEntry] finds it.
func (d *Doc) orderPart(
	e *ast.MappingValueNode,
	held string,
	ses []*ast.MappingValueNode,
	later, earlier bool,
	memo map[ast.Node]map[string]*ast.MappingValueNode,
) (misreadFound, bool) {
	ks, kok := d.nodeSpan(e.Key)
	if !kok {
		return misreadFound{}, false
	}

	// The offset of the entry se, and where a message names its source.
	place := func(se *ast.MappingValueNode, last bool) (int, string) {
		s, ok := d.nodeSpan(se)
		if !ok {
			return ks.start, "at " + placeText(d.nodeSite(e.Key, true).at.Start)
		}

		return s.start, "at " + placeText(d.entrySite(d.sourceEntry(e, held, se, last, memo)).at.Start)
	}

	first, firstAt := place(ses[0], false)
	last, lastAt := place(ses[len(ses)-1], true)

	name, _ := d.node.Resolver().KeyName(ses[len(ses)-1].Key)
	at := d.nodeSite(e.Key, true)
	mergeAt := placeText(at.at.Start)

	// No path names a mapping below a key without a name of its own, such
	// as an alias key, so the message then names the key alone.
	m := d.tree().parent[e]
	path, edited := "the key "+cut(name), "the key "+cut(name)

	if d.pathNames(m) {
		path, edited = at.path.Child(name).String(), d.textPath(m).Child(name).String()
	}

	read, kept := "the source "+lastAt, "the earlier source "+firstAt
	if later {
		read, kept = "a source after the source "+firstAt, "that earlier source"
	}

	if earlier {
		kept = "an earlier source"
	}

	return misreadFound{
		kind: misreadOrder, at: at, start: ks.start, order: [2]int{last, first},
		fix: fmt.Sprintf("write %s in one source of the merge key, or after the merge key", cut(name)),
		msg: fmt.Sprintf("go-yaml's decoder reads %s from %s of the merge key at %s, but the merge spec keeps %s",
			path, read, mergeAt, kept),
		edited: fmt.Sprintf("go-yaml's decoder reads %s from a later source of a merge key %s, but the "+
			"merge spec keeps the earlier source", edited, inEdited),
	}, true
}

// sourceEntry returns the entry of the source of e, a << merge key of the
// current text, that brings in se, the entry of the key name that the
// source reads, as [Doc.specEntries] reads them with memo. That is se when
// the source holds it, or else the merge key of the source that brings it
// in. Of the sources that bring in se, it reads the last one with last,
// and else the first one.
func (d *Doc) sourceEntry(
	e *ast.MappingValueNode,
	name string,
	se *ast.MappingValueNode,
	last bool,
	memo map[ast.Node]map[string]*ast.MappingValueNode,
) *ast.MappingValueNode {
	srcs := d.formOf(e).sources
	if last {
		srcs = slices.Clone(srcs)
		slices.Reverse(srcs)
	}

	for _, src := range srcs {
		if d.specEntries(src, memo)[name] != se {
			continue
		}

		entries, _ := readEntries(astnode.Content(src))
		if slices.Contains(entries, se) {
			return se
		}

		for _, own := range entries {
			if astnode.IsMergeKey(own.Key) && slices.Contains(d.heldEntries(d.formOf(own).sources, memo)[name], se) {
				return own
			}
		}

		break
	}

	return se
}

// readsReferenceAfter reports whether e, a << merge key of the current
// text, can read the key name from a reference document after the source
// that brings in se, the entry of the key that go-yaml's decoder reads from
// the sources of the document, as [Doc.specEntries] reads them with memo.
// An alias of a reference document after that source can bring in the key
// again, and so can a later source that reads one, as
// [Doc.readsReference] tells with refs.
func (d *Doc) readsReferenceAfter(
	e *ast.MappingValueNode,
	name string,
	se *ast.MappingValueNode,
	memo map[ast.Node]map[string]*ast.MappingValueNode,
	refs map[ast.Node]bool,
) bool {
	f := d.formOf(e)

	for i, src := range slices.Backward(f.sources) {
		if d.specEntries(src, memo)[name] == se {
			return d.mergeReadsReference(f, i+1, refs)
		}
	}

	return false
}

// readsReferenceBefore reports whether e, a << merge key of the current
// text, reads a reference document before the source that brings in se.
// That entry of the key name is the one that the merge spec keeps from the
// sources of the document, as [Doc.specEntries] reads them with memo. The
// reference
// document can hold the key, so the merge spec can keep its entry instead.
// That source can be an alias of a reference document or a source that
// reads one, as [Doc.readsReference] tells with refs.
func (d *Doc) readsReferenceBefore(
	e *ast.MappingValueNode,
	name string,
	se *ast.MappingValueNode,
	memo map[ast.Node]map[string]*ast.MappingValueNode,
	refs map[ast.Node]bool,
) bool {
	f := d.formOf(e)

	for i, src := range f.sources {
		if d.specEntries(src, memo)[name] == se {
			return f.ref != nil && f.firstRef <= i ||
				slices.ContainsFunc(f.sources[:i], func(s ast.Node) bool { return d.readsReference(s, refs) })
		}
	}

	return false
}

// mergeReadsReference reports whether a << merge key of the form f reads a
// reference document at its source from or after it. It reads one through
// an alias of one among its items, or through a source that reads one, as
// [Doc.readsReference] tells with refs.
func (d *Doc) mergeReadsReference(f mergeForm, from int, refs map[ast.Node]bool) bool {
	if f.ref != nil && from <= f.beforeRef {
		return true
	}

	return slices.ContainsFunc(f.sources[min(from, len(f.sources)):], func(s ast.Node) bool {
		return d.readsReference(s, refs)
	})
}

// readsReference reports whether src, a mapping of the document that a <<
// merge key brings in, reads a reference document through a merge key of
// its own. That merge key holds an alias of one as its value or as an item
// of its sequence, or a source that reads one. Such a mapping
// can bring in any key. The engine tells it from the aliases of the
// document, without a read of the reference document. Refs holds the
// answer for each mapping that an earlier call read, and a mapping that a
// merge inside it brings in again reads none there.
func (d *Doc) readsReference(src ast.Node, refs map[ast.Node]bool) bool {
	m := astnode.Content(src)
	if r, ok := refs[m]; ok {
		return r
	}

	refs[m] = false

	entries, _ := readEntries(m)

	for _, e := range entries {
		if astnode.IsMergeKey(e.Key) && d.mergeReadsReference(d.formOf(e), 0, refs) {
			refs[m] = true

			return true
		}
	}

	return false
}

// specEntries returns the entries that src, a source of a << merge key,
// reads by the merge spec under each key: its own entries, then for each
// key that it does not write, the entries of its own merge keys, earlier
// sources first. Memo holds the entries of each source that an earlier
// call read, and a source that a merge inside it brings in again reads no
// entries there.
func (d *Doc) specEntries(
	src ast.Node,
	memo map[ast.Node]map[string]*ast.MappingValueNode,
) map[string]*ast.MappingValueNode {
	m := astnode.Content(src)
	if out, ok := memo[m]; ok {
		return out
	}

	out := make(map[string]*ast.MappingValueNode)
	memo[m] = map[string]*ast.MappingValueNode{}

	entries, _ := readEntries(m)

	for _, e := range entries {
		if astnode.IsMergeKey(e.Key) {
			continue
		}

		if name, ok := d.mergeName(e.Key); ok {
			out[name] = e
		}
	}

	for _, e := range entries {
		if !astnode.IsMergeKey(e.Key) {
			continue
		}

		for _, s := range d.formOf(e).sources {
			for name, se := range d.specEntries(s, memo) {
				if _, ok := out[name]; !ok {
					out[name] = se
				}
			}
		}
	}

	memo[m] = out

	return out
}

// pathNames reports whether a path names m, a mapping of the current text:
// whether the path from the root that spells the keys and the indexes above
// m, as [Doc.pathIn] gives it, selects m. No path names a mapping below a
// key without a name of its own, such as an alias key. Nor does a path name
// a mapping of an entry that a later entry of its key hides, because the
// path of its key selects the later entry.
func (d *Doc) pathNames(m ast.Node) bool {
	rel, ok := d.pathIn(d.node.AST(), m)
	if !ok {
		return false
	}

	p, err := d.resolve(paths.Doc().Join(rel))

	return err == nil && p.exists && astnode.Content(p.value) == astnode.Content(m)
}

// precedencePart returns the misread of own, an own entry of the current
// text before e, a << merge key of its mapping. The sources of e hold the
// key of own, as [Doc.precedenceMisreads] finds it. The message names e as
// the merge key that go-yaml's decoder reads. An alias of a reference
// document can bring in the key after the source that go-yaml's decoder
// reads, as later tells. That alias can be a later item of the merge
// sequence of e, or the value of a merge key after e. The engine does not
// read a reference document, so the message then names no merge key.
func (d *Doc) precedencePart(e, own *ast.MappingValueNode, later bool) (misreadFound, bool) {
	ks, kok := d.nodeSpan(e.Key)
	oks, ookay := d.nodeSpan(own.Key)

	if !kok || !ookay {
		return misreadFound{}, false
	}

	name, _ := d.node.Resolver().KeyName(own.Key)
	at := d.entrySite(own)

	// No path names a mapping below a key without a name of its own, such
	// as an alias key, or a mapping that a later entry of its key hides. The
	// message then names the key alone, so it names no path that reads
	// another value. A message about the edited text names the entry by its
	// path there.
	m := d.tree().parent[own]
	what, edited := "the key "+cut(name), "the key "+cut(name)

	if d.pathNames(m) {
		what, edited = at.path.String(), d.textPath(m).Child(name).String()
	}

	// The merge key of the edited text keeps its place in the user's text
	// when a call did not write it.
	merge := "the merge key at " + placeText(d.nodeSite(e.Key, true).at.Start)

	msg := fmt.Sprintf("go-yaml's decoder reads %s from %s, but the merge spec keeps the own entry written before it",
		what, merge)
	if later {
		msg = fmt.Sprintf("go-yaml's decoder reads %s from a merge key after it, but the merge spec keeps the own "+
			"entry written before it", what)
	}

	if d.origin[ks.start] < 0 || later {
		merge = "a merge key after it"
	}

	return misreadFound{
		kind: misreadPrecedence, at: at, start: oks.start,
		fix: fmt.Sprintf("write the merge key before %s, where both readings agree", cut(name)),
		msg: msg,
		edited: fmt.Sprintf("go-yaml's decoder reads %s from %s %s, but the merge spec keeps the own entry "+
			"written before it", edited, merge, inEdited),
	}, true
}

// mergeState holds the merges of a text: each kept merge, by the origin of
// the first byte of its merge key, with the keys of its mapping and of its
// source.
type mergeState struct {
	kept map[int]keptKeys
}

// keptKeys are the keys of the mapping of a kept merge, other than its
// merge key, and the keys of its source.
type keptKeys struct {
	own, source []string
}

// mergeState returns the merges of the current text.
func (d *Doc) mergeState() mergeState {
	st := mergeState{kept: make(map[int]keptKeys)}

	for _, k := range d.keptMerges() {
		st.kept[d.firstOrigin(k.entry.Key)] = keptKeys{own: d.ownKeys(k.m), source: d.ownKeys(k.s)}
	}

	return st
}

// mergeKept returns the problem of the call through h that named names,
// which ran, when the text after it breaks a merge rule that the checks
// before the call do not predict, or nil. A kept merge of the text that the call
// found, which the text after it holds at the origin of its merge key,
// must read a mapping (rule M5), and its mapping must hide the same keys
// of its source (rule M2).
func (d *Doc) mergeKept(h *Node, named string) error {
	res := d.node.Resolver()

	for _, k := range d.mergeEntries() {
		before, ok := d.mergesAt.kept[d.firstOrigin(k.entry.Key)]
		if !ok {
			continue
		}

		alias, isAlias := k.entry.Value.(*ast.AliasNode)
		if !isAlias {
			continue
		}

		anchor, err := res.Anchor(alias)
		if err != nil {
			continue
		}

		k.s = anchor

		if _, mapping := readEntries(astnode.Content(anchor)); !mapping {
			merge := d.nodeSite(k.entry.Key, true)
			pos := merge.at.Start

			return d.recordRefusal(&instance{
				construct: ConstructMergeForm,
				at:        merge,
				reach: fmt.Sprintf("leaves %s at %d:%d reading a value other than a mapping",
					mergeText(k), pos.Line+1, pos.Col+1),
				object:  "merge a value other than a mapping",
				fix:     "keep a mapping at the anchor that the merge key reads",
				related: []relatedSite{{site: d.mappingSite(k.m), note: "this mapping merges the value"}},
			}, named)
		}

		own, source := d.ownKeys(k.m), d.ownKeys(k.s)

		// The mapping hides the keys that it hid when it holds the same keys.
		if slices.Equal(own, before.own) {
			continue
		}

		for _, key := range slices.Sorted(slices.Values(append(slices.Clone(own), before.own...))) {
			changed := slices.Contains(own, key) != slices.Contains(before.own, key)
			if changed && (slices.Contains(source, key) || slices.Contains(before.source, key)) {
				return d.mergeKeysProblem(k, d.keySite(h, k.m, key), key, slices.Contains(own, key), named)
			}
		}
	}

	return nil
}

// referenceNames returns each mapping of the current text whose names a <<
// merge key of an alias of a reference document decides, with that merge
// key, as [Doc.formOf] reads it. A mapping that holds such a merge key reads
// names that the reference document holds. So does a mapping whose names
// such a mapping reads through its merge keys, as [Doc.formOf] gives the
// sources of each. The merge key of a mapping is the first such merge key of
// the first such mapping in the order of the text. The engine does not read
// which names the reference document holds. The index of the parse keeps
// the mappings, each as [astnode.Content] gives it.
func (d *Doc) referenceNames() map[ast.Node]*ast.MappingValueNode {
	t := d.parseIndex()
	if t.refNames != nil {
		return t.refNames
	}

	out := make(map[ast.Node]*ast.MappingValueNode)
	t.refNames = out

	for _, mm := range d.mergeMappings() {
		i := slices.IndexFunc(mm.entries, func(e *ast.MappingValueNode) bool {
			return astnode.IsMergeKey(e.Key) && d.formOf(e).ref != nil
		})
		if i < 0 {
			continue
		}

		seen := make(map[ast.Node]bool)
		stack := []ast.Node{mm.m}

		for len(stack) > 0 {
			m := astnode.Content(stack[len(stack)-1])
			stack = stack[:len(stack)-1]

			if seen[m] {
				continue
			}

			seen[m] = true

			if _, ok := out[m]; !ok {
				out[m] = mm.entries[i]
			}

			entries, _ := readEntries(m)
			for _, e := range entries {
				if astnode.IsMergeKey(e.Key) {
					stack = append(stack, d.formOf(e).sources...)
				}
			}
		}
	}

	return out
}

// referenceChange returns the first change of a name that c, a call whose
// walk is w, makes at its place in a mapping whose names a merge of a
// reference document decides, as [Doc.referenceNames] finds them, with its
// merge key. An earlier step of the path comes before a later one. It
// reports false when the call makes no such change. A Set of a name that
// such a mapping does not write, and a Move into such a mapping, look the
// name up through the merge key on their own paths. Those paths fail with
// the problem of the alias first, as [Node.At] tells, so only a change of
// an own entry that the call finds reaches this rule.
func (d *Doc) referenceChange(c reachCall, w *callWalk) (keyChange, *ast.MappingValueNode, bool) {
	decided := d.referenceNames()
	if len(decided) == 0 || !w.p.exists {
		return keyChange{}, nil, false
	}

	var (
		first keyChange
		merge *ast.MappingValueNode
	)

	for _, ch := range d.keyChanges(c, w, nil, true) {
		e, ok := decided[astnode.Content(ch.node)]
		if ok && (merge == nil || ch.step < first.step) {
			first, merge = ch, e
		}
	}

	return first, merge, merge != nil
}

// referenceProblem returns the problem of the call c, whose walk is w, that
// removes, renames, or moves the own entry at its place, in a mapping whose
// names e, a << merge key of an alias of a reference document, decides, as
// [Doc.referenceChange] finds it. The engine does not read which names the
// reference document holds, so it cannot tell whether the mapping then
// reads another value under the name. The problem matches
// [go.jacobcolvin.com/niceyaml/paths.ErrReferenceAlias] and
// [go.jacobcolvin.com/niceyaml/paths.ErrAlias], as the problem of a path
// through such a merge key does, and the Apply records it. It binds at the
// entry of the call, with a detail at the merge key.
func (d *Doc) referenceProblem(c reachCall, w *callWalk, e *ast.MappingValueNode) error {
	merge := d.nodeSite(e.Key, true)
	holder := d.textPath(d.mappingOf(e))
	k := keptMerge{m: d.mappingOf(e), entry: e}
	pos := merge.at.Start

	var name string

	if f := d.formOf(e); f.ref != nil {
		name, _ = astnode.Name(f.ref.Value)
	}

	ref := d.referenceWords(name)

	msg := fmt.Sprintf("%s %s, but %s at %d:%d reads %s, which decides which names %s reads",
		d.subject(), c.named, mergeText(k), pos.Line+1, pos.Col+1, ref, holder)

	at := d.entrySite(w.p.entry)
	details := []error{
		d.user.Bind(niceyaml.NewError(
			fmt.Sprintf("%s reads %s here", mergeText(k), ref),
			niceyaml.AtPath(merge.path), niceyaml.AtRange(merge.at), niceyaml.WithDetails(d.writerDetails(merge)...),
		)),
		errors.New(d.referenceFix(holder)),
	}

	details = append(details, d.callDetails(at)...)

	problem := d.user.Bind(niceyaml.Invalid(&referenceError{msg: msg},
		niceyaml.AtPath(at.path), niceyaml.AtRange(at.at), niceyaml.WithDetails(details...)))
	d.record(problem)

	return problem //nolint:wrapcheck // The binding is the problem of the call.
}

// referenceFix returns the fix of the problem of [Doc.referenceProblem] for
// a call in holder, the path of the mapping whose names a reference
// document decides. A Set of holder writes the whole mapping without the
// merge key, and a merge of a mapping of the document lets the engine read
// the names. No call sets the root of the document, so at the root the fix
// names the merge alone.
func (d *Doc) referenceFix(holder paths.Path) string {
	if holder.IsRoot() {
		return "merge a mapping of the document, or write the entries that the document needs in the text"
	}

	return fmt.Sprintf("set %s to the whole value that it needs, or merge a mapping of the document", holder)
}

// referenceError is the problem of a call that changes a name of a mapping
// whose names a << merge key of an alias of a reference document decides,
// as [Doc.referenceProblem] gives it. It wraps
// [go.jacobcolvin.com/niceyaml/paths.ErrAlias] and matches
// [go.jacobcolvin.com/niceyaml/paths.ErrReferenceAlias], as every error of
// an alias of a reference document does.
type referenceError struct {
	msg string
}

// Error returns the message of e.
func (e *referenceError) Error() string {
	return e.msg
}

// Unwrap returns [go.jacobcolvin.com/niceyaml/paths.ErrAlias].
func (e *referenceError) Unwrap() error {
	return paths.ErrAlias
}

// Is reports whether target is
// [go.jacobcolvin.com/niceyaml/paths.ErrReferenceAlias].
func (e *referenceError) Is(target error) bool {
	return target == paths.ErrReferenceAlias
}

// mergeEntries returns each << merge key of the current text whose value
// is an alias, with the mapping that holds it.
func (d *Doc) mergeEntries() []keptMerge {
	var out []keptMerge

	for _, mm := range d.mergeMappings() {
		for _, e := range mm.entries {
			if _, alias := e.Value.(*ast.AliasNode); alias && astnode.IsMergeKey(e.Key) {
				out = append(out, keptMerge{m: mm.m, entry: e})
			}
		}
	}

	return out
}

// aliasBindings returns the anchor that each alias of the current text
// reads, as the resolver of the text binds it, by the origin of the first
// byte of each: the origin of the first byte of the anchor, or the least
// int for an alias that reads no anchor of the text.
func (d *Doc) aliasBindings() map[int]int {
	out := make(map[int]int)

	for _, r := range d.aliases() {
		if r.start >= len(d.origin) || d.origin[r.start] < 0 {
			continue
		}

		out[d.origin[r.start]] = d.boundTo(r)
	}

	return out
}

// boundTo returns the origin of the first byte of the anchor that the
// alias of r reads, or the least int for an alias that reads no anchor of
// the current text.
func (d *Doc) boundTo(r ref) int {
	alias, ok := r.node.(*ast.AliasNode)
	if !ok {
		return math.MinInt
	}

	a, ok := d.anchorOf(alias)
	if !ok || a.start >= len(d.origin) {
		return math.MinInt
	}

	return d.origin[a.start]
}

// rebound returns the origin of the first alias of the current text, in
// the order of the text, that reads another anchor than bound holds for
// it, as [Doc.aliasBindings] gives them, or -1.
func (d *Doc) rebound(bound map[int]int) int {
	for _, r := range d.aliases() {
		if r.start >= len(d.origin) {
			continue
		}

		o := d.origin[r.start]
		if was, ok := bound[o]; ok && was != d.boundTo(r) {
			return o
		}
	}

	return -1
}

// moveOrderProblem returns the problem of a Move that named names, after
// which the alias whose first byte has the origin o reads another anchor
// than before, or none, as an alias that comes before the anchor that it
// read does. The engine does not write a copy of the value at the alias.
// The problem binds at the alias in the text before the Move, and the
// Apply records it.
func (d *Doc) moveOrderProblem(o int, named string) error {
	in := instance{
		construct: ConstructAliasCopy,
		fix:       "move the alias after the anchor first, or set it to the value it reads",
	}

	reads := "this alias"

	for _, r := range d.aliases() {
		if r.start >= len(d.origin) || d.origin[r.start] != o {
			continue
		}

		in.at = d.nodeSite(r.node, false)

		if merge, ok := d.mergeReader(r.node); ok {
			pos := d.nodeSite(merge.Key, true)
			reads = fmt.Sprintf("the merge key at %d:%d of %s", pos.at.Start.Line+1, pos.at.Start.Col+1, pos.path)
		}
	}

	in.but = fmt.Sprintf(", after which %s comes before the anchor that it reads, and %s does not write a copy of "+
		"a value at its alias", reads, d.appName())

	return d.recordRefusal(&in, named)
}

// scalarProblem returns the problem of the call through h that named
// names, which ran, when a block scalar of the current text reads another
// string than its writer gave it. A block scalar of the user's text that
// the call kept reads the string of the user's text, as
// [Doc.readUserScalars] reads it, so a call that changes that string
// reaches [ConstructBlockScalar]. A line that a call adds after a scalar
// that ends the text without a line break changes the string, and so does
// a comment line that a call leaves inside the indentation of a scalar. A
// block scalar that the engine wrote reads the string of its note, as
// [Doc.keepStrings] keeps it, so another string shows a bug of the engine.
func (d *Doc) scalarProblem(h *Node, named string) error {
	for _, lit := range d.literals() {
		if lit.Start == nil || lit.Value == nil {
			continue
		}

		hs, ok := d.tokenSpan(lit.Start)
		if !ok || hs.start >= len(d.origin) {
			continue
		}

		str := lit.Value.Value

		switch o := d.origin[hs.start]; {
		case o >= 0 && d.ownByte(hs.start):
			was, ok := d.scalars[o]
			if !ok || was == str {
				continue
			}

			return d.recordRefusal(new(d.scalarInstance(lit, was)), named)

		case o < 0 && d.notes[-1-o].hasStr && d.notes[-1-o].str != str:
			return d.bug(h, fmt.Errorf("go-yaml reads the block scalar that %s writes at %s as %s, not %s",
				d.subject(), d.offsetPath(hs.start), cut(strconv.Quote(str)), cut(strconv.Quote(d.notes[-1-o].str))))
		}
	}

	return nil
}

// scalarInstance returns the instance of [ConstructBlockScalar] of lit, a
// block scalar of the user's text whose string was before a call and that
// go-yaml reads otherwise after it.
func (d *Doc) scalarInstance(lit *ast.LiteralNode, was string) instance {
	str := lit.Value.Value
	in := instance{
		construct: ConstructBlockScalar,
		at:        d.nodeSite(lit, false),
		fix:       "set the scalar to the string that it needs, or edit another place",
		but: fmt.Sprintf(", after which this block scalar reads %s, not %s, and %s does not change the string "+
			"of a block scalar of the user's text", cut(strconv.Quote(str)), cut(strconv.Quote(was)), d.appName()),
	}

	// A line after a scalar at the end of a text without a line break adds
	// the break to its string.
	if !strings.HasSuffix(was, "\n") && strings.TrimRight(str, "\n") == was {
		in.but, in.call = "", "adds lines after this block scalar, which ends the text without a line break, so it "+
			"changes the string of the scalar, when it %s"
		in.fix = "end the scalar with a line break, which adds one to its string, or edit another place"
	}

	return in
}

// commentProblem returns the problem of the call through h that named
// names, which ran, when the first byte of a comment of the text that the
// call found is text of a value after the call. A block scalar takes in a
// comment line inside its indentation, as after a call that removes the
// entry between them. The engine keeps the string of its own block scalar,
// as [Doc.keepStrings] keeps it, so a comment inside such a scalar shows a
// bug of the engine. The problem of any other comment binds at the comment.
func (d *Doc) commentProblem(h *Node, named string) error {
	before := make(map[int]bool)
	for _, o := range d.commentOrigins(d.callStart) {
		before[o] = true
	}

	if len(before) == 0 {
		return nil
	}

	now := make(map[int]bool)
	for _, o := range d.commentOrigins(d.save()) {
		now[o] = true
	}

	// A line break that a call writes can take the origin of the line after
	// it, so only the byte of the comment itself counts.
	for i, o := range d.origin {
		if o < 0 || now[o] || !before[o] || !d.userText(i, 1) {
			continue
		}

		at := d.site(span{start: i, end: i + 1}, false)
		pos := at.at.Start

		for _, lit := range d.literals() {
			hs, hok := d.tokenSpan(lit.Start)
			s, ok := d.nodeSpan(lit)

			if hok && ok && s.start <= i && i < s.end && hs.start < len(d.origin) && d.origin[hs.start] < 0 {
				return d.bug(h, fmt.Errorf("go-yaml reads the comment at %d:%d as text of the block scalar that %s "+
					"writes", pos.Line+1, pos.Col+1, d.subject()))
			}
		}

		return d.recordRefusal(&instance{
			construct: ConstructBlockScalar,
			at:        at,
			but: fmt.Sprintf(", after which the comment at %d:%d is text of a value, and %s does not change the "+
				"text of a value of the user's text", pos.Line+1, pos.Col+1, d.appName()),
			fix: "indent the comment less than the value before it, or edit another place",
		}, named)
	}

	return nil
}

// commentOrigins returns the origin of the first byte of each comment of
// the text of s, in the order of the text, for the bytes of the user's
// text. The index of the parse of s keeps them for the origins of s. The
// caller must not change the returned slice.
func (d *Doc) commentOrigins(s state) []int {
	now := d.save()
	d.show(s)

	defer d.show(now)

	return d.parseIndex().commented.read(d.origin, d.readCommentOrigins)
}

// readCommentOrigins reads the comment origins of the current text, as
// [Doc.commentOrigins] returns them.
func (d *Doc) readCommentOrigins() []int {
	var out []int

	for _, tk := range d.commentTokens() {
		if cs, ok := d.tokenSpan(tk); ok && cs.start < len(d.origin) && d.origin[cs.start] >= 0 {
			out = append(out, d.origin[cs.start])
		}
	}

	return out
}

// madeProblem returns the problem of the call through h that named names,
// which ran, when the current text holds an instance of a construct that
// the engine does not edit that the text that the Apply found did not hold
// at its origins. The text of a Doc holds no instance of a construct that
// [New] refuses, so each such instance is one that the call made, as a
// Move makes an alias inside the value of the anchor that it reads. An
// instance of a construct that a call reaches counts when no zone of
// the Apply has its construct and the origin of its first byte. The
// problem of a construct whose first byte the engine wrote matches
// [ErrEngine]. Any other construct lies in
// the bytes of the user, which the call kept or moved, so its problem
// matches [ErrUnsupported].
func (d *Doc) madeProblem(h *Node, named string) error {
	if found := d.find(true); len(found) > 0 {
		in := firstInText(found)
		in.reach, in.object = "makes "+in.what, "edit such a document"

		return d.madeRefusal(h, in, named)
	}

	zones := d.constructZones(true)
	slices.SortStableFunc(zones, func(a, b *zone) int { return cmp.Compare(a.at, b.at) })

	for _, z := range zones {
		if !d.heldZone(z) && !d.heldComment(z) {
			in := z.in
			in.reach = "makes " + strings.TrimPrefix(strings.TrimPrefix(in.reach, "goes through "), "reaches ")

			return d.madeRefusal(h, &in, named)
		}
	}

	return nil
}

// heldZone reports whether z, a zone of the current text, is a zone of the
// Apply running: a zone of the same construct whose first part starts at
// the origin of the first part of z.
func (d *Doc) heldZone(z *zone) bool {
	return d.zoneKeys[keyOfZone(z)]
}

// zoneKey is what [Doc.heldZone] compares of two zones: the origin of
// the first byte of its first part, and its construct.
type zoneKey [2]int

// keyOfZone returns the key of z, as [zoneKey] describes it.
func keyOfZone(z *zone) zoneKey {
	k := zoneKey{math.MinInt, int(z.in.construct)}
	if len(z.parts) > 0 && len(z.parts[0].origins) > 0 {
		k[0] = z.parts[0].origins[0]
	}

	return k
}

// heldComment reports whether z, the zone of a flow collection of the
// current text with a comment inside it, holds a comment that go-yaml's
// lexer read inside that collection in the text that the call found. A
// "#" right after a comma opens no comment in YAML, though go-yaml's lexer
// reads one there, so that text has no zone at the collection, as
// [Doc.commentZone] tells. A call that writes a space before such a "#"
// keeps the comment that go-yaml read.
func (d *Doc) heldComment(z *zone) bool {
	if z.in.construct != ConstructFlowLayout || len(z.in.related) == 0 || len(z.parts[0].origins) == 0 {
		return false
	}

	comment, flow := z.in.related[0].origin, z.parts[0].origins[0]

	now := d.save()
	d.show(d.callStart)

	defer d.show(now)

	for _, tk := range d.commentTokens() {
		cs, ok := d.tokenSpan(tk)
		if !ok || d.originAt(cs.start) != comment {
			continue
		}

		held := false

		d.inspect(func(n ast.Node) bool {
			if fs, ok := d.nodeSpan(n); ok && isFlow(n) && fs.start < cs.start && cs.start < fs.end {
				held = held || d.originAt(fs.start) == flow
			}

			return !held
		})

		return held
	}

	return false
}

// madeRefusal returns the problem of the call through h that named names,
// which made in, as [Doc.madeProblem] finds it. The engine wrote the first
// byte of a construct whose site has the origin of a note, so the problem
// of such a construct matches [ErrEngine].
func (d *Doc) madeRefusal(h *Node, in *instance, named string) error {
	if in.at.origin < 0 {
		return d.bug(h, fmt.Errorf("%s %s, which %s, though %s does not write such YAML",
			d.subject(), named, in.reach, d.appName()))
	}

	return d.recordRefusal(in, named)
}
