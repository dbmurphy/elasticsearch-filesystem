package contract

import (
	"crypto/sha256"
	"encoding/base32"
	"strings"
	"unicode/utf8"
)

// MaxNameLen is the conservative cross-platform filename byte limit (NAME_MAX is
// 255 on Linux ext4/APFS). Encoded names must not exceed it.
const MaxNameLen = 255

// escapePrefix marks an encoded filename. A raw document ID that begins with
// this prefix is always encoded so encoding round-trips.
const escapePrefix = "@"

const (
	encReversible = "@e=" // percent-style reversible encoding
	encHashed     = "@h=" // hash-based short name; needs a reverse lookup map
)

// b32 is lowercase, no padding, for compact hashed names.
var b32 = base32.StdEncoding.WithPadding(base32.NoPadding)

// ReservedNames are virtual filenames that never refer to a document. A document
// whose ID equals one of these is exposed through its encoded form instead.
var ReservedNames = map[string]struct{}{
	"profile.md":    {},
	".mapping.json": {},
	".fields.json":  {},
	".sync.json":    {},
	".esfs-info":    {},
}

// IsReserved reports whether name is a reserved virtual filename.
func IsReserved(name string) bool {
	_, ok := ReservedNames[name]
	return ok
}

// isRawSafe reports whether id can be used verbatim as a filename segment: valid
// UTF-8, within length, no path/control bytes, not reserved, and not starting
// with the escape prefix.
func isRawSafe(id string) bool {
	if id == "" || len(id) > MaxNameLen {
		return false
	}
	if strings.HasPrefix(id, escapePrefix) {
		return false
	}
	if IsReserved(id) {
		return false
	}
	if !utf8.ValidString(id) {
		return false
	}
	for _, r := range id {
		if r == '/' || r == 0 || r < 0x20 || r == 0x7f {
			return false
		}
	}
	return true
}

// EncodeID renders a document ID as a filesystem-safe name.
//
//   - Safe IDs are returned verbatim.
//   - Otherwise a reversible "@e=" percent encoding is used when it fits.
//   - Over-long IDs use a deterministic "@h=" short name; the (name -> id)
//     mapping must be recorded by the caller (DocStore) so reads can resolve it.
//
// hashed reports whether the result is a hashed short name requiring a reverse
// lookup to decode.
func EncodeID(id string) (name string, hashed bool) {
	if isRawSafe(id) {
		return id, false
	}
	enc := encReversible + percentEscape(id)
	if len(enc) <= MaxNameLen {
		return enc, false
	}
	sum := sha256.Sum256([]byte(id))
	return encHashed + strings.ToLower(b32.EncodeToString(sum[:20])), true
}

// DecodeID reverses EncodeID for raw and reversible names. Hashed names cannot
// be decoded from the name alone; callers must consult the reverse map and
// should surface a KindNameTooLong/KindNotFound diagnostic otherwise.
func DecodeID(name string) (id string, hashed bool, err error) {
	switch {
	case strings.HasPrefix(name, encReversible):
		raw, derr := percentUnescape(strings.TrimPrefix(name, encReversible))
		if derr != nil {
			return "", false, Wrap(KindNotFound, derr, "invalid encoded name %q", name)
		}
		return raw, false, nil
	case strings.HasPrefix(name, encHashed):
		return "", true, nil
	default:
		if IsReserved(name) {
			return "", false, Errf(KindNotFound, "%q is a reserved virtual name, not a document", name)
		}
		return name, false, nil
	}
}

// IsHashedName reports whether a name is a hashed short name.
func IsHashedName(name string) bool { return strings.HasPrefix(name, encHashed) }

// unreserved is the RFC 3986 unreserved set; these bytes are never escaped.
func isUnreserved(b byte) bool {
	switch {
	case b >= 'A' && b <= 'Z', b >= 'a' && b <= 'z', b >= '0' && b <= '9':
		return true
	case b == '-' || b == '_' || b == '.' || b == '~':
		return true
	default:
		return false
	}
}

const hexdigits = "0123456789ABCDEF"

func percentEscape(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		if isUnreserved(c) {
			b.WriteByte(c)
			continue
		}
		b.WriteByte('%')
		b.WriteByte(hexdigits[c>>4])
		b.WriteByte(hexdigits[c&0x0f])
	}
	return b.String()
}

func percentUnescape(s string) (string, error) {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c != '%' {
			b.WriteByte(c)
			continue
		}
		if i+2 >= len(s) {
			return "", Errf(KindNotFound, "truncated percent escape")
		}
		hi, ok1 := unhex(s[i+1])
		lo, ok2 := unhex(s[i+2])
		if !ok1 || !ok2 {
			return "", Errf(KindNotFound, "invalid percent escape")
		}
		b.WriteByte(hi<<4 | lo)
		i += 2
	}
	return b.String(), nil
}

func unhex(c byte) (byte, bool) {
	switch {
	case c >= '0' && c <= '9':
		return c - '0', true
	case c >= 'a' && c <= 'f':
		return c - 'a' + 10, true
	case c >= 'A' && c <= 'F':
		return c - 'A' + 10, true
	default:
		return 0, false
	}
}
