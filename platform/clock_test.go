package platform

import "testing"

var clockSink int64

func BenchmarkNow(b *testing.B) {
	b.ReportAllocs()
	var n int64
	for b.Loop() {
		n = Now()
	}
	clockSink = n
}
