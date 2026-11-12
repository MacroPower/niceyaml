package niceyaml

import (
	"go.jacobcolvin.com/niceyaml/internal/excerpt"
	"go.jacobcolvin.com/niceyaml/paths"
	"go.jacobcolvin.com/niceyaml/position"
)

// Located is where an error resolved in a [Source] that
// [NewSourceFromDerived] built: the path its message shows, and the range
// of the text it marks. The locate function of that constructor takes one
// and returns the [Place] of the error in the home document.
type Located struct {
	// Path is the path of the error, from `$`, when HasPath is set.
	Path paths.Path
	// Range is the range of the derived text that an excerpt of the error
	// marks, in the coordinates of [Source.Lines] of the derived Source,
	// when HasRange is set. It spans every range that the error marks,
	// from the start of the first to the end of the last. The range of an
	// error that marks nothing is empty at the position of the error.
	Range position.Range
	// HasPath reports whether Path holds the path of the error. It is
	// false for an error without a path, such as a refusal of the parser.
	HasPath bool
	// HasRange is false for an error that did not resolve a location, and
	// for one whose location lies outside the derived text.
	HasRange bool
}

// Place is where an error that resolved in a [Source] that
// [NewSourceFromDerived] built binds in the home document. The locate
// function of that constructor returns one for each [Located].
type Place struct {
	// Details holds the errors that the binding gains as details. A detail
	// with a location binds in the home document.
	Details []error
	// Path is the path that the message shows in place of the path of
	// [Located], when HasPath is set. An error without a path shows it too.
	Path paths.Path
	// Range is the range of the text that the error marks in the home
	// document, when HasRange is set. Without it, the error binds to the
	// home document without a position.
	Range position.Range
	// HasPath reports whether the message shows Path. Without it, the
	// message shows the path of [Located].
	HasPath bool
	// HasRange reports whether the error binds at Range.
	HasRange bool
}

// NewSourceFromDerived creates a new [*Source] of text, a text that a
// program derived from home, a document of another Source. A loader that
// expands environment variables, includes, or templates before it parses
// derives such a text. Every error bound in the Source binds in home
// instead, at the place that locate gives for the location that the error
// resolved in text. A message, an excerpt, and [ErrorTree.Name] thus show
// the document that the user wrote. A nil locate keeps each location as
// it is, for a text that keeps every line and column of home. Home can be
// a document of a derived Source too. An error then binds in home, and it
// moves on from there to the home of that Source, until it reaches a
// Source that no program derived.
//
// The Source starts from the settings of the Source of home, its name,
// its file path, its references, its unmarshalers, and its limits among
// them. It applies opts over them, as [Source.WithText] does. The
// unmarshalers of a [WithUnmarshalers] in opts thus apply over those of
// home. A function of opts for a type replaces one of home, and a nil
// function removes it. A program that puts defaults of its own under
// those of home passes both lists in that order:
//
//	niceyaml.WithUnmarshalers(slices.Concat(defaults, doc.Source().Unmarshalers())...)
//
// The limits stay those of home, as [Source.ByteLimit],
// [Source.DocumentByteLimit], and [Source.DepthLimit] report them. A text that
// grows past them fails as the text of home fails. [WithoutLimits] turns
// them off for a copy that passed them already. Such a Source keeps the
// bounds that the limits of home put on its aliases, because the text
// holds the aliases of home.
//
// The example expands environment variables before the parse and binds
// each error at the range that a map of the expansion gives:
//
//	home := niceyaml.NewSourceFromString(text, niceyaml.WithName(path))
//	doc, err := home.Document()
//	if err != nil {
//		return err
//	}
//
//	work := niceyaml.NewSourceFromDerived(expanded, doc, func(at niceyaml.Located) niceyaml.Place {
//		return niceyaml.Place{Range: mapBack(at.Range), HasRange: at.HasRange, Path: at.Path, HasPath: at.HasPath}
//	})
//	cfg, err := work.Decode[Config](ctx)
//
// A nil home gives a Source of text with opts alone, which binds its
// errors in itself.
func NewSourceFromDerived(text string, home *Node, locate func(Located) Place, opts ...SourceOption) *Source {
	if home == nil {
		return NewSourceFromString(text, opts...)
	}

	if locate == nil {
		locate = samePlace
	}

	cfg := home.source.pinned()
	cfg.aliasBudget = home.doc.state.Budget()

	for _, opt := range opts {
		opt(&cfg)
	}

	src := newStringSource(text, cfg)
	src.relocation = &relocation{home: home, locate: locate}

	return src
}

// samePlace returns the place of at in a home document whose lines and
// columns the derived text keeps.
func samePlace(at Located) Place {
	return Place{Path: at.Path, HasPath: at.HasPath, Range: at.Range, HasRange: at.HasRange}
}

// relocation sends each error bound in a derived [Source] to the document
// that the derived text stands for.
type relocation struct {
	// The document that the derived text stands for. Each error binds
	// there.
	home *Node
	// The function that gives the place in home of a location in the
	// derived text.
	locate func(Located) Place
}

// relocate moves e from a derived Source to the home document of r. In
// the derived Source, e binds at its location, under path when hasPath
// is true. It returns the path that e shows in the home document, whether
// e shows one, and the details that the place of the location adds.
func (e *SourceError) relocate(r *relocation, path paths.Path, hasPath bool) (paths.Path, bool, []error) {
	var at Located

	if hasPath {
		at.Path, at.HasPath = path, true
	}

	if e.locErr == nil && checkInRange(e.loc, e.source.lines) == nil {
		at.Range, at.HasRange = excerpt.Cover(e.loc.highlight(e.source.lines), e.loc.pos), true
	}

	place := r.locate(at)

	e.source, e.node = r.home.source, r.home

	switch {
	case place.HasRange:
		rng := place.Range
		e.loc, e.locErr = location{pos: rng.Start, rng: &rng}, nil

	case e.locErr == nil:
		e.loc, e.locErr = location{}, errUnlocated
	}

	// The path that the message shows names the value as the home
	// document spells it. The place can give a path to an error without
	// one, such as a refusal of the parser at a byte that an edit wrote.
	if place.HasPath {
		e.err = relocated(e.err, AtPath(place.Path))
		path, hasPath = place.Path, true
	}

	return path, hasPath, place.Details
}

// relocatedDetails holds the details that the place of one relocation of
// an error adds, with the relocation whose home document binds them.
type relocatedDetails struct {
	r       *relocation
	details []error
}

// relocateAll moves e from a derived Source to the home document of its
// relocation, as [SourceError.relocate] does. A home document can belong
// to a derived Source too. Then e moves on to the home of that Source, and
// on from there, until it binds in a Source without a relocation. In the
// first Source, e binds under path when hasPath is true. RelocateAll
// returns the details that the place of each relocation adds, in the
// order of the relocations.
func (e *SourceError) relocateAll(path paths.Path, hasPath bool) []relocatedDetails {
	var all []relocatedDetails

	for r := e.source.relocation; r != nil; r = e.source.relocation {
		var details []error

		path, hasPath, details = e.relocate(r, path, hasPath)
		if len(details) > 0 {
			all = append(all, relocatedDetails{r: r, details: details})
		}
	}

	return all
}

// addRelocatedDetails binds each detail of all in the home document of
// its relocation and adds it to e as a detail.
func (e *SourceError) addRelocatedDetails(all []relocatedDetails) {
	for _, rd := range all {
		for _, d := range rd.details {
			e.addChild(d, binder{src: rd.r.home.source, node: rd.r.home, locate: true}, childBase{}, true)
		}
	}
}
