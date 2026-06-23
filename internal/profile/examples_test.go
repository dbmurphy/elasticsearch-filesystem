package profile

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/dbmurphy/elasticsearch-filesystem/internal/escore"
)

// TestGenerateExamples renders real profile.md output into the repo's examples/
// directory. It is skipped during normal test runs and executed deliberately to
// (re)generate documentation examples:
//
//	ESFS_WRITE_EXAMPLES=1 go test ./internal/profile/ -run TestGenerateExamples
//
// Using the real generator guarantees the committed examples never drift from
// actual output.
func TestGenerateExamples(t *testing.T) {
	if os.Getenv("ESFS_WRITE_EXAMPLES") != "1" {
		t.Skip("set ESFS_WRITE_EXAMPLES=1 to regenerate examples/")
	}
	f := escore.NewFake()
	f.SetCaps("conversations", map[string]escore.FieldCap{
		"subject":        {Name: "subject", Type: "text", Searchable: true},
		"body":           {Name: "body", Type: "text", Searchable: true},
		"body_semantic":  {Name: "body_semantic", Type: "semantic_text"},
		"status":         {Name: "status", Type: "keyword", Searchable: true},
		"@timestamp":     {Name: "@timestamp", Type: "date"},
		"customer_email": {Name: "customer_email", Type: "keyword", Searchable: true},
	})
	f.Seed("conversations", "r1", `{"subject":"refund request","body":"please process my refund","status":"open","customer_email":"a@example.com","@timestamp":"2026-06-23T10:00:00Z"}`)
	f.Seed("orders", "o-1001", `{"item":"red shoes","total":59.99,"@timestamp":"2026-06-22T09:00:00Z"}`)

	cfg := escore.DefaultConfig()
	cfg.RedactFields = []string{"customer_email"}
	cfg.Indices = map[string]escore.IndexSettings{
		"conversations": {
			SearchFields:   []string{"subject", "body"},
			SemanticFields: []string{"body_semantic"},
			TimeField:      strptr("@timestamp"),
		},
	}
	g := New(f, cfg)
	ctx := context.Background()

	mount, err := g.MountProfile(ctx)
	if err != nil {
		t.Fatal(err)
	}
	idx, err := g.IndexProfile(ctx, "conversations")
	if err != nil {
		t.Fatal(err)
	}

	dir := repoExamplesDir(t)
	write(t, filepath.Join(dir, "profile.mount.md"), mount)
	write(t, filepath.Join(dir, "profile.index.md"), idx)
}

func strptr(s string) *string { return &s }

func write(t *testing.T, path string, b []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, b, 0o644); err != nil {
		t.Fatal(err)
	}
	t.Logf("wrote %s (%d bytes)", path, len(b))
}

func repoExamplesDir(t *testing.T) string {
	t.Helper()
	// internal/profile -> repo root is two levels up.
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	return filepath.Join(wd, "..", "..", "examples")
}
