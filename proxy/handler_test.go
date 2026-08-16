package proxy

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
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

func TestIsOverrideAllowAll(t *testing.T) {
	cfg := &config.Policy{
		Overrides: config.Overrides{OverrideCommand: "apple"},
	}
	req := httptest.NewRequest(http.MethodGet, "/pypi/simple/requests/?apple", nil)

	if !isOverrideAllowAll(cfg, req) {
		t.Error("expected override token in query to enable global override")
	}

	req = httptest.NewRequest(http.MethodGet, "/pypi/simple/requests/", nil)
	if isOverrideAllowAll(cfg, req) {
		t.Error("expected override to remain disabled when token is absent")
	}

	cfg.Overrides.OverrideCommand = ""
	req = httptest.NewRequest(http.MethodGet, "/pypi/simple/requests/?apple", nil)
	if isOverrideAllowAll(cfg, req) {
		t.Error("expected empty override command in policy to disable overrides")
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

func TestHandlerFiltersPyPIPreleases(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/pypi/numpy/json":
			_, _ = w.Write([]byte(`{
				"info":{"name":"numpy","version":"2.0.0"},
				"releases":{
					"2.0.0":[{"filename":"numpy-2.0.0.tar.gz","upload_time_iso_8601":"2024-06-16T00:00:00Z"}],
					"2.0.0a1":[{"filename":"numpy-2.0.0a1.tar.gz","upload_time_iso_8601":"2023-10-14T00:00:00Z"}]
				}
			}`))
		case "/simple/numpy/":
			_, _ = w.Write([]byte(`{
				"meta":{"api-version":"1.1"},
				"name":"numpy",
				"files":[
					{"filename":"numpy-2.0.0.tar.gz","url":"../../files/numpy-2.0.0.tar.gz"},
					{"filename":"numpy-2.0.0a1.tar.gz","url":"../../files/numpy-2.0.0a1.tar.gz"}
				],
				"versions":["2.0.0","2.0.0a1"]
			}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer upstream.Close()

	handler := NewHandler(&config.Policy{
		Upstreams: map[string]string{"pypi": upstream.URL},
		Rules:     config.Rules{BlockPrerelease: true},
	})
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/pypi/simple/numpy/", nil))

	response := recorder.Result()
	defer response.Body.Close()
	body, _ := io.ReadAll(response.Body)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, body = %s", response.StatusCode, body)
	}
	if got := response.Header.Get("X-Pkgwatch-Blocked-Versions"); got != "2.0.0a1" {
		t.Errorf("X-Pkgwatch-Blocked-Versions = %q, want %q", got, "2.0.0a1")
	}
	if strings.Contains(string(body), "2.0.0a1") {
		t.Errorf("pre-release remains in PyPI index: %s", body)
	}
	if !strings.Contains(string(body), "2.0.0") {
		t.Errorf("stable release missing from PyPI index: %s", body)
	}
}

func TestHandler_InvalidUpstreamReturnsInternalServerError(t *testing.T) {
	handler := NewHandler(&config.Policy{
		Upstreams: map[string]string{"pypi": "::not-a-valid-url::"},
	})
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/pypi/simple/requests/", nil))

	if recorder.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusBadGateway)
	}
}

func TestForwardPreservesUpstreamBasePath(t *testing.T) {
	var gotPath string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.WriteHeader(http.StatusNoContent)
	}))
	defer upstream.Close()

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/npm/lodash", nil)
	if err := forward(recorder, request, upstream.URL+"/registry", "/npm", false, nil); err != nil {
		t.Fatalf("forward() error: %v", err)
	}

	if gotPath != "/registry/lodash" {
		t.Errorf("upstream path = %q, want %q", gotPath, "/registry/lodash")
	}
}
