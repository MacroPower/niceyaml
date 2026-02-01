package registry

import (
	"context"
	"errors"
	"path/filepath"
	"sync"

	"go.jacobcolvin.com/niceyaml"
	"go.jacobcolvin.com/niceyaml/schema"
	"go.jacobcolvin.com/niceyaml/schema/loader"
)

var (
	// ErrNoDirective indicates no schema directive was found in the document.
	ErrNoDirective = errors.New("no schema directive")

	// ErrNoFilePath indicates a document has no file path, which is required
	// for resolving relative schema paths in directives.
	ErrNoFilePath = errors.New("document has no file path")
)

// directiveMatchLoader matches and loads schemas from yaml-language-server
// directives.
type directiveMatchLoader struct {
	cachedDoc       *niceyaml.DocumentDecoder
	cachedDirective *schema.Directive
	opts            []loader.HTTPOption
	mu              sync.Mutex
}

// Directive creates a new [MatchLoader] for yaml-language-server schema
// directives.
//
// Match parses the document's tokens and caches the result, which Load then
// reuses if called for the same document. This avoids duplicate parsing when
// used with [Registry], which calls Match followed by Load.
//
//	reg.Register(registry.Directive())
func Directive(opts ...loader.HTTPOption) MatchLoader {
	return &directiveMatchLoader{opts: opts}
}

// Match implements [matcher.Matcher].
func (m *directiveMatchLoader) Match(_ context.Context, doc *niceyaml.DocumentDecoder) bool {
	directive := m.parseAndCache(doc)

	return directive != nil
}

// Load implements [loader.Loader].
func (m *directiveMatchLoader) Load(ctx context.Context, doc *niceyaml.DocumentDecoder) (loader.Result, error) {
	directive := m.parseAndCache(doc)
	if directive == nil {
		return loader.Result{}, ErrNoDirective
	}

	filePath := doc.FilePath()
	if filePath == "" {
		return loader.Result{}, ErrNoFilePath
	}

	baseDir := filepath.Dir(filePath)

	//nolint:wrapcheck // Loader errors already wrapped with context.
	return loader.Ref(baseDir, directive.Schema, m.opts...).Load(ctx, doc)
}

// parseAndCache parses the directive from doc, caching the result for reuse.
// Returns the cached directive if doc matches the previously cached document.
func (m *directiveMatchLoader) parseAndCache(doc *niceyaml.DocumentDecoder) *schema.Directive {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Return cached result if same document. This uses pointer identity because
	// Registry always passes the same DocumentDecoder instance from Match to Load.
	// Decoder.Documents() yields by reference, so the same pointer is used
	// throughout the validation of a single document.
	if m.cachedDoc == doc {
		return m.cachedDirective
	}

	// Parse and cache.
	tokens := doc.Tokens()
	if tokens == nil {
		m.cachedDoc = doc
		m.cachedDirective = nil

		return nil
	}

	directive := schema.ParseDocumentDirective(tokens)
	m.cachedDoc = doc
	m.cachedDirective = directive

	return directive
}
