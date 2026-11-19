// Package clip implements ClipNest's clipboard capture pipeline: a Win32
// clipboard listener, a content-aware capture layer, a file-backed store and
// the service types that the webview binds to.
package clip

// Kind is the content type of a clipboard entry.
type Kind string

const (
	KindText     Kind = "text" // plain text
	KindLink     Kind = "link" // a single URL
	KindImage    Kind = "image"
	KindFiles    Kind = "files"  // CF_HDROP: file/folder paths (references only)
	KindRichText Kind = "rich"   // HTML fragment or RTF + plain text fallback
	KindCustom   Kind = "custom" // any other clipboard format, kept as raw bytes
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

// Captured is what the clipboard layer hands to the capture pipeline.
type Captured struct {
	Kind      Kind
	Text      string
	HTML      string
	RTF       string
	PNG       []byte // encoded PNG for image entries
	Binary    []byte // raw payload for custom formats
	Width     int
	Height    int
	Files     []FileRef
	Hash      string
	Preview   string
	Size      int
	SourceApp string

	FormatName string
	FormatID   uint32
}
