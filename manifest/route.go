package manifest

import (
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/goccy/go-yaml/ast"
	"github.com/goccy/go-yaml/token"

	"go.jacobcolvin.com/niceyaml"
	"go.jacobcolvin.com/niceyaml/internal/astnode"
	"go.jacobcolvin.com/niceyaml/internal/hook"
	"go.jacobcolvin.com/niceyaml/internal/mapkey"
	"go.jacobcolvin.com/niceyaml/paths"
	"go.jacobcolvin.com/niceyaml/position"
)

// maxMergeDepth is the number of nested `<<` merge keys that the search of
// a field follows into the mappings that they bring in.
const maxMergeDepth = 16

// fieldRead is what a load reads at the path of a type field or of the
// name of a document.
type fieldRead struct {
	// The unbound problem of a path that passes through an alias or a
	// merge key, or of a value that is not text, or nil.
	err error
	// The unbound problem of a scalar that go-yaml's decoder reads as
	// another string than its text, or nil.
	misread error
	// The string that go-yaml's decoder reads from the scalar, as
	// [mapkey.StringOf] gives it.
	text string
	// Whether the document writes a scalar at the path.
	found bool
}

// fieldName returns the name of the field at p, as messages give it: the
// key of its last selector, such as "apiVersion".
func fieldName(p paths.Path) string {
	if last, ok := p.Last(); ok && last.Kind == paths.SelectorChild {
		return last.Name
	}

	return p.String()
}

// fieldPaths returns the paths of the type fields and of the name that a
// load reads.
func (c *registryConfig) fieldPaths() []paths.Path {
	fields := []paths.Path{c.versionField}
	if !c.versionOnly {
		fields = append(fields, c.kindField)
	}

	if c.hasNameField {
		fields = append(fields, c.nameField)
	}

	return fields
}

// readField reads the scalar at p, a path of names and indexes, in the
// document doc. A path that passes through an alias or a `<<` merge key
// fails at that alias or merge key. The read cannot follow an alias into a
// reference document, and a rewrite of the field reaches every value that
// shares the anchor. A path through a key that a later merge key from a
// reference document might replace fails at that merge key too. The
// detail of that problem names the first key before the merge key that p
// or a path of reads passes through. The author thus moves the merge key
// above every such key at once. A path through a key whose place a decode
// reads from another key of the mapping fails at that key, such as 0x10
// for 16. So does a path through a key before a key that does not decode,
// because the decode might read that key in its place. The keys read as
// niceyaml's decode of the document reads them, with the anchors of the
// reference documents of its Source. The field reads as go-yaml's decoder
// reads its value into a string, under its tags. So "!!null x" reads as a
// field that the document leaves out, as any null does, and the number 2
// reads "2". A value that the decoder does not read into a string, such as
// a mapping or a timestamp, is not text. The read also notes a scalar that
// the decoder reads as another string than its text, such as 1.10 as "1.1".
func readField(doc *niceyaml.Node, p paths.Path, reads []paths.Path) fieldRead {
	res := doc.Resolver()
	keys := hook.Keys(doc)
	name := fieldName(p)
	cur := doc.AST()
	selectors := slices.Collect(p.Selectors())

	for depth, sel := range selectors {
		if alias, ok := astnode.Content(cur).(*ast.AliasNode); ok {
			return fieldRead{err: aliasProblem(p, name, alias)}
		}

		content := astnode.Content(cur)

		switch sel.Kind {
		case paths.SelectorChild:
			entry, other, read := findEntry(res, keys, content, sel.Name)

			switch {
			case read == readsUndecoded:
				return fieldRead{err: undecodedProblem(p, name, other)}

			case read == readsReference:
				advice := mergeAdvice(res, content, other, selectors[:depth], append([]paths.Path{p}, reads...))

				return fieldRead{err: unreadProblem(p, name, advice, astnode.KeyToken(other))}

			case other != nil && astnode.IsMergeKey(other.Key):
				return fieldRead{err: mergeProblem(p, name, astnode.KeyToken(other))}
			case other != nil:
				return fieldRead{err: repeatProblem(doc, p, name, other, entry)}
			case entry != nil:
				cur = entry.Value
			default:
				return fieldRead{}
			}

		case paths.SelectorIndex:
			seq, ok := content.(*ast.SequenceNode)
			if !ok || sel.Index >= len(seq.Values) {
				return fieldRead{}
			}

			cur = seq.Values[sel.Index]

		default:
			return fieldRead{}
		}
	}

	if alias, ok := astnode.Content(cur).(*ast.AliasNode); ok {
		return fieldRead{err: aliasProblem(p, name, alias)}
	}

	text, ok, err := mapkey.StringOf(cur)
	own, scalar := astnode.ScalarText(cur)

	switch {
	case err != nil || ok && !scalar:
		return fieldRead{err: newProblem(ErrMissingTypeField, name+" must be text", niceyaml.AtPath(p))}
	case !ok:
		return fieldRead{}
	case own != text:
		return fieldRead{text: text, found: true, misread: misreadProblem(p, cur, own, text)}
	}

	return fieldRead{text: text, found: true}
}

