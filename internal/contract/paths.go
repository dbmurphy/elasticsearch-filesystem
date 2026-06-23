package contract

import "strings"

// NodeKind classifies a path inside the ESFS mount.
type NodeKind int

const (
	NodeRoot         NodeKind = iota // /esfs
	NodeIndexDir                     // /esfs/<index>
	NodeDocument                     // /esfs/<index>/<id>
	NodeMountProfile                 // /esfs/profile.md
	NodeMountSync                    // /esfs/.sync.json
	NodeIndexProfile                 // /esfs/<index>/profile.md
	NodeIndexSync                    // /esfs/<index>/.sync.json
	NodeIndexMapping                 // /esfs/<index>/.mapping.json
	NodeIndexFields                  // /esfs/<index>/.fields.json
	NodeInvalid                      // not a valid ESFS path
)

// Node is the parsed result of an ESFS-relative path.
type Node struct {
	Kind  NodeKind
	Index string // populated for index/document/index-virtual nodes
	ID    string // decoded document ID for NodeDocument
	// HashedName is the on-disk name when the document is exposed via a hashed
	// short name (ID could not be recovered from the name alone).
	HashedName string
}

// Virtual filenames recognized at the mount root.
const (
	FileProfile = "profile.md"
	FileSync    = ".sync.json"
	FileMapping = ".mapping.json"
	FileFields  = ".fields.json"
)

// ParsePath classifies a mount-relative slash path (without a leading slash).
// The empty string is the mount root.
//
// It intentionally does not consult Elasticsearch; it only does structural
// classification and reversible ID decoding. Existence is resolved later by the
// catalog/doc store.
func ParsePath(rel string) Node {
	rel = strings.Trim(rel, "/")
	if rel == "" {
		return Node{Kind: NodeRoot}
	}
	parts := strings.Split(rel, "/")
	switch len(parts) {
	case 1:
		switch parts[0] {
		case FileProfile:
			return Node{Kind: NodeMountProfile}
		case FileSync:
			return Node{Kind: NodeMountSync}
		}
		if !validIndexName(parts[0]) {
			return Node{Kind: NodeInvalid}
		}
		return Node{Kind: NodeIndexDir, Index: parts[0]}
	case 2:
		index, leaf := parts[0], parts[1]
		if !validIndexName(index) {
			return Node{Kind: NodeInvalid}
		}
		switch leaf {
		case FileProfile:
			return Node{Kind: NodeIndexProfile, Index: index}
		case FileSync:
			return Node{Kind: NodeIndexSync, Index: index}
		case FileMapping:
			return Node{Kind: NodeIndexMapping, Index: index}
		case FileFields:
			return Node{Kind: NodeIndexFields, Index: index}
		}
		if IsHashedName(leaf) {
			return Node{Kind: NodeDocument, Index: index, HashedName: leaf}
		}
		id, hashed, err := DecodeID(leaf)
		if err != nil || hashed {
			return Node{Kind: NodeInvalid}
		}
		return Node{Kind: NodeDocument, Index: index, ID: id}
	default:
		// ESFS does not model nested document hierarchies in v1.
		return Node{Kind: NodeInvalid}
	}
}

// validIndexName applies Elasticsearch's index-name constraints that matter for
// path safety: no slashes, control bytes, leading dot reservation handled
// separately, and within length. ESFS hides system/hidden indices by default,
// so a leading dot at this layer is treated as invalid for traversal.
func validIndexName(name string) bool {
	if name == "" || len(name) > MaxNameLen {
		return false
	}
	if strings.HasPrefix(name, ".") {
		return false
	}
	for _, r := range name {
		switch r {
		case '/', '\\', '*', '?', '"', '<', '>', '|', ' ', ',', 0:
			return false
		}
		if r < 0x20 || r == 0x7f {
			return false
		}
	}
	return true
}
