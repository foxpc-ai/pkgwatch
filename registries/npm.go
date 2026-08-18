package registries

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type NPM struct{}

func isNPMPrerelease(version string) bool {
	publicVersion := strings.SplitN(version, "+", 2)[0]
	return strings.Contains(publicVersion, "-")
}

func (NPM) ExtractPackage(r *http.Request) *PackageMeta {
	path := strings.TrimPrefix(r.URL.Path, "/npm/")

	if path == "" {
		return nil
	}

	// scoped package: @scope/pkg[/rest]
	if strings.HasPrefix(path, "@") {
		parts := strings.SplitN(path, "/", 4)
		if len(parts) < 2 || parts[1] == "" {
			return nil
		}
		pkg := &PackageMeta{Name: parts[0] + "/" + parts[1]}
		if len(parts) >= 3 && parts[2] != "" && parts[2] != "-" {
			pkg.Version = parts[2]
		}
		return pkg
	}

	// plain package: pkg[/rest]
	parts := strings.SplitN(path, "/", 3)
	name := parts[0]
	if name == "" {
		return nil
	}
	pkg := &PackageMeta{Name: name}
	if len(parts) >= 2 && parts[1] != "" && parts[1] != "-" {
		pkg.Version = parts[1]
	}
	return pkg
}

type npmAPIResponse struct {
	Name     string                     `json:"name"`
	DistTags map[string]string          `json:"dist-tags"`
	Time     map[string]string          `json:"time"`
	Versions map[string]json.RawMessage `json:"versions"`
}

func (NPM) FetchMetadata(ctx context.Context, pkg *PackageMeta, upstream string) (*PackageMeta, error) {
	index, err := (NPM{}).FetchVersionIndex(ctx, pkg, upstream)
	if err != nil {
		return nil, err
	}
	meta := index.Versions[index.Latest]
	if meta == nil {
		return nil, fmt.Errorf("no publish time found for %s@%s", pkg.Name, index.Latest)
	}
	return meta, nil
}

func (NPM) FetchVersionIndex(ctx context.Context, pkg *PackageMeta, upstream string) (*VersionIndex, error) {
	apiURL, err := npmPackageURL(upstream, pkg.Name)
	if err != nil {
		return nil, err
	}

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
		return nil, fmt.Errorf("package %q not found on npm", pkg.Name)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("npm registry returned %d", resp.StatusCode)
	}

	var data npmAPIResponse
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		return nil, err
	}

	latest := data.DistTags["latest"]
	if latest == "" {
		return nil, fmt.Errorf("no latest dist-tag for %s", pkg.Name)
	}

	index := &VersionIndex{Latest: latest, Versions: make(map[string]*PackageMeta)}
	for version, publishedStr := range data.Time {
		if version == "created" || version == "modified" {
			continue
		}
		published, err := time.Parse(time.RFC3339, publishedStr)
		if err != nil {
			return nil, fmt.Errorf("could not parse publish time for %s@%s: %w", pkg.Name, version, err)
		}
		index.Versions[version] = &PackageMeta{
			Name:         data.Name,
			Version:      version,
			PublishedAt:  published,
			IsPrerelease: isNPMPrerelease(version),
		}
	}
	return index, nil
}

func npmPackageURL(upstream, name string) (string, error) {
	target, err := url.Parse(upstream)
	if err != nil {
		return "", fmt.Errorf("invalid npm upstream: %w", err)
	}
	target.Path = strings.TrimRight(target.Path, "/") + "/" + strings.TrimLeft(name, "/")
	target.RawPath = ""
	return target.String(), nil
}

func (NPM) FilterResponse(resp *http.Response, allowed map[string]bool) error {
	if resp.StatusCode != http.StatusOK || !strings.Contains(resp.Header.Get("Content-Type"), "json") {
		return nil
	}

	var data map[string]json.RawMessage
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		return fmt.Errorf("decode npm metadata response: %w", err)
	}
	_ = resp.Body.Close()

	filterMap := func(field string) error {
		raw, ok := data[field]
		if !ok || len(bytes.TrimSpace(raw)) == 0 || string(bytes.TrimSpace(raw)) == "null" {
			data[field] = []byte("{}")
			return nil
		}
		var values map[string]json.RawMessage
		if err := json.Unmarshal(raw, &values); err != nil {
			return fmt.Errorf("decode npm %s: %w", field, err)
		}
		for version := range values {
			if !allowed[version] {
				delete(values, version)
			}
		}
		encoded, err := json.Marshal(values)
		if err != nil {
			return err
		}
		data[field] = encoded
		return nil
	}
	if err := filterMap("versions"); err != nil {
		return err
	}
	if raw, ok := data["time"]; ok && len(raw) > 0 {
		var times map[string]json.RawMessage
		if err := json.Unmarshal(raw, &times); err != nil {
			return fmt.Errorf("decode npm time: %w", err)
		}
		for key := range times {
			if key != "created" && key != "modified" && !allowed[key] {
				delete(times, key)
			}
		}
		encoded, err := json.Marshal(times)
		if err != nil {
			return err
		}
		data["time"] = encoded
	}
	if raw, ok := data["dist-tags"]; ok && len(raw) > 0 {
		var tags map[string]string
		if err := json.Unmarshal(raw, &tags); err != nil {
			return fmt.Errorf("decode npm dist-tags: %w", err)
		}
		for tag, version := range tags {
			if !allowed[version] {
				delete(tags, tag)
			}
		}
		encoded, err := json.Marshal(tags)
		if err != nil {
			return err
		}
		data["dist-tags"] = encoded
	}

	body, err := json.Marshal(data)
	if err != nil {
		return err
	}
	resp.Body = io.NopCloser(bytes.NewReader(body))
	resp.ContentLength = int64(len(body))
	resp.Header.Set("Content-Length", fmt.Sprint(len(body)))
	return nil
}
