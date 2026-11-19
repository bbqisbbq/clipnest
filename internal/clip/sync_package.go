package clip

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"
)

// 同步包的格式：package.json 索引与单条记录的结构体、zip 的构建与解析、
// 本机包的组装（附件预算、墓碑并集）以及「内容没变就不上传」的指纹。
// 不负责：怎么传输（sync_dav.go）、怎么合并条目（sync_run.go / sync_merge.go）。

// manifestItem is the per-entry index record inside a package.
type manifestItem struct {
	Hash      string    `json:"hash"`
	ID        string    `json:"id"`
	Kind      Kind      `json:"kind"`
	Preview   string    `json:"preview,omitempty"`
	UpdatedAt time.Time `json:"updatedAt"`
	Blob      string    `json:"blob,omitempty"` // ".png" / ".bin"
	Pinned    bool      `json:"pinned,omitempty"`
	SeenCount int       `json:"seenCount,omitempty"`
	CopyCount int       `json:"copyCount,omitempty"`
}

// settingsPayload carries settings across machines with its own timestamp.
type settingsPayload struct {
	UpdatedAt time.Time `json:"updatedAt"`
	Device    string    `json:"device,omitempty"`
	Values    Settings  `json:"values"`
}

// pkgIndex is package.json: everything needed to merge without opening the
// per-item files.
type pkgIndex struct {
	Version   int       `json:"version"`
	DeviceID  string    `json:"deviceId"`
	Device    string    `json:"device"`
	UpdatedAt time.Time `json:"updatedAt"`
	// Entries carries every entry (clipboard captures, snippets, handwritten).
	Entries   []manifestItem   `json:"entries"`
	Deleted   []Tombstone      `json:"deleted,omitempty"`
	Settings  *settingsPayload `json:"settings,omitempty"`
	Truncated bool             `json:"truncated,omitempty"`
}

// entryRecord is one entries/<hash>.json body.
type entryRecord struct {
	Version int   `json:"version"`
	Entry   Entry `json:"entry"`
}

// snippetRecord is one snippets/<hash>.json body.
type snippetRecord struct {
	Version int     `json:"version"`
	Snippet Snippet `json:"snippet"`
}

// syncPackage is a parsed archive.
type syncPackage struct {
	index   pkgIndex
	entries []Entry
	blobs   map[string][]byte // "entry-<hash><ext>"
	size    int64
}

// ---- package (zip) ------------------------------------------------------

// buildPackage serialises an index + records + payloads into one archive.
func buildPackage(idx pkgIndex, entries []entryRecord, blobs map[string][]byte) ([]byte, error) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)

	head, err := json.Marshal(idx)
	if err != nil {
		return nil, err
	}
	w, err := zw.CreateHeader(&zip.FileHeader{Name: "package.json", Method: zip.Deflate})
	if err != nil {
		return nil, err
	}
	if _, err := w.Write(head); err != nil {
		return nil, err
	}

	for _, rec := range entries {
		body, err := json.Marshal(rec)
		if err != nil {
			return nil, err
		}
		f, err := zw.CreateHeader(&zip.FileHeader{
			Name:   "entries/" + rec.Entry.Hash + ".json",
			Method: zip.Deflate,
		})
		if err != nil {
			return nil, err
		}
		if _, err := f.Write(body); err != nil {
			return nil, err
		}
	}

	// Stable order keeps the fingerprint (and therefore the skip-push check)
	// deterministic between runs.
	names := make([]string, 0, len(blobs))
	for name := range blobs {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		f, err := zw.CreateHeader(&zip.FileHeader{
			Name:   "blobs/" + name,
			Method: zip.Store, // already-compressed payloads
		})
		if err != nil {
			return nil, err
		}
		if _, err := f.Write(blobs[name]); err != nil {
			return nil, err
		}
	}

	if err := zw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// parsePackage reads an archive produced by buildPackage.
func parsePackage(data []byte) (*syncPackage, error) {
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, fmt.Errorf("同步包解析失败: %w", err)
	}
	pkg := &syncPackage{blobs: make(map[string][]byte, 8), size: int64(len(data))}

	for _, f := range zr.File {
		switch {
		case f.Name == "package.json":
			rc, err := f.Open()
			if err != nil {
				return nil, err
			}
			body, err := io.ReadAll(io.LimitReader(rc, 64<<20))
			rc.Close()
			if err != nil {
				return nil, err
			}
			if err := json.Unmarshal(body, &pkg.index); err != nil {
				return nil, fmt.Errorf("同步包索引解析失败: %w", err)
			}
		case strings.HasPrefix(f.Name, "entries/") && strings.HasSuffix(f.Name, ".json"):
			rc, err := f.Open()
			if err != nil {
				return nil, err
			}
			body, err := io.ReadAll(io.LimitReader(rc, 8<<20))
			rc.Close()
			if err != nil {
				return nil, err
			}
			var rec entryRecord
			if err := json.Unmarshal(body, &rec); err != nil {
				continue // 单条坏数据不影响整包
			}
			if rec.Entry.Hash == "" {
				continue
			}
			pkg.entries = append(pkg.entries, rec.Entry)
		case strings.HasPrefix(f.Name, "blobs/"):
			rc, err := f.Open()
			if err != nil {
				return nil, err
			}
			body, err := io.ReadAll(io.LimitReader(rc, 64<<20))
			rc.Close()
			if err != nil {
				return nil, err
			}
			pkg.blobs[strings.TrimPrefix(f.Name, "blobs/")] = body
		}
	}
	return pkg, nil
}

