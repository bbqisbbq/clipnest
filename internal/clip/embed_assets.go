package clip

// 语义搜索资源的「一键下载」。
//
// 为什么不打进安装包：解压后 45MB（onnxruntime.dll 21.6MB + embed_model.onnx 22.9MB
// + 词表 107KB），而绝大多数人不需要语义搜索。安装包还是 25MB，谁想要谁点一下 ——
// 和 PP-OCR 资源是同一个套路。
//
// 资源放在本仓库的一个 **pre-release**（tag `embed-assets-v1`）：
//   · 它不会出现在 /releases/latest 里，所以不影响自动更新；
//   · 模型是固定的，URL 与哈希写死在代码里。
//
// 下载流程（顺序很重要）：下到临时文件 → 校验大小 + SHA256 → 解压白名单里的文件
// （每个先写 .tmp 再 rename，避免半个文件让引擎"看起来装了但用不了"）。

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

	"github.com/bbqisbbq/clipnest/internal/embed"
)

const (
	// embedAssetsURL 指向仓库里那个资源 pre-release 的资产。
	embedAssetsURL = "https://github.com/bbqisbbq/clipnest/releases/download/embed-assets-v1/embed-assets.zip"
	// embedAssetsSize / embedAssetsSHA 是打包时实测的字节数与哈希（换资源包必须一起改）。
	embedAssetsSize = 23957666
	embedAssetsSHA  = "ba898fda36c11e0dd45b927d032c06edfad087080f6ebd37c015d11fcd0e1d96"
	// embedDownloadTimeout 覆盖整个下载：45MB 在慢网下也不该被 30 秒掐断。
	embedDownloadTimeout = 10 * time.Minute
)

// EmbedProgress 是 `embed:progress` 事件的数据。
type EmbedProgress struct {
	// Stage: download | extract | done
	Stage string `json:"stage"`
	// Received / Total 是字节数（extract 阶段是 已解压文件数/总数）
	Received int64 `json:"received"`
	Total    int64 `json:"total"`
	// Text 给界面直接显示的一句话
	Text string `json:"text"`
}

// EmbedStatus 是设置页要的状态。
type EmbedStatus struct {
	// Enabled 用户开没开（设置项，默认开）
	Enabled bool `json:"enabled"`
	// Ready 资源齐不齐（齐全 == 可以立刻语义检索）
	Ready bool `json:"ready"`
	// Compiled 这个构建带不带引擎（没 cgo 的构建恒 false）
	Compiled bool `json:"compiled"`
	// Done / Pending / Failed 向量编码进度
	Done    int `json:"done"`
	Pending int `json:"pending"`
	Failed  int `json:"failed"`
	// Reason 不能用的原因（给界面直接显示）
	Reason string `json:"reason"`
}

// embedAssetFiles 是允许从包里解出来的文件（白名单，防 zip 路径穿越与夹带）。
var embedAssetFiles = map[string]bool{
	embed.FileLibrary: true,
	embed.FileModel:   true,
	embed.FileVocab:   true,
}

// EmbedStatus 报告语义搜索当前的状态（设置页据此决定显示什么）。
func (s *ClipService) EmbedStatus() EmbedStatus {
	dir := s.EmbedAssetsDir()
	st := EmbedStatus{
		Enabled:  s.store.Settings().EmbedSearch,
		Compiled: embed.CompiledIn(),
	}
	st.Done, st.Pending, st.Failed = s.store.VectorStats()
	if err := embed.Ready(dir); err != nil {
		if _, statErr := os.Stat(dir); statErr != nil {
			st.Reason = fmt.Sprintf("资源还没下载（%s）", dir)
		} else {
			st.Reason = err.Error()
		}
		return st
	}
	st.Ready = true
	return st
}

