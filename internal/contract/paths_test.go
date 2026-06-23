package contract

import (
	"syscall"
	"testing"
)

func TestParsePath(t *testing.T) {
	docName, _ := EncodeID("doc1")
	encName, _ := EncodeID("with/slash")
	tests := []struct {
		rel  string
		want NodeKind
		idx  string
		id   string
	}{
		{"", NodeRoot, "", ""},
		{"/", NodeRoot, "", ""},
		{"profile.md", NodeMountProfile, "", ""},
		{".sync.json", NodeMountSync, "", ""},
		{"conversations", NodeIndexDir, "conversations", ""},
		{"conversations/profile.md", NodeIndexProfile, "conversations", ""},
		{"conversations/.sync.json", NodeIndexSync, "conversations", ""},
		{"conversations/.mapping.json", NodeIndexMapping, "conversations", ""},
		{"conversations/.fields.json", NodeIndexFields, "conversations", ""},
		{"conversations/" + docName, NodeDocument, "conversations", "doc1"},
		{"conversations/" + encName, NodeDocument, "conversations", "with/slash"},
		{".hidden", NodeInvalid, "", ""},
		{"a/b/c", NodeInvalid, "", ""},
	}
	for _, tc := range tests {
		got := ParsePath(tc.rel)
		if got.Kind != tc.want {
			t.Errorf("ParsePath(%q).Kind = %v, want %v", tc.rel, got.Kind, tc.want)
			continue
		}
		if tc.idx != "" && got.Index != tc.idx {
			t.Errorf("ParsePath(%q).Index = %q, want %q", tc.rel, got.Index, tc.idx)
		}
		if tc.id != "" && got.ID != tc.id {
			t.Errorf("ParsePath(%q).ID = %q, want %q", tc.rel, got.ID, tc.id)
		}
	}
}

func TestParseHashedDocument(t *testing.T) {
	id := stringOfLen(5000)
	name, hashed := EncodeID(id)
	if !hashed {
		t.Fatal("setup: expected hashed")
	}
	n := ParsePath("conversations/" + name)
	if n.Kind != NodeDocument || n.Index != "conversations" || n.HashedName != name {
		t.Fatalf("hashed document parse wrong: %+v", n)
	}
	if n.ID != "" {
		t.Fatalf("hashed document should not carry decoded ID, got %q", n.ID)
	}
}

func TestErrnoMapping(t *testing.T) {
	tests := []struct {
		err  error
		want syscall.Errno
	}{
		{Errf(KindNotFound, "x"), syscall.ENOENT},
		{Errf(KindPermission, "x"), syscall.EACCES},
		{Errf(KindReadOnly, "x"), syscall.EROFS},
		{Errf(KindUnsupported, "x"), syscall.EPERM},
		{Errf(KindInvalidJSON, "x"), syscall.EINVAL},
		{Errf(KindConflict, "x"), syscall.EAGAIN},
		{Errf(KindUpstream, "x"), syscall.EIO},
		{Errf(KindNameTooLong, "x"), syscall.ENAMETOOLONG},
		{Errf(KindUsage, "x"), syscall.EINVAL},
	}
	for _, tc := range tests {
		if got := Errno(tc.err); got != tc.want {
			t.Errorf("Errno(%v) = %v, want %v", tc.err, got, tc.want)
		}
	}
}

func stringOfLen(n int) string {
	b := make([]byte, n)
	for i := range b {
		b[i] = 'x'
	}
	return string(b)
}
