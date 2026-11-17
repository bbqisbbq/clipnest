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

// clipFormat is one entry of the clipboard format enumeration.
type clipFormat struct {
	id   uint32
	name string
}

// enumerateFormats lists every format currently on the open clipboard.
func enumerateFormats() []clipFormat {
	out := make([]clipFormat, 0, 16)
	var next uint32
	for {
		id, _, _ := procEnumClipboardFormats.Call(uintptr(next))
		if id == 0 {
			break
		}
		next = uint32(id)
		out = append(out, clipFormat{id: next, name: formatName(next)})
	}
	return out
}

// formatName resolves a clipboard format id to its name where possible.
func formatName(id uint32) string {
	if id < 0xC000 {
		switch id {
		case cfText:
			return "CF_TEXT"
		case cfBitmap:
			return "CF_BITMAP"
		case 3:
			return "CF_METAFILEPICT"
		case 8:
			return "CF_DIB"
		case 9:
			return "CF_PALETTE"
		case cfUnicodeText:
			return "CF_UNICODETEXT"
		case 14:
			return "CF_ENHMETAFILE"
		case cfHDrop:
			return "CF_HDROP"
		case cfDIBV5:
			return "CF_DIBV5"
		}
		return ""
	}
	buf := make([]uint16, 256)
	n, _, _ := procGetClipboardFormatNameW.Call(uintptr(id), uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)))
	if n == 0 {
		return ""
	}
	return windows.UTF16ToString(buf[:n])
}

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

// isStorableCustomFormat filters out handles that are not HGLOBAL payloads
// (GlobalSize on a HBITMAP/HMETAFILE corrupts the heap) and clipboard plumbing.
func isStorableCustomFormat(f clipFormat) bool {
	switch f.id {
	case cfBitmap, 3 /*CF_METAFILEPICT*/, 9 /*CF_PALETTE*/, 14 /*CF_ENHMETAFILE*/,
		cfText, cfUnicodeText, cfHDrop, cfDIB, cfDIBV5:
		return false
	}
	if formats.png != 0 && f.id == formats.png {
		return false
	}
	if formats.html != 0 && f.id == formats.html {
		return false
	}
	if formats.rtf != 0 && f.id == formats.rtf {
		return false
	}
	if formats.uriList != 0 && f.id == formats.uriList {
		return false
	}
	switch strings.ToLower(f.name) {
	case "", "dataobject", "ole private data", "dataobjectattributes",
		"dataobjectattributesrequiringelevation", "shell idlist array",
		"preferred dropeffect", "dropdescription", "zoneidentifier",
		"filename", "filenamew", "filegroupdescriptor", "filegroupdescriptorw",
		"untrusteddragdrop", "logical file name", "file contents":
		return false
	}
	return true
}

// WriteRTFToClipboard puts an RTF payload plus a plain-text fallback on the
// clipboard, which is what Word/WordPad/Outlook expect.
func WriteRTFToClipboard(rtf, plain string) error {
	initFormats()
	if formats.rtf == 0 {
		return errors.New("当前系统没有可用的 RTF 剪贴板格式")
	}
	if err := openClipboard(); err != nil {
		return err
	}
	defer closeClipboard()
	if r, _, cerr := procEmptyClipboard.Call(); r == 0 {
		return fmt.Errorf("EmptyClipboard: %v", cerr)
	}
	if err := setClipboardBytes(formats.rtf, []byte(rtf)); err != nil {
		return err
	}
	if plain == "" {
		plain = rtfToText(rtf)
	}
	if plain == "" {
		return nil
	}
	return setClipboardBytes(cfUnicodeText, utf16Bytes(plain))
}

// WriteRawToClipboard restores a custom entry under its original format id.
func WriteRawToClipboard(formatID uint32, formatName string, data []byte) error {
	initFormats()
	if len(data) == 0 {
		return errors.New("没有可复制的数据")
	}
	id := formatID
	if formatName != "" {
		if reg := registerFormat(formatName); reg != 0 {
			id = reg
		}
	}
	if id == 0 {
		return fmt.Errorf("无法还原剪贴板格式 %q", formatName)
	}
	if err := openClipboard(); err != nil {
		return err
	}
	defer closeClipboard()
	if r, _, cerr := procEmptyClipboard.Call(); r == 0 {
		return fmt.Errorf("EmptyClipboard: %v", cerr)
	}
	return setClipboardBytes(id, data)
}

// rtfToText is a small RTF-to-plain converter: enough for previews and for the
// text fallback, not a full RTF parser.
func rtfToText(rtf string) string {
	var b strings.Builder
	depth := 0
	skipDepth := 0 // depth of the group we are currently ignoring (0 = not skipping)

	for i := 0; i < len(rtf); i++ {
		c := rtf[i]
		switch c {
		case '{':
			depth++
			if i+1 < len(rtf) {
				head := rtf[i+1:]
				for _, dest := range []string{
					"\\fonttbl", "\\colortbl", "\\stylesheet", "\\info", "\\*", "\\pict",
					"\\header", "\\footer", "\\object", "\\themedata", "\\datastore",
				} {
					if strings.HasPrefix(head, dest) {
						if skipDepth == 0 {
							skipDepth = depth
						}
						break
					}
				}
			}
		case '}':
			if skipDepth != 0 && depth <= skipDepth {
				skipDepth = 0
			}
			depth--
		case '\\':
			j := i + 1
			for j < len(rtf) && ((rtf[j] >= 'a' && rtf[j] <= 'z') || (rtf[j] >= 'A' && rtf[j] <= 'Z')) {
				j++
			}
			word := rtf[i+1 : j]
			for j < len(rtf) && (rtf[j] == '-' || (rtf[j] >= '0' && rtf[j] <= '9')) {
				j++
			}
			if j < len(rtf) && rtf[j] == ' ' {
				j++
			}
			if skipDepth == 0 {
				switch word {
				case "par", "line", "sect", "row":
					b.WriteString("\n")
				case "tab", "cell":
					b.WriteString("\t")
				case "emdash":
					b.WriteString("—")
				case "endash":
					b.WriteString("–")
				case "lquote":
					b.WriteString("‘")
				case "rquote":
					b.WriteString("’")
				case "ldblquote":
					b.WriteString("“")
				case "rdblquote":
					b.WriteString("”")
				case "bullet":
					b.WriteString("•")
				}
			}
			i = j - 1
		default:
			if skipDepth == 0 && c != '\r' && c != '\n' {
				b.WriteByte(c)
			}
		}
	}
	return strings.TrimSpace(b.String())
}

func humanBytes(n int) string {
	switch {
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.1f KB", float64(n)/(1<<10))
	default:
		return fmt.Sprintf("%d B", n)
	}
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
