package contract

import (
	"strings"
	"testing"
)

func TestEncodeDecodeRoundTrip(t *testing.T) {
	cases := []string{
		"simple",
		"abc-123_def.456~",
		"UPPER",
		"with space",
		"with/slash",
		"emoji-😀",
		"tab\tinside",
		"@starts-with-at",
		"profile.md",    // reserved -> must be encoded
		".mapping.json", // reserved -> must be encoded
		"newline\nhere",
		"percent%sign",
	}
	for _, id := range cases {
		name, hashed := EncodeID(id)
		if hashed {
			t.Fatalf("unexpected hashed encoding for %q", id)
		}
		if len(name) > MaxNameLen {
			t.Fatalf("encoded name too long for %q", id)
		}
		got, gotHashed, err := DecodeID(name)
		if err != nil {
			t.Fatalf("decode %q (name %q): %v", id, name, err)
		}
		if gotHashed {
			t.Fatalf("decode reported hashed for %q", id)
		}
		if got != id {
			t.Fatalf("round-trip mismatch: id=%q name=%q got=%q", id, name, got)
		}
	}
}

func TestSimpleIDsStaySimple(t *testing.T) {
	for _, id := range []string{"simple", "doc_1", "a.b-c~d", "2026"} {
		name, hashed := EncodeID(id)
		if hashed || name != id {
			t.Fatalf("expected %q verbatim, got %q (hashed=%v)", id, name, hashed)
		}
	}
}

func TestReservedNamesEncoded(t *testing.T) {
	for name := range ReservedNames {
		enc, _ := EncodeID(name)
		if enc == name {
			t.Fatalf("reserved name %q must be encoded, stayed verbatim", name)
		}
		if !strings.HasPrefix(enc, "@e=") {
			t.Fatalf("reserved name %q expected reversible encoding, got %q", name, enc)
		}
	}
}

func TestLongIDHashed(t *testing.T) {
	id := strings.Repeat("x", 5000)
	name, hashed := EncodeID(id)
	if !hashed {
		t.Fatalf("expected hashed encoding for very long id")
	}
	if len(name) > MaxNameLen {
		t.Fatalf("hashed name too long: %d", len(name))
	}
	if !IsHashedName(name) {
		t.Fatalf("IsHashedName false for %q", name)
	}
	// Deterministic.
	name2, _ := EncodeID(id)
	if name != name2 {
		t.Fatalf("hashed name not deterministic")
	}
	_, gotHashed, err := DecodeID(name)
	if err != nil || !gotHashed {
		t.Fatalf("decode of hashed name should report hashed without error, got hashed=%v err=%v", gotHashed, err)
	}
}
