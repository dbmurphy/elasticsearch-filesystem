package escore

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/dbmurphy/elasticsearch-filesystem/internal/contract"
)

func capsWith(fields map[string]FieldCap) FieldCaps {
	return FieldCaps{Index: "conversations", Fields: fields}
}

func TestPlanLexicalDefault(t *testing.T) {
	caps := capsWith(map[string]FieldCap{
		"body": {Name: "body", Type: "text", Searchable: true},
	})
	pq, err := planQuery(SearchRequest{Indices: []string{"conversations"}, Query: "refund"}, caps, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if pq.Mode != ModeLexical {
		t.Fatalf("expected lexical, got %s", pq.Mode)
	}
	b, _ := json.Marshal(pq.Query)
	if !strings.Contains(string(b), "simple_query_string") {
		t.Fatalf("expected simple_query_string, got %s", b)
	}
}

func TestPlanSemanticWhenAvailable(t *testing.T) {
	caps := capsWith(map[string]FieldCap{
		"body_semantic": {Name: "body_semantic", Type: "semantic_text"},
	})
	pq, err := planQuery(SearchRequest{Indices: []string{"conversations"}, Query: "red shoes"}, caps, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if pq.Mode != ModeSemantic {
		t.Fatalf("expected semantic, got %s", pq.Mode)
	}
	b, _ := json.Marshal(pq.Query)
	if !strings.Contains(string(b), "\"semantic\"") {
		t.Fatalf("expected semantic clause, got %s", b)
	}
}

func TestPlanRespectsConfiguredSearchFields(t *testing.T) {
	caps := capsWith(map[string]FieldCap{
		"subject": {Name: "subject", Type: "text", Searchable: true},
		"body":    {Name: "body", Type: "text", Searchable: true},
		"noise":   {Name: "noise", Type: "text", Searchable: true},
	})
	req := SearchRequest{Indices: []string{"conversations"}, Query: "refund", SearchFields: []string{"subject", "body"}}
	pq, err := planQuery(req, caps, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(pq.Query)
	s := string(b)
	if !strings.Contains(s, "subject") || !strings.Contains(s, "body") {
		t.Fatalf("expected configured fields in query: %s", s)
	}
	if strings.Contains(s, "noise") {
		t.Fatalf("non-configured field should be excluded: %s", s)
	}
}

func TestPlanRespectsConfiguredSemanticFields(t *testing.T) {
	// Caps have no semantic_text, but config names a semantic field explicitly.
	caps := capsWith(map[string]FieldCap{"body": {Name: "body", Type: "text", Searchable: true}})
	req := SearchRequest{Indices: []string{"conversations"}, Query: "red shoes", Mode: ModeSemantic, SemanticFields: []string{"body_semantic"}}
	pq, err := planQuery(req, caps, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if pq.Mode != ModeSemantic {
		t.Fatalf("expected semantic mode, got %s", pq.Mode)
	}
	b, _ := json.Marshal(pq.Query)
	if !strings.Contains(string(b), "body_semantic") {
		t.Fatalf("expected configured semantic field: %s", b)
	}
}

func TestPlanSemanticForcedErrorsWithoutFields(t *testing.T) {
	caps := capsWith(map[string]FieldCap{"body": {Name: "body", Type: "text", Searchable: true}})
	_, err := planQuery(SearchRequest{Indices: []string{"conversations"}, Query: "x", Mode: ModeSemantic}, caps, time.Now())
	if contract.KindOf(err) != contract.KindUsage {
		t.Fatalf("expected usage error, got %v", err)
	}
}

func TestPlanSinceInfersSingleDateField(t *testing.T) {
	caps := capsWith(map[string]FieldCap{
		"body":       {Name: "body", Type: "text", Searchable: true},
		"@timestamp": {Name: "@timestamp", Type: "date"},
	})
	now := time.Date(2026, 6, 23, 12, 0, 0, 0, time.UTC)
	pq, err := planQuery(SearchRequest{Indices: []string{"conversations"}, Query: "x", Since: 7 * 24 * time.Hour}, caps, now)
	if err != nil {
		t.Fatal(err)
	}
	if pq.TimeField != "@timestamp" {
		t.Fatalf("expected @timestamp, got %q", pq.TimeField)
	}
	if pq.GTE != "2026-06-16T12:00:00Z" {
		t.Fatalf("unexpected gte %q", pq.GTE)
	}
	b, _ := json.Marshal(pq.Query)
	if !strings.Contains(string(b), "\"range\"") || !strings.Contains(string(b), "\"filter\"") {
		t.Fatalf("range filter missing: %s", b)
	}
}

func TestPlanSinceAmbiguousErrors(t *testing.T) {
	caps := capsWith(map[string]FieldCap{
		"created_at": {Name: "created_at", Type: "date"},
		"updated_at": {Name: "updated_at", Type: "date"},
	})
	_, err := planQuery(SearchRequest{Indices: []string{"conversations"}, Since: time.Hour}, caps, time.Now())
	if contract.KindOf(err) != contract.KindUsage {
		t.Fatalf("expected usage error for ambiguous time field, got %v", err)
	}
	if !strings.Contains(err.Error(), "multiple candidates") {
		t.Fatalf("error should list candidates: %v", err)
	}
}

func TestPlanSinceNoDateFieldErrors(t *testing.T) {
	caps := capsWith(map[string]FieldCap{"body": {Name: "body", Type: "text"}})
	_, err := planQuery(SearchRequest{Indices: []string{"conversations"}, Since: time.Hour}, caps, time.Now())
	if contract.KindOf(err) != contract.KindUsage {
		t.Fatalf("expected usage error, got %v", err)
	}
}
