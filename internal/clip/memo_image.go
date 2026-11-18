package clip

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"image"
	"image/png"
	"bytes"
	"os"
	"path/filepath"
	"strings"
)

// 备忘附件：混合存储。
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
	Path    string `json:"path"`     // 相对 dataDir（file 存储时有值）
}

// SaveMemoImage stores an image (data URL or bare base64) and returns the token
// to put inside Markdown: memoimg:<hash>.
func (s *ClipService) SaveMemoImage(dataB64 string) (string, error) {
	mime, payload, err := decodeImagePayload(dataB64)
	if err != nil {
		return "", err
	}
	if len(payload) == 0 {
		return "", errors.New("图片内容为空")
	}
	sum := sha256.Sum256(payload)
	hash := hex.EncodeToString(sum[:])[:24]
	ext := extForMime(mime)

	storage := "db"
	relPath := ""
	if len(payload) > memoInlineMax {
		// 大文件落盘：只有元数据进库
		relPath = filepath.Join("memo_files", hash+"."+ext)
		abs := filepath.Join(s.store.Dir(), relPath)
		if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
			return "", err
		}
		if err := os.WriteFile(abs, payload, 0o644); err != nil {
			return "", err
		}
		storage = "file"
	}

	var blob []byte
	if storage == "db" {
		blob = payload
	}
	if _, err := s.store.db.Exec(`
		INSERT INTO memo_blobs (hash, ext, mime, data, storage, size, path)
		VALUES (?,?,?,?,?,?,?)
		ON CONFLICT(hash) DO UPDATE SET
			mime = excluded.mime, size = excluded.size,
			storage = excluded.storage, path = excluded.path,
			data = CASE WHEN excluded.storage = 'db' THEN excluded.data ELSE memo_blobs.data END`,
		hash, ext, mime, blob, storage, len(payload), relPath); err != nil {
		return "", err
	}
	Logf(s.store.Dir(), "memo image saved: %s (%s, %s, %d bytes)", hash, mime, storage, len(payload))
	return memoImagePrefix + hash, nil
}

// GetMemoImage returns a data URL for display. Oversized images come back as a
// downscaled PNG so a 20 MB screenshot does not become a 27 MB string.
func (s *ClipService) GetMemoImage(hash string) (string, error) {
	meta, payload, err := s.loadMemoImage(hash)
	if err != nil {
		return "", err
	}
	if meta.Mime == "" {
		meta.Mime = "image/png"
	}
	if meta.Size > memoRenderMax {
		if thumb, err := downscalePNG(payload, memoThumbEdge); err == nil && len(thumb) > 0 {
			return "data:image/png;base64," + encodeBase64(thumb), nil
		}
	}
	return "data:" + meta.Mime + ";base64," + encodeBase64(payload), nil
}

// GetMemoImageInfo reports where an attachment lives (used by the UI to show
// size / storage kind, and to open the original file).
func (s *ClipService) GetMemoImageInfo(hash string) (MemoImageMeta, error) {
	meta, payload, err := s.loadMemoImage(hash)
	if err != nil {
		return MemoImageMeta{}, err
	}
	if meta.Size == 0 {
		meta.Size = int64(len(payload))
	}
	return meta, nil
}

// OpenMemoImage opens the original file in the system viewer (large ones only
// live on disk, so there is always a real file for them).
func (s *ClipService) OpenMemoImage(hash string) error {
	meta, payload, err := s.loadMemoImage(hash)
	if err != nil {
		return err
	}
	if meta.Storage != "file" || meta.Path == "" {
		// 小图只在库里：导出成临时文件再打开
		tmp := filepath.Join(os.TempDir(), "clipnest-"+meta.Hash+"."+meta.Ext)
		if err := os.WriteFile(tmp, payload, 0o644); err != nil {
			return err
		}
		return shellOpen(tmp)
	}
	return shellOpen(filepath.Join(s.store.Dir(), meta.Path))
}

func (s *ClipService) loadMemoImage(hash string) (MemoImageMeta, []byte, error) {
	hash = strings.TrimPrefix(strings.TrimSpace(hash), memoImagePrefix)
	if hash == "" {
		return MemoImageMeta{}, nil, errors.New("缺少图片标识")
	}
	var (
		meta MemoImageMeta
		data []byte
		path string
	)
	err := s.store.db.QueryRow(
		`SELECT hash, ext, mime, data, storage, size, path FROM memo_blobs WHERE hash = ?`, hash).
		Scan(&meta.Hash, &meta.Ext, &meta.Mime, &data, &meta.Storage, &meta.Size, &path)
	if err != nil {
		return MemoImageMeta{}, nil, err
	}
	meta.Path = path
	if meta.Storage == "file" && path != "" {
		buf, err := os.ReadFile(filepath.Join(s.store.Dir(), path))
		if err != nil {
			return MemoImageMeta{}, nil, err
		}
		return meta, buf, nil
	}
	if meta.Size == 0 {
		meta.Size = int64(len(data))
	}
	return meta, data, nil
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

// PruneMemoImages drops attachments no memo references any more (DB row + file).
func (s *Store) PruneMemoImages() int {
	type row struct {
		hash    string
		storage string
		path    string
	}
	rows, err := s.db.Query(`SELECT hash, storage, path FROM memo_blobs`)
	if err != nil {
		return 0
	}
	var all []row
	for rows.Next() {
		var r row
		if rows.Scan(&r.hash, &r.storage, &r.path) == nil {
			all = append(all, r)
		}
	}
	rows.Close()
	removed := 0
	for _, r := range all {
		var n int
		if err := s.db.QueryRow(
			`SELECT COUNT(*) FROM memos WHERE content LIKE ?`,
			"%"+memoImagePrefix+r.hash+"%").Scan(&n); err != nil {
			continue
		}
		if n > 0 {
			continue
		}
		if _, err := s.db.Exec(`DELETE FROM memo_blobs WHERE hash = ?`, r.hash); err == nil {
			removed++
			if r.storage == "file" && r.path != "" {
				_ = os.Remove(filepath.Join(s.dir, r.path))
			}
		}
	}
	return removed
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
