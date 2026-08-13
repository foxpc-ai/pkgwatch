package proxy

import (
	"testing"
	"time"

	"github.com/foxpc-ai/pkgwatch/config"
	"github.com/foxpc-ai/pkgwatch/registries"
)

func TestCheckRules_MinAge(t *testing.T) {
	cfg := &config.Policy{
		Rules: config.Rules{MinAgeDays: 7},
	}

	tests := []struct {
		name        string
		publishedAt time.Time
		wantBlocked bool
	}{
		{"too new", time.Now().Add(-2 * 24 * time.Hour), true},
		{"old enough", time.Now().Add(-10 * 24 * time.Hour), false},
		{"exactly at boundary", time.Now().Add(-7 * 24 * time.Hour), false},
	}

	for _, tc := range tests {
		meta := &registries.PackageMeta{Name: "pkg", Version: "1.0.0", PublishedAt: tc.publishedAt}
		violation := checkRules(cfg, meta)
		blocked := violation != ""
		if blocked != tc.wantBlocked {
			t.Errorf("%s: wantBlocked=%v, got violation=%q", tc.name, tc.wantBlocked, violation)
		}
	}
}

func TestIsAllowed(t *testing.T) {
	cfg := &config.Policy{
		Overrides: config.Overrides{Allow: []string{"trusted-pkg", "internal-lib"}},
	}

	if !isAllowed(cfg, "trusted-pkg") {
		t.Error("expected trusted-pkg to be allowed")
	}
	if isAllowed(cfg, "unknown-pkg") {
		t.Error("expected unknown-pkg to not be allowed")
	}
}

func TestEvictExpired(t *testing.T) {
	handler := NewHandler(&config.Policy{})
	key := "pypi/example"
	expired := &cacheEntry{expiresAt: time.Now().Add(-time.Minute)}
	handler.cache.Store(key, expired)

	if !handler.evictExpired(key, expired, time.Now()) {
		t.Fatal("expected expired cache entry to be evicted")
	}
	if _, ok := handler.cache.Load(key); ok {
		t.Error("expired cache entry remains stored")
	}
}

func TestEvictExpiredDoesNotDeleteReplacement(t *testing.T) {
	handler := NewHandler(&config.Policy{})
	key := "pypi/example"
	stale := &cacheEntry{expiresAt: time.Now().Add(-time.Minute)}
	replacement := &cacheEntry{expiresAt: time.Now().Add(time.Minute)}
	handler.cache.Store(key, replacement)

	if handler.evictExpired(key, stale, time.Now()) {
		t.Fatal("stale expiry unexpectedly deleted a replacement")
	}
	got, ok := handler.cache.Load(key)
	if !ok || got != replacement {
		t.Error("replacement cache entry was not preserved")
	}
}
