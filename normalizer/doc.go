// Package normalizer provides composable Unicode normalization for text search.
//
// A search for "cafe" should find "Café", and "uber" should match "ÜBER".
// Three Unicode properties defeat a byte comparison. Diacritics are
// combining marks that stay in the text. Case folding differs from
// lowercasing for many scripts. Fullwidth characters occupy different code
// points than their ASCII counterparts.
//
// A [Normalizer] handles all three by chaining Unicode transformations into
// a pipeline that [New] defines once at construction time. Transformations
// run in a fixed order: width folding, diacritics removal, case folding, then
// any custom transformers. Option order does not change that sequence.
//
// By default, the pipeline removes diacritics and case-folds text:
//
//	n := normalizer.New()
//	n.Normalize("Café") // "cafe"
//
// [Option] values toggle individual pipeline stages or append custom
// [transform.Transformer] implementations with [WithTransformer].
package normalizer
