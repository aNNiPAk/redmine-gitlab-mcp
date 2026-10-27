package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestRetryAndCache(t *testing.T) {
	var n atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if n.Add(1) < 3 {
			w.WriteHeader(503)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()
	c := NewClient(srv.URL, "secret", "PRIVATE-TOKEN")
	ctx := context.Background()
	result, _, e := c.JSON(ctx, "api", false)
	if e != nil || Map(result)["ok"] != true || n.Load() != 3 {
		t.Fatalf("retry failed: %v, %#v n=%d", e, result, n.Load())
	}
	_, _, e = c.JSON(ctx, "api", false)
	if e != nil || n.Load() != 3 {
		t.Fatal("cache miss")
	}
	_, _, e = c.JSON(ctx, "api", true)
	if e != nil || n.Load() != 4 {
		t.Fatal("refresh didn't bypass cache")
	}
}
func TestNeverRetry403(t *testing.T) {
	var n atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { n.Add(1); w.WriteHeader(403) }))
	defer srv.Close()
	c := NewClient(srv.URL, "secret", "PRIVATE-TOKEN")
	_, _, e := c.JSON(context.Background(), "x", false)
	if e == nil || n.Load() != 1 {
		t.Fatalf("403 retried: %v", n.Load())
	}
}
func TestOversizedResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("1234567890")) }))
	defer srv.Close()
	c := NewClient(srv.URL, "secret", "PRIVATE-TOKEN")
	_, _, e := c.Get(context.Background(), c.URL("test"), 5)
	if e == nil {
		t.Fatal("oversize body accepted")
	}
}
func TestRedmineAttachmentSSRF(t *testing.T) {
	if _, e := CheckRedmineAttachment("https://redmine.local", "https://evil.test/attachments/download/123"); e == nil {
		t.Fatal("external attachment allowed")
	}
	if _, e := CheckRedmineAttachment("https://redmine.local/app", "https://redmine.local/attachments/download/123"); e == nil {
		t.Fatal("prefix bypass allowed")
	}
}
func TestCacheExpiration(t *testing.T) {
	c := NewCache()
	c.Put("a", []byte(`{}`), http.Header{})
	c.mu.Lock()
	v := c.entries["a"]
	v.Expires = time.Now().Add(-time.Second)
	c.entries["a"] = v
	c.mu.Unlock()
	if _, _, ok := c.Get("a"); ok {
		t.Fatal("expired entry served")
	}
}
