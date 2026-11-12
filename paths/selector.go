package paths

import (
	"fmt"
	"iter"
	"strconv"
)

// SelectorKind identifies the kind of a [Selector]. The kinds start at 1,
// so the zero [Selector] does not match any kind and differs from a
// `.name` selector with the empty name.
type SelectorKind int

// [SelectorKind] constants.
const (
	// SelectorChild is the `.name` selector, which [Path.Child] appends.
	SelectorChild SelectorKind = iota + 1
	// SelectorIndex is the `[n]` selector, which [Path.Index] appends.
	SelectorIndex
	// SelectorChildAll is the `.*` selector, which [Path.ChildAll] appends.
	SelectorChildAll
	// SelectorIndexAll is the `[*]` selector, which [Path.IndexAll]
	// appends.
	SelectorIndexAll
	// SelectorRecursive is the `..name` selector, which [Path.Recursive]
	// appends.
	SelectorRecursive
	// SelectorRecursiveAll is the `..*` selector, which
	// [Path.RecursiveAll] appends.
	SelectorRecursiveAll
	// SelectorKey is the `~` selector, which [Path.Key] appends.
	SelectorKey
)

// String returns the name of the kind in kebab case: "child",
// "index", "child-all", "index-all", "recursive", "recursive-all", or
// "key". It returns "SelectorKind(n)" for any other value n, such as the
// kind of the zero [Selector].
func (k SelectorKind) String() string {
	switch k {
	case SelectorChild:
		return "child"
	case SelectorIndex:
		return "index"
	case SelectorChildAll:
		return "child-all"
	case SelectorIndexAll:
		return "index-all"
	case SelectorRecursive:
		return "recursive"
	case SelectorRecursiveAll:
		return "recursive-all"
	case SelectorKey:
		return "key"
	default:
		return "SelectorKind(" + strconv.Itoa(int(k)) + ")"
	}
}

// Selector is one selector of a [Path], as [Path.Selectors] and
// [Path.Last] give it. Kind says which selector it is. Name holds the
// name of a selector of kind [SelectorChild] or [SelectorRecursive], and
// Index holds the index of one of kind [SelectorIndex].
//
// Name is the text that the selector matches a key by. That text can
// differ from the way the source spells the key. A path that a caller
// builds or parses holds the names the caller wrote. The path $.ports.16
// thus holds the name 16 whether or not a key of the document spells it
// that way.
//
// A path that the library resolves holds the text that
// [Resolver.KeyName] gives each key. The path of a [Match] is such a
// path, and so is the path of a Node from
// [go.jacobcolvin.com/niceyaml.Node.Nodes]. That text is the source text
// of a scalar that is not a string, such as 0x10, True, or ~. For a
// string, it is the unquoted and unescaped text. For an alias key, it is
// the text of the content of its anchor.
//
// A JSON Pointer built from the names thus spells a key as the YAML
// source does. That spelling can differ from the key that the JSON form
// of the document holds. The key 0x10 gives /ports/0x10, but JSON holds
// the key 16. [go.jacobcolvin.com/niceyaml.AtData] locates an error by the
// names of decoded data instead, and
// [go.jacobcolvin.com/niceyaml.Node.DataLocator] maps those names to a path
// of the document.
type Selector struct {
	Name  string
	Index int
	Kind  SelectorKind
}

// String returns the selector in path expression syntax, as [Path.String]
// writes it, such as `.name`, `[0]`, or `~`. A name that Path.String
// quotes comes back in single quotes here too. A control character or a
// bidirectional embedding, override, or isolate in it comes back as an
// escape. A selector of an unknown kind, such as the zero Selector, gives
// the empty string.
func (s Selector) String() string {
	seg, ok := s.segment()
	if !ok {
		return ""
	}

	return seg.String()
}

// segment returns the internal form of s, and reports false for a
// selector of an unknown kind. It keeps Name and Index only for the kinds
// that hold them, so two selectors of a kind without a name or an index
// give equal segments.
func (s Selector) segment() (segment, bool) {
	switch s.Kind {
	case SelectorChild:
		return segment{kind: segmentChild, name: s.Name}, true
	case SelectorIndex:
		return segment{kind: segmentIndex, index: s.Index}, true
	case SelectorChildAll:
		return segment{kind: segmentChildAll}, true
	case SelectorIndexAll:
		return segment{kind: segmentIndexAll}, true
	case SelectorRecursive:
		return segment{kind: segmentRecursive, name: s.Name}, true
	case SelectorRecursiveAll:
		return segment{kind: segmentRecursiveAll}, true
	case SelectorKey:
		return segment{kind: segmentKey}, true
	default:
		return segment{}, false
	}
}

