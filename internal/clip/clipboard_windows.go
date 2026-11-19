//go:build windows

package clip

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"image"
	"image/png"
	"path/filepath"
	"strings"
	"unsafe"
)

// 读剪贴板：枚举当前格式，按「文件 → 图片 → HTML → RTF → 文本/URI → 其它原始格式」
// 的顺序判定（先命中者胜），并把结果构造成 Captured。
// 不负责：格式常量与归类/跳过清单（clipboard_formats_windows.go）、
// 写回剪贴板（clipboard_write_windows.go）。

// captureClipboard inspects the current clipboard and returns a Captured value.
// It returns (nil, nil) when the clipboard holds nothing ClipNest can store.
//
// Classification order (first match wins, because that is what a user expects to
// paste back): files → image → HTML → RTF → text/URI → any other raw format.
func captureClipboard() (*Captured, error) {
	initFormats()
	if err := openClipboard(); err != nil {
		return nil, err
	}
	defer closeClipboard()

	formatsOnClipboard := enumerateFormats()
	has := func(id uint32) bool {
		for _, f := range formatsOnClipboard {
			if f.id == id {
				return true
			}
		}
		return false
	}

	// ---- file list (CF_HDROP) -------------------------------------------
	if has(cfHDrop) {
		if files, ok := readHDrop(); ok && len(files) > 0 {
			return buildFilesCapture(files), nil
		}
	}

	// ---- image -----------------------------------------------------------
	if c, err := readImageLocked(); err == nil && c != nil {
		return c, nil
	}

	// ---- plain text (needed as the fallback for rich formats) -----------
	text := ""
	if has(cfUnicodeText) {
		if b, err := readFormat(cfUnicodeText); err == nil {
			text = decodeUTF16(b)
		}
	}
	if text == "" && has(cfText) {
		if b, err := readFormat(cfText); err == nil {
			text = decodeANSI(b)
		}
	}

	// ---- HTML fragment ---------------------------------------------------
	if formats.html != 0 && has(formats.html) {
		if b, err := readFormat(formats.html); err == nil {
			if frag := htmlFragment(b); frag != "" {
				plain := text
				if plain == "" {
					plain = stripTags(frag)
				}
				if strings.TrimSpace(plain) != "" || strings.TrimSpace(frag) != "" {
					return buildHTMLCapture(plain, frag), nil
				}
			}
		}
	}

	// ---- RTF (Word, WordPad, Outlook …) ---------------------------------
	if formats.rtf != 0 && has(formats.rtf) {
		if b, err := readFormat(formats.rtf); err == nil {
			rtf := stripANSI(string(b))
			if strings.TrimSpace(rtf) != "" {
				plain := text
				if plain == "" {
					plain = rtfToText(rtf)
				}
				return buildRTFCapture(plain, rtf), nil
			}
		}
	}

	// ---- uri-list --------------------------------------------------------
	if formats.uriList != 0 && has(formats.uriList) {
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

	// ---- anything else, kept as raw bytes -------------------------------
	if c := readCustomLocked(formatsOnClipboard); c != nil {
		return c, nil
	}

	return nil, nil
}

// readCustomLocked stores the first "real" format ClipNest does not classify.
func readCustomLocked(formatsOnClipboard []clipFormat) *Captured {
	const maxCustom = 1 << 20 // 1 MiB: enough for colours, ranges, small blobs
	for _, f := range formatsOnClipboard {
		if !isStorableCustomFormat(f) {
			continue
		}
		data, err := readFormat(f.id)
		if err != nil || len(data) == 0 || len(data) > maxCustom {
			continue
		}
		name := f.name
		if name == "" {
			name = fmt.Sprintf("CF_%d", f.id)
		}
		sum := sha256.Sum256(append([]byte(name+"\x00"), data...))
		return &Captured{
			Kind:       KindCustom,
			Binary:     data,
			FormatName: name,
			FormatID:   f.id,
			Text:       name,
			Preview:    fmt.Sprintf("%s · %s", name, humanBytes(len(data))),
			Hash:       hex.EncodeToString(sum[:16]),
			Size:       len(data),
			SourceApp:  foregroundAppName(),
		}
	}
	return nil
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

func buildHTMLCapture(plain, htmlFragment string) *Captured {
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

func buildRTFCapture(plain, rtf string) *Captured {
	sum := sha256.Sum256([]byte("rtf\x00" + plain + "\x00" + rtf))
	return &Captured{
		Kind:      KindRichText,
		Text:      plain,
		RTF:       rtf,
		Preview:   makePreview(strings.TrimSpace(plain), 240),
		Hash:      hex.EncodeToString(sum[:16]),
		Size:      len(plain) + len(rtf),
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