// DownloadEmbedAssets 下载并安装语义搜索资源，返回一句给界面显示的结果。
func (s *ClipService) DownloadEmbedAssets() (string, error) {
	if !s.embedDownloading.CompareAndSwap(false, true) {
		return "", errors.New("已经在下载了")
	}
	defer s.embedDownloading.Store(false)

	if !embed.CompiledIn() {
		return "", embed.ErrUnavailable
	}
	dir := s.EmbedAssetsDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}

	s.emit("embed:progress", EmbedProgress{Stage: "download", Total: embedAssetsSize, Text: "正在下载语义模型…"})

	zipPath, err := s.fetchEmbedZip()
	if err != nil {
		s.emit("embed:progress", EmbedProgress{Stage: "download", Text: "下载失败：" + err.Error()})
		return "", err
	}
	defer func() { _ = os.Remove(zipPath) }()

	installed, err := s.extractEmbedZip(zipPath, dir)
	if err != nil {
		s.emit("embed:progress", EmbedProgress{Stage: "extract", Text: "解压失败：" + err.Error()})
		return "", err
	}
	if err := embed.Ready(dir); err != nil {
		s.emit("embed:progress", EmbedProgress{Stage: "extract", Text: "资源仍不完整"})
		return "", err
	}
	// 装好之后立刻把历史条目过一遍：不然用户会以为"装了但搜不到效果"，
	// 要等下一次重启才补扫。
	go s.enqueueStaleVectors()
	msg := fmt.Sprintf("已安装 %d 个文件（%s）", installed, dir)
	s.emit("embed:progress", EmbedProgress{Stage: "done", Received: embedAssetsSize, Total: embedAssetsSize, Text: msg})
	Logf(s.store.Dir(), "embed: 资源下载完成（%s）", msg)
	return msg, nil
}

// OpenEmbedAssetsDir 打开资源目录（没有就建一个）。
func (s *ClipService) OpenEmbedAssetsDir() error {
	dir := s.EmbedAssetsDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	return revealInExplorer(dir)
}

// fetchEmbedZip 下载资源包到临时文件，校验大小与 SHA256。
func (s *ClipService) fetchEmbedZip() (string, error) {
	req, err := http.NewRequestWithContext(s.bgCtx, http.MethodGet, embedAssetsURL, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", "ClipNest/"+Version)

	client := &http.Client{Timeout: embedDownloadTimeout}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("连接失败: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("HTTP %d（资源包可能被撤下或网络被拦）", resp.StatusCode)
	}

	tmp := filepath.Join(os.TempDir(), "clipnest-embed-assets.zip")
	f, err := os.Create(tmp)
	if err != nil {
		return "", err
	}
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
				s.emit("embed:progress", EmbedProgress{
					Stage: "download", Received: received, Total: embedAssetsSize,
					Text: fmt.Sprintf("正在下载… %d%%", received*100/maxInt64(embedAssetsSize, 1)),
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
	if received != embedAssetsSize {
		_ = os.Remove(tmp)
		return "", fmt.Errorf("文件大小不对：收到 %d 字节，应为 %d", received, embedAssetsSize)
	}
	sum := hex.EncodeToString(hasher.Sum(nil))
	if sum != embedAssetsSHA {
		_ = os.Remove(tmp)
		return "", fmt.Errorf("校验失败：SHA256 是 %s，应为 %s", sum, embedAssetsSHA)
	}
	return tmp, nil
}

// extractEmbedZip 把白名单里的文件解到 dir，返回解出来的个数。
func (s *ClipService) extractEmbedZip(zipPath, dir string) (int, error) {
	r, err := zip.OpenReader(zipPath)
	if err != nil {
		return 0, fmt.Errorf("不是有效的 zip: %w", err)
	}
	defer r.Close()

	wanted := len(embedAssetFiles)
	done := 0
	for _, f := range r.File {
		name := filepath.Base(f.Name)
		if !embedAssetFiles[name] {
			continue
		}
		if err := extractOne(f, filepath.Join(dir, name)); err != nil {
			return done, fmt.Errorf("解压 %s 失败: %w", name, err)
		}
		done++
		s.emit("embed:progress", EmbedProgress{
			Stage: "extract", Received: int64(done), Total: int64(wanted),
			Text: fmt.Sprintf("正在解压 %s（%d/%d）", name, done, wanted),
		})
	}
	if done != wanted {
		return done, fmt.Errorf("包里只有 %d/%d 个文件", done, wanted)
	}
	return done, nil
}
