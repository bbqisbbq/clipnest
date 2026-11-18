//go:build windows

package clip

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"os"
	"os/exec"
	"strings"
	"testing"
)

// These tests drive the real Windows clipboard. They run sequentially because
// the clipboard is global state.
//
// If a ClipNest instance is running it is also wiring itself to that same
// clipboard (and briefly holding it open), which makes round-trip assertions
// flaky — so the whole file skips instead of failing for the wrong reason.
func TestMain(m *testing.M) {
	if clipnestRunning() {
		fmt.Fprintln(os.Stderr, "skip: a ClipNest instance is running and shares the clipboard; quit it first")
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func clipnestRunning() bool {
	tasklist := os.Getenv("WINDIR") + `\System32\tasklist.exe`
	out, err := exec.Command(tasklist, "/FI", "IMAGENAME eq clipnest.exe", "/NH").Output()
	if err != nil {
		return false
	}
	return strings.Contains(strings.ToLower(string(out)), "clipnest.exe")
}

func TestDIBRoundTrip(t *testing.T) {
	img := image.NewNRGBA(image.Rect(0, 0, 4, 3))
	img.SetNRGBA(0, 0, color.NRGBA{R: 255, A: 255})
	img.SetNRGBA(3, 2, color.NRGBA{G: 128, B: 64, A: 255})

	dib := imageToDIB(img)
	back, err := dibToImage(dib)
	if err != nil {
		t.Fatalf("dibToImage: %v", err)
	}
	if back.Bounds() != img.Bounds() {
		t.Fatalf("bounds = %v, want %v", back.Bounds(), img.Bounds())
	}
	// 24/32bpp round trip must preserve the top row: the DIB is stored
	// bottom-up, so a missing flip shows up here.
	if back.NRGBAAt(0, 0) != img.NRGBAAt(0, 0) {
		t.Errorf("top-left = %v, want %v", back.NRGBAAt(0, 0), img.NRGBAAt(0, 0))
	}
	if back.NRGBAAt(3, 2) != img.NRGBAAt(3, 2) {
		t.Errorf("bottom-right = %v, want %v", back.NRGBAAt(3, 2), img.NRGBAAt(3, 2))
	}
}

func TestTextRoundTrip(t *testing.T) {
	const want = "ClipNest 剪贴板回写测试 ✓"
	if err := WriteTextToClipboard(want); err != nil {
		t.Fatalf("WriteTextToClipboard: %v", err)
	}
	cap, err := captureClipboard()
	if err != nil {
		t.Fatalf("captureClipboard: %v", err)
	}
	if cap == nil {
		t.Fatal("captureClipboard returned nothing")
	}
	if cap.Kind != KindText && cap.Kind != KindLink {
		t.Fatalf("kind = %q, want text", cap.Kind)
	}
	if cap.Text != want {
		t.Fatalf("text = %q, want %q", cap.Text, want)
	}
}

func TestLinkDetection(t *testing.T) {
	if err := WriteTextToClipboard("https://github.com/bbqisbbq/clipnest"); err != nil {
		t.Fatalf("WriteTextToClipboard: %v", err)
	}
	cap, _ := captureClipboard()
	if cap == nil || cap.Kind != KindLink {
		t.Fatalf("kind = %v, want link", cap)
	}
}

func TestImageRoundTrip(t *testing.T) {
	img := image.NewNRGBA(image.Rect(0, 0, 8, 6))
	for y := 0; y < 6; y++ {
		for x := 0; x < 8; x++ {
			img.SetNRGBA(x, y, color.NRGBA{R: uint8(x * 30), G: uint8(y * 40), B: 200, A: 255})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	if err := WriteImageToClipboard(buf.Bytes()); err != nil {
		t.Fatalf("WriteImageToClipboard: %v", err)
	}
	cap, err := captureClipboard()
	if err != nil {
		t.Fatalf("captureClipboard: %v", err)
	}
	if cap == nil || cap.Kind != KindImage {
		t.Fatalf("kind = %v, want image", cap)
	}
	if cap.Width != 8 || cap.Height != 6 {
		t.Fatalf("size = %dx%d, want 8x6", cap.Width, cap.Height)
	}
}

func TestFilesRoundTrip(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Skip("no executable path")
	}
	if err := WriteFilesToClipboard([]string{exe}); err != nil {
		t.Fatalf("WriteFilesToClipboard: %v", err)
	}
	cap, _ := captureClipboard()
	if cap == nil || cap.Kind != KindFiles {
		t.Fatalf("kind = %v, want files", cap)
	}
	if len(cap.Files) != 1 || cap.Files[0].Path != exe {
		t.Fatalf("files = %+v, want %q", cap.Files, exe)
	}
	if !cap.Files[0].Exists {
		t.Errorf("file %q reported as missing", exe)
	}
}

func TestCustomFormatRoundTrip(t *testing.T) {
	payload := []byte{0xDE, 0xAD, 0xBE, 0xEF, 0x01, 0x02, 0x03}
	const formatName = "ClipNestSmokeFmt"
	if err := WriteRawToClipboard(0, formatName, payload); err != nil {
		t.Fatalf("WriteRawToClipboard: %v", err)
	}
	cap, err := captureClipboard()
	if err != nil {
		t.Fatalf("captureClipboard: %v", err)
	}
	if cap == nil {
		t.Fatal("custom format was not captured at all")
	}
	if cap.Kind != KindCustom {
		t.Fatalf("kind = %q, want custom", cap.Kind)
	}
	if cap.FormatName != formatName {
		t.Fatalf("format name = %q, want %q", cap.FormatName, formatName)
	}
	if !bytes.Equal(cap.Binary, payload) {
		t.Fatalf("payload = % X, want % X", cap.Binary, payload)
	}
}

func TestRTFCapture(t *testing.T) {
	const rtf = `{\rtf1\ansi\deff0{\fonttbl{\f0 Calibri;}}\f0\fs24 Hello \b ClipNest\b0\par second line}`
	if err := WriteRTFToClipboard(rtf, ""); err != nil {
		t.Fatalf("WriteRTFToClipboard: %v", err)
	}
	cap, err := captureClipboard()
	if err != nil || cap == nil {
		t.Fatalf("captureClipboard: %v %v", cap, err)
	}
	if cap.Kind != KindRichText || cap.RTF == "" {
		t.Fatalf("kind=%q rtf=%q, want rich with RTF payload", cap.Kind, cap.RTF)
	}
	if !strings.Contains(cap.Text, "ClipNest") {
		t.Errorf("plain text fallback = %q, want it to contain ClipNest", cap.Text)
	}
}

func TestCaptureDedupeKeepsEverything(t *testing.T) {
	dir := t.TempDir()
	st, err := Open(dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer st.Close()

	c := Captured{Kind: KindText, Text: "hello", Preview: "hello", Hash: "h1", Size: 5}
	first, isNew, err := st.SaveCapture(c)
	if err != nil || !isNew {
		t.Fatalf("first SaveCapture: entry=%v new=%v err=%v", first, isNew, err)
	}
	if !first.HasAction(ActionClipboard) {
		t.Errorf("采集条目应该带 clipboard 能力: %v", first.Actions)
	}

	again, isNew, err := st.SaveCapture(c)
	if err != nil || isNew {
		t.Fatalf("second SaveCapture: new=%v err=%v", isNew, err)
	}
	if again.ID != first.ID {
		t.Fatalf("dedupe created a new id: %s vs %s", again.ID, first.ID)
	}
	if again.SeenCount != 2 {
		t.Fatalf("seenCount = %d, want 2", again.SeenCount)
	}

	for i := 0; i < 5; i++ {
		if _, _, err := st.SaveCapture(Captured{
			Kind: KindText, Text: string(rune('a' + i)),
			Preview: string(rune('a' + i)), Hash: "h" + string(rune('a'+i)), Size: 1,
		}); err != nil {
			t.Fatal(err)
		}
	}
	// 不再有自动清理：写多少就该留多少
	if got := st.CountEntries(); got != 6 {
		t.Fatalf("entries = %d, want 6（不做自动清理）", got)
	}
	if _, ok := st.GetEntry(first.ID); !ok {
		t.Error("最早的一条被删掉了——策略是保留为主")
	}
}
func TestStorePersistsAcrossReopen(t *testing.T) {
	dir := t.TempDir()
	st, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := st.SaveCapture(Captured{Kind: KindLink, Text: "https://example.com", Preview: "example", Hash: "z", Size: 19}); err != nil {
		t.Fatal(err)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}

	st2, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer st2.Close()
	list := st2.ListEntries(EntryQuery{})
	if len(list) != 1 || list[0].Content != "https://example.com" {
		t.Fatalf("reloaded list = %+v", list)
	}
}