// readTypeField reads the type field at p in doc, as [readField] reads it.
// A scalar that go-yaml's decoder reads as another string than its text,
// such as 1.10 as "1.1" or "!!str 1.0" as "1", names no kind or version
// that the document writes, so its read fails.
func readTypeField(doc *niceyaml.Node, p paths.Path, reads []paths.Path) fieldRead {
	r := readField(doc, p, reads)
	if r.misread != nil {
		return fieldRead{err: r.misread}
	}

	return r
}

// misreadProblem returns the problem of the field at p, whose value is the
// scalar node with the text own, which go-yaml's decoder reads as the
// string text. The message shows the tags of node with own, and it asks
// for own in quotes.
func misreadProblem(p paths.Path, node ast.Node, own, text string) error {
	shown := own
	if strings.ContainsAny(own, "\r\n") || strings.TrimSpace(own) != own {
		shown = strconv.Quote(own)
	}

	var tags []string

	for n := node; n != nil; {
		switch x := n.(type) {
		case *ast.AnchorNode:
			n = x.Value
		case *ast.TagNode:
			tags = append(tags, x.Start.Value)
			n = x.Value

		default:
			n = nil
		}
	}

	shown = strings.Join(append(tags, shown), " ")

	return newProblem(ErrMissingTypeField, fmt.Sprintf("go-yaml's decoder reads %s as %q; write %q", shown, text, own),
		niceyaml.AtPath(p))
}

// findEntry returns the entry of the mapping node that a path through name
// selects. The decode reads another entry in its place when the mapping
// holds a later entry whose key the decode reads as the key of the entry,
// such as 0x10 for 16. That later entry can be an entry of the mapping
// itself or one that a `<<` merge key brings in. The function also returns
// the later entry then, or the last merge entry that brings it in. That
// merge entry wins over the merge entries before it. A key that a write
// of the field creates reads as name, so without the entry, findEntry
// returns the entry that the decode reads as name. The keys that a decode
// reads come from keys, a reader of the keys of the document.
//
// The lookup cannot read the sources of a merge key that reads a reference
// document. Such a merge key after the entry of the mapping itself might
// replace that entry, so findEntry returns the merge key with
// readsReference. It also returns a later key that does not decode, or the
// merge key that brings it in, with readsUndecoded, because the decode
// might read that key as the key of the entry. After a merge key that the
// lookup cannot read for another reason, such as an alias without an
// anchor, findEntry returns the entry of the mapping itself. It returns nil
// for both when neither the mapping nor a merge source that it can read
// holds the key.
func findEntry(
	res *paths.Resolver, keys *mapkey.Keys, node ast.Node, name string,
) (*ast.MappingValueNode, *ast.MappingValueNode, laterKind) {
	entries, ok := astnode.Entries(node)
	if !ok {
		return nil, nil, readsKey
	}

	found, err := res.Entry(node, name)
	if e, ok := found.(*ast.MappingValueNode); ok && err == nil && slices.Contains(entries, e) {
		if key, ok := keys.Key(e.Key); ok {
			later, read := laterRead(res, keys, entries[slices.Index(entries, e)+1:], name, key)

			return e, later, read
		}

		return e, nil, readsKey
	}

	if err != nil && !errors.Is(err, paths.ErrNotFound) {
		for i, e := range slices.Backward(entries) {
			key, ok := res.KeyName(e.Key)
			if !ok || key != name || astnode.IsMergeKey(e.Key) {
				continue
			}

			if !errors.Is(err, paths.ErrReferenceAlias) {
				return e, nil, readsKey
			}

			if merge := unreadMerge(res, entries[i+1:]); merge != nil {
				return nil, merge, readsReference
			}

			return e, nil, readsKey
		}
	}

	later, read := laterRead(res, keys, entries, name, name)

	return nil, later, read
}

