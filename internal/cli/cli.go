// Package cli implements the `esfs` command-line surface: activation (env),
// path encoding (path), diagnostics (doctor), the optional search helper (grep),
// and mount lifecycle (mount/unmount).
package cli

import (
	"fmt"
	"io"
)

// Version is the build version, overridable via -ldflags.
var Version = "0.0.1-dev"

// Main dispatches an `esfs` subcommand and returns an exit code.
func Main(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		usage(stdout)
		return 2
	}
	cmd, rest := args[0], args[1:]
	switch cmd {
	case "env":
		return cmdEnv(rest, stdout, stderr)
	case "path":
		return cmdPath(rest, stdout, stderr)
	case "doctor":
		return cmdDoctor(rest, stdout, stderr)
	case "grep":
		return cmdGrep(rest, stdout, stderr)
	case "mount":
		return cmdMount(rest, stdout, stderr)
	case "unmount", "umount":
		return cmdUnmount(rest, stdout, stderr)
	case "version", "--version", "-v":
		fmt.Fprintf(stdout, "esfs %s\n", Version)
		return 0
	case "help", "--help", "-h":
		usage(stdout)
		return 0
	default:
		fmt.Fprintf(stderr, "esfs: unknown command %q\n", cmd)
		usage(stderr)
		return 2
	}
}

func usage(w io.Writer) {
	fmt.Fprint(w, `esfs - Elasticsearch as an SMFS-style filesystem

usage: esfs <command> [args]

commands:
  mount     mount Elasticsearch indices as a filesystem
  unmount   unmount an ESFS mountpoint
  env       print shell activation for routed ls/find/grep (eval "$(esfs env)")
  path      print the canonical ESFS path for an index/document id
  grep      run a routed Elasticsearch search (debug/scripting helper)
  doctor    diagnose mount, Elasticsearch, auth, routing, and sync state
  version   print the esfs version

environment:
  ESFS_ENDPOINT, ESFS_API_KEY_ENV, ESFS_CONFIG, ESFS_MOUNT,
  ESFS_GREP_MODE (auto|semantic|lexical|exact), ESFS_STRICT_GREP=1
`)
}

// firstNonFlag returns the first argument that is not a flag.
func firstNonFlag(args []string) string {
	for _, a := range args {
		if len(a) == 0 || a[0] != '-' {
			return a
		}
	}
	return ""
}
