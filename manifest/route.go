package manifest

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/goccy/go-yaml/ast"
	"github.com/goccy/go-yaml/token"

	"go.jacobcolvin.com/niceyaml"
	"go.jacobcolvin.com/niceyaml/internal/astnode"
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
	err  error
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

// readField reads the scalar at p, a path of names and indexes, in the
// document doc. A path that passes through an alias or a `<<` merge key
// fails at that alias or merge key. The read cannot follow an alias into a
// reference document, and a rewrite of the field reaches every value that
// shares the anchor. A null reads as a field that the document leaves out.
func readField(doc *niceyaml.Node, p paths.Path) fieldRead {
	res := doc.Resolver()
	name := fieldName(p)
	cur := doc.AST()

	for sel := range p.Selectors() {
		if alias, ok := astnode.Content(cur).(*ast.AliasNode); ok {
			return fieldRead{err: aliasProblem(p, name, alias)}
		}

		content := astnode.Content(cur)

		switch sel.Kind {
		case paths.SelectorChild:
			entry, merge := findEntry(res, content, sel.Name)

			switch {
			case entry != nil:
				cur = entry.Value
			case merge != nil:
				return fieldRead{err: mergeProblem(p, name, astnode.KeyToken(merge))}
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

	content := astnode.Content(cur)
	if astnode.IsNil(content) || content.Type() == ast.NullType {
		return fieldRead{}
	}

	tk := astnode.Token(content)
	if !astnode.IsScalar(content) || tk == nil {
		return fieldRead{err: newProblem(ErrMissingTypeField, name+" must be text", niceyaml.AtPath(p))}
	}

	return fieldRead{text: tk.Value, found: true}
}

// findEntry returns the entry of the mapping node whose key reads name, the
// later one when the mapping writes the key twice. Without such an entry,
// it returns the `<<` merge entry that brings the key in, or nil.
func findEntry(res *paths.Resolver, node ast.Node, name string) (*ast.MappingValueNode, *ast.MappingValueNode) {
	entries, ok := astnode.Entries(node)
	if !ok {
		return nil, nil
	}

	var hit, merge *ast.MappingValueNode

	for _, e := range entries {
		if astnode.IsMergeKey(e.Key) {
			if merge == nil && mergeHolds(res, e, name, 0) {
				merge = e
			}

			continue
		}

		if key, ok := res.KeyName(e.Key); ok && key == name {
			hit = e
		}
	}

	return hit, merge
}

// mergeHolds reports whether a mapping that the merge entry e brings in
// holds the key name, itself or through a merge key of its own.
func mergeHolds(res *paths.Resolver, e *ast.MappingValueNode, name string, depth int) bool {
	if depth > maxMergeDepth {
		return false
	}

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
				if mergeHolds(res, inner, name, depth+1) {
					return true
				}

				continue
			}

			if key, ok := res.KeyName(inner.Key); ok && key == name {
				return true
			}
		}
	}

	return false
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
