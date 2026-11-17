package clip

import (
	"bufio"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// Store is an append-friendly, file-backed clipboard history.
//
// Layout below the data directory (default: %APPDATA%\ClipNest):
//
//	items.jsonl   one JSON object per entry, rewritten atomically on change
//	settings.json capture/UI settings
//	blobs\        PNG payloads for image entries
//
// The whole history lives in memory; writes are coalesced and flushed from a
// background goroutine so capturing never blocks the UI thread.
type Store struct {
	mu     sync.RWMutex
	dir    string
	blobs  string
	items  map[string]*Item
	byHash map[string]string // hash -> item id
	order  []string          // ids, newest first (kept in sync on writes)

	settings    Settings
	lastCapture time.Time

	dirty   chan struct{}
	done    chan struct{}
	flushed chan struct{}
	closeMu sync.Once
	closed  bool
}

// Open loads (or initialises) a store rooted at dir.
func Open(dir string) (*Store, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	blobs := filepath.Join(dir, "blobs")
	if err := os.MkdirAll(blobs, 0o755); err != nil {
		return nil, err
	}
	s := &Store{
		dir:     dir,
		blobs:   blobs,
		items:   make(map[string]*Item),
		byHash:  make(map[string]string),
		settings: DefaultSettings(),
		dirty:    make(chan struct{}, 1),
		done:     make(chan struct{}),
		flushed:  make(chan struct{}),
	}
	if err := s.loadItems(); err != nil {
		return nil, err
	}
	if err := s.loadSettings(); err != nil {
		return nil, err
	}
	s.rebuildOrder()
	go s.saver()
	return s, nil
}

// Dir returns the data directory.
func (s *Store) Dir() string { return s.dir }

func (s *Store) loadItems() error {
	f, err := os.Open(filepath.Join(s.dir, "items.jsonl"))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var it Item
		if err := json.Unmarshal([]byte(line), &it); err != nil {
			continue // tolerate a torn line rather than losing the whole history
		}
		if it.ID == "" {
			continue
		}
		s.items[it.ID] = &it
		if it.Hash != "" {
			s.byHash[it.Hash] = it.ID
		}
		if it.UpdatedAt.After(s.lastCapture) {
			s.lastCapture = it.UpdatedAt
		}
	}
	return sc.Err()
}

func (s *Store) loadSettings() error {
	data, err := os.ReadFile(filepath.Join(s.dir, "settings.json"))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	var st = DefaultSettings()
	if err := json.Unmarshal(data, &st); err != nil {
		return nil // keep defaults on a corrupt file
	}
	s.settings = st
	return nil
}

// PeekSettings reads settings.json without opening the whole store. main() uses
// it to decide whether the window starts hidden.
func PeekSettings(dir string) (Settings, error) {
	data, err := os.ReadFile(filepath.Join(dir, "settings.json"))
	if err != nil {
		return DefaultSettings(), err
	}
	st := DefaultSettings()
	if err := json.Unmarshal(data, &st); err != nil {
		return DefaultSettings(), err
	}
	return st, nil
}

// rebuildOrder sorts ids newest-first. Must be called with the lock held or
// before the store is shared.
func (s *Store) rebuildOrder() {
	ids := make([]string, 0, len(s.items))
	for id := range s.items {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool {
		a, b := s.items[ids[i]], s.items[ids[j]]
		if a.Pinned != b.Pinned {
			return a.Pinned
		}
		return a.UpdatedAt.After(b.UpdatedAt)
	})
	s.order = ids
}

func (s *Store) reorder() {
	sort.SliceStable(s.order, func(i, j int) bool {
		a, b := s.items[s.order[i]], s.items[s.order[j]]
		if a == nil || b == nil {
			return false
		}
		if a.Pinned != b.Pinned {
			return a.Pinned
		}
		return a.UpdatedAt.After(b.UpdatedAt)
	})
}

func (s *Store) markDirty() {
	select {
	case s.dirty <- struct{}{}:
	default:
	}
}

func (s *Store) saver() {
	defer close(s.flushed)
	for {
		select {
		case <-s.dirty:
			select {
			case <-time.After(400 * time.Millisecond):
			case <-s.done:
				_ = s.flush()
				return
			}
			_ = s.flush()
		case <-s.done:
			_ = s.flush()
			return
		}
	}
}

// flush atomically rewrites items.jsonl.
func (s *Store) flush() error {
	s.mu.RLock()
	list := make([]Item, 0, len(s.items))
	for _, id := range s.order {
		if it := s.items[id]; it != nil {
			list = append(list, *it)
		}
	}
	closed := s.closed
	s.mu.RUnlock()
	if closed && len(list) == 0 {
		return nil
	}

	path := filepath.Join(s.dir, "items.jsonl")
	tmp := path + ".tmp"
	f, err := os.Create(tmp)
	if err != nil {
		return err
	}
	w := bufio.NewWriterSize(f, 256*1024)
	enc := json.NewEncoder(w)
	for i := range list {
		if err := enc.Encode(&list[i]); err != nil {
			f.Close()
			os.Remove(tmp)
			return err
		}
	}
	if err := w.Flush(); err != nil {
		f.Close()
		os.Remove(tmp)
		return err
	}
	if err := f.Close(); err != nil {
		os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, path)
}

