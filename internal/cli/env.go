package cli

import (
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/dbmurphy/elasticsearch-filesystem/internal/routing"
)

// cmdEnv prints shell activation: it ensures a shim directory containing
// grep/ls/find wrappers exists, then emits exports the user can `eval`.
func cmdEnv(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("env", flag.ContinueOnError)
	fs.SetOutput(stderr)
	mount := fs.String("mount", defaultMount(), "ESFS mount root to activate")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	shimDir, err := ensureShimDir()
	if err != nil {
		fmt.Fprintf(stderr, "esfs env: %v\n", err)
		return 2
	}
	fmt.Fprintf(stdout, "export %s=%q\n", routing.EnvMount, *mount)
	fmt.Fprintf(stdout, "export PATH=%q\n", shimDir+":"+os.Getenv("PATH"))
	fmt.Fprintln(stdout, "# ESFS routing active: ls, find, grep route to Elasticsearch inside the mount.")
	fmt.Fprintln(stdout, "# Run via: eval \"$(esfs env)\"")
	return 0
}

func defaultMount() string {
	if v := os.Getenv(routing.EnvMount); v != "" {
		return v
	}
	return "/esfs"
}

// ensureShimDir creates a per-user directory of wrapper scripts named grep, ls,
// and find that exec the installed esfs-<tool> shims. The wrappers reference the
// esfs-* binaries by absolute path resolved from this executable's directory.
func ensureShimDir() (string, error) {
	cacheDir, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	shimDir := filepath.Join(cacheDir, "esfs", "shims")
	if err := os.MkdirAll(shimDir, 0o755); err != nil {
		return "", err
	}
	selfDir := exeDir()
	for _, tool := range []string{"grep", "ls", "find"} {
		target := filepath.Join(selfDir, "esfs-"+tool)
		script := "#!/bin/sh\nexec " + shellQuote(target) + " \"$@\"\n"
		path := filepath.Join(shimDir, tool)
		if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
			return "", err
		}
	}
	return shimDir, nil
}

func exeDir() string {
	exe, err := os.Executable()
	if err != nil {
		return "."
	}
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		exe = resolved
	}
	return filepath.Dir(exe)
}

func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
