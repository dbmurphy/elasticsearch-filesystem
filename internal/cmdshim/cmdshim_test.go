package cmdshim

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/dbmurphy/elasticsearch-filesystem/internal/contract"
	"github.com/dbmurphy/elasticsearch-filesystem/internal/escore"
	"github.com/dbmurphy/elasticsearch-filesystem/internal/routing"
)

func newDeps(t *testing.T, cwd string) (*Deps, *escore.Fake, *bytes.Buffer, *bytes.Buffer, *[]string) {
	t.Helper()
	t.Setenv(routing.EnvMount, "/esfs")
	f := escore.NewFake()
	var out, errb bytes.Buffer
	var passthrough []string
	d := &Deps{
		Core:   f,
		Cwd:    cwd,
		Stdout: &out,
		Stderr: &errb,
		ExecReal: func(tool string, args []string) int {
			passthrough = append(passthrough, tool+" "+strings.Join(args, " "))
			return 0
		},
	}
	return d, f, &out, &errb, &passthrough
}

func TestRoutedLsRoot(t *testing.T) {
	d, f, out, _, _ := newDeps(t, "/esfs")
	f.Seed("conversations", "a", `{}`)
	f.Seed("logs", "b", `{}`)
	if rc := d.RunLs(context.Background(), nil); rc != 0 {
		t.Fatalf("rc=%d", rc)
	}
	s := out.String()
	for _, want := range []string{"profile.md", ".sync.json", "conversations", "logs"} {
		if !strings.Contains(s, want) {
			t.Fatalf("ls root missing %q in:\n%s", want, s)
		}
	}
}

func TestRoutedLsIndex(t *testing.T) {
	d, f, out, _, _ := newDeps(t, "/esfs/conversations")
	f.Seed("conversations", "doc-1", `{}`)
	if rc := d.RunLs(context.Background(), nil); rc != 0 {
		t.Fatalf("rc=%d", rc)
	}
	name, _ := contract.EncodeID("doc-1")
	if !strings.Contains(out.String(), name) {
		t.Fatalf("ls index missing %q in:\n%s", name, out.String())
	}
}

func TestRoutedFindIndex(t *testing.T) {
	d, f, out, _, _ := newDeps(t, "/esfs")
	f.Seed("conversations", "a", `{}`)
	f.Seed("conversations", "b", `{}`)
	if rc := d.RunFind(context.Background(), []string{"/esfs/conversations"}); rc != 0 {
		t.Fatalf("rc=%d", rc)
	}
	s := out.String()
	if !strings.Contains(s, "/esfs/conversations\n") {
		t.Fatalf("find should print the index dir:\n%s", s)
	}
	an, _ := contract.EncodeID("a")
	if !strings.Contains(s, "/esfs/conversations/"+an) {
		t.Fatalf("find should print doc path:\n%s", s)
	}
}

func TestRoutedGrepMatch(t *testing.T) {
	d, f, out, errb, _ := newDeps(t, "/esfs/conversations")
	f.Seed("conversations", "a", `{"body":"please refund"}`)
	f.Seed("conversations", "b", `{"body":"hello"}`)
	rc := d.RunGrep(context.Background(), []string{"refund"})
	if rc != 0 {
		t.Fatalf("expected match rc=0, got %d (err=%s)", rc, errb.String())
	}
	an, _ := contract.EncodeID("a")
	if !strings.Contains(out.String(), "/esfs/conversations/"+an) {
		t.Fatalf("grep should report matching doc path:\n%s", out.String())
	}
	if !strings.Contains(errb.String(), "mode=") {
		t.Fatalf("grep should disclose mode on stderr:\n%s", errb.String())
	}
}

func TestRoutedGrepNoMatch(t *testing.T) {
	d, f, _, _, _ := newDeps(t, "/esfs/conversations")
	f.Seed("conversations", "a", `{"body":"hello"}`)
	rc := d.RunGrep(context.Background(), []string{"zzz-nomatch"})
	if rc != 1 {
		t.Fatalf("expected no-match rc=1, got %d", rc)
	}
}

func TestRoutedGrepPathsOnly(t *testing.T) {
	d, f, out, _, _ := newDeps(t, "/esfs/conversations")
	f.Seed("conversations", "a", `{"body":"refund"}`)
	rc := d.RunGrep(context.Background(), []string{"-l", "refund"})
	if rc != 0 {
		t.Fatalf("rc=%d", rc)
	}
	an, _ := contract.EncodeID("a")
	line := strings.TrimSpace(strings.Split(out.String(), "\n")[0])
	if line != "/esfs/conversations/"+an {
		t.Fatalf("paths-only should print bare path, got %q", line)
	}
}

func TestGrepPassthroughNonESFS(t *testing.T) {
	d, _, _, _, pass := newDeps(t, "/tmp/not-esfs")
	rc := d.RunGrep(context.Background(), []string{"foo", "/tmp/not-esfs/file"})
	if rc != 0 {
		t.Fatalf("rc=%d", rc)
	}
	if len(*pass) != 1 || !strings.HasPrefix((*pass)[0], "grep ") {
		t.Fatalf("expected passthrough to real grep, got %v", *pass)
	}
}

func TestGrepMixedScopeRejected(t *testing.T) {
	d, f, _, errb, _ := newDeps(t, "/esfs/conversations")
	f.Seed("conversations", "a", `{"body":"refund"}`)
	rc := d.RunGrep(context.Background(), []string{"refund", "/esfs/conversations", "/tmp/plain"})
	if rc != 2 {
		t.Fatalf("expected rc=2 for mixed scope, got %d", rc)
	}
	if !strings.Contains(errb.String(), "mixing ESFS and non-ESFS") {
		t.Fatalf("expected mixed-scope diagnostic, got %s", errb.String())
	}
}

func TestGrepSinceUsesDateField(t *testing.T) {
	d, f, out, errb, _ := newDeps(t, "/esfs/conversations")
	f.SetCaps("conversations", map[string]escore.FieldCap{
		"@timestamp": {Name: "@timestamp", Type: "date"},
		"body":       {Name: "body", Type: "text", Searchable: true},
	})
	f.Seed("conversations", "a", `{"body":"red shoes"}`)
	rc := d.RunGrep(context.Background(), []string{"red shoes", "--since", "7d"})
	if rc != 0 {
		t.Fatalf("since grep rc=%d err=%s", rc, errb.String())
	}
	if !strings.Contains(out.String(), "/esfs/conversations/") {
		t.Fatalf("since grep should match, got:\n%s", out.String())
	}
}

func TestGrepSemanticForcedWithoutFieldsErrors(t *testing.T) {
	d, f, _, errb, _ := newDeps(t, "/esfs/conversations")
	f.SetCaps("conversations", map[string]escore.FieldCap{"body": {Name: "body", Type: "text", Searchable: true}})
	f.Seed("conversations", "a", `{"body":"x"}`)
	rc := d.RunGrep(context.Background(), []string{"--mode", "semantic", "x"})
	if rc != 2 {
		t.Fatalf("expected rc=2 when semantic forced without fields, got %d", rc)
	}
	if !strings.Contains(errb.String(), "semantic") {
		t.Fatalf("expected semantic diagnostic, got %s", errb.String())
	}
}