// laterKind is how a decode might read a later entry of a mapping in place
// of the entry of a field, as [findEntry] reports it.
type laterKind uint8

const (
	// The later entry, if any, reads as the key of the field.
	readsKey laterKind = iota
	// The later entry is a merge key that reads a reference document.
	readsReference
	// The later entry has a key that does not decode, or is a merge key
	// that brings in such a key.
	readsUndecoded
)

// laterRead returns the last of entries whose key a decode reads as key.
// That entry is an entry itself or a merge entry. The merge entry brings
// in an entry that a path through name selects, or one that the decode
// reads as key, as [mergeHolds] tells. The result is nil when none of
// entries does. A key that does not decode might read as key, so
// laterRead returns the last entry with such a key, or the merge entry
// that brings it in, with readsUndecoded, when no later entry reads as
// key.
func laterRead(
	res *paths.Resolver, keys *mapkey.Keys, entries []*ast.MappingValueNode, name, key string,
) (*ast.MappingValueNode, laterKind) {
	for _, e := range slices.Backward(entries) {
		if astnode.IsMergeKey(e.Key) {
			holds, undecoded := mergeHolds(res, keys, e, name, key, 0)

			switch {
			case holds:
				return e, readsKey
			case undecoded:
				return e, readsUndecoded
			}

			continue
		}

		decoded, ok := keys.Key(e.Key)

		switch {
		case !ok:
			return e, readsUndecoded
		case decoded == key:
			return e, readsKey
		}
	}

	return nil, readsKey
}

// mergeAdvice returns the detail of the problem of a field that the merge
// entry of the mapping node might replace. The selectors of at lead to
// node. The detail asks the author to put the merge key before the first
// entry of node that one of the paths of reads passes through, as
// [readEntries] finds those entries. An alias of the merge key must come
// after the anchor it reads. When that anchor lies in such an entry, or in
// an entry between that one and the merge key, the move would put the alias
// before its anchor. The detail then asks to move the anchor out of an
// entry that a path of reads passes through, or to move the entries that
// hold the anchors too.
func mergeAdvice(
	res *paths.Resolver, node ast.Node, merge *ast.MappingValueNode, at []paths.Selector, reads []paths.Path,
) string {
	entries, _ := astnode.Entries(node)
	end := max(slices.Index(entries, merge), 0)
	read := readEntries(res, entries[:end], at, reads)

	first := slices.IndexFunc(entries[:end], func(e *ast.MappingValueNode) bool { return read[e] })
	if first < 0 {
		return "put the merge key before the fields that the load reads"
	}

	key, _ := res.KeyName(entries[first].Key)

	var moved []string

	for _, e := range entries[first:end] {
		anchor, ok := mergeAnchor(res, merge, e)
		if !ok {
			continue
		}

		own, _ := res.KeyName(e.Key)
		if read[e] {
			return fmt.Sprintf("move the anchor %s out of %s, then put the merge key before %s", anchor, own, key)
		}

		moved = append(moved, own)
	}

	if len(moved) == 0 {
		return "put the merge key before " + key
	}

	return fmt.Sprintf("put %s and the merge key before %s", inSentence(moved), key)
}

// readEntries returns each of entries that one of the paths of reads passes
// through, in a mapping that the selectors of at lead to. Such an entry is
// the last entry of the mapping with its key, because the lookup of a key
// reads that entry.
func readEntries(
	res *paths.Resolver, entries []*ast.MappingValueNode, at []paths.Selector, reads []paths.Path,
) map[*ast.MappingValueNode]bool {
	names := map[string]bool{}

	for _, r := range reads {
		selectors := slices.Collect(r.Selectors())
		if len(selectors) > len(at) && slices.Equal(selectors[:len(at)], at) &&
			selectors[len(at)].Kind == paths.SelectorChild {
			names[selectors[len(at)].Name] = true
		}
	}

	last := map[string]*ast.MappingValueNode{}

	for _, e := range entries {
		if key, ok := res.KeyName(e.Key); ok && !astnode.IsMergeKey(e.Key) && names[key] {
			last[key] = e
		}
	}

	read := make(map[*ast.MappingValueNode]bool, len(last))
	for _, e := range last {
		read[e] = true
	}

	return read
}

