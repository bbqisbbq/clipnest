package clip

// PP-OCR 资源的「一键下载」。
//
// 为什么不打进安装包：解压后 42MB（onnxruntime.dll 21.6MB + det 4.6MB + rec 15.8MB +
// 字典），而绝大多数人用默认的系统引擎就够了。所以装的时候不背这个包袱，谁想要
// 高精度谁点一下 —— 安装包还是 24MB。
//
// 资源放在本仓库的一个 **pre-release**（tag `ocr-assets-v1`）：
//   · 它不会出现在 /releases/latest 里，所以**不影响自动更新**（实测 latest 仍是应用版本）；
//   · 内容是固定的，模型的版本不会随应用每次发版而变，所以 URL 与哈希可以写死在代码里。
//
// 下载流程（顺序很重要）：下到临时文件 → 校验**大小 + SHA256** → 解压白名单里的 4 个文件
// （每个先写 .tmp 再 rename，避免半个文件留在那儿让引擎"看起来装了但用不了"）。

import (
	"archive/zip"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/bbqisbbq/clipnest/internal/ppocr"
)

const (
	// ocrAssetsURL 指向仓库里那个资源 pre-release 的资产。
	ocrAssetsURL = "https://github.com/bbqisbbq/clipnest/releases/download/ocr-assets-v1/ppocr-assets.zip"
	// ocrAssetsSize / ocrAssetsSHA 是发布时实测的字节数与哈希（换资源包必须一起改）。
	ocrAssetsSize = 26513339
	ocrAssetsSHA  = "beaf3fb267f910d4665c94dd03d1142313384eb4162400552f26dfdc9c39aebc"
	// ocrDownloadTimeout 覆盖整个下载：40MB 在慢网下也不该被 30 秒掐断。
	ocrDownloadTimeout = 10 * time.Minute
)

// OCRProgress 是 `ocr:progress` 事件的数据（设置页拿它画进度）。
type OCRProgress struct {
	// Stage: download | extract | done
	Stage string `json:"stage"`
	// Received / Total 是字节数（extract 阶段用 已解压文件数/总数）
	Received int64 `json:"received"`
	Total    int64 `json:"total"`
	// Text 给界面直接显示的一句话
	Text string `json:"text"`
}

// ocrAssetFiles 是允许从包里解出来的文件（白名单，防 zip 里的路径穿越与夹带）。
var ocrAssetFiles = map[string]bool{
	ppocr.FileLibrary: true,
	ppocr.FileDet:     true,
	ppocr.FileRec:     true,
	ppocr.FileKeys:    true,
}

// DownloadOCRAssets 下载并安装 PP-OCR 资源，返回一句给界面显示的结果。
func (s *ClipService) DownloadOCRAssets() (string, error) {
	if !s.ocrDownloading.CompareAndSwap(false, true) {
		return "", errors.New("已经在下载了")
	}
	defer s.ocrDownloading.Store(false)

	if !ppocr.CompiledIn() {
		return "", ppocr.ErrUnavailable
	}
	dir := s.OcrAssetsDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}

	s.emit("ocr:progress", OCRProgress{Stage: "download", Total: ocrAssetsSize, Text: "正在下载 OCR 资源…"})

	zipPath, err := s.fetchOCRZip()
	if err != nil {
		s.emit("ocr:progress", OCRProgress{Stage: "download", Text: "下载失败：" + err.Error()})
		return "", err
	}
	defer func() { _ = os.Remove(zipPath) }()

	installed, err := s.extractOCRZip(zipPath, dir)
	if err != nil {
		s.emit("ocr:progress", OCRProgress{Stage: "extract", Text: "解压失败：" + err.Error()})
		return "", err
	}
	if err := ppocr.Ready(dir); err != nil {
		s.emit("ocr:progress", OCRProgress{Stage: "extract", Text: "资源仍不完整"})
		return "", err
	}
	msg := fmt.Sprintf("已安装 %d 个文件（%s）", installed, dir)
	s.emit("ocr:progress", OCRProgress{Stage: "done", Received: ocrAssetsSize, Total: ocrAssetsSize, Text: msg})
	Logf(dir, "ocr: 资源下载完成（%s）", msg)
	return msg, nil
}

