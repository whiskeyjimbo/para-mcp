package cursorstore_test

import (
	"context"
	"testing"
	"time"

	"github.com/whiskeyjimbo/para-mcp/internal/server/cursorstore"
)

func TestInMemory_PutGet(t *testing.T) {
	s := cursorstore.NewInMemory()
	ctx := context.Background()
	c := cursorstore.Cursor{Data: []byte(`{"s":"updated_at"}`)}
	if err := s.Put(ctx, "h1", c, time.Minute); err != nil {
		t.Fatal(err)
	}
	got, err := s.Get(ctx, "h1")
	if err != nil {
		t.Fatal(err)
	}
	if string(got.Data) != string(c.Data) {
		t.Errorf("want %s, got %s", c.Data, got.Data)
	}
}

func TestInMemory_Expired(t *testing.T) {
	s := cursorstore.NewInMemory()
	ctx := context.Background()
	_ = s.Put(ctx, "h2", cursorstore.Cursor{Data: []byte("{}")}, time.Nanosecond)
	time.Sleep(time.Millisecond)
	_, err := s.Get(ctx, "h2")
	if err == nil {
		t.Fatal("want error for expired cursor, got nil")
	}
}

func TestInMemory_Delete(t *testing.T) {
	s := cursorstore.NewInMemory()
	ctx := context.Background()
	_ = s.Put(ctx, "h3", cursorstore.Cursor{Data: []byte("{}")}, time.Minute)
	_ = s.Delete(ctx, "h3")
	_, err := s.Get(ctx, "h3")
	if err == nil {
		t.Fatal("want error after delete, got nil")
	}
}

func TestInMemory_MaxEntries(t *testing.T) {
	s := cursorstore.NewInMemory(cursorstore.WithMaxEntries(2))
	ctx := context.Background()
	// Fill to max with short TTL so eviction can happen.
	_ = s.Put(ctx, "a", cursorstore.Cursor{Data: []byte("{}")}, time.Nanosecond)
	_ = s.Put(ctx, "b", cursorstore.Cursor{Data: []byte("{}")}, time.Nanosecond)
	time.Sleep(time.Millisecond)
	// Third put triggers eviction of expired entries.
	_ = s.Put(ctx, "c", cursorstore.Cursor{Data: []byte("{}")}, time.Minute)
	if _, err := s.Get(ctx, "c"); err != nil {
		t.Fatal("entry c should survive after eviction")
	}
}
