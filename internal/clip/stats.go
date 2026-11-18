package clip

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Usage stats without any telemetry: GitHub publishes how many times each release
// asset was downloaded, and the repo traffic API gives views/clones for the last
// 14 days. Both are read-only and need no service of our own.
// AssetDownload is one release asset with its download count.
type AssetDownload struct {
	Name      string `json:"name"`
	Downloads int    `json:"downloads"`
	Release   string `json:"release"`
	Published string `json:"published"`
}

// GetDownloadStats reads GitHub release asset download counts — a zero-privacy
// proxy for "how many people installed it". No telemetry involved.
func (s *ClipService) GetDownloadStats() ([]AssetDownload, error) {
	url := strings.TrimRight(updateAPI(), "/") + "/repos/" + updateRepo + "/releases?per_page=20"
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "ClipNest/"+Version)

	client := &http.Client{Timeout: 20 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GitHub API 返回 %d", resp.StatusCode)
	}

	var releases []struct {
		TagName   string `json:"tag_name"`
		Published string `json:"published_at"`
		Assets    []struct {
			Name  string `json:"name"`
			Count int    `json:"download_count"`
		} `json:"assets"`
	}
	if err := json.Unmarshal(body, &releases); err != nil {
		return nil, err
	}

	var out []AssetDownload
	for _, rel := range releases {
		date := rel.Published
		if len(date) >= 10 {
			date = date[:10]
		}
		for _, a := range rel.Assets {
			out = append(out, AssetDownload{
				Name:      a.Name,
				Downloads: a.Count,
				Release:   rel.TagName,
				Published: date,
			})
		}
	}
	return out, nil
}