// fetchOCRZip 下载资源包到临时文件，校验大小与 SHA256，返回本地路径。
func (s *ClipService) fetchOCRZip() (string, error) {
	ctx := s.bgCtx
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, ocrAssetsURL, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", "ClipNest/"+Version)

	client := &http.Client{Timeout: ocrDownloadTimeout}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("连接失败: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("HTTP %d（资源包可能被撤下或网络被拦）", resp.StatusCode)
	}

	tmp := filepath.Join(os.TempDir(), "clipnest-ocr-assets.zip")
	f, err := os.Create(tmp)
	if err != nil {
		return "", err
	}
	// 边下边算哈希、边报进度：40MB 在慢网下要几十秒，界面不能干等着。
	hasher := sha256.New()
	buf := make([]byte, 128*1024)
	var received int64
	lastEmit := time.Now()
	for {
		n, readErr := resp.Body.Read(buf)
		if n > 0 {
			if _, werr := f.Write(buf[:n]); werr != nil {
				_ = f.Close()
				_ = os.Remove(tmp)
				return "", fmt.Errorf("写入失败: %w", werr)
			}
			hasher.Write(buf[:n])
			received += int64(n)
			if time.Since(lastEmit) > 200*time.Millisecond {
				lastEmit = time.Now()
				s.emit("ocr:progress", OCRProgress{
					Stage: "download", Received: received, Total: ocrAssetsSize,
					Text: fmt.Sprintf("正在下载… %d%%", received*100/maxInt64(ocrAssetsSize, 1)),
				})
			}
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			_ = f.Close()
			_ = os.Remove(tmp)
			return "", fmt.Errorf("下载中断: %w", readErr)
		}
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(tmp)
		return "", err
	}

	if received != ocrAssetsSize {
		_ = os.Remove(tmp)
		return "", fmt.Errorf("文件大小不对：收到 %d 字节，应为 %d", received, ocrAssetsSize)
	}
	sum := hex.EncodeToString(hasher.Sum(nil))
	if sum != ocrAssetsSHA {
		_ = os.Remove(tmp)
		return "", fmt.Errorf("校验失败：SHA256 是 %s，应为 %s", sum, ocrAssetsSHA)
	}
	return tmp, nil
}

// extractOCRZip 把白名单里的文件解到 dir，返回解出来的个数。
func (s *ClipService) extractOCRZip(zipPath, dir string) (int, error) {
	r, err := zip.OpenReader(zipPath)
	if err != nil {
		return 0, fmt.Errorf("不是有效的 zip: %w", err)
	}
	defer r.Close()

	wanted := len(ocrAssetFiles)
	done := 0
	for _, f := range r.File {
		name := filepath.Base(f.Name)
		if !ocrAssetFiles[name] {
			continue
		}
		if err := extractOne(f, filepath.Join(dir, name)); err != nil {
			return done, fmt.Errorf("解压 %s 失败: %w", name, err)
		}
		done++
		s.emit("ocr:progress", OCRProgress{
			Stage: "extract", Received: int64(done), Total: int64(wanted),
			Text: fmt.Sprintf("正在解压 %s（%d/%d）", name, done, wanted),
		})
	}
	if done != wanted {
		return done, fmt.Errorf("包里只有 %d/%d 个文件", done, wanted)
	}
	return done, nil
}

// extractOne 单个文件解压：先写 .tmp 再 rename，中途失败不会留下半个文件。
func extractOne(f *zip.File, target string) error {
	rc, err := f.Open()
	if err != nil {
		return err
	}
	defer rc.Close()

	tmp := target + ".tmp"
	out, err := os.Create(tmp)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, rc); err != nil {
		_ = out.Close()
		_ = os.Remove(tmp)
		return err
	}
	if err := out.Close(); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, target)
}

func maxInt64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}
