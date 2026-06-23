package cli

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/dbmurphy/elasticsearch-filesystem/internal/appctx"
	"github.com/dbmurphy/elasticsearch-filesystem/internal/routing"
)

type check struct {
	name string
	ok   bool
	note string
}

// cmdDoctor verifies the full ESFS contract surface: configuration, Elasticsearch
// connectivity/auth, index visibility, FUSE backend availability, routing
// activation, and shim installation.
func cmdDoctor(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("doctor", flag.ContinueOnError)
	fs.SetOutput(stderr)
	if err := fs.Parse(args); err != nil {
		return 2
	}
	var checks []check

	// Config.
	cfg, cfgErr := appctx.LoadConfig()
	checks = append(checks, check{"config", cfgErr == nil && cfg.Endpoint != "", endpointNote(cfg.Endpoint, cfgErr)})

	// Elasticsearch connectivity + auth.
	if cfg.Endpoint != "" {
		client, err := appctx.NewClient(cfg)
		if err != nil {
			checks = append(checks, check{"elasticsearch", false, err.Error()})
		} else {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			info, perr := client.Ping(ctx)
			cancel()
			if perr != nil {
				checks = append(checks, check{"elasticsearch", false, perr.Error()})
			} else {
				checks = append(checks, check{"elasticsearch", true, fmt.Sprintf("%s (v%s)", info.ClusterName, info.Version.Number)})
				ctx2, cancel2 := context.WithTimeout(context.Background(), 5*time.Second)
				idx, ierr := client.VisibleIndices(ctx2)
				cancel2()
				if ierr != nil {
					checks = append(checks, check{"indices", false, ierr.Error()})
				} else {
					checks = append(checks, check{"indices", true, fmt.Sprintf("%d visible", len(idx))})
				}
			}
		}
	}

	// FUSE backend.
	fok, fnote := fuseStatus()
	checks = append(checks, check{"fuse backend", fok, fnote})

	// Routing activation + shims.
	checks = append(checks, check{"routing", routing.Active(), routingNote()})
	checks = append(checks, check{"shims", shimsInstalled(), shimNote()})

	// Sync policy summary.
	checks = append(checks, check{"sync policy", true, fmt.Sprintf("write=%s delete_sync=%v", writePolicyName(cfg.WritePolicy), cfg.DeleteSync)})

	allOK := true
	for _, c := range checks {
		mark := "ok "
		if !c.ok {
			mark = "FAIL"
			allOK = false
		}
		fmt.Fprintf(stdout, "[%s] %-16s %s\n", mark, c.name, c.note)
	}
	if !allOK {
		return 1
	}
	return 0
}

func endpointNote(ep string, err error) string {
	if err != nil {
		return err.Error()
	}
	if ep == "" {
		return "no endpoint; set ESFS_ENDPOINT or a config file"
	}
	return ep
}

func fuseStatus() (bool, string) {
	switch runtime.GOOS {
	case "linux":
		if _, err := os.Stat("/dev/fuse"); err == nil {
			return true, "/dev/fuse present"
		}
		return false, "/dev/fuse missing; install fuse3"
	case "darwin":
		if _, err := os.Stat("/Library/Filesystems/macfuse.fs"); err == nil {
			return true, "macFUSE installed"
		}
		if _, err := os.Stat("/usr/local/lib/libfuse-t.dylib"); err == nil {
			return true, "FUSE-T installed"
		}
		return false, "no macFUSE/FUSE-T; install one to mount (kextless FUSE-T preferred)"
	default:
		return false, runtime.GOOS + " not a supported mount platform"
	}
}

func routingNote() string {
	if routing.Active() {
		return strings.Join(routing.Mounts(), ", ")
	}
	return "inactive; run: eval \"$(esfs env)\""
}

func shimsInstalled() bool {
	dir, err := os.UserCacheDir()
	if err != nil {
		return false
	}
	shim := filepath.Join(dir, "esfs", "shims", "grep")
	_, err = os.Stat(shim)
	return err == nil
}

func shimNote() string {
	if shimsInstalled() {
		return "installed"
	}
	return "not installed; run: eval \"$(esfs env)\""
}