// mergeAnchor returns the name of an anchor inside entry that an alias of
// the merge entry reads, and reports whether entry holds one.
func mergeAnchor(res *paths.Resolver, merge, entry *ast.MappingValueNode) (string, bool) {
	for _, v := range astnode.MergeValues(merge) {
		alias, ok := v.(*ast.AliasNode)
		if !ok {
			continue
		}

		anchor, err := res.Anchor(alias)
		if err != nil {
			continue
		}

		inside := false

		astnode.Inspect(entry, func(n ast.Node) bool {
			inside = inside || n == anchor

			return !inside
		})

		if name, ok := astnode.Name(alias.Value); ok && inside {
			return name, true
		}
	}

	return "", false
}

// inSentence returns names as a list in a sentence, as in "a, b, and c".
func inSentence(names []string) string {
	switch len(names) {
	case 1:
		return names[0]
	case 2:
		return names[0] + " and " + names[1]
	default:
		return strings.Join(names[:len(names)-1], ", ") + ", and " + names[len(names)-1]
	}
}

// unreadMerge returns the last merge entry of entries that brings in a
// mapping of a reference document, as [mergeUnread] reports. Without one,
// it returns the last merge entry of entries.
func unreadMerge(res *paths.Resolver, entries []*ast.MappingValueNode) *ast.MappingValueNode {
	var last *ast.MappingValueNode

	for _, e := range slices.Backward(entries) {
		if !astnode.IsMergeKey(e.Key) {
			continue
		}

		if mergeUnread(res, e, 0) {
			return e
		}

		if last == nil {
			last = e
		}
	}

	return last
}

// mergeUnread reports whether a mapping that the merge entry e brings in,
// itself or through a merge key of its own, comes from a reference
// document.
func mergeUnread(res *paths.Resolver, e *ast.MappingValueNode, depth int) bool {
	if depth > maxMergeDepth {
		return false
	}

	for _, v := range astnode.MergeValues(e) {
		m, err := res.Deref(v)
		if errors.Is(err, paths.ErrReferenceAlias) {
			return true
		}

		entries, ok := astnode.Entries(astnode.Content(m))
		if err != nil || !ok {
			continue
		}

		for _, inner := range entries {
			if astnode.IsMergeKey(inner.Key) && mergeUnread(res, inner, depth+1) {
				return true
			}
		}
	}

	return false
}

// mergeHolds reports whether a mapping that the merge entry e brings in
// holds an entry that a path through name selects. It also reports whether
// the mapping holds an entry that a decode reads as key, itself or through
// a merge key of its own. Its second result reports whether such a mapping
// holds a key that does not decode, which might read as key.
func mergeHolds(
	res *paths.Resolver, keys *mapkey.Keys, e *ast.MappingValueNode, name, key string, depth int,
) (bool, bool) {
	if depth > maxMergeDepth {
		return false, false
	}

	unread := false

	for _, v := range astnode.MergeValues(e) {
		m, err := res.Deref(v)
		if err != nil {
			continue
		}

		entries, ok := astnode.Entries(astnode.Content(m))
		if !ok {
			continue
		}

		for _, inner := range entries {
			if astnode.IsMergeKey(inner.Key) {
				holds, undecoded := mergeHolds(res, keys, inner, name, key, depth+1)
				if holds {
					return true, false
				}

				unread = unread || undecoded

				continue
			}

			if text, ok := res.KeyName(inner.Key); ok && text == name {
				return true, false
			}

			decoded, ok := keys.Key(inner.Key)
			if ok && decoded == key {
				return true, false
			}

			unread = unread || !ok
		}
	}

	return false, unread
}

// aliasProblem returns the problem of the field name at p, which comes from
// alias.
func aliasProblem(p paths.Path, name string, alias *ast.AliasNode) error {
	return newProblem(ErrFieldAlias, name+" must not come from an alias",
		niceyaml.AtPath(p), atAlias(alias),
		niceyaml.WithDetails(fmt.Errorf("write the %s in the document itself", name)))
}

// mergeProblem returns the problem of the field name at p, which the merge
// key at tk brings in.
func mergeProblem(p paths.Path, name string, tk *token.Token) error {
	return newProblem(ErrFieldAlias, name+" must not come from a merge key",
		niceyaml.AtPath(p), atToken(tk),
		niceyaml.WithDetails(fmt.Errorf("write the %s in the document itself", name)))
}

