package contract

import "testing"

func BenchmarkEncodeDecodeSimple(b *testing.B) {
	for i := 0; i < b.N; i++ {
		name, _ := EncodeID("doc-000123")
		if _, _, err := DecodeID(name); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkEncodeDecodeEscaped(b *testing.B) {
	for i := 0; i < b.N; i++ {
		name, _ := EncodeID("with/slash and space:colon")
		if _, _, err := DecodeID(name); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkParsePath(b *testing.B) {
	for i := 0; i < b.N; i++ {
		_ = ParsePath("conversations/doc-000123")
	}
}
