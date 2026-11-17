//go:build windows

package clip

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"image"
	"image/png"
	"net/url"
	"path/filepath"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
)

// captureClipboard inspects the current clipboard and returns a Captured value.
// It returns (nil, nil) when the clipboard holds nothing ClipNest can store.
func captureClipboard() (*Captured, error) {
	initFormats()
	if err := openClipboard(); err != nil {
		return nil, err
	}
	defer closeClipboard()

	// ---- file list (CF_HDROP) -------------------------------------------
	if formatAvailable(cfHDrop) {
		if files, ok := readHDrop(); ok && len(files) > 0 {
			return buildFilesCapture(files), nil
		}
	}

	// ---- image -----------------------------------------------------------
	if c, err := readImageLocked(); err == nil && c != nil {
		return c, nil
	}

	// ---- rich text (HTML Format) ----------------------------------------
	text := ""
	if formatAvailable(cfUnicodeText) {
		if b, err := readFormat(cfUnicodeText); err == nil {
			text = decodeUTF16(b)
		}
	}
	if text == "" && formatAvailable(cfText) {
		if b, err := readFormat(cfText); err == nil {
			text = decodeANSI(b)
		}
	}

	if formats.html != 0 && formatAvailable(formats.html) {
		if b, err := readFormat(formats.html); err == nil {
			if frag := htmlFragment(b); frag != "" {
				plain := text
				if plain == "" {
					plain = stripTags(frag)
				}
				if strings.TrimSpace(plain) != "" || strings.TrimSpace(frag) != "" {
					return buildRichCapture(plain, frag), nil
				}
			}
		}
	}

	// ---- uri-list --------------------------------------------------------
	if formats.uriList != 0 && formatAvailable(formats.uriList) {
		if b, err := readFormat(formats.uriList); err == nil {
			for _, line := range strings.Split(stripANSI(string(b)), "\n") {
				line = strings.TrimSpace(line)
				if line != "" && !strings.HasPrefix(line, "#") {
					return buildTextCapture(line), nil
				}
			}
		}
	}

	// ---- plain text ------------------------------------------------------
	if strings.TrimSpace(text) != "" {
		return buildTextCapture(text), nil
	}

	// ---- OLE fallback ----------------------------------------------------
	// Nothing was published as a native format. Apps using OLE with delayed
	// rendering only expose an IDataObject, so ask it to render for us.
	if files, ok := oleReadHDrop(); ok && len(files) > 0 {
		return buildFilesCapture(files), nil
	}
	if oleText, ok := oleReadText(); ok && strings.TrimSpace(oleText) != "" {
		return buildTextCapture(oleText), nil
	}

	return nil, nil
}

func readImageLocked() (*Captured, error) {
	// Native PNG uploads (Chrome, Firefox, Snipaste, ...).
	if formats.png != 0 && formatAvailable(formats.png) {
		if data, err := readFormat(formats.png); err == nil && len(data) > 8 {
			if img, err := png.Decode(bytes.NewReader(data)); err == nil {
				return buildImageCapture(img)
			}
		}
	}

	for _, format := range []uint32{cfDIBV5, cfDIB} {
		if !formatAvailable(format) {
			continue
		}
		data, err := readFormat(format)
		if err != nil || len(data) < 40 {
			continue
		}
		img, err := dibToImage(data)
		if err != nil {
			continue
		}
		return buildImageCapture(img)
	}
	return nil, nil
}

func readHDrop() ([]FileRef, bool) {
	h, _, _ := procGetClipboardData.Call(uintptr(cfHDrop))
	if h == 0 {
		return nil, false
	}
	count, _, _ := procDragQueryFileW.Call(h, 0xFFFFFFFF, 0, 0)
	if count == 0 {
		return nil, false
	}
	out := make([]FileRef, 0, count)
	for i := uintptr(0); i < count; i++ {
		buf := make([]uint16, 4096)
		n, _, _ := procDragQueryFileW.Call(h, i, uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)))
		if n == 0 || int(n) >= len(buf) {
			continue
		}
		out = append(out, statFile(FileRef{Path: utf16SliceToString(buf[:n])}))
	}
	for i := range out {
		out[i].Name = filepath.Base(out[i].Path)
	}
	return out, len(out) > 0
}

// ---- capture builders ---------------------------------------------------

func buildTextCapture(text string) *Captured {
	kind := KindText
	trimmed := strings.TrimSpace(text)
	if isSingleURL(trimmed) {
		kind = KindLink
	}
	sum := sha256.Sum256([]byte("text\x00" + text))
	return &Captured{
		Kind:      kind,
		Text:      text,
		Preview:   makePreview(trimmed, 240),
		Hash:      hex.EncodeToString(sum[:16]),
		Size:      len(text),
		SourceApp: foregroundAppName(),
	}
}