// selector returns the exported form of s. It sets Name and Index only
// for the kinds that hold them.
func (s segment) selector() Selector {
	switch s.kind {
	case segmentChild:
		return Selector{Kind: SelectorChild, Name: s.name}
	case segmentIndex:
		return Selector{Kind: SelectorIndex, Index: s.index}
	case segmentIndexAll:
		return Selector{Kind: SelectorIndexAll}
	case segmentRecursive:
		return Selector{Kind: SelectorRecursive, Name: s.name}
	case segmentKey:
		return Selector{Kind: SelectorKey}
	case segmentChildAll:
		return Selector{Kind: SelectorChildAll}
	case segmentRecursiveAll:
		return Selector{Kind: SelectorRecursiveAll}
	default:
		return Selector{}
	}
}

// Selectors returns the selectors of the path, in order from where it
// starts. The `$` or `@` that the path starts at is not a selector, so
// Selectors leaves it out. A path with no selectors, such as [Doc] or
// [Current], yields an empty sequence. A caller that needs a form other
// than the path expression can read the selectors one by one, for
// example to build a JSON Pointer:
//
//	escape := strings.NewReplacer("~", "~0", "/", "~1")
//
//	var ptr strings.Builder
//	for sel := range p.Selectors() {
//		switch sel.Kind {
//		case paths.SelectorChild:
//			ptr.WriteString("/" + escape.Replace(sel.Name))
//		case paths.SelectorIndex:
//			ptr.WriteString("/" + strconv.Itoa(sel.Index))
//		case paths.SelectorKey:
//			// A pointer names the entry, key and value alike.
//		default:
//			return "", fmt.Errorf("no JSON Pointer for %s", sel)
//		}
//	}
//
// The names are the text each selector matches a key by, as [Selector]
// describes.
func (p Path) Selectors() iter.Seq[Selector] {
	return func(yield func(Selector) bool) {
		for _, seg := range p.segments {
			if !yield(seg.selector()) {
				return
			}
		}
	}
}

// Append returns a copy of the path with sels appended, in order. It
// undoes [Path.Selectors], so a caller can change some selectors of a path
// and build the path again. The copy starts where the path does:
//
//	sels := slices.Collect(p.Selectors()) // $.items[3].name
//	for i, sel := range sels {
//		if sel.Kind == paths.SelectorIndex {
//			sels[i] = paths.Selector{Kind: paths.SelectorIndexAll}
//		}
//	}
//
//	p.Prefix(0).Append(sels...) // $.items[*].name
//
// Append reads Name only for the kinds [SelectorChild] and
// [SelectorRecursive], and Index only for the kind [SelectorIndex]. It
// ignores them for the other kinds, so the result equals the path that
// the matching builder, such as [Path.IndexAll], gives.
//
// Panics on a selector of an unknown kind, such as the zero [Selector].
// Also panics on a selector of kind SelectorIndex with a negative Index,
// as [Path.Index] does. A caller that builds selectors from untrusted input
// should check the kind and the index before it calls Append.
func (p Path) Append(sels ...Selector) Path {
	segs := make([]segment, 0, len(sels))

	for _, sel := range sels {
		seg, ok := sel.segment()
		if !ok {
			panic(fmt.Sprintf("paths.Path.Append: unknown selector kind %s", sel.Kind))
		}

		if seg.kind == segmentIndex && seg.index < 0 {
			panic(fmt.Sprintf("paths.Path.Append: index %d is below 0", seg.index))
		}

		segs = append(segs, seg)
	}

	return p.extend(segs...)
}

// Last returns the last selector of the path and true, or the zero
// [Selector] and false for a path with no selectors, such as [Doc] or
// [Current]. In the path of a match of a `.*` or `[*]` selector, the last
// selector names the entry or the element that the match is. It thus
// gives the key name of a job or the index of an item:
//
//	for _, job := range jobs { // $.jobs.*
//		sel, _ := job.Path().Last()
//		fmt.Println(sel.Name) // build, test, ...
//	}
//
// For a path that ends in the `~` selector from [Path.Key], Last gives
// that selector, of kind [SelectorKey]. For the path of a key or of a
// value, the last selector of [Path.Value] names the entry.
func (p Path) Last() (Selector, bool) {
	if len(p.segments) == 0 {
		return Selector{}, false
	}

	return p.segments[len(p.segments)-1].selector(), true
}

// Len returns the number of selectors the path holds, which is 0 for [Doc]
// and [Current]. The `$` or `@` that a path starts at is not a selector.
func (p Path) Len() int {
	return len(p.segments)
}
