package cli

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"runtime"
	"strings"
	"syscall"
	"time"

	"github.com/dbmurphy/elasticsearch-filesystem/internal/appctx"
	"github.com/dbmurphy/elasticsearch-filesystem/internal/escore"
	"github.com/dbmurphy/elasticsearch-filesystem/internal/fusefs"
	"github.com/dbmurphy/elasticsearch-filesystem/internal/profile"
	"github.com/dbmurphy/elasticsearch-filesystem/internal/vfs"
)

// cmdMount builds the shared core and mounts ESFS, blocking until the mount is
// unmounted or the process is interrupted.
func cmdMount(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("mount", flag.ContinueOnError)
	fs.SetOutput(stderr)
	mount := fs.String("mount", defaultMount(), "mountpoint")
	endpoint := fs.String("endpoint", "", "Elasticsearch endpoint (overrides config/env)")
	apiKeyEnv := fs.String("api-key-env", "", "env var holding the API key")
	indices := fs.String("index", "", "comma-separated allowlist of indices to expose (default: ALL visible indices in the cluster)")
	deleteSync := fs.Bool("delete-sync", false, "enable document deletion on unlink")
	debug := fs.Bool("debug", false, "enable FUSE debug logging")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	cfg, err := appctx.LoadConfig()
	if err != nil {
		fmt.Fprintf(stderr, "esfs mount: %v\n", err)
		return 2
	}
	if *endpoint != "" {
		cfg.Endpoint = *endpoint
	}
	if *apiKeyEnv != "" {
		cfg.APIKeyEnv = *apiKeyEnv
	}
	if *deleteSync {
		cfg.DeleteSync = true
	}
	if *indices != "" {
		cfg.VisibleIndices = splitCSV(*indices)
	}
	if cfg.Endpoint == "" {
		fmt.Fprintln(stderr, "esfs mount: no Elasticsearch endpoint; set --endpoint, ESFS_ENDPOINT, or a config file")
		return 2
	}

	client, err := appctx.NewClient(cfg)
	if err != nil {
		fmt.Fprintf(stderr, "esfs mount: %v\n", err)
		return 2
	}

	// Fail fast if the cluster is unreachable.
	pctx, pcancel := context.WithTimeout(context.Background(), 5*time.Second)
	if _, err := client.Ping(pctx); err != nil {
		pcancel()
		fmt.Fprintf(stderr, "esfs mount: cannot reach Elasticsearch: %v\n", err)
		return 2
	}
	pcancel()

	gen := profile.New(client, cfg)
	sync := escore.NewSyncTracker(time.Now, escore.SyncPolicy{
		WritePolicy: cfg.WritePolicy.String(),
		DeleteSync:  cfg.DeleteSync,
	})
	v := vfs.New(vfs.Deps{
		Store: client, Catalog: client, Lister: client, Searcher: client,
		Profiles: gen, Sync: sync, Config: cfg,
	})

	if err := os.MkdirAll(*mount, 0o755); err != nil {
		fmt.Fprintf(stderr, "esfs mount: cannot create mountpoint %s: %v\n", *mount, err)
		return 2
	}

	server, err := fusefs.Mount(v, *mount, *debug)
	if err != nil {
		fmt.Fprintf(stderr, "esfs mount: %v\n", err)
		fmt.Fprintf(stderr, "esfs: ensure a FUSE backend is installed (%s)\n", fuseHint())
		return 2
	}
	scope := "all visible indices"
	if len(cfg.VisibleIndices) > 0 {
		scope = fmt.Sprintf("%d index allowlist (%s)", len(cfg.VisibleIndices), strings.Join(cfg.VisibleIndices, ", "))
	}
	fmt.Fprintf(stdout, "esfs: mounted %s at %s\n", scope, *mount)
	fmt.Fprintf(stdout, "esfs: activate routed commands with: eval \"$(esfs env --mount %s)\"\n", *mount)

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sig
		fmt.Fprintln(stdout, "\nesfs: unmounting...")
		_ = server.Unmount()
	}()
	server.Wait()
	return 0
}

// cmdUnmount unmounts an ESFS mountpoint using the platform unmount tool.
func cmdUnmount(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("unmount", flag.ContinueOnError)
	fs.SetOutput(stderr)
	if err := fs.Parse(args); err != nil {
		return 2
	}
	mp := firstNonFlag(fs.Args())
	if mp == "" {
		mp = defaultMount()
	}
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "linux":
		if path, err := exec.LookPath("fusermount3"); err == nil {
			cmd = exec.Command(path, "-u", mp)
		} else if path, err := exec.LookPath("fusermount"); err == nil {
			cmd = exec.Command(path, "-u", mp)
		} else {
			cmd = exec.Command("umount", mp)
		}
	default:
		cmd = exec.Command("umount", mp)
	}
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	if err := cmd.Run(); err != nil {
		fmt.Fprintf(stderr, "esfs unmount: %v\n", err)
		return 2
	}
	fmt.Fprintf(stdout, "esfs: unmounted %s\n", mp)
	return 0
}

func splitCSV(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func fuseHint() string {
	if runtime.GOOS == "darwin" {
		return "macFUSE or FUSE-T on macOS"
	}
	return "fuse3 on Linux"
}