// buildOwnPackage renders everything this machine knows: all record bodies, the
// blobs no peer has yet, our tombstones and (when newest) our settings.
func (s *ClipService) buildOwnPackage(peerHasBlob map[string]bool, peerTombstones []Tombstone, st Settings) ([]byte, string, error) {
	entries := s.store.ListEntries(EntryQuery{Limit: 500})
	idx := pkgIndex{
		Version:   syncPackageVersion,
		DeviceID:  st.SyncDeviceID,
		Device:    s.deviceName(),
		UpdatedAt: time.Now().UTC(),
		Entries:   make([]manifestItem, 0, len(entries)),
	}
	records := make([]entryRecord, 0, len(entries))
	blobs := make(map[string][]byte, 8)
	budget := syncBlobBudget

	for _, e := range entries {
		if e.Hash == "" {
			continue
		}
		ext := blobExtForKind(e.Kind)
		if memoImageToken(e.Content) == "" {
			ext = ""
		}
		idx.Entries = append(idx.Entries, manifestItem{
			Hash:      e.Hash,
			ID:        e.ID,
			Kind:      e.Kind,
			Preview:   truncateRunes(e.Preview(), 120),
			UpdatedAt: e.UpdatedAt,
			Blob:      ext,
			Pinned:    e.Pinned,
			SeenCount: e.SeenCount,
			CopyCount: e.CopyCount,
		})
		records = append(records, entryRecord{Version: syncPackageVersion, Entry: e})

		if ext == "" || peerHasBlob["entry-"+e.Hash+ext] {
			continue // a peer already carries this payload
		}
		if budget <= 0 {
			idx.Truncated = true
			continue
		}
		payload, _, err := s.store.LoadEntryBlob(memoImageToken(e.Content))
		if err != nil || len(payload) == 0 || len(payload) > budget {
			idx.Truncated = true
			continue
		}
		blobs["entry-"+e.Hash+ext] = payload
		budget -= len(payload)
	}

	horizon := time.Now().AddDate(0, 0, -tombstoneRetentionDays)

	idx.Deleted = mergeTombstones(peerTombstones, s.store.TombstonesScoped("entry", horizon, maxTombstones))
	s.store.PruneTombstones(horizon)

	if st.SyncSettings {
		idx.Settings = &settingsPayload{
			UpdatedAt: st.SyncUpdatedAt,
			Device:    s.deviceName(),
			Values:    machineOnlyFields(st),
		}
	}

	blobNames := make([]string, 0, len(blobs))
	for name := range blobs {
		blobNames = append(blobNames, name)
	}
	payload, err := buildPackage(idx, records, blobs)
	if err != nil {
		return nil, "", err
	}
	return payload, packageFingerprint(idx, blobNames), nil
}

// packageFingerprint digests the *content* of a package (not the archive bytes
// and not the "updatedAt" stamp), so an idle machine can tell that re-uploading
// would change nothing.
func packageFingerprint(idx pkgIndex, blobNames []string) string {
	h := sha256.New()
	for _, it := range idx.Entries {
		fmt.Fprintf(h, "i|%s|%d|%t|%d|%d|%s\n", it.Hash, it.UpdatedAt.UnixNano(), it.Pinned, it.SeenCount, it.CopyCount, it.Blob)
	}
	for _, t := range idx.Deleted {
		fmt.Fprintf(h, "d|%s|%d\n", t.Hash, t.DeletedAt.UnixNano())
	}
	sort.Strings(blobNames)
	for _, n := range blobNames {
		fmt.Fprintf(h, "b|%s\n", n)
	}
	if idx.Settings != nil {
		fmt.Fprintf(h, "s|%d|%s\n", idx.Settings.UpdatedAt.UnixNano(), shareableStamp(idx.Settings.Values))
	}
	return hex.EncodeToString(h.Sum(nil))
}

// blobExt reports the extension a record carries, from its derived blob name.
func truncateRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n])
}
