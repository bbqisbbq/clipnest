package clip

// 截图：快捷键 → 拍下光标所在的显示器 → 全屏遮罩窗里框选 → 裁出来存成图片条目。
//
// 分工：抓屏与窗口摆放是 Win32 的事（shot_windows.go）；选区的交互在
// frontend/src/Shot.tsx；这里只做"拍下来、交出去、收回来、落库、顺带复制"。
// 裁切在前端做：只有那边知道 CSS 像素与图片像素的换算比例（各屏缩放不同，
// 后端算不出来也不会比浏览器的 naturalWidth/clientWidth 更准）。

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"image"
	"image/png"
	"time"
)

const (
	// DefaultShotHotkey is the default accelerator for region screenshots.
	DefaultShotHotkey = "Ctrl+Shift+A"
	// minShotEdge 以下的选区当作误触（快捷键按完随手点了一下鼠标，不该存一条 2×3 的图）。
	minShotEdge = 8
)

// ShotInfo tells the overlay what it is looking at (and whether a frame is ready at all).
type ShotInfo struct {
	Ready  bool `json:"ready"`
	Width  int  `json:"width"`
	Height int  `json:"height"`
}

// StartShot freezes the screen and opens the selection overlay.
//
// 顺序很重要：**先拍、再显示窗口**。反过来的话遮罩会把自己截进图里，
// 用户看到的是一张"带半透明黑色遮罩的桌面"。
func (s *ClipService) StartShot() error {
	if s.hooks.ShowShotWindow == nil || s.hooks.HideShotWindow == nil {
		return errors.New("截图窗口未就绪")
	}
	// 遮罩已经开着时再按一次快捷键：什么都不做。否则截到的就是"遮罩自己"
	// （一张压暗的、带上次选框的截图），而且窗口会闪一下。
	s.mu.Lock()
	pending := s.shot != nil
	s.mu.Unlock()
	if pending {
		return nil
	}
	// 记下按快捷键时前台是谁：截图条目的"来源程序"应该是它，而不是 ClipNest 自己。
	src := foregroundAppName()
	frame, err := CaptureCursorMonitor()
	if err != nil {
		Logf(s.store.Dir(), "screenshot capture failed: %v", err)
		return fmt.Errorf("截屏失败: %w", err)
	}
	s.mu.Lock()
	s.shot = frame
	s.shotSourceApp = src
	s.mu.Unlock()

	if !s.hooks.ShowShotWindow() {
		s.mu.Lock()
		s.shot = nil
		s.mu.Unlock()
		return errors.New("无法显示截图窗口")
	}
	Logf(s.store.Dir(), "shot: frame %dx%d at (%d,%d) from %q",
		frame.Width, frame.Height, frame.Left, frame.Top, src)
	return nil
}

// ShotBounds reports where the overlay window must go (physical pixels).
func (s *ClipService) ShotBounds() (left, top, width, height int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.shot == nil {
		return 0, 0, 0, 0
	}
	return s.shot.Left, s.shot.Top, s.shot.Width, s.shot.Height
}

// GetShotInfo is the bound view of the pending frame.
func (s *ClipService) GetShotInfo() ShotInfo {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.shot == nil {
		return ShotInfo{}
	}
	return ShotInfo{Ready: true, Width: s.shot.Width, Height: s.shot.Height}
}

// CurrentShotPNG hands the frozen frame to the overlay's <img> tag.
//
// 走 asset handler（/shot/current）而不是绑定方法：一张 4K 截图 base64 之后是几 MB
// 的字符串，塞进 JSON 里传一次要几十毫秒，而且这段字符串会一直躺在 JS 堆里。
func (s *ClipService) CurrentShotPNG() []byte {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.shot == nil {
		return nil
	}
	return s.shot.PNG
}

