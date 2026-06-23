package contract

import (
	"errors"
	"fmt"
	"syscall"
)

// Kind classifies an ESFS error so it can be mapped consistently to a POSIX
// errno for filesystem callers and to an exit code / diagnostic for the CLI and
// routed command shims.
type Kind int

const (
	// KindInternal is an unexpected ESFS bug; mapped to EIO.
	KindInternal Kind = iota
	// KindNotFound is a missing document, index, or path; mapped to ENOENT.
	KindNotFound
	// KindPermission is an authn/authz failure; mapped to EACCES.
	KindPermission
	// KindReadOnly is a write to a read-only profile/mount; mapped to EROFS.
	KindReadOnly
	// KindUnsupported is an operation ESFS does not implement (chmod, rename to
	// a new ID, multi-index alias write, etc.); mapped to EPERM.
	KindUnsupported
	// KindInvalidJSON is a flush of content that is not a JSON object; mapped to
	// EINVAL.
	KindInvalidJSON
	// KindConflict is an optimistic-concurrency version conflict; mapped to
	// EAGAIN.
	KindConflict
	// KindUpstream is an Elasticsearch timeout/unavailable/shard failure; mapped
	// to EIO.
	KindUpstream
	// KindNameTooLong is an unencodable/over-long path segment; mapped to
	// ENAMETOOLONG.
	KindNameTooLong
	// KindUsage is a CLI/grep usage or query-planning diagnostic; not a
	// filesystem error. Mapped to EINVAL for FS callers and exit code 2 for the
	// CLI.
	KindUsage
)

// Error is the canonical ESFS error. It carries a Kind for errno/exit mapping
// plus a human-readable, diagnostic-friendly message.
type Error struct {
	Kind Kind
	Msg  string
	Err  error // optional wrapped cause
}

func (e *Error) Error() string {
	if e.Err != nil {
		return fmt.Sprintf("%s: %v", e.Msg, e.Err)
	}
	return e.Msg
}

func (e *Error) Unwrap() error { return e.Err }

// Errf builds an *Error with a formatted message.
func Errf(k Kind, format string, args ...any) *Error {
	return &Error{Kind: k, Msg: fmt.Sprintf(format, args...)}
}

// Wrap builds an *Error wrapping a cause.
func Wrap(k Kind, cause error, format string, args ...any) *Error {
	return &Error{Kind: k, Msg: fmt.Sprintf(format, args...), Err: cause}
}

// KindOf extracts the Kind from any error, defaulting to KindInternal.
func KindOf(err error) Kind {
	var e *Error
	if errors.As(err, &e) {
		return e.Kind
	}
	return KindInternal
}

// Errno maps an error to the POSIX errno used by the FUSE layer.
func Errno(err error) syscall.Errno {
	switch KindOf(err) {
	case KindNotFound:
		return syscall.ENOENT
	case KindPermission:
		return syscall.EACCES
	case KindReadOnly:
		return syscall.EROFS
	case KindUnsupported:
		return syscall.EPERM
	case KindInvalidJSON, KindUsage:
		return syscall.EINVAL
	case KindConflict:
		return syscall.EAGAIN
	case KindNameTooLong:
		return syscall.ENAMETOOLONG
	case KindUpstream, KindInternal:
		return syscall.EIO
	default:
		return syscall.EIO
	}
}

// ExitCode maps an error to a CLI/shim exit code. ESFS follows grep's broad
// convention: 0 match, 1 no match, 2 error. All errors here are exit code 2;
// callers represent "no match" separately.
func ExitCode(err error) int {
	if err == nil {
		return 0
	}
	return 2
}
