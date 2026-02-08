// Package normalizer provides composable Unicode normalization for text search.
//
// Matching user input against YAML content is surprisingly hard. A search for
// "cafe" should find "Café", and "uber" should match "ÜBER". Unicode makes
// this non-trivial: diacritics are combining marks that survive simple byte
// comparison, case folding differs from lowercasing for many scripts, and
// full-width characters occupy different code points than their ASCII
// counterparts.
//
// A [Normalizer] solves this by chaining Unicode transformations into a
// pipeline that is built once at construction time. Transformations run in a
// fixed order: width folding, diacritics removal, case folding, then any
// custom transformers. This ensures results are deterministic regardless of
// option order.
//
// By default, [New] removes diacritics and case-folds, which covers the
// most common search needs:
//
//	n := normalizer.New()
//	n.Normalize("Café") // "cafe"
//
// [Option] values toggle individual pipeline stages or append custom
// [transform.Transformer] implementations via [WithTransformer].
package normalizer
