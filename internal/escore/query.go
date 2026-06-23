package escore

import (
	"sort"
	"time"

	"github.com/dbmurphy/elasticsearch-filesystem/internal/contract"
)

// inferableTimeFields are the only field names ESFS will auto-pick for --since,
// and only when exactly one exists and field_caps confirms it is date-typed.
var inferableTimeFields = []string{"@timestamp", "timestamp", "created_at", "updated_at"}

// plannedQuery is the compiled ES query plus the resolved execution details
// ESFS reports back to the user for honest labeling.
type plannedQuery struct {
	Query     map[string]any
	Mode      SearchMode
	TimeField string
	GTE       string // resolved lower bound for --since (RFC3339), for explain
}

// planQuery compiles a SearchRequest against an index's field capabilities into
// an Elasticsearch query, resolving search mode and the --since time field
// without requiring any ES-side setup.
func planQuery(req SearchRequest, caps FieldCaps, now time.Time) (plannedQuery, error) {
	var pq plannedQuery

	// Resolve text/semantic clause.
	textClause, mode, err := planText(req, caps)
	if err != nil {
		return pq, err
	}
	pq.Mode = mode

	var filters []map[string]any
	if req.Since > 0 {
		tf, gte, terr := planSince(req, caps, now)
		if terr != nil {
			return pq, terr
		}
		pq.TimeField = tf
		pq.GTE = gte
		filters = append(filters, map[string]any{
			"range": map[string]any{
				tf: map[string]any{"gte": gte},
			},
		})
	}

	boolq := map[string]any{}
	if textClause != nil {
		boolq["must"] = []any{textClause}
	} else {
		boolq["must"] = []any{map[string]any{"match_all": map[string]any{}}}
	}
	if len(filters) > 0 {
		fs := make([]any, len(filters))
		for i, f := range filters {
			fs[i] = f
		}
		boolq["filter"] = fs
	}
	pq.Query = map[string]any{"bool": boolq}
	return pq, nil
}

// planText builds the free-text clause and resolves semantic-vs-lexical mode
// honestly: semantic only when a semantic_text field exists (ES performs query
// inference with no client-side setup); otherwise ranked lexical.
func planText(req SearchRequest, caps FieldCaps) (map[string]any, SearchMode, error) {
	if req.Query == "" {
		return nil, req.Mode, nil // ls/find style; mode is irrelevant
	}
	// Per-index SemanticFields override auto-detection.
	semFields := req.SemanticFields
	if len(semFields) == 0 {
		semFields = semanticTextFields(caps)
	}
	want := req.Mode
	if want == "" || want == ModeAuto {
		want = ModeSemantic // auto prefers semantic when available
	}

	switch want {
	case ModeSemantic:
		if len(semFields) == 0 {
			if req.Mode == ModeSemantic {
				return nil, "", contract.Errf(contract.KindUsage,
					"no semantic-capable fields found; rerun with ranked lexical fallback enabled or configure a semantic field")
			}
			return lexicalClause(req, caps), ModeLexical, nil
		}
		shoulds := make([]any, 0, len(semFields))
		for _, f := range semFields {
			shoulds = append(shoulds, map[string]any{
				"semantic": map[string]any{"field": f, "query": req.Query},
			})
		}
		return map[string]any{"bool": map[string]any{"should": shoulds, "minimum_should_match": 1}}, ModeSemantic, nil
	default: // ModeLexical
		return lexicalClause(req, caps), ModeLexical, nil
	}
}

// semanticTextFields returns only semantic_text fields, which ES can query with
// no client-provided embedding. dense/sparse vectors are excluded because they
// require a query-time embedding ESFS does not synthesize in v1.
func semanticTextFields(caps FieldCaps) []string {
	var out []string
	for _, f := range caps.Fields {
		if f.Type == "semantic_text" {
			out = append(out, f.Name)
		}
	}
	sort.Strings(out)
	return out
}

func lexicalClause(req SearchRequest, caps FieldCaps) map[string]any {
	sqs := map[string]any{
		"query":            req.Query,
		"default_operator": "and",
	}
	// Per-index SearchFields override the default of all text fields.
	tf := req.SearchFields
	if len(tf) == 0 {
		tf = caps.TextFields()
	}
	if len(tf) > 0 {
		tf = append([]string(nil), tf...)
		sort.Strings(tf)
		fields := make([]any, len(tf))
		for i, f := range tf {
			fields[i] = f
		}
		sqs["fields"] = fields
	}
	return map[string]any{"simple_query_string": sqs}
}

// planSince resolves the timestamp field for a relative time filter and returns
// the RFC3339 lower bound anchored to the client clock (profile tz when set).
func planSince(req SearchRequest, caps FieldCaps, now time.Time) (field, gte string, err error) {
	field = req.TimeField
	if field == "" {
		field, err = inferTimeField(caps)
		if err != nil {
			return "", "", err
		}
	} else {
		// Validate the explicit field is date-typed via field_caps.
		if !isDateField(caps, field) {
			return "", "", contract.Errf(contract.KindUsage,
				"--since field %q is not a date/date_nanos field per field capabilities", field)
		}
	}
	lower := now.Add(-req.Since).UTC().Format(time.RFC3339)
	return field, lower, nil
}

func inferTimeField(caps FieldCaps) (string, error) {
	var found []string
	for _, cand := range inferableTimeFields {
		if isDateField(caps, cand) {
			found = append(found, cand)
		}
	}
	switch len(found) {
	case 1:
		return found[0], nil
	case 0:
		return "", contract.Errf(contract.KindUsage,
			"--since requires a date field; none of %v are mapped as date; configure time_field", inferableTimeFields)
	default:
		return "", contract.Errf(contract.KindUsage,
			"--since requires a configured date field; found multiple candidates %v", found)
	}
}

func isDateField(caps FieldCaps, name string) bool {
	f, ok := caps.Fields[name]
	return ok && (f.Type == "date" || f.Type == "date_nanos")
}
