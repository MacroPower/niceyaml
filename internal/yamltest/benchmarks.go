package yamltest

import "testing"

// ReportTimePer reports the time that each unit of work takes, as the
// metric "ns/<unit>", for a benchmark that does count units in each
// iteration. The benchmark calls it after its loop:
//
//	for b.Loop() {
//		decodeAll(docs)
//	}
//
//	yamltest.ReportTimePer(b, len(docs), "doc")
func ReportTimePer(b *testing.B, count int, unit string) {
	b.Helper()

	b.ReportMetric(float64(b.Elapsed().Nanoseconds())/float64(b.N*count), "ns/"+unit)
}
