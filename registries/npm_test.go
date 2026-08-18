package registries

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestNPM_ExtractPackage(t *testing.T) {
	tests := []struct {
		path        string
		wantName    string
		wantVersion string
		wantNil     bool
	}{
		{"/npm/lodash", "lodash", "", false},
		{"/npm/lodash/4.17.21", "lodash", "4.17.21", false},
		{"/npm/@babel/core", "@babel/core", "", false},
		{"/npm/@babel/core/7.0.0", "@babel/core", "7.0.0", false},
		{"/npm/", "", "", true},
		{"/npm/@scope", "", "", true},
	}

	for _, tc := range tests {
		r := httptest.NewRequest(http.MethodGet, tc.path, nil)
		got := NPM{}.ExtractPackage(r)

		if tc.wantNil {
			if got != nil {
				t.Errorf("path %q: expected nil, got %+v", tc.path, got)
			}
			continue
		}
		if got == nil {
			t.Errorf("path %q: expected package %q, got nil", tc.path, tc.wantName)
			continue
		}
		if got.Name != tc.wantName {
			t.Errorf("path %q: got name %q, want %q", tc.path, got.Name, tc.wantName)
		}
		if got.Version != tc.wantVersion {
			t.Errorf("path %q: got version %q, want %q", tc.path, got.Version, tc.wantVersion)
		}
	}
}

func TestNPM_FetchMetadata(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{
			"name": "lodash",
			"dist-tags": { "latest": "4.17.21" },
			"time": { "4.17.21": "2021-02-20T15:42:16Z" }
		}`))
	}))
	defer upstream.Close()

	pkg := &PackageMeta{Name: "lodash"}
	meta, err := NPM{}.FetchMetadata(t.Context(), pkg, upstream.URL)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if meta.Name != "lodash" {
		t.Errorf("got name %q, want %q", meta.Name, "lodash")
	}
	if meta.Version != "4.17.21" {
		t.Errorf("got version %q, want %q", meta.Version, "4.17.21")
	}
	if meta.IsPrerelease {
		t.Error("expected IsPrerelease=false for stable version")
	}
}

func TestNPM_FetchMetadata_Prerelease(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{
			"name": "some-pkg",
			"dist-tags": { "latest": "2.0.0-beta.1" },
			"time": { "2.0.0-beta.1": "2024-01-10T10:00:00Z" }
		}`))
	}))
	defer upstream.Close()

	pkg := &PackageMeta{Name: "some-pkg"}
	meta, err := NPM{}.FetchMetadata(t.Context(), pkg, upstream.URL)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !meta.IsPrerelease {
		t.Error("expected IsPrerelease=true for beta version")
	}
}

func TestIsNPMPrerelease(t *testing.T) {
	tests := []struct {
		version string
		want    bool
	}{
		{"1.0.0", false},
		{"1.0.0-beta.1", true},
		{"1.0.0-beta.1+build-1", true},
		{"1.0.0+build-1", false},
	}

	for _, tc := range tests {
		t.Run(tc.version, func(t *testing.T) {
			if got := isNPMPrerelease(tc.version); got != tc.want {
				t.Errorf("isNPMPrerelease(%q) = %v, want %v", tc.version, got, tc.want)
			}
		})
	}
}

func TestNPM_FetchMetadata_NotFound(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer upstream.Close()

	_, err := NPM{}.FetchMetadata(t.Context(), &PackageMeta{Name: "ghost-pkg"}, upstream.URL)
	if err == nil {
		t.Error("expected error for 404, got nil")
	}
}

func TestNPM_FetchVersionIndex_ScopedPackagePath(t *testing.T) {
	var requestURI string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestURI = r.RequestURI
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"name":"@babel/core",
			"dist-tags":{"latest":"7.0.0"},
			"time":{"7.0.0":"2018-08-27T00:00:00Z"}
		}`))
	}))
	defer upstream.Close()

	_, err := (NPM{}).FetchVersionIndex(t.Context(), &PackageMeta{Name: "@babel/core"}, upstream.URL)
	if err != nil {
		t.Fatalf("FetchVersionIndex() error: %v", err)
	}
	if requestURI != "/@babel/core" {
		t.Errorf("upstream request URI = %q, want %q", requestURI, "/@babel/core")
	}
}

func TestNPM_FilterResponse(t *testing.T) {
	body := `{
		"dist-tags": {"latest":"2.0.0-beta.1","stable":"1.0.0"},
		"versions": {"1.0.0":{"name":"pkg"},"2.0.0-beta.1":{"name":"pkg"}},
		"time": {"1.0.0":"2024-01-01T00:00:00Z","2.0.0-beta.1":"2024-02-01T00:00:00Z"}
	}`
	resp := &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}
	resp.Header.Set("Content-Type", "application/json")

	if err := (NPM{}).FilterResponse(resp, map[string]bool{"1.0.0": true}); err != nil {
		t.Fatalf("FilterResponse() error: %v", err)
	}
	filtered, _ := io.ReadAll(resp.Body)
	got := string(filtered)
	if strings.Contains(got, "2.0.0-beta.1") {
		t.Errorf("blocked version remains in response: %s", got)
	}
	if !strings.Contains(got, "1.0.0") {
		t.Errorf("allowed version missing from response: %s", got)
	}
}

func TestNPM_FilterResponse_MissingVersionsField(t *testing.T) {
	body := `{
		"dist-tags": {"latest":"1.0.0"},
		"time": {"1.0.0":"2024-01-01T00:00:00Z"}
	}`
	resp := &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}
	resp.Header.Set("Content-Type", "application/json")

	if err := (NPM{}).FilterResponse(resp, map[string]bool{"1.0.0": true}); err != nil {
		t.Fatalf("FilterResponse() unexpected error: %v", err)
	}
}
