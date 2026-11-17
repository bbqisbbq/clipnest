//go:build !windows

package clip

import "errors"

// This file keeps the package buildable (and testable) on non-Windows hosts.
// ClipNest itself targets Windows: the clipboard listener, DIB conversion and
// tray integration are all Win32 only.

type Watcher struct{ changes chan struct{} }

func StartWatcher() (*Watcher, error) { return nil, ErrNotSupported }
func (w *Watcher) Changes() <-chan struct{} {
	if w == nil {
		return nil
	}
	return w.changes
}
func (w *Watcher) Stop() {}

func captureClipboard() (*Captured, error)                        { return nil, ErrNotSupported }
func WriteTextToClipboard(string) error                          { return ErrNotSupported }
func WriteHTMLToClipboard(string, string) error                  { return ErrNotSupported }
func WriteImageToClipboard([]byte) error                         { return ErrNotSupported }
func WriteFilesToClipboard([]string) error                       { return ErrNotSupported }
func foregroundAppName() string                                  { return "" }
func setAutoStart(bool, string) error                            { return ErrNotSupported }
func autoStartEnabled() bool                                     { return false }
func shellOpen(string) error                                     { return ErrNotSupported }
func revealInExplorer(string) error                              { return ErrNotSupported }
func clipboardOwnerApp() string                                  { return "" }

var errNoOp = errors.New("not supported on this platform")
