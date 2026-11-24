package clip

// 网页「复制图片」的补丁。
//
// 浏览器（Chrome / Edge）复制一张网页上的图片时，剪贴板里**经常根本没有位图**：
// 只有一段 HTML Format（内容就是一个 <img src="https://…">）加上一段文本
// （alt 文字，或者图片地址）。按老规则这条会被归成「富文本」，于是历史里多出一条
// "图片的链接"——粘回浏览器当然还是能看到图，可条目里既没有图片也没有可读内容。
//
// 现在：认得出「整段就是一个 <img>」的片段，就去把真正的图片下载回来，存成图片条目
// （还能顺带被 OCR 认出图里的字）。下载失败或用户关掉了这个功能，才退回原来的富文本。
//
// 不负责：要不要收（设置开关）、去重与落库（service_capture.go / entry.go）。

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	"image/png"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"
)

const (
	// maxRemoteImageBytes 是单张远程图片的上限：比它大的不抓，免得一条复制把内存和库都撑爆。
	maxRemoteImageBytes = 24 << 20
	// remoteImageTimeout 是抓一张图愿意等多久。超过这个时间就退回富文本条目，
	// 让用户至少还能从历史里把链接捞回来。
	remoteImageTimeout = 8 * time.Second
	// remoteImageUA：不少站点会拒绝空 UA 或者无 Referer 的直连取图。
	remoteImageUA = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 " +
		"(KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36"
)

var (
	imgTagRe  = regexp.MustCompile(`(?is)<img\b[^>]*>`)
	srcAttrRe = regexp.MustCompile(`(?is)\bsrc\s*=\s*(?:"([^"]*)"|'([^']*)'|([^\s>]+))`)
	wrapperRe = regexp.MustCompile(`(?is)</?(?:a|p|div|span|center|body|html|br|meta|font)\b[^>]*>`)
	commentRe = regexp.MustCompile(`(?s)<!--.*?-->`)
)

// imageOnlyFragment reports whether an HTML clipboard fragment is nothing but one
// image (optionally wrapped in <a>/<p>/<meta> …) and returns the src it points at.
//
// 只接受"整体就是一张图"的片段：正文明明有字、只是顺带带了张图的那种，仍然按富文本收
// ——那是用户真正想保留的东西。
func imageOnlyFragment(frag string) (string, bool) {
	body := commentRe.ReplaceAllString(frag, "")
	tags := imgTagRe.FindAllString(body, -1)
	if len(tags) != 1 {
		return "", false
	}
	rest := wrapperRe.ReplaceAllString(strings.Replace(body, tags[0], "", 1), "")
	if strings.TrimSpace(rest) != "" {
		// 还有别的文字/元素：这是"一段带图的富文本"，不是"一张图"。
		return "", false
	}
	m := srcAttrRe.FindStringSubmatch(tags[0])
	if m == nil {
		return "", false
	}
	src := strings.TrimSpace(firstNonEmpty(m[1], m[2], m[3]))
	if src == "" || strings.HasPrefix(strings.ToLower(src), "memoimg:") {
		return "", false
	}
	return src, true
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

// fetchRemoteImage downloads src (http(s) or an inline data: URI) and returns a
// Captured for an image entry. Everything is re-encoded to PNG, the same bottle
// the clipboard path puts images in, so hashes and copy-back agree.
func fetchRemoteImage(ctx context.Context, src string) (*Captured, error) {
	raw, err := loadImageBytes(ctx, src)
	if err != nil {
		return nil, err
	}
	img, format, err := image.Decode(bytes.NewReader(raw))
	if err != nil {
		return nil, fmt.Errorf("无法解码图片（%s）: %w", format, err)
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return nil, err
	}
	data := buf.Bytes()
	sum := sha256.Sum256(data)
	b := img.Bounds()
	return &Captured{
		Kind:    KindImage,
		PNG:     data,
		Width:   b.Dx(),
		Height:  b.Dy(),
		Preview: fmt.Sprintf("%d × %d 网页图片", b.Dx(), b.Dy()),
		Hash:    hex.EncodeToString(sum[:16]),
		Size:    len(data),
	}, nil
}

// loadImageBytes reads the payload of an image URL, with a hard size cap.
func loadImageBytes(ctx context.Context, src string) ([]byte, error) {
	if strings.HasPrefix(strings.ToLower(src), "data:") {
		idx := strings.Index(src, ",")
		if idx < 0 {
			return nil, errors.New("data URI 格式不对")
		}
		payload, err := base64.StdEncoding.DecodeString(src[idx+1:])
		if err != nil {
			return nil, fmt.Errorf("data URI 解码失败: %w", err)
		}
		if len(payload) == 0 || len(payload) > maxRemoteImageBytes {
			return nil, errors.New("data URI 图片太大或为空")
		}
		return payload, nil
	}
	if !strings.HasPrefix(strings.ToLower(src), "http://") && !strings.HasPrefix(strings.ToLower(src), "https://") {
		return nil, errors.New("不支持的图片地址")
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, src, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", remoteImageUA)
	req.Header.Set("Accept", "image/avif,image/webp,image/apng,image/*,*/*;q=0.8")

	resp, err := remoteImageClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	if ct := strings.ToLower(resp.Header.Get("Content-Type")); ct != "" && !strings.HasPrefix(ct, "image/") && !strings.HasPrefix(ct, "application/octet-stream") {
		return nil, fmt.Errorf("返回的不是图片（%s）", ct)
	}
	limit := io.LimitReader(resp.Body, maxRemoteImageBytes+1)
	data, err := io.ReadAll(limit)
	if err != nil {
		return nil, err
	}
	if len(data) == 0 {
		return nil, errors.New("图片为空")
	}
	if len(data) > maxRemoteImageBytes {
		return nil, fmt.Errorf("图片超过 %d MB", maxRemoteImageBytes>>20)
	}
	return data, nil
}

// remoteImageClient 是抓网页图片专用的客户端：跟随重定向、带超时，
// 并且不复用别的 HTTP 客户端的连接池（那些是同步用的，超时策略不一样）。
var remoteImageClient = &http.Client{Timeout: remoteImageTimeout}

// richFallbackFromRemote 把"认出是图片但没抓到"的这次采集降级成富文本条目：
// 至少把地址与 alt 文字留在历史里，比什么也没有强。
func richFallbackFromRemote(c Captured) Captured {
	text := strings.TrimSpace(c.Text)
	html := c.HTML
	if text == "" {
		text = c.RemoteImageURL
	}
	if html == "" {
		html = fmt.Sprintf(`<a href="%s">%s</a>`, c.RemoteImageURL, text)
	}
	return Captured{
		Kind:      KindRichText,
		Text:      text,
		HTML:      html,
		Preview:   makePreview(text, 240),
		Hash:      htmlHash(text, html),
		Size:      len(text) + len(html),
		SourceApp: c.SourceApp,
	}
}

// previewForRemote is the placeholder preview shown while the download runs.
func previewForRemote(src string) string {
	preview := makePreview(src, 200)
	if preview == "" {
		return "网页图片"
	}
	return preview
}
