// Package normalizer provides composable Unicode normalization for text search.
//
// A search for "cafe" should find "Café", and "uber" should match "ÜBER".
// Three Unicode properties defeat a byte comparison: diacritics are combining
// marks that survive it, case folding differs from lowercasing for many
// scripts, and fullwidth characters occupy different code points than their
// ASCII counterparts.
//
// A [Normalizer] solves this by chaining Unicode transformations into a
// pipeline that [New] defines once at construction time. Transformations run
// in a fixed order: width folding, diacritics removal, case folding, then any
// custom transformers. Option order does not change that sequence.
//
// By default, [New] removes diacritics and case-folds:
//
//	n := normalizer.New()
//	n.Normalize("Café") // "cafe"
//
// [Option] values toggle individual pipeline stages or append custom
// [transform.Transformer] implementations via [WithTransformer].
package normalizer
