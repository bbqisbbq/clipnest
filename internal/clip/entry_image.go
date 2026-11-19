package clip

import (
	"bytes"
	"errors"
	"image"
	"image/png"
	"regexp"
	"strings"
)

// 条目附件的图片处理：data URL 解码、MIME 嗅探、缩略图缩放，以及旧版本留下的引用格式。
// 只做纯解码/缩放，不碰数据库——字节存取与「小图进库、大图落盘」的策略在 entry_blob.go。

// downscalePNG shrinks an image so its longest edge is at most edge pixels.
// Nearest-neighbour is enough for a preview and keeps us dependency-free.
func downscalePNG(src []byte, edge int) ([]byte, error) {
	img, _, err := image.Decode(bytes.NewReader(src))
	if err != nil {
		return nil, err
	}
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	if w <= edge && h <= edge {
		return src, nil
	}
	scale := float64(edge) / float64(w)
	if h > w {
		scale = float64(edge) / float64(h)
	}
	nw, nh := int(float64(w)*scale), int(float64(h)*scale)
	if nw < 1 {
		nw = 1
	}
	if nh < 1 {
		nh = 1
	}
	dst := image.NewRGBA(image.Rect(0, 0, nw, nh))
	for y := 0; y < nh; y++ {
		sy := b.Min.Y + y*h/nh
		for x := 0; x < nw; x++ {
			sx := b.Min.X + x*b.Dx()/nw
			dst.Set(x, y, img.At(sx, sy))
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, dst); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func decodeImagePayload(dataB64 string) (mime string, payload []byte, err error) {
	raw := strings.TrimSpace(dataB64)
	if raw == "" {
		return "", nil, errors.New("没有图片数据")
	}
	if idx := strings.Index(raw, ","); strings.HasPrefix(raw, "data:") && idx > 0 {
		header := raw[5:idx]
		mime = strings.TrimSpace(strings.SplitN(header, ";", 2)[0])
		raw = raw[idx+1:]
	}
	payload, err = decodeBase64Image(raw)
	if err != nil {
		return "", nil, err
	}
	if mime == "" {
		mime = sniffImageMime(payload)
	}
	if !strings.HasPrefix(mime, "image/") {
		return "", nil, errors.New("不支持的图片类型：" + mime)
	}
	return mime, payload, nil
}

func sniffImageMime(b []byte) string {
	if len(b) > 8 && b[0] == 0x89 && b[1] == 'P' && b[2] == 'N' && b[3] == 'G' {
		return "image/png"
	}
	if len(b) > 3 && b[0] == 0xFF && b[1] == 0xD8 {
		return "image/jpeg"
	}
	if len(b) > 12 && string(b[0:4]) == "GIF8" {
		return "image/gif"
	}
	if len(b) > 12 && string(b[8:12]) == "WEBP" {
		return "image/webp"
	}
	return "image/png"
}

func extForMime(mime string) string {
	switch mime {
	case "image/jpeg":
		return "jpg"
	case "image/gif":
		return "gif"
	case "image/webp":
		return "webp"
	default:
		return "png"
	}
}

// ---- 旧数据迁移 ----------------------------------------------------------

// legacyImageRef matches the references older builds wrote into memo bodies:
//
//	![](3a45ab6124e06b6d.png)
//
// i.e. a bare "<itemID><ext>" with no scheme. The webview cannot load those, so
// such memos always showed a broken image. We resolve them against the clipboard
// blobs table, copy the bytes into memo_blobs and rewrite the reference to
// memoimg:<hash>.
var legacyImageRef = regexp.MustCompile(`!\[([^\]]*)\]\(([0-9a-fA-F]{8,})(\.(?:png|jpg|jpeg|gif|webp|bmp))\)`)
