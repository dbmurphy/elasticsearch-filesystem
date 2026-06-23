# ESFS

Elasticsearch mounted as an SMFS-style filesystem.

- Write policy: safe-create-update
- Delete sync: false
- Profiles: local-only, no Elasticsearch-side setup

## Visible indices

- `conversations` (index)
- `orders` (index)

## Examples

```sh
ls /esfs
cat /esfs/<index>/<id>
cp updated.json /esfs/<index>/<id>   # writes back to Elasticsearch
eval "$(esfs env)"
grep "red shoes" --since 7d /esfs/<index>
```
