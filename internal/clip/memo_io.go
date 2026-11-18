package clip

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Import / export in the Memos Export Format 1.0 (usememos/memos, MIT).
//
// Layout of a .zip:
//
//	manifest.json                  {format:"memos-export", formatVersion:"1.0", …}
//	memos/<uid>.json               one record (contentPath points at the .md below)
//	memos/<uid>.md                 Markdown body
//	attachments/<uid>/<filename>   attachment bytes (import: ignored for now)
//
// We read "memos/*.json" plus the file its contentPath names, and write the same
// shape, so a ClipNest library can move into a Memos instance and back.

const memosExportFormat = "memos-export"
const memosExportVersion = "1.0"

type memosManifest struct {
	Format        string `json:"format"`
	FormatVersion string `json:"formatVersion"`
	Generator     struct {
		Name    string `json:"name"`
		Version string `json:"version"`
	} `json:"generator"`
	ExportTime string `json:"exportTime"`
	Scope      struct {
		Kind string `json:"kind"`
		User struct {
			Username    string `json:"username"`
			DisplayName string `json:"displayName,omitempty"`
		} `json:"user"`
	} `json:"scope"`
	Counts struct {
		Memos       int `json:"memos"`
		Attachments int `json:"attachments"`
	} `json:"counts"`
}

type memosRecord struct {
	UID         string   `json:"uid"`
	Creator     string   `json:"creator"`
	CreateTime  string   `json:"createTime"`
	UpdateTime  string   `json:"updateTime"`
	State       string   `json:"state"`
	Visibility  string   `json:"visibility"`
	Pinned      bool     `json:"pinned"`
	ContentPath string   `json:"contentPath"`
	Tags        []string `json:"tags,omitempty"`
}

// ExportMemosZip renders every memo as a Memos-compatible archive.
func (s *ClipService) ExportMemosZip() ([]byte, error) {
	memos := s.store.AllMemos()
	if len(memos) == 0 {
		return nil, errors.New("还没有备忘可导出")
	}

	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	now := time.Now().UTC()

	var manifest memosManifest
	manifest.Format = memosExportFormat
	manifest.FormatVersion = memosExportVersion
	manifest.Generator.Name = "ClipNest"
	manifest.Generator.Version = Version
	manifest.ExportTime = now.Format(time.RFC3339)
	manifest.Scope.Kind = "USER"
	manifest.Scope.User.Username = "clipnest"
	manifest.Scope.User.DisplayName = "ClipNest"
	manifest.Counts.Memos = len(memos)

	writeJSON := func(name string, v any) error {
		w, err := zw.CreateHeader(&zip.FileHeader{Name: name, Method: zip.Deflate, Modified: now})
		if err != nil {
			return err
		}
		body, err := json.MarshalIndent(v, "", "  ")
		if err != nil {
			return err
		}
		_, err = w.Write(body)
		return err
	}

	if err := writeJSON("manifest.json", manifest); err != nil {
		return nil, err
	}

	for _, m := range memos {
		uid := m.ID
		rec := memosRecord{
			UID:         uid,
			Creator:     "users/1",
			CreateTime:  m.CreatedAt.UTC().Format(time.RFC3339),
			UpdateTime:  m.UpdatedAt.UTC().Format(time.RFC3339),
			State:       "NORMAL",
			Visibility:  "PRIVATE",
			Pinned:      m.Pinned,
			ContentPath: "memos/" + uid + ".md",
			Tags:        m.Tags,
		}
		if m.Archived {
			rec.State = "ARCHIVED"
		}
		if err := writeJSON("memos/"+uid+".json", rec); err != nil {
			return nil, err
		}
		w, err := zw.CreateHeader(&zip.FileHeader{Name: rec.ContentPath, Method: zip.Deflate, Modified: now})
		if err != nil {
			return nil, err
		}
		if _, err := w.Write([]byte(m.Content)); err != nil {
			return nil, err
		}
	}

	if err := zw.Close(); err != nil {
		return nil, err
	}
	Logf(s.store.Dir(), "memos export: %d notes, %d bytes", len(memos), buf.Len())
	return buf.Bytes(), nil
}

