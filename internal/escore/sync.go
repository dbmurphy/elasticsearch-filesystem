package escore

import (
	"context"
	"encoding/json"
	"sync"
	"time"
)

// SyncEvent records the last sync activity for a scope, surfaced via .sync.json.
type SyncEvent struct {
	LastWrite   *time.Time `json:"last_write,omitempty"`
	LastRefresh *time.Time `json:"last_refresh,omitempty"`
	LastError   string     `json:"last_error,omitempty"`
	LastErrorAt *time.Time `json:"last_error_at,omitempty"`
	PendingDocs int        `json:"pending_docs"`
}

// SyncTracker maintains sync state for the mount and per index, and exposes it
// as the virtual .sync.json files.
type SyncTracker struct {
	mu     sync.Mutex
	now    func() time.Time
	mount  SyncEvent
	byIdx  map[string]*SyncEvent
	policy SyncPolicy
}

// SyncPolicy is the policy summary embedded in .sync.json so users can see how
// the mount is configured.
type SyncPolicy struct {
	WritePolicy string `json:"write_policy"`
	DeleteSync  bool   `json:"delete_sync"`
	ReadOnly    bool   `json:"read_only"`
}

// NewSyncTracker builds a tracker. now may be nil (defaults to time.Now).
func NewSyncTracker(now func() time.Time, pol SyncPolicy) *SyncTracker {
	if now == nil {
		now = time.Now
	}
	return &SyncTracker{now: now, byIdx: map[string]*SyncEvent{}, policy: pol}
}

func (s *SyncTracker) idx(index string) *SyncEvent {
	if s.byIdx[index] == nil {
		s.byIdx[index] = &SyncEvent{}
	}
	return s.byIdx[index]
}

// RecordWrite notes a successful write for an index scope.
func (s *SyncTracker) RecordWrite(index string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	t := s.now()
	s.mount.LastWrite = &t
	e := s.idx(index)
	e.LastWrite = &t
}

// RecordError notes a failed sync for an index scope.
func (s *SyncTracker) RecordError(index string, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	t := s.now()
	msg := err.Error()
	s.mount.LastError = msg
	s.mount.LastErrorAt = &t
	e := s.idx(index)
	e.LastError = msg
	e.LastErrorAt = &t
}

// RecordRefresh notes a remote-refresh check for an index scope.
func (s *SyncTracker) RecordRefresh(index string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	t := s.now()
	s.mount.LastRefresh = &t
	s.idx(index).LastRefresh = &t
}

// SetPending sets the count of staged-but-unflushed docs for an index.
func (s *SyncTracker) SetPending(index string, n int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.idx(index).PendingDocs = n
}

type syncDoc struct {
	Scope  string     `json:"scope"`
	Policy SyncPolicy `json:"policy"`
	Status SyncEvent  `json:"status"`
}

// MountJSON renders mount-scope .sync.json.
func (s *SyncTracker) MountJSON() []byte {
	s.mu.Lock()
	defer s.mu.Unlock()
	b, _ := json.MarshalIndent(syncDoc{Scope: "mount", Policy: s.policy, Status: s.mount}, "", "  ")
	return append(b, '\n')
}

// IndexJSON renders index-scope .sync.json.
func (s *SyncTracker) IndexJSON(index string) []byte {
	s.mu.Lock()
	defer s.mu.Unlock()
	st := SyncEvent{}
	if e := s.byIdx[index]; e != nil {
		st = *e
	}
	b, _ := json.MarshalIndent(syncDoc{Scope: index, Policy: s.policy, Status: st}, "", "  ")
	return append(b, '\n')
}

// ProfileGenerator renders virtual profile.md content. Implemented by the
// profile package; declared here to avoid an import cycle with vfs.
type ProfileGenerator interface {
	MountProfile(ctx context.Context) ([]byte, error)
	IndexProfile(ctx context.Context, index string) ([]byte, error)
}
