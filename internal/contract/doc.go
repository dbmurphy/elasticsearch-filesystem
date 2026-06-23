// Package contract defines the ESFS filesystem and routed-command contract in
// code: path encoding, path classification, the error model, and the syscall
// error mapping. These types are the single source of truth shared by the FUSE
// daemon (esfsd), the shared query core (escore), the routed command shims
// (esfs-grep/ls/find), and the CLI (esfs).
//
// ESFS exposes Elasticsearch as an SMFS-style filesystem:
//
//	/esfs/                         -> visible indices and aliases + virtual files
//	/esfs/<index>/                 -> documents in <index> + virtual files
//	/esfs/<index>/<id>             -> a single document rendered as JSON
//	/esfs/profile.md               -> mount-scope orientation digest (virtual)
//	/esfs/<index>/profile.md       -> index-scope orientation digest (virtual)
//	/esfs/<index>/.mapping.json    -> optional local mapping diagnostic (virtual)
//	/esfs/<index>/.fields.json     -> optional local field-caps diagnostic (virtual)
//	/esfs/.sync.json               -> mount-scope sync status (virtual)
//	/esfs/<index>/.sync.json       -> index-scope sync status (virtual)
//
// Document IDs are reversibly encoded to filesystem-safe names (see pathenc.go).
// Reserved virtual filenames never shadow real documents; a document whose ID
// collides with a reserved name is exposed through its escaped form.
package contract
