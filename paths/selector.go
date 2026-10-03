package paths

import "iter"

// SelectorKind identifies the kind of a [Selector]. The kinds start at 1,
// so the zero [Selector] matches no kind and reads apart from a `.name`
// selector with the empty name.
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

// Selector is one selector of a [Path], as [Path.Selectors] and
// [Path.Last] give it. Kind says which selector it is. Name is set for
// [SelectorChild] and [SelectorRecursive], and Index for [SelectorIndex].
//
// Name is the text the selector matches a key by, which can differ from
// the way the source spells the key. A path the library resolves, such as
// the path of a [Match] or of a Node from
// [go.jacobcolvin.com/niceyaml.Node.Nodes], holds the text
// [Resolver.KeyName] gives each key. That is the source text of a scalar
// that is not a string, such as 0x10, True, or ~, the unquoted and
// unescaped text of a string, and the text of the anchor's content for an
// alias key. A path a caller builds or parses holds the names the caller
// wrote, so the path $.ports.16 holds the name 16 whether or not a key of
// the document spells it that way.
//
// A JSON Pointer built from the names thus spells a key as the YAML
// source does, which can differ from the key the JSON form of the
// document holds. The key 0x10 gives /ports/0x10, where JSON holds the
// key 16.
type Selector struct {
	Name  string
	Index int
	Kind  SelectorKind
}

// String returns the selector in path expression syntax, as [Path.String]
// writes it, such as `.name`, `[0]`, or `~`. A name that holds a reserved
// character, `:`, or whitespace comes back in single quotes. A selector of
// no known kind, such as the zero Selector, gives the empty string.
func (s Selector) String() string {
	seg, ok := s.segment()
	if !ok {
		return ""
	}

	return seg.String()
}

// segment returns the internal form of s, and reports false for a
// selector of no known kind.
func (s Selector) segment() (segment, bool) {
	seg := segment{name: s.Name, index: s.Index}

	switch s.Kind {
	case SelectorChild:
		seg.kind = segmentChild
	case SelectorIndex:
		seg.kind = segmentIndex
	case SelectorChildAll:
		seg.kind = segmentChildAll
	case SelectorIndexAll:
		seg.kind = segmentIndexAll
	case SelectorRecursive:
		seg.kind = segmentRecursive
	case SelectorRecursiveAll:
		seg.kind = segmentRecursiveAll
	case SelectorKey:
		seg.kind = segmentKey
	default:
		return segment{}, false
	}

	return seg, true
}

// selector returns the exported form of s. It leaves out the name and the
// index of a kind that holds neither.
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
// starts, and leaves out the `$` or `@` it starts at, which is no
// selector. A path with no selectors, such as [Doc] or [Current], yields
// none. A caller that needs a form other than the path
// expression reads the selectors one by one, such as to build a JSON
// Pointer:
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

// Last returns the last selector of the path and true, or the zero
// [Selector] and false for a path with no selectors, such as [Doc] or
// [Current]. The last selector
// of a match of a `.*` or `[*]` selector names the entry or the element
// the match is, so it gives the key name of a job or the index of an
// item:
//
//	for _, job := range jobs { // $.jobs.*
//		sel, _ := job.Path().Last()
//		fmt.Println(sel.Name) // build, test, ...
//	}
//
// A path that ends in the `~` selector from [Path.Key] gives that
// selector, of kind [SelectorKey], and the name of its entry is the last
// selector of [Path.Parent].
func (p Path) Last() (Selector, bool) {
	if len(p.segments) == 0 {
		return Selector{}, false
	}

	return p.segments[len(p.segments)-1].selector(), true
}

// Len returns the number of selectors the path holds, which is 0 for [Doc]
// and [Current]. The `$` or `@` a path starts at is no selector.
func (p Path) Len() int {
	return len(p.segments)
}
