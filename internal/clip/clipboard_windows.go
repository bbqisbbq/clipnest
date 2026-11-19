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
//
// 三个 hashXxx 是采集侧的唯一哈希口径：Captured.Hash 用它们算，写回剪贴板时
// （entryEchoHash）也用它们算，这样"自己刚写出去的内容"才能被认出来并丢掉。
// 千万不要在这里改用 contentHash()：entry 的 hash 列是另一套口径（同步配对用），
// 两者混用会让手写备忘复制回去时"抑制"失效、凭空多出一条采集条目。

func textHash(text string) string {
	sum := sha256.Sum256([]byte("text\x00" + text))
	return hex.EncodeToString(sum[:16])
}

func htmlHash(plain, fragment string) string {
	sum := sha256.Sum256([]byte("rich\x00" + plain + "\x00" + fragment))
	return hex.EncodeToString(sum[:16])
}

func rtfHash(plain, rtf string) string {
	sum := sha256.Sum256([]byte("rtf\x00" + plain + "\x00" + rtf))
	return hex.EncodeToString(sum[:16])
}

func filesHash(paths []string) string {
	sum := sha256.Sum256([]byte("files\x00" + strings.Join(paths, "\x00")))
	return hex.EncodeToString(sum[:16])
}

func buildTextCapture(text string) *Captured {
	kind := KindText
	trimmed := strings.TrimSpace(text)
	if isSingleURL(trimmed) {
		kind = KindLink
	}
	return &Captured{
		Kind:      kind,
		Text:      text,
		Preview:   makePreview(trimmed, 240),
		Hash:      textHash(text),
		Size:      len(text),
		SourceApp: foregroundAppName(),
	}
}

func buildHTMLCapture(plain, htmlFragment string) *Captured {
	return &Captured{
		Kind:      KindRichText,
		Text:      plain,
		HTML:      htmlFragment,
		Preview:   makePreview(strings.TrimSpace(plain), 240),
		Hash:      htmlHash(plain, htmlFragment),
		Size:      len(plain) + len(htmlFragment),
		SourceApp: foregroundAppName(),
	}
}

func buildRTFCapture(plain, rtf string) *Captured {
	return &Captured{
		Kind:      KindRichText,
		Text:      plain,
		RTF:       rtf,
		Preview:   makePreview(strings.TrimSpace(plain), 240),
		Hash:      rtfHash(plain, rtf),
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
	preview := strings.Join(names, ", ")
	return &Captured{
		Kind:      KindFiles,
		Text:      strings.Join(paths, "\r\n"),
		Files:     files,
		Preview:   makePreview(preview, 240),
		Hash:      filesHash(paths),
		Size:      int(total),
		SourceApp: foregroundAppName(),
	}
}

// entryEchoHash returns the capture-side hash that re-reading this entry from the
// clipboard would produce. writeEntryToClipboard records it so the listener can
// drop the echo of our own copy-back — without it, copying a hand-written memo
// back to the clipboard creates a second entry that looks like it came from
// ClipNest itself (来源 = clipnest).
//
// 图片走的是 entry.Hash：采集与写回用的是同一个 PNG 编码器，字节一致。
// 自定义格式没有可重算的口径，返回 ""（交给 SaveCapture 的内容去重兜底）。
func entryEchoHash(e Entry) string {
	switch e.Kind {
	case KindImage:
		return e.Hash
	case KindFiles:
		paths := make([]string, 0, len(e.Files))
		for _, f := range e.Files {
			paths = append(paths, f.Path)
		}
		return filesHash(paths)
	case KindRichText:
		if e.RTF != "" {
			return rtfHash(e.Content, e.RTF)
		}
		return htmlHash(e.Content, e.HTML)
	case KindCustom:
		return ""
	default: // KindText / KindLink：写的是 CF_UNICODETEXT，读回来是同一个字符串
		return textHash(e.Content)
	}
}
