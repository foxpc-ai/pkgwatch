package registries

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
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

func TestPyPI_FetchVersionIndex_ClassifiesPrerelease(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"info": {"name": "numpy", "version": "2.0.0"},
			"releases": {
				"2.0.0": [{"filename":"numpy-2.0.0.tar.gz","upload_time_iso_8601":"2024-06-16T00:00:00Z"}],
				"2.0.0a1": [{"filename":"numpy-2.0.0a1.tar.gz","upload_time_iso_8601":"2023-10-14T00:00:00Z"}]
			}
		}`))
	}))
	defer upstream.Close()

	index, err := (PyPI{}).FetchVersionIndex(t.Context(), &PackageMeta{Name: "numpy"}, upstream.URL)
	if err != nil {
		t.Fatalf("FetchVersionIndex() error: %v", err)
	}
	if !index.Versions["2.0.0a1"].IsPrerelease {
		t.Error("expected numpy@2.0.0a1 to be classified as a pre-release")
	}
	if index.Files["numpy-2.0.0a1.tar.gz"] != "2.0.0a1" {
		t.Error("expected pre-release file to map to its exact version")
	}
}

func TestPyPI_PrereleaseClassification(t *testing.T) {
	tests := []struct {
		version string
		want    bool
	}{
		{"1.0", false},
		{"1.0+beta", false},
		{"1.0a1", true},
		{"1.0alpha1", true},
		{"1.0beta1", true},
		{"1.0preview1", true},
		{"1.0-rc1", true},
		{"1.0.dev1", true},
	}

	for _, tc := range tests {
		t.Run(tc.version, func(t *testing.T) {
			if got := isPyPIPreRelease(tc.version); got != tc.want {
				t.Errorf("isPyPIPreRelease(%q) = %v, want %v", tc.version, got, tc.want)
			}
		})
	}
}

func TestPyPI_FilterResponse(t *testing.T) {
	body := `{
		"meta":{"api-version":"1.1"},
		"name":"demo",
		"files":[
			{"filename":"demo-1.0.0.tar.gz","url":"../../files/demo-1.0.0.tar.gz"},
			{"filename":"demo-2.0.0rc1.tar.gz","url":"../../files/demo-2.0.0rc1.tar.gz"}
		],
		"versions":["1.0.0","2.0.0rc1"]
	}`
	resp := &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}
	resp.Header.Set("Content-Type", "application/vnd.pypi.simple.v1+json")
	files := map[string]string{"demo-1.0.0.tar.gz": "1.0.0", "demo-2.0.0rc1.tar.gz": "2.0.0rc1"}

	if err := (PyPI{}).FilterResponse(resp, files, map[string]bool{"1.0.0": true}); err != nil {
		t.Fatalf("FilterResponse() error: %v", err)
	}
	filtered, _ := io.ReadAll(resp.Body)
	got := string(filtered)
	if strings.Contains(got, "2.0.0rc1") {
		t.Errorf("blocked version remains in response: %s", got)
	}
	if !strings.Contains(got, "1.0.0") {
		t.Errorf("allowed version missing from response: %s", got)
	}
}