// repeatProblem returns the problem of the field name at p, which the
// decode reads from other, an entry of the mapping whose key it reads as
// the key of the field. The problem binds at the key of other. The detail
// binds at the key of entry, the entry of the field in doc, or the problem
// does not have it for a nil entry.
func repeatProblem(doc *niceyaml.Node, p paths.Path, name string, other, entry *ast.MappingValueNode) error {
	text := keyText(other)
	details := []error{fmt.Errorf("write the key as %s", name)}

	if entry != nil {
		details = []error{
			doc.Bind(niceyaml.NewError(fmt.Sprintf("a decode reads %s in place of this %s", text, name),
				atKey(entry))),
			errors.New("remove one of the two"),
		}
	}

	return newProblem(ErrFieldAlias, fmt.Sprintf("%s must not come from the key %s", name, text),
		niceyaml.AtPath(p), atKey(other), niceyaml.WithDetails(details...))
}

// atKey returns an [niceyaml.ErrorOption] that locates an error at the key
// of entry, as [atAlias] locates an alias key.
func atKey(entry *ast.MappingValueNode) niceyaml.ErrorOption {
	if alias, ok := astnode.Content(entry.Key).(*ast.AliasNode); ok {
		return atAlias(alias)
	}

	return atToken(astnode.KeyToken(entry))
}

// keyText returns the key of entry as the document writes it, such as 0x10
// or *k.
func keyText(entry *ast.MappingValueNode) string {
	key := ast.Node(entry.Key)
	if explicit, ok := key.(*ast.MappingKeyNode); ok {
		key = explicit.Value
	}

	return strings.TrimSpace(key.String())
}

// undecodedProblem returns the problem of the field name at p, which a
// decode might read from other: an entry whose key does not decode, or a
// merge entry that brings in such a key. The problem binds at the key of
// other.
func undecodedProblem(p paths.Path, name string, other *ast.MappingValueNode) error {
	msg := fmt.Sprintf("%s might come from the key %s, which does not decode", name, keyText(other))
	advice := fmt.Errorf("write the key %s so that it decodes", keyText(other))

	if astnode.IsMergeKey(other.Key) {
		msg = name + " might come from a merge key that brings in a key that does not decode"
		advice = fmt.Errorf("write the %s in the document itself", name)
	}

	return newProblem(ErrFieldAlias, msg, niceyaml.AtPath(p), atKey(other), niceyaml.WithDetails(advice))
}

// unreadProblem returns the problem of the field name at p, which the
// merge key at tk might replace. The merge key reads a reference document.
// The detail gives advice, as [mergeAdvice] writes it.
func unreadProblem(p paths.Path, name, advice string, tk *token.Token) error {
	return newProblem(ErrFieldAlias, name+" might come from a merge key that reads a reference document",
		niceyaml.AtPath(p), atToken(tk),
		niceyaml.WithDetails(errors.New(advice)))
}

// atToken returns an [niceyaml.ErrorOption] that locates an error at the
// token tk, which the excerpt marks whole.
func atToken(tk *token.Token) niceyaml.ErrorOption {
	if tk == nil || tk.Position == nil {
		return niceyaml.WithDetails()
	}

	return niceyaml.AtPosition(position.NewFromToken(tk))
}

// atAlias returns an [niceyaml.ErrorOption] that locates an error at
// alias, from its "*" through its name.
func atAlias(alias *ast.AliasNode) niceyaml.ErrorOption {
	if alias.Start == nil || alias.Start.Position == nil {
		return atToken(alias.GetToken())
	}

	start := position.NewFromToken(alias.Start)
	end := position.New(start.Line, start.Col+1)

	if tk := astnode.Token(alias.Value); tk != nil && tk.Position != nil {
		at := position.NewFromToken(tk)
		end = position.New(at.Line, at.Col+utf8.RuneCountInString(tk.Value))
	}

	return niceyaml.AtRange(position.NewRange(start, end))
}

// startOf returns an [niceyaml.ErrorOption] that locates an error at the
// start of the document doc, its first token that is not a comment.
func startOf(doc *niceyaml.Node) niceyaml.ErrorOption {
	return atToken(astnode.StartToken(doc.AST()))
}

