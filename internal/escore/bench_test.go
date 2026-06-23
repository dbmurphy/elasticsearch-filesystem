package escore

import (
	"context"
	"fmt"
	"testing"
	"time"
)

// These benchmarks measure ESFS-side overhead (encoding, planning, streaming
// orchestration) against the in-memory fake, isolating the component the
// performance gates target separately from cluster latency. Run on a FUSE host
// the scripts/bench.sh harness adds end-to-end mount measurements.

func seedFake(n int) *Fake {
	f := NewFake()
	f.SetCaps("conversations", map[string]FieldCap{
		"body":       {Name: "body", Type: "text", Searchable: true},
		"@timestamp": {Name: "@timestamp", Type: "date"},
	})
	for i := 0; i < n; i++ {
		f.Seed("conversations", fmt.Sprintf("doc-%06d", i), `{"body":"refund please process my refund"}`)
	}
	return f
}

func BenchmarkListStream(b *testing.B) {
	f := seedFake(10000)
	ctx := context.Background()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		ch, errf := f.List(ctx, "conversations")
		cnt := 0
		for range ch {
			cnt++
		}
		if err := errf(); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkSearchStream(b *testing.B) {
	f := seedFake(10000)
	ctx := context.Background()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		res, err := f.Search(ctx, SearchRequest{Indices: []string{"conversations"}, Query: "refund"})
		if err != nil {
			b.Fatal(err)
		}
		for range res.Hits {
		}
		_ = res.Err()
	}
}

func BenchmarkPlanQuery(b *testing.B) {
	caps := FieldCaps{Index: "conversations", Fields: map[string]FieldCap{
		"body":       {Name: "body", Type: "text", Searchable: true},
		"@timestamp": {Name: "@timestamp", Type: "date"},
	}}
	req := SearchRequest{Indices: []string{"conversations"}, Query: "red shoes", Since: 7 * 24 * time.Hour}
	now := time.Now()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := planQuery(req, caps, now); err != nil {
			b.Fatal(err)
		}
	}
}
