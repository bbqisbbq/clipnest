package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/bbqisbbq/clipnest/internal/clip"
)

func main() {
	dir := filepath.Join(os.Getenv("APPDATA"), "ClipNest")
	st, err := clip.LoadSettingsFrom(dir)
	fmt.Printf("dir=%s err=%v hotkey=%q captureHotkey=%q preset=%q\n", dir, err, st.Hotkey, st.CaptureHotkey, st.ThemePreset)
}