// route is where a document goes, its kind and its version, after the load
// reads its type fields.
type route struct {
	// The kind that the document goes to, or nil.
	kind *kindEntry
	// The version that the document declares, or nil when routing failed.
	version *versionEntry
	// The kind that the document counts toward for [Required] and
	// [Singleton], or nil. Its kind field names this kind, whatever its
	// version.
	counted *kindEntry
	// The details on the first error of a document that the load reads as
	// a kind that it does not name.
	adoption []error
	// The text of the kind field, or "" when the document does not have
	// one.
	wroteKind  string
	versionTxt fieldRead
	kindTxt    fieldRead
}

// versionLabel returns the version name as a message outside quotes shows
// it: "version 2" under [WithVersionOnly], and the name itself otherwise.
func (r *Registry) versionLabel(name string) string {
	if r.cfg.versionOnly {
		return "version " + name
	}

	return name
}

// label returns v as a message of a load names it outside quotes.
func (r *Registry) label(v *versionEntry) string {
	if v.cfg.unversioned {
		return "the unversioned layout of " + v.kind.kind.name
	}

	return r.versionLabel(v.cfg.name)
}

// app returns the name that [WithAppName] gives the app, or "this program"
// without it.
func (r *Registry) app() string {
	if r.cfg.appName != "" {
		return r.cfg.appName
	}

	return "this program"
}

// newerRelease returns the detail that a later release of the app might
// read what, such as "this version".
func (r *Registry) newerRelease(what string) error {
	return fmt.Errorf("a newer release of %s might read %s", r.app(), what)
}

// findRemoval returns the removal of version for the kind name, which a
// document writes, or nil. A removal under another name of the same kind
// matches too.
func (r *Registry) findRemoval(version, name string) *removal {
	entry := r.names[name]

	for _, rm := range r.removed {
		if rm.version != version {
			continue
		}

		if rm.kind == name || entry != nil && r.names[rm.kind] == entry {
			return rm
		}
	}

	return nil
}

// removedProblem returns the problem of a document that declares the
// version that rm removed.
func (r *Registry) removedProblem(doc *niceyaml.Node, rm *removal, hint bool) error {
	details := detailsOf(rm.details)

	if hint && r.cfg.migrateHint != "" {
		details = append(details, errors.New(r.cfg.migrateHint))
	}

	if rm.version == "" {
		return newProblem(ErrRemovedVersion, "removed unversioned layout of "+rm.kind,
			startOf(doc), niceyaml.WithDetails(details...))
	}

	return newProblem(ErrRemovedVersion, fmt.Sprintf("removed version %q of %s", rm.version, rm.kind),
		niceyaml.AtPath(r.cfg.versionField), niceyaml.WithDetails(details...))
}

// unknownKind returns the problem of a document whose kind field does not
// name a kind of the registry. The parameter version holds the version
// text that the document declares, or "" when it does not declare one.
func (r *Registry) unknownKind(name, version string) error {
	var details []error

	if near := r.closestKind(name); near != "" {
		details = append(details, fmt.Errorf("did you mean %q?", near))
	} else {
		names := make([]string, 0, len(r.kinds))
		for _, e := range r.kinds {
			names = append(names, e.kind.name)
		}

		details = append(details, fmt.Errorf("%s reads %s", r.app(), joinAnd(names)))
	}

	if version != "" && slices.ContainsFunc(r.kinds, func(e *kindEntry) bool { return e.byName[version] != nil }) {
		details = append(details, r.newerRelease("this kind"))
	}

	return newProblem(ErrUnknownKind, fmt.Sprintf("unknown kind %q", name),
		niceyaml.AtPath(r.cfg.kindField), niceyaml.WithDetails(details...))
}

// closestKind returns the name of a kind, registered or old, that name
// misspells, or "". It picks the name with the least edit distance from
// name, ignoring case. It returns "" when that distance is more than 2 and
// more than a third of the runes of name.
func (r *Registry) closestKind(name string) string {
	best, bestDist := "", 0

	names := make([]string, 0, len(r.names))
	for n := range r.names {
		names = append(names, n)
	}

	slices.Sort(names)

	for _, n := range names {
		d := distance(strings.ToLower(name), strings.ToLower(n))
		if best == "" || d < bestDist {
			best, bestDist = n, d
		}
	}

	limit := max(2, utf8.RuneCountInString(name)/3)
	if best == "" || bestDist > limit {
		return ""
	}

	return best
}

