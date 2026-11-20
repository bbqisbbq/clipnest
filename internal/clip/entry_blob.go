package clip

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"regexp"
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
	if err := s.SaveEntryBlobAs(hash, payload, ext); err != nil {
		return "", err
	}
	return memoImagePrefix + hash, nil
}

// SaveEntryBlobAs stores bytes under an explicit key. Used by captures whose
// payload has no place in the Markdown body: 私有格式（Excel 区间、颜色、音频…）
// 的正文只要是格式名（给人看的），字节则挂在这条条目的采集哈希下，
// 回填时按 `Entry.Hash` 取回来（见 service_paste.go 的 KindCustom 分支）。
func (s *Store) SaveEntryBlobAs(key string, payload []byte, ext string) error {
	if len(payload) == 0 {
		return errors.New("内容为空")
	}
	key = strings.TrimPrefix(strings.TrimSpace(key), memoImagePrefix)
	if key == "" {
		return errors.New("缺少附件标识")
	}
	if ext == "" {
		ext = "png"
	}
	hash := key
	mime := sniffImageMime(payload)

	storage := "db"
	relPath := ""
	var data []byte
	if len(payload) > memoInlineMax {
		relPath = "entry_files/" + hash + "." + ext
		abs := filepath.Join(s.dir, relPath)
		if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(abs, payload, 0o644); err != nil {
			return err
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
		return err
	}
	return nil
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

// entryBlobKey reports where an entry's payload lives: the memoimg token inside
// the body for images, or the capture hash for 私有格式 (their body is a human
// readable format name, not a token). Empty = this entry carries no payload.
func entryBlobKey(e Entry) string {
	if token := memoImageToken(e.Content); token != "" {
		return token
	}
	if e.Kind == KindCustom && e.Hash != "" {
		return e.Hash
	}
	return ""
}

// entryBlobExt is the extension to store/fetch that payload under. It doubles as
// the suffix inside the sync package ("entry-<hash>.bin"), so it must stay stable.
func entryBlobExt(e Entry) string {
	if memoImageToken(e.Content) != "" {
		return blobExtForKind(e.Kind)
	}
	if e.Kind == KindCustom {
		return ".bin"
	}
	return ""
}

// ---- 附件垃圾回收 -------------------------------------------------------

var blobTokenPattern = regexp.MustCompile(`memoimg:[0-9a-f]{6,64}`)

// memoImageTokens lists every memoimg token in a body (a body may carry several
// pictures). Keys come back bare (no prefix), ready for LoadEntryBlob.
func memoImageTokens(content string) []string {
	matches := blobTokenPattern.FindAllString(content, -1)
	if len(matches) == 0 {
		return nil
	}
	out := make([]string, 0, len(matches))
	for _, m := range matches {
		out = append(out, strings.TrimPrefix(m, memoImagePrefix))
	}
	return out
}

// GCBlobs drops attachments nothing references any more: the entry_blobs row,
// and the file on disk when it was too big to inline.
//
// 内容寻址意味着同一张图可以被多条条目共用，所以判断"还在用"只能扫正文里的
// 令牌（外加私有格式按 hash 引用）。只在删除条目后跑一次——这个操作本来就不频繁，
// 而它要遍历整张 entries 表。
func (s *Store) GCBlobs() (rows, files int) {
	referenced := make(map[string]bool, 256)
	rowsIter, err := s.db.Query(`SELECT content, hash, kind FROM entries`)
	if err != nil {
		Logf(s.dir, "blob gc: scan entries failed: %v", err)
		return 0, 0
	}
	for rowsIter.Next() {
		var (
			content, hash, kind string
		)
		if rowsIter.Scan(&content, &hash, &kind) != nil {
			continue
		}
		for _, token := range memoImageTokens(content) {
			referenced[token] = true
		}
		if Kind(kind) == KindCustom && hash != "" {
			referenced[hash] = true
		}
	}
	rowsIter.Close()

	type blobRow struct{ hash, storage, path string }
	var dead []blobRow
	blobIter, err := s.db.Query(`SELECT hash, storage, path FROM entry_blobs`)
	if err != nil {
		Logf(s.dir, "blob gc: scan blobs failed: %v", err)
		return 0, 0
	}
	for blobIter.Next() {
		var b blobRow
		if blobIter.Scan(&b.hash, &b.storage, &b.path) != nil {
			continue
		}
		if !referenced[b.hash] {
			dead = append(dead, b)
		}
	}
	blobIter.Close()

	for _, b := range dead {
		if _, err := s.db.Exec(`DELETE FROM entry_blobs WHERE hash = ?`, b.hash); err != nil {
			Logf(s.dir, "blob gc: delete row %s: %v", b.hash, err)
			continue
		}
		rows++
		if b.storage == "file" && b.path != "" {
			if err := os.Remove(filepath.Join(s.dir, b.path)); err == nil {
				files++
			}
		}
	}
	// 行没了但文件还在（旧版本留下的）：按文件名里的 hash 再扫一遍目录。
	if dirEntries, err := os.ReadDir(filepath.Join(s.dir, "entry_files")); err == nil {
		for _, de := range dirEntries {
			if de.IsDir() {
				continue
			}
			hash, _, _ := strings.Cut(de.Name(), ".")
			if referenced[hash] {
				continue
			}
			if err := os.Remove(filepath.Join(s.dir, "entry_files", de.Name())); err == nil {
				files++
			}
		}
	}
	if rows > 0 || files > 0 {
		Logf(s.dir, "blob gc: removed %d blob rows, %d files", rows, files)
	}
	return rows, files
}

// 条目附件：混合存储。
//
// 为什么不全部进数据库：SQLite 的 BLOB 适合小对象，几 MB 的图片会把 WAL、
// 备份、同步包一起拖大，而且每次读都要整块进内存。所以：
//
//	≤ 512 KB  → 进 entry_blobs（便携：整个 data 目录拷走就完整）
//	> 512 KB  → 落盘 <dataDir>/entry_files/<hash>.<ext>，库里只留元数据
//
// 正文统一引用 memoimg:<hash>，前端拿到的永远是 data URL——大图会先缩成
// 缩略图（nearest-neighbour，够看），要原图再「打开原文件」。
// 私有格式（KindCustom）没有可读的正文，字节挂在采集哈希下，不走 memoimg 令牌，
// 见 SaveEntryBlobAs。

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
