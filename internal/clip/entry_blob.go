package clip

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"
)

// 条目附件的存取：内容寻址的 entry_blobs 表（小图进库、大图落盘）、memoimg:<hash>
// 令牌、按条目类型取扩展名，以及图片的 data URL 读写。
// 不负责：图片解码与缩略图缩放（entry_image.go）。

// ---- 附件（内容寻址，剪贴板图片与备忘图片共用） --------------------------

// SaveEntryBlob stores bytes under their content hash and returns the token used
// in Markdown. Small blobs stay in SQLite; larger ones go to disk so the
// database (and its WAL, and the sync package) stay small.
func (s *Store) SaveEntryBlob(payload []byte, ext string) (string, error) {
	if len(payload) == 0 {
		return "", errors.New("内容为空")
	}
	if ext == "" {
		ext = "png"
	}
	sum := sha256.Sum256(payload)
	hash := hex.EncodeToString(sum[:])[:24]
	mime := sniffImageMime(payload)

	storage := "db"
	relPath := ""
	var data []byte
	if len(payload) > memoInlineMax {
		relPath = "entry_files/" + hash + "." + ext
		abs := filepath.Join(s.dir, relPath)
		if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
			return "", err
		}
		if err := os.WriteFile(abs, payload, 0o644); err != nil {
			return "", err
		}
		storage = "file"
	} else {
		data = payload
	}
	if _, err := s.db.Exec(`
		INSERT INTO entry_blobs (hash, ext, mime, data, storage, size, path)
		VALUES (?,?,?,?,?,?,?)
		ON CONFLICT(hash) DO UPDATE SET
			mime = excluded.mime, size = excluded.size, storage = excluded.storage,
			path = excluded.path,
			data = CASE WHEN excluded.storage = 'db' THEN excluded.data ELSE entry_blobs.data END`,
		hash, ext, mime, data, storage, len(payload), relPath); err != nil {
		return "", err
	}
	return memoImagePrefix + hash, nil
}

// LoadEntryBlob returns the bytes behind a hash (with or without the token prefix).
func (s *Store) LoadEntryBlob(hash string) ([]byte, string, error) {
	hash = strings.TrimPrefix(strings.TrimSpace(hash), memoImagePrefix)
	if hash == "" {
		return nil, "", errors.New("缺少标识")
	}
	var (
		mime, storage, path string
		data                []byte
	)
	if err := s.db.QueryRow(
		`SELECT mime, storage, path, data FROM entry_blobs WHERE hash = ?`, hash).
		Scan(&mime, &storage, &path, &data); err != nil {
		return nil, "", err
	}
	if storage == "file" && path != "" {
		buf, err := os.ReadFile(filepath.Join(s.dir, path))
		if err != nil {
			return nil, mime, err
		}
		return buf, mime, nil
	}
	return data, mime, nil
}

// blobExtForKind is the file extension used for an entry's attachment.
func blobExtForKind(k Kind) string {
	if k == KindImage {
		return ".png"
	}
	return ""
}

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