// SaveShot stores the cropped region as an image entry (and copies it to the
// clipboard when the setting says so).
func (s *ClipService) SaveShot(dataURL string) (Entry, error) {
	_, payload, err := decodeImagePayload(dataURL)
	if err != nil {
		return Entry{}, err
	}
	// 一律"解码 → 用 Go 的编码器重新编码成 PNG"。两个理由：
	//   ① 条目里的图片都按 PNG 一种口径存（回填、同步、OCR 都靠它）；
	//   ② 走 HTTP 转发请求时浏览器编码出来的 PNG 字节，和采集侧读回同一张图时重编码
	//      出来的字节不一样，回声抑制（entryEchoHash = 条目 hash）就失效——"截图后自动
	//      复制到剪贴板"会立刻被自己再收一条。过一遍 Go 的编码器后两边字节一致。
	img, _, err := image.Decode(bytes.NewReader(payload))
	if err != nil {
		return Entry{}, fmt.Errorf("无法解码截图: %w", err)
	}
	bounds := img.Bounds()
	width, height := bounds.Dx(), bounds.Dy()
	if width < minShotEdge || height < minShotEdge {
		return Entry{}, errors.New("选区太小")
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return Entry{}, err
	}
	pngBytes := buf.Bytes()

	s.mu.Lock()
	src := s.shotSourceApp
	s.shot = nil
	s.mu.Unlock()

	sum := sha256.Sum256(pngBytes)
	entry, _ := s.saveCaptured(Captured{
		Kind:      KindImage,
		PNG:       pngBytes,
		Width:     width,
		Height:    height,
		Preview:   fmt.Sprintf("%d × %d 截图", width, height),
		Hash:      hex.EncodeToString(sum[:16]),
		Size:      len(pngBytes),
		SourceApp: src,
	})
	s.hideShotWindow()
	if entry == nil {
		return Entry{}, errors.New("截图保存失败（详见日志）")
	}
	Logf(s.store.Dir(), "shot saved: crop %dx%d (%s, entry %s)", width, height, humanBytes(len(pngBytes)), entry.ID[:8])

	if s.store.Settings().CopyShotToClipboard {
		// 顺手放进剪贴板：截完就能直接 Ctrl+V。写之前记下"回声"哈希（采集侧口径），
		// 否则监听器会把我们自己写的这张图再收一遍。
		s.mu.Lock()
		s.suppressHash = entryEchoHash(*entry)
		s.suppressAt = time.Now()
		s.mu.Unlock()
		if err := WriteImageToClipboard(pngBytes); err != nil {
			Logf(s.store.Dir(), "shot copy to clipboard failed: %v", err)
		}
	}
	return *entry, nil
}

// CancelShot drops the frame and hides the overlay (Esc, or a click without a drag).
func (s *ClipService) CancelShot() {
	s.mu.Lock()
	s.shot = nil
	s.mu.Unlock()
	s.hideShotWindow()
}

func (s *ClipService) hideShotWindow() {
	if s.hooks.HideShotWindow != nil {
		s.hooks.HideShotWindow()
	}
}

// metaShotInit marks the one-time repair of profiles that predate screenshots.
const metaShotInit = "shot.defaultsInit"

// EnsureShotDefaults repairs a profile whose settings.json was written before
// screenshots existed: those blobs simply have no shotHotkey (解出来是空串 = 不注册)
// and copyShotToClipboard 也会是 false。只跑一次（meta 标记），用户之后主动清空热键
// 或关掉"复制到剪贴板"都仍然作数。
//
// 修完必须走 hooks.OnSettings 重新注册热键——和 EnsureCaptureHotkey 一样，
// 只写库不注册的话，这次启动里 Ctrl+Shift+A 依然是空的。
func (s *ClipService) EnsureShotDefaults() {
	if s.store.Meta(metaShotInit) != "" {
		return
	}
	st := s.store.Settings()
	if st.ShotHotkey == "" || !st.CopyShotToClipboard {
		if st.ShotHotkey == "" {
			st.ShotHotkey = DefaultShotHotkey
		}
		st.CopyShotToClipboard = true
		if err := s.store.SaveSettings(st); err != nil {
			Logf(s.store.Dir(), "shot defaults save failed: %v", err)
		} else if s.hooks.OnSettings != nil {
			if err := s.hooks.OnSettings(st); err != nil {
				Logf(s.store.Dir(), "shot hotkey re-register failed: %v", err)
			}
		}
	}
	_ = s.store.SetMeta(metaShotInit, "1")
}
