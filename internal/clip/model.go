// Package clip implements ClipNest's clipboard capture pipeline: a Win32
// clipboard listener, a content-aware capture layer, a file-backed store and
// the service types that the webview binds to.
package clip

import "time"

// Kind is the content type of a clipboard entry.
type Kind string

const (
	KindText     Kind = "text" // plain text
	KindLink     Kind = "link" // a single URL
	KindImage    Kind = "image"
	KindFiles    Kind = "files" // CF_HDROP: file/folder paths (references only)
	KindRichText Kind = "rich" // HTML fragment + plain text fallback
)

// FileRef is one entry of a captured file list. ClipNest deliberately stores a
// reference (path + metadata) instead of a copy: see docs/FILE-STRATEGY.md.
type FileRef struct {
	Path   string `json:"path"`
	Name   string `json:"name"`
	Size   int64  `json:"size"`
	IsDir  bool   `json:"isDir"`
	Exists bool   `json:"exists"`
}

// Item is one clipboard entry.
type Item struct {
	ID        string    `json:"id"`
	Kind      Kind      `json:"kind"`
	Text      string    `json:"text"`
	Preview   string    `json:"preview"`
	Hash      string    `json:"hash"`
	Size      int       `json:"size"`
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
	SeenCount int       `json:"seenCount"`
	CopyCount int       `json:"copyCount"`
	Pinned    bool      `json:"pinned"`
	SourceApp string    `json:"sourceApp,omitempty"`

	// Image entries: PNG blob name inside the store's blob directory.
	ImageFile string `json:"imageFile,omitempty"`
	Width     int    `json:"width,omitempty"`
	Height    int    `json:"height,omitempty"`

	// Rich text entries keep the HTML fragment; Text holds the plain version.
	HTML string `json:"html,omitempty"`

	// File entries.
	Files []FileRef `json:"files,omitempty"`
}

// Filter describes a list query.
type Filter struct {
	Query  string `json:"query"`
	Kind   string `json:"kind"`   // "" = all
	Pinned bool   `json:"pinned"` // only pinned
	Limit  int    `json:"limit"`
	Offset int    `json:"offset"`
}

// Stats is the summary shown in the UI header.
type Stats struct {
	Total        int            `json:"total"`
	Pinned       int            `json:"pinned"`
	ByKind       map[string]int `json:"byKind"`
	StorageBytes int64          `json:"storageBytes"`
	LastCapture  string         `json:"lastCapture"`
}

// Captured is what the clipboard layer hands to the capture pipeline.
type Captured struct {
	Kind      Kind
	Text      string
	HTML      string
	PNG       []byte // encoded PNG for image entries
	Width     int
	Height    int
	Files     []FileRef
	Hash      string
	Preview   string
	Size      int
	SourceApp string
}
