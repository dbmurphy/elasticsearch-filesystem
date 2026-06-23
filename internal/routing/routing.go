// Package routing implements ESFS scope detection for the routed command shims
// (esfs-ls/find/grep). It decides whether the current directory or a path
// operand falls inside an ESFS mount and, if so, maps it to an index/document
// scope. Activation is explicit: routing only engages when ESFS_MOUNT is set
// (by `eval "$(esfs env)"` or Bash Tool mode), so unactivated shells and
// absolute system-tool paths always pass through to the real tools.
package routing

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/dbmurphy/elasticsearch-filesystem/internal/contract"
)

// EnvMount is the activation/mount-roots variable; colon-separated absolute
// mount roots.
const EnvMount = "ESFS_MOUNT"

// EnvGrepMode mirrors ESFS_GREP_MODE (auto|semantic|lexical|exact).
const EnvGrepMode = "ESFS_GREP_MODE"

// EnvStrictGrep mirrors ESFS_STRICT_GREP=1 (force exact fallback silently).
const EnvStrictGrep = "ESFS_STRICT_GREP"

// Scope describes how an operand maps into ESFS.
type Scope struct {
	Root string        // matched mount root
	Rel  string        // mount-relative slash path
	Node contract.Node // parsed node
}

// Mounts returns configured mount roots, cleaned and absolute.
func Mounts() []string {
	raw := os.Getenv(EnvMount)
	if raw == "" {
		return nil
	}
	var out []string
	for _, p := range strings.Split(raw, ":") {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		if abs, err := filepath.Abs(p); err == nil {
			out = append(out, filepath.Clean(abs))
		}
	}
	return out
}

// Active reports whether ESFS routing is activated in this environment.
func Active() bool { return len(Mounts()) > 0 }

// Resolve maps a filesystem path to an ESFS scope when it lies within a mount
// root. cwd is used to absolutize relative operands.
func Resolve(cwd, path string) (Scope, bool) {
	abs := path
	if !filepath.IsAbs(abs) {
		abs = filepath.Join(cwd, path)
	}
	abs = filepath.Clean(abs)
	for _, root := range Mounts() {
		if abs == root {
			return Scope{Root: root, Rel: "", Node: contract.ParsePath("")}, true
		}
		if strings.HasPrefix(abs, root+string(filepath.Separator)) {
			rel := filepath.ToSlash(strings.TrimPrefix(abs, root+string(filepath.Separator)))
			return Scope{Root: root, Rel: rel, Node: contract.ParsePath(rel)}, true
		}
	}
	return Scope{}, false
}

// IsSystemToolPath reports whether the invoked program path is an absolute
// system tool that must never be routed (e.g. /usr/bin/grep).
func IsSystemToolPath(arg0 string) bool {
	switch arg0 {
	case "/bin/ls", "/usr/bin/ls",
		"/bin/cat", "/usr/bin/cat",
		"/usr/bin/find", "/bin/find",
		"/bin/grep", "/usr/bin/grep":
		return true
	}
	return false
}
