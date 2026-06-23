package escore

import (
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/dbmurphy/elasticsearch-filesystem/internal/contract"
)

// Config is a resolved ESFS profile: how to reach Elasticsearch, global safety
// policies, and optional per-index overrides. JSON uses human-readable values
// (durations like "10s", write_policy as a string); legacy numeric forms are
// still accepted for backward compatibility.
type Config struct {
	Endpoint   string `json:"endpoint"`
	APIKeyEnv  string `json:"api_key_env"`
	APIKey     string `json:"api_key"`
	CACertFile string `json:"ca_cert_file"`
	Insecure   bool   `json:"insecure"`

	// VisibleIndices is an allowlist of indices/aliases to expose. Empty (the
	// default) exposes ALL visible indices in the cluster under one mount.
	VisibleIndices []string

	// Global defaults (overridable per index via Indices).
	WritePolicy  WritePolicy
	DeleteSync   bool
	TimeField    string
	Timezone     string
	GrepMode     SearchMode // global default search mode: auto|semantic|lexical
	RedactFields []string

	RequestTimeout time.Duration
	Concurrency    int
	CacheTTL       time.Duration

	// ProfileOptOut disables profile generation for the named indices (global).
	ProfileOptOut []string

	// Indices holds per-index overrides keyed by index name.
	Indices map[string]IndexSettings
}

// IndexSettings are per-index overrides. Nil/empty fields inherit the global
// default. This is where users map how each index searches.
type IndexSettings struct {
	// TimeField is the date field used by `--since` for this index.
	TimeField *string `json:"time_field,omitempty"`
	// SearchFields restricts lexical search to these fields (better ranking
	// than searching every text field).
	SearchFields []string `json:"search_fields,omitempty"`
	// SemanticFields names semantic_text fields to use for semantic search,
	// overriding auto-detection.
	SemanticFields []string `json:"semantic_fields,omitempty"`
	// GrepMode is the default search mode for this index: auto|semantic|lexical.
	GrepMode *string `json:"grep_mode,omitempty"`
	// RedactFields are additional fields to redact in this index's profile.
	RedactFields []string `json:"redact_fields,omitempty"`
	// Profile enables/disables profile.md generation for this index.
	Profile *bool `json:"profile,omitempty"`
	// WritePolicy overrides the write policy for this index.
	WritePolicy *WritePolicy `json:"write_policy,omitempty"`
	// DeleteSync overrides delete sync for this index.
	DeleteSync *bool `json:"delete_sync,omitempty"`
}

// ResolvedIndex is the effective per-index configuration after merging global
// defaults with per-index overrides.
type ResolvedIndex struct {
	TimeField      string
	SearchFields   []string
	SemanticFields []string
	GrepMode       SearchMode
	RedactFields   []string
	Profile        bool
	WritePolicy    WritePolicy
	DeleteSync     bool
}

// DefaultConfig returns conservative defaults.
func DefaultConfig() Config {
	return Config{
		WritePolicy:    PolicySafeCreateUpdate,
		DeleteSync:     false,
		GrepMode:       ModeAuto,
		RequestTimeout: 10 * time.Second,
		Concurrency:    8,
		CacheTTL:       30 * time.Second,
		Indices:        map[string]IndexSettings{},
	}
}

// ForIndex returns the effective configuration for an index, merging global
// defaults with any per-index overrides.
func (c Config) ForIndex(name string) ResolvedIndex {
	r := ResolvedIndex{
		TimeField:    c.TimeField,
		GrepMode:     c.GrepMode,
		RedactFields: append([]string(nil), c.RedactFields...),
		Profile:      !contains(c.ProfileOptOut, name),
		WritePolicy:  c.WritePolicy,
		DeleteSync:   c.DeleteSync,
	}
	if r.GrepMode == "" {
		r.GrepMode = ModeAuto
	}
	s, ok := c.Indices[name]
	if !ok {
		return r
	}
	if s.TimeField != nil {
		r.TimeField = *s.TimeField
	}
	r.SearchFields = append([]string(nil), s.SearchFields...)
	r.SemanticFields = append([]string(nil), s.SemanticFields...)
	if s.GrepMode != nil {
		r.GrepMode = SearchMode(*s.GrepMode)
	}
	if len(s.RedactFields) > 0 {
		r.RedactFields = append(r.RedactFields, s.RedactFields...)
	}
	if s.Profile != nil {
		r.Profile = *s.Profile
	}
	if s.WritePolicy != nil {
		r.WritePolicy = *s.WritePolicy
	}
	if s.DeleteSync != nil {
		r.DeleteSync = *s.DeleteSync
	}
	return r
}