// Close flushes pending writes and stops background work. It blocks until the
// last flush finished so a following Open() sees the file on disk.
func (s *Store) Close() error {
	s.closeMu.Do(func() {
		s.mu.Lock()
		s.closed = true
		s.mu.Unlock()
		close(s.done)
		select {
		case <-s.flushed:
		case <-time.After(5 * time.Second):
		}
	})
	return nil
}

func newID() string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(b[:])
}

func hashOf(parts ...string) string {
	h := sha256.New()
	for _, p := range parts {
		h.Write([]byte(p))
		h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil))[:32]
}

// SaveBlob persists an image payload and returns its file name.
func (s *Store) SaveBlob(id string, data []byte) (string, error) {
	name := id + ".png"
	if err := os.WriteFile(filepath.Join(s.blobs, name), data, 0o644); err != nil {
		return "", err
	}
	return name, nil
}

// BlobPath resolves a blob file name to an absolute path.
func (s *Store) BlobPath(name string) string {
	if name == "" {
		return ""
	}
	return filepath.Join(s.blobs, filepath.Base(name))
}

// ReadBlobData returns a data: URL for the webview.
func (s *Store) ReadBlobData(name string) (string, error) {
	p := s.BlobPath(name)
	data, err := os.ReadFile(p)
	if err != nil {
		return "", err
	}
	return "data:image/png;base64," + encodeBase64(data), nil
}

func (s *Store) deleteBlob(name string) {
	if name == "" {
		return
	}
	_ = os.Remove(s.BlobPath(name))
}

// Add inserts a capture, or refreshes an existing entry with the same content.
// The bool reports whether the entry is new to the history.
func (s *Store) Add(c Captured) (*Item, bool, error) {
	id := newID()
	imageFile := ""
	if len(c.PNG) > 0 {
		name, err := s.SaveBlob(id, c.PNG)
		if err != nil {
			return nil, false, err
		}
		imageFile = name
	}

	now := nowUTC()
	s.mu.Lock()
	if existingID, ok := s.byHash[c.Hash]; ok {
		if it := s.items[existingID]; it != nil {
			it.UpdatedAt = now
			it.SeenCount++
			if c.SourceApp != "" {
				it.SourceApp = c.SourceApp
			}
			s.lastCapture = now
			s.reorder()
			s.mu.Unlock()
			s.markDirty()
			cp := *it
			return &cp, false, nil
		}
	}

	it := &Item{
		ID:        id,
		Kind:      c.Kind,
		Text:      c.Text,
		Preview:   c.Preview,
		Hash:      c.Hash,
		Size:      c.Size,
		CreatedAt: now,
		UpdatedAt: now,
		SeenCount: 1,
		SourceApp: c.SourceApp,
		ImageFile: imageFile,
		Width:     c.Width,
		Height:    c.Height,
		HTML:      c.HTML,
		Files:     c.Files,
	}
	s.items[it.ID] = it
	if c.Hash != "" {
		s.byHash[c.Hash] = it.ID
	}
	s.order = append([]string{it.ID}, s.order...)
	s.lastCapture = now
	s.mu.Unlock()
	s.markDirty()
	cp := *it
	return &cp, true, nil
}

// Get returns a copy of one entry.
func (s *Store) Get(id string) (Item, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	it, ok := s.items[id]
	if !ok {
		return Item{}, false
	}
	return *it, true
}

// List returns entries matching the filter, newest (and pinned) first.
func (s *Store) List(f Filter) []Item {
	if f.Limit <= 0 || f.Limit > 2000 {
		f.Limit = 500
	}
	q := strings.ToLower(strings.TrimSpace(f.Query))

	s.mu.RLock()
	out := make([]Item, 0, min(len(s.order), f.Limit))
	skipped := 0
	for _, id := range s.order {
		it := s.items[id]
		if it == nil {
			continue
		}
		if f.Pinned && !it.Pinned {
			continue
		}
		if f.Kind != "" && string(it.Kind) != f.Kind && f.Kind != "all" {
			continue
		}
		if q != "" && !matches(it, q) {
			continue
		}
		if skipped < f.Offset {
			skipped++
			continue
		}
		out = append(out, *it)
		if len(out) >= f.Limit {
			break
		}
	}
	s.mu.RUnlock()
	return out
}

func matches(it *Item, q string) bool {
	if strings.Contains(strings.ToLower(it.Text), q) {
		return true
	}
	if strings.Contains(strings.ToLower(it.Preview), q) {
		return true
	}
	if it.SourceApp != "" && strings.Contains(strings.ToLower(it.SourceApp), q) {
		return true
	}
	for _, f := range it.Files {
		if strings.Contains(strings.ToLower(f.Name), q) || strings.Contains(strings.ToLower(f.Path), q) {
			return true
		}
	}
	return false
}

// Count returns the number of entries matching the filter.
func (s *Store) Count(f Filter) int {
	f.Limit = 1 << 30
	f.Offset = 0
	return len(s.List(f))
}

