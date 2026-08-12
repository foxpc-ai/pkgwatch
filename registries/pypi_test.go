package registries

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestPyPI_ExtractPackage(t *testing.T) {
	tests := []struct {
		path     string
		wantName string
		wantNil  bool
	}{
		{"/pypi/simple/requests/", "requests", false},
		{"/pypi/simple/numpy/", "numpy", false},
		{"/pypi/", "", true},
		{"/pypi/simple/", "", true},
	}

	for _, tc := range tests {
		r := httptest.NewRequest(http.MethodGet, tc.path, nil)
		got := PyPI{}.ExtractPackage(r)

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
	}
}

func TestPyPI_FetchMetadata(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{
			"info": {"name": "requests", "version": "2.31.0"},
			"releases": {
				"2.31.0": [{"upload_time_iso_8601": "2023-05-22T15:00:00Z"}]
			}
		}`))
	}))
	defer upstream.Close()

	pkg := &PackageMeta{Name: "requests"}
	meta, err := PyPI{}.FetchMetadata(t.Context(), pkg, upstream.URL)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if meta.Name != "requests" {
		t.Errorf("got name %q, want %q", meta.Name, "requests")
	}
	if meta.Version != "2.31.0" {
		t.Errorf("got version %q, want %q", meta.Version, "2.31.0")
	}
}
