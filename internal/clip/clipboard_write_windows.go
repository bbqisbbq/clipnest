//go:build windows

package clip

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"image/png"
)

// 写剪贴板：文本/HTML/RTF/图片/文件列表/自定义格式的回填，以及它们需要的
// DROPFILES 与 "HTML Format" 信封。
// 不负责：读取与归类（clipboard_windows.go / clipboard_formats_windows.go）。

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
	initFormats()
	// 先确认格式就绪再动剪贴板：以前这里少了 initFormats，"自动收集"关着启动时
	// formats.html 还是 0，于是先 EmptyClipboard 再失败 —— 用户的剪贴板被清空。
	if formats.html == 0 {
		return errors.New("当前系统没有可用的 HTML 剪贴板格式")
	}
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
