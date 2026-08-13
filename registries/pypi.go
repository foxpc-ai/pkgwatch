package registries

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

type PackageMeta struct {
	Name         string
	Version      string
	PublishedAt  time.Time
	IsPrerelease bool
}

type VersionIndex struct {
	Latest   string
	Versions map[string]*PackageMeta
	Files    map[string]string
}

type Registry interface {
	ExtractPackage(r *http.Request) *PackageMeta
	FetchMetadata(ctx context.Context, pkg *PackageMeta, upstream string) (*PackageMeta, error)
	FetchVersionIndex(ctx context.Context, pkg *PackageMeta, upstream string) (*VersionIndex, error)
}

type PyPI struct{}

var httpClient = &http.Client{Timeout: 10 * time.Second}

// matches PEP 440 pre-release markers: alpha (aN), beta (bN), rc (rcN), dev
var prereleaseRE = regexp.MustCompile(`(?i)\d(a|b|rc)\d|\.dev\d`)

func (PyPI) ExtractPackage(r *http.Request) *PackageMeta {
	path := strings.TrimPrefix(r.URL.Path, "/pypi")
	parts := strings.Split(strings.Trim(path, "/"), "/")
	if len(parts) < 2 || parts[0] != "simple" {
		return nil
	}
	return &PackageMeta{Name: parts[1]}
}

type pypiAPIResponse struct {
	Info struct {
		Name    string `json:"name"`
		Version string `json:"version"`
	} `json:"info"`
	Releases map[string][]struct {
		Filename   string `json:"filename"`
		UploadTime string `json:"upload_time_iso_8601"`
	} `json:"releases"`
}

func (PyPI) FetchMetadata(ctx context.Context, pkg *PackageMeta, upstream string) (*PackageMeta, error) {
	index, err := (PyPI{}).FetchVersionIndex(ctx, pkg, upstream)
	if err != nil {
		return nil, err
	}
	meta := index.Versions[index.Latest]
	if meta == nil {
		return nil, fmt.Errorf("no release files found for %s@%s", pkg.Name, index.Latest)
	}
	return meta, nil
}

func (PyPI) FetchVersionIndex(ctx context.Context, pkg *PackageMeta, upstream string) (*VersionIndex, error) {
	apiURL := fmt.Sprintf("%s/pypi/%s/json", strings.TrimRight(upstream, "/"), url.PathEscape(pkg.Name))

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, apiURL, nil)
	if err != nil {
		return nil, err
	}

	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return nil, fmt.Errorf("package %q not found on PyPI", pkg.Name)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("PyPI API returned %d", resp.StatusCode)
	}

	var data pypiAPIResponse
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		return nil, err
	}

	index := &VersionIndex{
		Latest:   data.Info.Version,
		Versions: make(map[string]*PackageMeta),
		Files:    make(map[string]string),
	}
	for version, files := range data.Releases {
		var published time.Time
		for _, file := range files {
			uploaded, err := time.Parse(time.RFC3339, file.UploadTime)
			if err != nil {
				return nil, fmt.Errorf("could not parse release date for %s@%s: %w", pkg.Name, version, err)
			}
			if uploaded.After(published) {
				published = uploaded
			}
			if file.Filename != "" {
				index.Files[file.Filename] = version
			}
		}
		if !published.IsZero() {
			index.Versions[version] = &PackageMeta{
				Name:         data.Info.Name,
				Version:      version,
				PublishedAt:  published,
				IsPrerelease: prereleaseRE.MatchString(version),
			}
		}
	}
	return index, nil
}

func (PyPI) FilterResponse(resp *http.Response, files map[string]string, allowed map[string]bool) error {
	if resp.StatusCode != http.StatusOK || !strings.Contains(resp.Header.Get("Content-Type"), "json") {
		return nil
	}

	var data struct {
		Meta     json.RawMessage   `json:"meta"`
		Name     string            `json:"name"`
		Files    []json.RawMessage `json:"files"`
		Versions []string          `json:"versions"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		return fmt.Errorf("decode PyPI simple response: %w", err)
	}
	_ = resp.Body.Close()

	filteredFiles := data.Files[:0]
	for _, raw := range data.Files {
		var file struct {
			Filename string `json:"filename"`
		}
		if err := json.Unmarshal(raw, &file); err != nil {
			return fmt.Errorf("decode PyPI file entry: %w", err)
		}
		version, known := files[file.Filename]
		if known && allowed[version] {
			filteredFiles = append(filteredFiles, raw)
		}
	}
	data.Files = filteredFiles

	filteredVersions := data.Versions[:0]
	for _, version := range data.Versions {
		if allowed[version] {
			filteredVersions = append(filteredVersions, version)
		}
	}
	data.Versions = filteredVersions

	body, err := json.Marshal(data)
	if err != nil {
		return err
	}
	resp.Body = io.NopCloser(bytes.NewReader(body))
	resp.ContentLength = int64(len(body))
	resp.Header.Set("Content-Length", fmt.Sprint(len(body)))
	return nil
}
