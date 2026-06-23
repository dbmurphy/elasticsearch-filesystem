package escore

import (
	"encoding/json"
	"testing"
	"time"
)

func TestConfigHumanReadableJSON(t *testing.T) {
	raw := `{
	  "endpoint": "http://es:9200",
	  "write_policy": "upsert-new-files",
	  "request_timeout": "5s",
	  "cache_ttl": "1m",
	  "grep_mode": "lexical",
	  "redact_fields": ["password"],
	  "indices": {
	    "conversations": {
	      "time_field": "@timestamp",
	      "search_fields": ["subject", "body"],
	      "grep_mode": "semantic",
	      "redact_fields": ["email"],
	      "delete_sync": true,
	      "write_policy": "create-only"
	    }
	  }
	}`
	var cfg Config
	if err := json.Unmarshal([]byte(raw), &cfg); err != nil {
		t.Fatal(err)
	}
	if cfg.WritePolicy != PolicyUpsert {
		t.Fatalf("write_policy = %v", cfg.WritePolicy)
	}
	if cfg.RequestTimeout != 5*time.Second {
		t.Fatalf("request_timeout = %v", cfg.RequestTimeout)
	}
	if cfg.CacheTTL != time.Minute {
		t.Fatalf("cache_ttl = %v", cfg.CacheTTL)
	}
	if cfg.GrepMode != ModeLexical {
		t.Fatalf("grep_mode = %v", cfg.GrepMode)
	}
}

func TestConfigLegacyNumericDurations(t *testing.T) {
	raw := `{"endpoint":"x","request_timeout":"10000000000","write_policy":0}`
	var cfg Config
	if err := json.Unmarshal([]byte(raw), &cfg); err != nil {
		t.Fatal(err)
	}
	if cfg.RequestTimeout != 10*time.Second {
		t.Fatalf("legacy ns duration = %v", cfg.RequestTimeout)
	}
	if cfg.WritePolicy != PolicySafeCreateUpdate {
		t.Fatalf("legacy numeric write_policy = %v", cfg.WritePolicy)
	}
}

func TestForIndexMergesOverrides(t *testing.T) {
	raw := `{
	  "endpoint":"x",
	  "time_field":"global_ts",
	  "redact_fields":["password"],
	  "delete_sync":false,
	  "indices":{
	    "conversations":{
	      "time_field":"@timestamp",
	      "search_fields":["body"],
	      "semantic_fields":["body_semantic"],
	      "grep_mode":"semantic",
	      "redact_fields":["email"],
	      "delete_sync":true
	    }
	  }
	}`
	var cfg Config
	if err := json.Unmarshal([]byte(raw), &cfg); err != nil {
		t.Fatal(err)
	}

	// Index with overrides.
	ri := cfg.ForIndex("conversations")
	if ri.TimeField != "@timestamp" {
		t.Fatalf("time_field override = %q", ri.TimeField)
	}
	if len(ri.SearchFields) != 1 || ri.SearchFields[0] != "body" {
		t.Fatalf("search_fields = %v", ri.SearchFields)
	}
	if ri.GrepMode != ModeSemantic {
		t.Fatalf("grep_mode = %v", ri.GrepMode)
	}
	if !ri.DeleteSync {
		t.Fatalf("delete_sync override not applied")
	}
	// Redaction merges global + per-index.
	if !contains(ri.RedactFields, "password") || !contains(ri.RedactFields, "email") {
		t.Fatalf("redact merge = %v", ri.RedactFields)
	}

	// Index without overrides inherits globals.
	other := cfg.ForIndex("logs")
	if other.TimeField != "global_ts" {
		t.Fatalf("inherited time_field = %q", other.TimeField)
	}
	if other.DeleteSync {
		t.Fatalf("logs should inherit delete_sync=false")
	}
	if other.GrepMode != ModeAuto {
		t.Fatalf("default grep mode should be auto, got %v", other.GrepMode)
	}
}

func TestShippedExampleConfigsParse(t *testing.T) {
	for _, p := range []string{"../../config.example.json", "../../examples/config.full.json"} {
		cfg, err := LoadConfig(p)
		if err != nil {
			t.Fatalf("load %s: %v", p, err)
		}
		if cfg.RequestTimeout <= 0 {
			t.Fatalf("%s: request_timeout not parsed", p)
		}
		// conversations is configured in both examples.
		ri := cfg.ForIndex("conversations")
		if len(ri.SearchFields) == 0 {
			t.Fatalf("%s: conversations search_fields missing", p)
		}
	}
}

func TestProfileOptOutResolves(t *testing.T) {
	raw := `{"endpoint":"x","profile_opt_out":["secrets"]}`
	var cfg Config
	if err := json.Unmarshal([]byte(raw), &cfg); err != nil {
		t.Fatal(err)
	}
	if cfg.ForIndex("secrets").Profile {
		t.Fatal("secrets should have profile disabled")
	}
	if !cfg.ForIndex("conversations").Profile {
		t.Fatal("conversations should have profile enabled")
	}
}
