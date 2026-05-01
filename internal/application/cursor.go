package application

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/whiskeyjimbo/paras/internal/core/domain"
)

const (
	cursorHandlePrefix = "h:"
	cursorInlineBudget = 1024
	cursorHandleTTL    = 10 * time.Minute
	cursorStoreMax     = 10_000
)

type cursorPayload struct {
	Sort    domain.SortField       `json:"s"`
	Desc    bool                   `json:"d,omitempty"`
	Scopes  []domain.ScopeID       `json:"sc"`
	Offsets map[domain.ScopeID]int `json:"o"`
}

type cursorStore interface {
	set(handle string, p cursorPayload)
	get(handle string) (cursorPayload, bool)
}

type inMemoryCursorStore struct {
	mu      sync.Mutex
	entries map[string]cursorEntry
}

type cursorEntry struct {
	payload cursorPayload
	expiry  time.Time
}

func newInMemoryCursorStore() *inMemoryCursorStore {
	return &inMemoryCursorStore{entries: make(map[string]cursorEntry)}
}

func (s *inMemoryCursorStore) set(handle string, p cursorPayload) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.entries) >= cursorStoreMax {
		now := time.Now()
		for k, e := range s.entries {
			if now.After(e.expiry) {
				delete(s.entries, k)
			}
		}
	}
	s.entries[handle] = cursorEntry{payload: p, expiry: time.Now().Add(cursorHandleTTL)}
}

func (s *inMemoryCursorStore) get(handle string) (cursorPayload, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.entries[handle]
	if !ok || time.Now().After(e.expiry) {
		delete(s.entries, handle)
		return cursorPayload{}, false
	}
	return e.payload, true
}

func encodeCursor(key []byte, store cursorStore, p cursorPayload) (string, error) {
	data, err := json.Marshal(p)
	if err != nil {
		return "", err
	}
	mac := cursorHMAC(key, data)
	inline := string(data) + "|" + mac
	if len(inline) <= cursorInlineBudget {
		return base64.RawURLEncoding.EncodeToString([]byte(inline)), nil
	}
	var handle [32]byte
	if _, err := rand.Read(handle[:]); err != nil {
		return "", err
	}
	h := base64.RawURLEncoding.EncodeToString(handle[:])
	store.set(h, p)
	return cursorHandlePrefix + h, nil
}

func decodeCursor(key []byte, store cursorStore, s string) (cursorPayload, error) {
	if strings.HasPrefix(s, cursorHandlePrefix) {
		handle := s[len(cursorHandlePrefix):]
		p, ok := store.get(handle)
		if !ok {
			return cursorPayload{}, fmt.Errorf("%w: handle expired", domain.ErrInvalidCursor)
		}
		return p, nil
	}
	raw, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return cursorPayload{}, fmt.Errorf("%w: %v", domain.ErrInvalidCursor, err)
	}
	str := string(raw)
	idx := strings.LastIndex(str, "|")
	if idx < 0 {
		return cursorPayload{}, fmt.Errorf("%w: missing mac", domain.ErrInvalidCursor)
	}
	data := []byte(str[:idx])
	if str[idx+1:] != cursorHMAC(key, data) {
		return cursorPayload{}, fmt.Errorf("%w: invalid mac", domain.ErrInvalidCursor)
	}
	var p cursorPayload
	if err := json.Unmarshal(data, &p); err != nil {
		return cursorPayload{}, fmt.Errorf("%w: %v", domain.ErrInvalidCursor, err)
	}
	return p, nil
}

func cursorHMAC(key, data []byte) string {
	mac := hmac.New(sha256.New, key)
	mac.Write(data)
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}