// ExportMemosToFile writes the archive to disk and returns the path.
func (s *ClipService) ExportMemosToFile(dir string) (string, error) {
	blob, err := s.ExportMemosZip()
	if err != nil {
		return "", err
	}
	if dir == "" {
		dir = s.store.Dir()
	}
	name := "clipnest-memos-" + time.Now().Format("20060102-1504") + ".zip"
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, blob, 0o644); err != nil {
		return "", err
	}
	Logf(s.store.Dir(), "memos export written: %s", path)
	return path, nil
}

// ImportResult summarises what an import did.
type ImportResult struct {
	Added   int    `json:"added"`
	Skipped int    `json:"skipped"`
	Error   string `json:"error,omitempty"`
}

// ImportMemosZip reads a Memos export archive (from bytes).
func (s *ClipService) ImportMemosZip(blob []byte) (ImportResult, error) {
	var res ImportResult
	zr, err := zip.NewReader(bytes.NewReader(blob), int64(len(blob)))
	if err != nil {
		return res, fmt.Errorf("不是有效的 zip: %w", err)
	}

	files := map[string]*zip.File{}
	for _, f := range zr.File {
		files[f.Name] = f
	}
	if _, ok := files["manifest.json"]; !ok {
		return res, errors.New("缺少 manifest.json，不像是 Memos 导出包")
	}

	var manifest memosManifest
	if raw, err := readZipFile(files["manifest.json"]); err == nil {
		_ = json.Unmarshal(raw, &manifest)
	}
	if manifest.Format != "" && manifest.Format != memosExportFormat {
		Logf(s.store.Dir(), "memos import: unknown format %q, trying anyway", manifest.Format)
	}

	now := nowUTC()
	for name, f := range files {
		if !strings.HasPrefix(name, "memos/") || !strings.HasSuffix(name, ".json") {
			continue
		}
		raw, err := readZipFile(f)
		if err != nil {
			res.Skipped++
			continue
		}
		var rec memosRecord
		if err := json.Unmarshal(raw, &rec); err != nil {
			res.Skipped++
			continue
		}

		content := ""
		path := rec.ContentPath
		if path == "" {
			path = strings.TrimSuffix(name, ".json") + ".md"
		}
		if cf, ok := files[path]; ok {
			if body, err := readZipFile(cf); err == nil {
				content = string(body)
			}
		}
		if strings.TrimSpace(content) == "" {
			res.Skipped++
			continue
		}

		m := Memo{
			Content:   content,
			Pinned:    rec.Pinned,
			Archived:  rec.State == "ARCHIVED",
			Source:    "import",
			CreatedAt: parseExportTime(rec.CreateTime, now),
			UpdatedAt: parseExportTime(rec.UpdateTime, now),
		}
		// 同样的内容不重复导入
		hash := memoHash(content)
		if _, exists := s.store.findMemoByHash(hash); exists {
			res.Skipped++
			continue
		}
		if _, err := s.store.ImportMemo(m); err != nil {
			res.Skipped++
			Logf(s.store.Dir(), "memos import: %v", err)
			continue
		}
		res.Added++
	}
	Logf(s.store.Dir(), "memos import: added=%d skipped=%d", res.Added, res.Skipped)
	return res, nil
}

// ImportMemosFromFile reads an archive from disk.
func (s *ClipService) ImportMemosFromFile(path string) (ImportResult, error) {
	blob, err := os.ReadFile(path)
	if err != nil {
		return ImportResult{}, err
	}
	return s.ImportMemosZip(blob)
}

func readZipFile(f *zip.File) ([]byte, error) {
	rc, err := f.Open()
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	return io.ReadAll(io.LimitReader(rc, 8<<20))
}

func parseExportTime(value string, fallback time.Time) time.Time {
	if value == "" {
		return fallback
	}
	if t, err := time.Parse(time.RFC3339Nano, value); err == nil {
		return t
	}
	return fallback
}