// Delete removes one entry.
func (s *Store) Delete(id string) bool {
	s.mu.Lock()
	it := s.items[id]
	if it == nil {
		s.mu.Unlock()
		return false
	}
	delete(s.items, id)
	if it.Hash != "" && s.byHash[it.Hash] == id {
		delete(s.byHash, it.Hash)
	}
	for i, oid := range s.order {
		if oid == id {
			s.order = append(s.order[:i], s.order[i+1:]...)
			break
		}
	}
	s.mu.Unlock()
	s.deleteBlob(it.ImageFile)
	s.markDirty()
	return true
}

// DeleteWhere removes every entry matching the filter that is not pinned.
// A nil pinned-wins policy keeps favorites safe from bulk clears.
func (s *Store) DeleteWhere(f Filter) int {
	f.Limit = 1 << 30
	victims := s.List(f)
	n := 0
	for _, it := range victims {
		if it.Pinned {
			continue
		}
		if s.Delete(it.ID) {
			n++
		}
	}
	return n
}

// SetPinned toggles the favourite flag.
func (s *Store) SetPinned(id string, pinned bool) (Item, bool) {
	s.mu.Lock()
	it := s.items[id]
	if it == nil {
		s.mu.Unlock()
		return Item{}, false
	}
	it.Pinned = pinned
	s.reorder()
	cp := *it
	s.mu.Unlock()
	s.markDirty()
	return cp, true
}

// MarkCopied records a copy-back so the UI can rank frequently used entries.
func (s *Store) MarkCopied(id string) {
	s.mu.Lock()
	if it := s.items[id]; it != nil {
		it.CopyCount++
		it.UpdatedAt = nowUTC()
		s.reorder()
	}
	s.mu.Unlock()
	s.markDirty()
}

// RefreshFiles re-stats every file reference of an entry (the "reference"
// strategy can go stale when the user moves or deletes the original).
func (s *Store) RefreshFiles(id string) (Item, bool) {
	s.mu.RLock()
	it := s.items[id]
	if it == nil {
		s.mu.RUnlock()
		return Item{}, false
	}
	paths := make([]FileRef, len(it.Files))
	copy(paths, it.Files)
	s.mu.RUnlock()

	for i := range paths {
		paths[i] = statFile(paths[i])
	}

	s.mu.Lock()
	if it := s.items[id]; it != nil {
		it.Files = paths
		cp := *it
		s.mu.Unlock()
		s.markDirty()
		return cp, true
	}
	s.mu.Unlock()
	return Item{}, false
}

func statFile(f FileRef) FileRef {
	info, err := os.Stat(f.Path)
	if err != nil {
		f.Exists = false
		f.Size = 0
		return f
	}
	f.Exists = true
	f.IsDir = info.IsDir()
	f.Size = info.Size()
	return f
}

// Prune enforces the retention policy (pinned entries survive).
func (s *Store) Prune(maxItems, maxAgeDays int) int {
	s.mu.RLock()
	total := 0
	for range s.items {
		total++
	}
	s.mu.RUnlock()

	removed := 0
	if maxAgeDays > 0 {
		cutoff := nowUTC().AddDate(0, 0, -maxAgeDays)
		s.mu.RLock()
		var old []string
		for id, it := range s.items {
			if !it.Pinned && it.UpdatedAt.Before(cutoff) {
				old = append(old, id)
			}
		}
		s.mu.RUnlock()
		for _, id := range old {
			if s.Delete(id) {
				removed++
			}
		}
		total -= removed
	}

	if maxItems > 0 && total > maxItems {
		s.mu.RLock()
		victims := make([]string, 0, total-maxItems)
		for i := len(s.order) - 1; i >= 0 && len(victims) < total-maxItems; i-- {
			it := s.items[s.order[i]]
			if it == nil || it.Pinned {
				continue
			}
			victims = append(victims, it.ID)
		}
		s.mu.RUnlock()
		for _, id := range victims {
			if s.Delete(id) {
				removed++
			}
		}
	}
	return removed
}

// Settings returns a copy of the current settings.
func (s *Store) Settings() Settings {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.settings.clone()
}

// SaveSettings persists settings synchronously.
func (s *Store) SaveSettings(st Settings) error {
	st = st.clone()
	s.mu.Lock()
	s.settings = st
	s.mu.Unlock()

	data, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return err
	}
	path := filepath.Join(s.dir, "settings.json")
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// Stats summarises the history.
func (s *Store) Stats() Stats {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := Stats{ByKind: make(map[string]int, 5), Total: len(s.items)}
	for _, it := range s.items {
		out.ByKind[string(it.Kind)]++
		if it.Pinned {
			out.Pinned++
		}
	}
	if !s.lastCapture.IsZero() {
		out.LastCapture = s.lastCapture.Local().Format(time.RFC3339)
	}
	var size int64
	_ = filepath.Walk(s.dir, func(_ string, info os.FileInfo, err error) error {
		if err == nil && !info.IsDir() {
			size += info.Size()
		}
		return nil
	})
	out.StorageBytes = size
	return out
}
