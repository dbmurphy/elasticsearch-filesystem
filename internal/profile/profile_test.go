package profile

import (
	"context"
	"strings"
	"testing"

	"github.com/dbmurphy/elasticsearch-filesystem/internal/escore"
)

func TestMountProfileListsIndices(t *testing.T) {
	f := escore.NewFake()
	f.Seed("conversations", "a", `{}`)
	f.Seed("logs", "b", `{}`)
	g := New(f, escore.DefaultConfig())
	b, err := g.MountProfile(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	for _, want := range []string{"# ESFS", "conversations", "logs", "Write policy", "eval \"$(esfs env)\""} {
		if !strings.Contains(s, want) {
			t.Fatalf("mount profile missing %q:\n%s", want, s)
		}
	}
}

func TestIndexProfileSemanticAndDate(t *testing.T) {
	f := escore.NewFake()
	f.SetCaps("conversations", map[string]escore.FieldCap{
		"body":          {Name: "body", Type: "text", Searchable: true},
		"body_semantic": {Name: "body_semantic", Type: "semantic_text"},
		"@timestamp":    {Name: "@timestamp", Type: "date"},
	})
	f.Seed("conversations", "a", `{"body":"hello","secret":"x"}`)
	cfg := escore.DefaultConfig()
	cfg.RedactFields = []string{"secret"}
	g := New(f, cfg)
	b, err := g.IndexProfile(context.Background(), "conversations")
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	if !strings.Contains(s, "Mode: semantic") {
		t.Fatalf("expected Search section to lead with Mode:\n%s", s)
	}
	if !strings.Contains(s, "Semantic fields: body_semantic") {
		t.Fatalf("expected semantic field listing:\n%s", s)
	}
	if !strings.Contains(s, "@timestamp") {
		t.Fatalf("expected date field note:\n%s", s)
	}
	if !strings.Contains(s, "<redacted>") {
		t.Fatalf("expected redacted sample field:\n%s", s)
	}
	if !strings.Contains(s, "redacted fields: secret") {
		t.Fatalf("expected redaction disclosure:\n%s", s)
	}
}

func TestIndexProfileOptOut(t *testing.T) {
	f := escore.NewFake()
	f.Seed("conversations", "a", `{}`)
	cfg := escore.DefaultConfig()
	cfg.ProfileOptOut = []string{"conversations"}
	g := New(f, cfg)
	b, _ := g.IndexProfile(context.Background(), "conversations")
	if !strings.Contains(string(b), "disabled") {
		t.Fatalf("opt-out index should report disabled, got:\n%s", b)
	}
}
