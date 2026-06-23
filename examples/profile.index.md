# conversations

- Approx documents: 1

## Search

- Mode: semantic (auto; falls back to ranked-lexical if unavailable)
- Semantic fields: body_semantic
- Lexical search fields (configured): subject, body
- Time filtering (`--since`) via: `@timestamp`
- Writes: safe-create-update; delete sync: false

## Fields

- `@timestamp`: date
- `body`: text
- `body_semantic`: semantic_text
- `customer_email`: keyword
- `status`: keyword
- `subject`: text

## Sample document shape

```json
{
  "@timestamp": "2026-06-23T10:00:00Z",
  "body": "please process my refund",
  "customer_email": "<redacted>",
  "status": "open",
  "subject": "refund request"
}
```

## Try it

```sh
grep <term> /esfs/conversations
grep <term> --since 7d /esfs/conversations
```

_redacted fields: customer_email_