func buildRichCapture(plain, htmlFragment string) *Captured {
	sum := sha256.Sum256([]byte("rich\x00" + plain + "\x00" + htmlFragment))
	return &Captured{
		Kind:      KindRichText,
		Text:      plain,
		HTML:      htmlFragment,
		Preview:   makePreview(strings.TrimSpace(plain), 240),
		Hash:      hex.EncodeToString(sum[:16]),
		Size:      len(plain) + len(htmlFragment),
		SourceApp: foregroundAppName(),
	}
}

func buildImageCapture(img image.Image) (*Captured, error) {
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return nil, err
	}
	data := buf.Bytes()
	sum := sha256.Sum256(data)
	b := img.Bounds()
	return &Captured{
		Kind:      KindImage,
		PNG:       data,
		Width:     b.Dx(),
		Height:    b.Dy(),
		Preview:   fmt.Sprintf("%d × %d 图像", b.Dx(), b.Dy()),
		Hash:      hex.EncodeToString(sum[:16]),
		Size:      len(data),
		SourceApp: foregroundAppName(),
	}, nil
}

func buildFilesCapture(files []FileRef) *Captured {
	names := make([]string, 0, len(files))
	paths := make([]string, 0, len(files))
	var total int64
	for _, f := range files {
		names = append(names, f.Name)
		paths = append(paths, f.Path)
		total += f.Size
	}
	sum := sha256.Sum256([]byte("files\x00" + strings.Join(paths, "\x00")))
	preview := strings.Join(names, ", ")
	return &Captured{
		Kind:      KindFiles,
		Text:      strings.Join(paths, "\r\n"),
		Files:     files,
		Preview:   makePreview(preview, 240),
		Hash:      hex.EncodeToString(sum[:16]),
		Size:      int(total),
		SourceApp: foregroundAppName(),
	}
}

// ---- clipboard writers --------------------------------------------------

// WriteTextToClipboard puts plain text on the clipboard.
func WriteTextToClipboard(text string) error {
	if err := openClipboard(); err != nil {
		return err
	}
	defer closeClipboard()
	if r, _, err := procEmptyClipboard.Call(); r == 0 {
		return fmt.Errorf("EmptyClipboard: %v", err)
	}
	return setClipboardBytes(cfUnicodeText, utf16Bytes(text))
}

// WriteHTMLToClipboard puts an HTML fragment plus a plain-text fallback on the
// clipboard, the same way browsers do.
func WriteHTMLToClipboard(fragment, plain string) error {
	payload := buildHTMLPayload(fragment)
	if err := openClipboard(); err != nil {
		return err
	}
	defer closeClipboard()
	if r, _, err := procEmptyClipboard.Call(); r == 0 {
		return fmt.Errorf("EmptyClipboard: %v", err)
	}
	if err := setClipboardBytes(formats.html, payload); err != nil {
		return err
	}
	if plain == "" {
		plain = stripTags(fragment)
	}
	return setClipboardBytes(cfUnicodeText, utf16Bytes(plain))
}

// WriteImageToClipboard puts a PNG on the clipboard as CF_DIB (+ native PNG
// for apps that prefer it).
func WriteImageToClipboard(pngData []byte) error {
	img, err := png.Decode(bytes.NewReader(pngData))
	if err != nil {
		return err
	}
	dib := imageToDIB(img)
	if err := openClipboard(); err != nil {
		return err
	}
	defer closeClipboard()
	if r, _, cerr := procEmptyClipboard.Call(); r == 0 {
		return fmt.Errorf("EmptyClipboard: %v", cerr)
	}
	if err := setClipboardBytes(cfDIB, dib); err != nil {
		return err
	}
	if formats.png != 0 {
		_ = setClipboardBytes(formats.png, pngData)
	}
	return nil
}

// WriteFilesToClipboard puts a CF_HDROP file list on the clipboard.
func WriteFilesToClipboard(paths []string) error {
	if len(paths) == 0 {
		return errors.New("没有可复制的文件")
	}
	payload := buildHDropPayload(paths)
	if err := openClipboard(); err != nil {
		return err
	}
	defer closeClipboard()
	if r, _, cerr := procEmptyClipboard.Call(); r == 0 {
		return fmt.Errorf("EmptyClipboard: %v", cerr)
	}
	return setClipboardBytes(cfHDrop, payload)
}