// distance returns the edit distance between a and b in runes.
func distance(a, b string) int {
	ar, br := []rune(a), []rune(b)
	prev := make([]int, len(br)+1)
	cur := make([]int, len(br)+1)

	for j := range prev {
		prev[j] = j
	}

	for i := 1; i <= len(ar); i++ {
		cur[0] = i

		for j := 1; j <= len(br); j++ {
			cost := 1
			if ar[i-1] == br[j-1] {
				cost = 0
			}

			cur[j] = min(prev[j]+1, cur[j-1]+1, prev[j-1]+cost)
		}

		prev, cur = cur, prev
	}

	return prev[len(br)]
}

// unknownVersion returns the problem of a document that declares version,
// which the kind of entry does not have. The parameter wrote holds the kind
// name that the document writes, or "" when the document does not have a
// kind field.
func (r *Registry) unknownVersion(entry *kindEntry, version, wrote string) error {
	var reads []string

	for _, v := range entry.versions {
		if v.removal == nil && !v.cfg.unversioned {
			reads = append(reads, r.versionLabel(v.cfg.name))
		}
	}

	details := []error{
		fmt.Errorf("%s reads %s", r.app(), joinAnd(reads)),
		r.newerRelease("this version"),
	}

	details = append(details, r.oldName(entry, wrote)...)

	return newProblem(ErrUnknownVersion, fmt.Sprintf("unknown version %q for %s", version, entry.kind.name),
		niceyaml.AtPath(r.cfg.versionField), niceyaml.WithDetails(details...))
}

// oldName returns the detail that names the old name wrote of the kind of
// entry, or nil when wrote is the registered name.
func (r *Registry) oldName(entry *kindEntry, wrote string) []error {
	if wrote == "" || wrote == entry.kind.name {
		return nil
	}

	for _, v := range slices.Backward(entry.versions) {
		if v.kindName == wrote {
			return []error{fmt.Errorf("%s calls %s %q", r.label(v), entry.kind.name, wrote)}
		}
	}

	return nil
}

// kindMismatch returns the problem of a document that pairs the version v
// with the kind name wrote, which v does not give.
func (r *Registry) kindMismatch(v *versionEntry, wrote string) error {
	kindField, versionField := fieldName(r.cfg.kindField), fieldName(r.cfg.versionField)

	details := []error{fmt.Errorf("%s calls it %q", r.label(v), v.kindName)}

	if other := gives(v.kind, wrote); other != nil {
		details = append(details, fmt.Errorf("set %s to %q, or set %s to %s, which calls it %q",
			kindField, v.kindName, versionField, other.cfg.name, wrote))
	} else {
		details = append(details, fmt.Errorf("set %s to %q", kindField, v.kindName))
	}

	return newProblem(ErrKindMismatch, fmt.Sprintf("%s has no kind %q", r.label(v), wrote),
		niceyaml.AtPath(r.cfg.kindField), niceyaml.WithDetails(details...))
}

// gives returns the version of entry that a document should declare when
// it writes the kind name name. Among the written version and the versions
// before it, it returns the newest version that gives name and that
// [WithRemoved] does not name. Without such a version, it returns the
// newest version that gives name. It returns nil when name is not the kind
// name of a version.
func gives(entry *kindEntry, name string) *versionEntry {
	var newest *versionEntry

	for _, v := range slices.Backward(entry.versions) {
		if v.kindName != name || v.cfg.unversioned {
			continue
		}

		if newest == nil {
			newest = v
		}

		if v.index <= entry.written.index && v.removal == nil {
			return v
		}
	}

	return newest
}

// missingTypeFields returns the problem of a document that lacks a type
// field. The flags version and kind report whether the document holds
// each field.
func (r *Registry) missingTypeFields(doc *niceyaml.Node, version, kind bool, details []error) error {
	vName, kName := fieldName(r.cfg.versionField), fieldName(r.cfg.kindField)

	var msg string

	switch {
	case r.cfg.versionOnly || kind:
		msg = "missing " + vName
	case version:
		msg = "missing " + kName
	default:
		msg = "missing " + vName + " and " + kName
	}

	return newProblem(ErrMissingTypeField, msg, startOf(doc), niceyaml.WithDetails(details...))
}

// typeFieldsText returns the type fields that the registry reads, as a
// detail names them: "apiVersion and kind", or "version".
func (r *Registry) typeFieldsText() string {
	if r.cfg.versionOnly {
		return fieldName(r.cfg.versionField)
	}

	return fieldName(r.cfg.versionField) + " and " + fieldName(r.cfg.kindField)
}
