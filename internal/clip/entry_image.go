package clip

import (
	"bytes"
	"errors"
	"image"
	"image/png"
	"regexp"
	"strings"
)

// 条目附件：混合存储。
//
// 为什么不全部进数据库：SQLite 的 BLOB 适合小对象，几 MB 的图片会把 WAL、
// 备份、同步包一起拖大，而且每次读都要整块进内存。所以：
//
//	≤ 512 KB  → 进 memo_blobs（便携：整个 data 目录拷走就完整）
//	> 512 KB  → 落盘 <dataDir>/memo_files/<hash>.<ext>，库里只留元数据
//
// 正文统一引用 memoimg:<hash>，前端拿到的永远是 data URL——大图会先缩成
// 缩略图（nearest-neighbour，够看），要原图再「打开原文件」。

const (
	memoImagePrefix = "memoimg:"
	// 内联进 SQLite 的上限
	memoInlineMax = 512 << 10
	// 返回给界面的上限：超过就回缩略图，避免 base64 把内存和渲染拖死
	memoRenderMax = 2 << 10 * 1024
	// 缩略图长边
	memoThumbEdge = 1280
)

// MemoImageMeta 描述一条附件在哪、多大。
type MemoImageMeta struct {
	Hash    string `json:"hash"`
	Ext     string `json:"ext"`
	Mime    string `json:"mime"`
	Size    int64  `json:"size"`
	Storage string `json:"storage"` // db | file
	Path    string `json:"path"`    // 相对 dataDir（file 存储时有值）
}

// SaveImage stores an image (data URL or bare base64) and returns the token to
// put in Markdown: memoimg:<hash>. Content-addressed, so the same picture is
// stored once.
func (s *ClipService) SaveImage(dataB64 string) (string, error) {
	mime, payload, err := decodeImagePayload(dataB64)
	if err != nil {
		return "", err
	}
	if len(payload) == 0 {
		return "", errors.New("图片内容为空")
	}
	return s.store.SaveEntryBlob(payload, extForMime(mime))
}

// GetImage returns a data URL for display. Oversized images come back as a
// downscaled PNG so a 20 MB screenshot does not become a 27 MB string.
func (s *ClipService) GetImage(hash string) (string, error) {
	payload, mime, err := s.store.LoadEntryBlob(hash)
	if err != nil {
		return "", err
	}
	if mime == "" {
		mime = "image/png"
	}
	if len(payload) > memoRenderMax {
		if thumb, err := downscalePNG(payload, memoThumbEdge); err == nil && len(thumb) > 0 {
			return "data:image/png;base64," + encodeBase64(thumb), nil
		}
	}
	return "data:" + mime + ";base64," + encodeBase64(payload), nil
}

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
