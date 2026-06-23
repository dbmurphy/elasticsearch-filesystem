#!/usr/bin/env bash
# Seed the acceptance Elasticsearch with a known conversations fixture.
#
# NOTE: Creating the index/mapping and inserting documents here is TEST FIXTURE
# setup performed by the harness with curl -- not by ESFS. ESFS itself performs
# no Elasticsearch-side setup; the no-setup guard in acceptance.sh verifies that.
set -euo pipefail

ES="${ESFS_ENDPOINT:-http://localhost:9200}"
INDEX="${ESFS_INDEX:-conversations}"

now="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
old="$(date -u -d '30 days ago' +%Y-%m-%dT%H:%M:%SZ)"

echo "seed: (re)creating index $INDEX"
curl -sf -X DELETE "$ES/$INDEX" >/dev/null 2>&1 || true
curl -sf -X PUT "$ES/$INDEX" -H 'Content-Type: application/json' -d '{
  "mappings": {
    "properties": {
      "body":       { "type": "text" },
      "@timestamp": { "type": "date" },
      "status":     { "type": "keyword" }
    }
  }
}' >/dev/null

echo "seed: bulk inserting fixture documents"
curl -sf -X POST "$ES/_bulk?refresh=true" -H 'Content-Type: application/x-ndjson' --data-binary @- >/dev/null <<EOF
{"index":{"_index":"$INDEX","_id":"r1"}}
{"body":"please process my refund","status":"open","@timestamp":"$now"}
{"index":{"_index":"$INDEX","_id":"r2"}}
{"body":"refund approved last month","status":"closed","@timestamp":"$old"}
{"index":{"_index":"$INDEX","_id":"s1"}}
{"body":"customer is looking for red shoes","status":"open","@timestamp":"$now"}
{"index":{"_index":"$INDEX","_id":"s2"}}
{"body":"the red shoes were returned","status":"closed","@timestamp":"$old"}
{"index":{"_index":"$INDEX","_id":"n1"}}
{"body":"general question about shipping","status":"open","@timestamp":"$now"}
{"index":{"_index":"$INDEX","_id":"n2"}}
{"body":"password reset request","status":"open","@timestamp":"$now"}
EOF

count="$(curl -sf "$ES/$INDEX/_count" | jq -r .count)"
echo "seed: $INDEX now has $count documents"