func contains(ss []string, v string) bool {
	for _, s := range ss {
		if s == v {
			return true
		}
	}
	return false
}

// ResolveAPIKey returns the API key from env (preferred) or inline config.
func (c Config) ResolveAPIKey() string {
	if c.APIKeyEnv != "" {
		if v := os.Getenv(c.APIKeyEnv); v != "" {
			return v
		}
	}
	return c.APIKey
}

// configJSON is the on-disk JSON shape with human-readable durations and a
// string write_policy. It maps to Config via UnmarshalJSON.
type configJSON struct {
	Endpoint       string                   `json:"endpoint"`
	APIKeyEnv      string                   `json:"api_key_env"`
	APIKey         string                   `json:"api_key"`
	CACertFile     string                   `json:"ca_cert_file"`
	Insecure       bool                     `json:"insecure"`
	VisibleIndices []string                 `json:"visible_indices"`
	WritePolicy    *WritePolicy             `json:"write_policy"`
	DeleteSync     bool                     `json:"delete_sync"`
	TimeField      string                   `json:"time_field"`
	Timezone       string                   `json:"timezone"`
	GrepMode       string                   `json:"grep_mode"`
	RedactFields   []string                 `json:"redact_fields"`
	RequestTimeout *string                  `json:"request_timeout"`
	Concurrency    int                      `json:"concurrency"`
	CacheTTL       *string                  `json:"cache_ttl"`
	ProfileOptOut  []string                 `json:"profile_opt_out"`
	Indices        map[string]IndexSettings `json:"indices"`
}

// UnmarshalJSON parses the human-readable config form into Config.
func (c *Config) UnmarshalJSON(data []byte) error {
	*c = DefaultConfig()
	var j configJSON
	if err := json.Unmarshal(data, &j); err != nil {
		return err
	}
	c.Endpoint = j.Endpoint
	c.APIKeyEnv = j.APIKeyEnv
	c.APIKey = j.APIKey
	c.CACertFile = j.CACertFile
	c.Insecure = j.Insecure
	c.VisibleIndices = j.VisibleIndices
	c.DeleteSync = j.DeleteSync
	c.TimeField = j.TimeField
	c.Timezone = j.Timezone
	c.RedactFields = j.RedactFields
	c.ProfileOptOut = j.ProfileOptOut
	if j.Concurrency > 0 {
		c.Concurrency = j.Concurrency
	}
	if j.Indices != nil {
		c.Indices = j.Indices
	}
	if j.GrepMode != "" {
		c.GrepMode = SearchMode(j.GrepMode)
	}
	if j.WritePolicy != nil {
		c.WritePolicy = *j.WritePolicy
	}
	if j.RequestTimeout != nil {
		d, err := parseDuration(*j.RequestTimeout)
		if err != nil {
			return contract.Wrap(contract.KindUsage, err, "request_timeout")
		}
		c.RequestTimeout = d
	}
	if j.CacheTTL != nil {
		d, err := parseDuration(*j.CacheTTL)
		if err != nil {
			return contract.Wrap(contract.KindUsage, err, "cache_ttl")
		}
		c.CacheTTL = d
	}
	return nil
}

// parseDuration accepts a Go duration string ("10s", "500ms"). A bare integer
// string is treated as nanoseconds for backward compatibility.
func parseDuration(s string) (time.Duration, error) {
	if s == "" {
		return 0, nil
	}
	if d, err := time.ParseDuration(s); err == nil {
		return d, nil
	}
	var ns int64
	if _, err := fmt.Sscan(s, &ns); err == nil {
		return time.Duration(ns), nil
	}
	return 0, fmt.Errorf("invalid duration %q (use forms like \"10s\")", s)
}

// LoadConfig reads a JSON config file and overlays it on the defaults.
func LoadConfig(path string) (Config, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return DefaultConfig(), contract.Wrap(contract.KindUsage, err, "read config %s", path)
	}
	var cfg Config
	if err := json.Unmarshal(b, &cfg); err != nil {
		return DefaultConfig(), contract.Wrap(contract.KindUsage, err, "parse config %s", path)
	}
	return cfg, nil
}
