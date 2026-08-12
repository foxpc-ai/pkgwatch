package registries

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type PackageMeta struct {
	Name         string
	Version      string
	PublishedAt  time.Time
	IsPrerelease bool
}

type Registry interface {
	ExtractPackage(r *http.Request) *PackageMeta
	FetchMetadata(ctx context.Context, pkg *PackageMeta, upstream string) (*PackageMeta, error)
}

type PyPI struct{}

var httpClient = &http.Client{Timeout: 10 * time.Second}

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
		UploadTime string `json:"upload_time_iso_8601"`
	} `json:"releases"`
}

func (PyPI) FetchMetadata(ctx context.Context, pkg *PackageMeta, upstream string) (*PackageMeta, error) {
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

	latest := data.Info.Version
	releases := data.Releases[latest]
	if len(releases) == 0 {
		return nil, fmt.Errorf("no release files found for %s@%s", pkg.Name, latest)
	}

	published, err := time.Parse(time.RFC3339, releases[0].UploadTime)
	if err != nil {
		return nil, fmt.Errorf("could not parse release date: %w", err)
	}

	return &PackageMeta{
		Name:        data.Info.Name,
		Version:     latest,
		PublishedAt: published,
	}, nil
}
