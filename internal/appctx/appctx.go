// Package appctx builds the ESFS shared core (config + Elasticsearch client)
// from configuration files and environment variables. It is the common
// bootstrap for the daemon, the CLI, and the routed command shims.
package appctx

import (
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/dbmurphy/elasticsearch-filesystem/internal/contract"
	"github.com/dbmurphy/elasticsearch-filesystem/internal/escore"
)

// Env var names for zero-config bootstrap (handy for Bash Tool mode).
const (
	EnvConfig    = "ESFS_CONFIG"
	EnvEndpoint  = "ESFS_ENDPOINT"
	EnvAPIKeyEnv = "ESFS_API_KEY_ENV"
	EnvInsecure  = "ESFS_INSECURE"
	EnvCACert    = "ESFS_CA_CERT"
	EnvDelete    = "ESFS_DELETE_SYNC"
	EnvTimeField = "ESFS_TIME_FIELD"
)

// DefaultConfigPath returns the conventional config location.
func DefaultConfigPath() string {
	if p := os.Getenv(EnvConfig); p != "" {
		return p
	}
	if dir, err := os.UserConfigDir(); err == nil {
		return filepath.Join(dir, "esfs", "config.json")
	}
	return ""
}

// LoadConfig resolves config from file (if present) then overlays env vars.
func LoadConfig() (escore.Config, error) {
	cfg := escore.DefaultConfig()
	if p := DefaultConfigPath(); p != "" {
		if _, err := os.Stat(p); err == nil {
			loaded, lerr := escore.LoadConfig(p)
			if lerr != nil {
				return cfg, lerr
			}
			cfg = loaded
		}
	}
	if v := os.Getenv(EnvEndpoint); v != "" {
		cfg.Endpoint = v
	}
	if v := os.Getenv(EnvAPIKeyEnv); v != "" {
		cfg.APIKeyEnv = v
	}
	if v := os.Getenv(EnvCACert); v != "" {
		cfg.CACertFile = v
	}
	if v := os.Getenv(EnvInsecure); v == "1" || v == "true" {
		cfg.Insecure = true
	}
	if v := os.Getenv(EnvDelete); v == "1" || v == "true" {
		cfg.DeleteSync = true
	}
	if v := os.Getenv(EnvTimeField); v != "" {
		cfg.TimeField = v
	}
	if v := os.Getenv("ESFS_TIMEOUT_MS"); v != "" {
		if ms, err := strconv.Atoi(v); err == nil {
			cfg.RequestTimeout = time.Duration(ms) * time.Millisecond
		}
	}
	return cfg, nil
}

// NewClient builds a live Elasticsearch client from resolved config.
func NewClient(cfg escore.Config) (*escore.ESClient, error) {
	if cfg.Endpoint == "" {
		return nil, contract.Errf(contract.KindUsage, "no Elasticsearch endpoint; set %s or a config file", EnvEndpoint)
	}
	return escore.NewESClient(cfg)
}