// buildHDropPayload serialises a DROPFILES structure with wide strings.
func buildHDropPayload(paths []string) []byte {
	var buf bytes.Buffer
	header := make([]byte, cfDropFilesSize)
	binary.LittleEndian.PutUint32(header[0:4], cfDropFilesSize) // pFiles
	binary.LittleEndian.PutUint32(header[4:8], 0)               // pt.x
	binary.LittleEndian.PutUint32(header[8:12], 0)              // pt.y
	binary.LittleEndian.PutUint32(header[12:16], 0)             // fNC
	binary.LittleEndian.PutUint32(header[16:20], 1)             // fWide
	buf.Write(header)
	for _, p := range paths {
		buf.Write(utf16Bytes(p)) // includes terminating null
	}
	buf.Write([]byte{0, 0}) // extra null terminates the list
	return buf.Bytes()
}

// buildHTMLPayload wraps a fragment in the "HTML Format" clipboard envelope.
func buildHTMLPayload(fragment string) []byte {
	headerTpl := "Version:0.9\r\nStartHTML:%010d\r\nEndHTML:%010d\r\nStartFragment:%010d\r\nEndFragment:%010d\r\n"
	prefix := "<html><body><!--StartFragment-->"
	suffix := "<!--EndFragment--></body></html>"
	headerLen := len(headerTpl) // constant: %010d keeps all widths fixed

	startHTML := headerLen
	startFragment := headerLen + len(prefix)
	endFragment := startFragment + len(fragment)
	endHTML := endFragment + len(suffix)

	header := fmt.Sprintf(headerTpl, startHTML, endHTML, startFragment, endFragment)
	return []byte(header + prefix + fragment + suffix)
}

// htmlFragment extracts the clipboard HTML fragment using the byte offsets in
// its header.
func htmlFragment(data []byte) string {
	headerEnd := bytes.Index(data, []byte("<!--StartFragment-->"))
	if headerEnd >= 0 {
		start := headerEnd + len("<!--StartFragment-->")
		if end := bytes.Index(data[start:], []byte("<!--EndFragment-->")); end >= 0 {
			return string(data[start : start+end])
		}
	}
	startOff, endOff := -1, -1
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(line, "StartFragment:"):
			if v, err := parseInt(strings.TrimPrefix(line, "StartFragment:")); err == nil {
				startOff = v
			}
		case strings.HasPrefix(line, "EndFragment:"):
			if v, err := parseInt(strings.TrimPrefix(line, "EndFragment:")); err == nil {
				endOff = v
			}
		}
	}
	if startOff > 0 && endOff > startOff && endOff <= len(data) {
		return string(data[startOff:endOff])
	}
	return ""
}

func parseInt(s string) (int, error) {
	var out int
	for _, r := range strings.TrimSpace(s) {
		if r < '0' || r > '9' {
			if out > 0 {
				return out, nil
			}
			return 0, errors.New("not a number")
		}
		out = out*10 + int(r-'0')
	}
	if out == 0 {
		return 0, errors.New("empty number")
	}
	return out, nil
}

// ---- small helpers ------------------------------------------------------

func makePreview(s string, limit int) string {
	s = strings.TrimSpace(s)
	s = strings.ReplaceAll(s, "\r\n", " ")
	s = strings.ReplaceAll(s, "\n", " ")
	s = strings.ReplaceAll(s, "\t", " ")
	for strings.Contains(s, "  ") {
		s = strings.ReplaceAll(s, "  ", " ")
	}
	r := []rune(s)
	if len(r) > limit {
		return string(r[:limit]) + "…"
	}
	return string(r)
}

func isSingleURL(s string) bool {
	if s == "" || len(s) > 2048 || strings.ContainsAny(s, " \t\r\n") {
		return false
	}
	u, err := url.Parse(s)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return false
	}
	switch strings.ToLower(u.Scheme) {
	case "http", "https", "ftp", "ftps", "file", "mailto":
		return true
	}
	return false
}

func decodeANSI(b []byte) string {
	if i := bytes.IndexByte(b, 0); i >= 0 {
		b = b[:i]
	}
	r := make([]rune, 0, len(b))
	for _, c := range b {
		r = append(r, rune(c))
	}
	return string(r)
}

func stripANSI(s string) string { return strings.TrimRight(s, "\x00") }

func stripTags(html string) string {
	var out strings.Builder
	inTag := false
	for _, r := range html {
		switch {
		case r == '<':
			inTag = true
		case r == '>':
			inTag = false
		case !inTag:
			out.WriteRune(r)
		}
	}
	text := out.String()
	text = strings.ReplaceAll(text, "&nbsp;", " ")
	text = strings.ReplaceAll(text, "&amp;", "&")
	text = strings.ReplaceAll(text, "&lt;", "<")
	text = strings.ReplaceAll(text, "&gt;", ">")
	return strings.TrimSpace(text)
}

func utf16SliceToString(u []uint16) string {
	return windows.UTF16ToString(u)
}
