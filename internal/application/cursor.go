package application

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/whiskeyjimbo/paras/internal/core/domain"
	"github.com/whiskeyjimbo/paras/internal/server/cursorstore"
)

const (
	cursorHandlePrefix = "h:"
	cursorInlineBudget = 1024
	cursorHandleTTL    = 10 * time.Minute
)

type cursorPayload struct {
	Sort    domain.SortField       `json:"s"`
	Desc    bool                   `json:"d,omitempty"`
	Scopes  []domain.ScopeID       `json:"sc"`
	Offsets map[domain.ScopeID]int `json:"o"`
}

// cursorStore is the private interface used by encodeCursor/decodeCursor.
type cursorStore interface {
	set(ctx context.Context, handle string, p cursorPayload)
	get(ctx context.Context, handle string) (cursorPayload, bool)
}

// publicCursorStoreAdapter bridges cursorstore.CursorStore to the private interface.
type publicCursorStoreAdapter struct {
	store cursorstore.CursorStore
	ttl   time.Duration
}

func newPublicCursorStoreAdapter(s cursorstore.CursorStore) *publicCursorStoreAdapter {
	return &publicCursorStoreAdapter{store: s, ttl: cursorHandleTTL}
}

func (a *publicCursorStoreAdapter) set(ctx context.Context, handle string, p cursorPayload) {
	data, err := json.Marshal(p)
	if err != nil {
		return
	}
	_ = a.store.Put(ctx, handle, cursorstore.Cursor{Data: data}, a.ttl)
}

func (a *publicCursorStoreAdapter) get(ctx context.Context, handle string) (cursorPayload, bool) {
	c, err := a.store.Get(ctx, handle)
	if err != nil {
		return cursorPayload{}, false
	}
	var p cursorPayload
	if err := json.Unmarshal(c.Data, &p); err != nil {
		return cursorPayload{}, false
	}
	return p, true
}

func newDefaultCursorStore() cursorStore {
	return newPublicCursorStoreAdapter(cursorstore.NewInMemory())
}

func encodeCursor(ctx context.Context, key []byte, store cursorStore, p cursorPayload) (string, error) {
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
	store.set(ctx, h, p)
	return cursorHandlePrefix + h, nil
}

func decodeCursor(ctx context.Context, key []byte, store cursorStore, s string) (cursorPayload, error) {
	if strings.HasPrefix(s, cursorHandlePrefix) {
		handle := s[len(cursorHandlePrefix):]
		p, ok := store.get(ctx, handle)
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
