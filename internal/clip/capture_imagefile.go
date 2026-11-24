package clip

// 复制「图片文件」（资源管理器里 Ctrl+C 一张 jpg/png）时，把**图片本体**存进条目，
// 而不是只留一行 `C:\Users\…\DSC01234.jpg`。
//
// 为什么这是文件复制策略的一个例外：`docs/FILE-STRATEGY.md` 定的默认是「只记引用」，
// 因为把用户文件拷进库要付磁盘与隐私的代价。但图片是唯一"看不见就等于没记"的类型——
// 一行路径对用户毫无用处，而图就在剪贴板旁边摆着。
//
// 三条边界（宁可不存，也不做半吊子）：
//  1. 只处理**恰好一个**文件，且不是目录（多选/文件夹仍按引用记，避免一次塞进来几百 MB）；
//  2. 只认 we 能解码的格式（png/jpeg/gif）；解码不了（webp/avif/psd…）就退回引用；
//  3. 源文件超过 imageFileMaxBytes 就退回引用 —— 剪贴板管理器不是相册。
//
// 存的是**原始字节**，不重新编码：jpg 进库还是 jpg（`sniffImageMime` 在出库时认得出），
// 既不掉画质也不涨体积，采集线程上只多一次读文件 + 一次读图片头。

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	"os"
	"path/filepath"
	"strings"
)

// imageFileMaxBytes 是"多少以内的图片文件才值得整份存进库"。
// 与网页图片自动下载的上限（24 MB）保持一致，免得用户遇到两套规矩。
const imageFileMaxBytes = 24 << 20

// imageFileExts 是我们能解码、也愿意直接存进库的扩展名。
// bmp/tiff 不在标准库解码器里（要 x/image），webp/avif 同理，所以它们继续走引用。
var imageFileExts = map[string]bool{
	".png":  true,
	".jpg":  true,
	".jpeg": true,
	".jfif": true,
	".gif":  true,
}

// imageFileCapture 把"复制了一个图片文件"变成"存了一张图"。
// 不满足条件时返回 nil，调用方（buildFilesCapture）照旧按引用记。
func imageFileCapture(files []FileRef) *Captured {
	if len(files) != 1 {
		return nil
	}
	f := files[0]
	if f.IsDir || f.Path == "" {
		return nil
	}
	if !imageFileExts[strings.ToLower(filepath.Ext(f.Path))] {
		return nil
	}
	if f.Size > imageFileMaxBytes {
		Logf("", "复制图片文件：%s 有 %s，超过上限，仍按路径引用记住", filepath.Base(f.Path), humanBytes(int(f.Size)))
		return nil
	}

	raw, err := os.ReadFile(f.Path)
	if err != nil || len(raw) == 0 {
		// 路径刚失效（剪切走、网络盘掉线）——退回引用，条目里仍有路径可查
		return nil
	}
	// 只读图片头：拿宽高 + 顺手确认"这确实是我们能解码的图"，
	// 不整张解码（一张 24 MB 的 jpg 解码要几百毫秒，会挡住下一次复制）
	cfg, _, err := image.DecodeConfig(bytes.NewReader(raw))
	if err != nil {
		return nil
	}

	sum := sha256.Sum256(raw)
	return &Captured{
		Kind:   KindImage,
		PNG:    raw, // 名字叫 PNG，实际是"任意受支持格式的原字节"（出库按 MIME 嗅探）
		Width:  cfg.Width,
		Height: cfg.Height,
		// 文件引用照旧保留：详情页能查到它是从哪个文件来的
		Files:     files,
		Text:      f.Path,
		Preview:   fmt.Sprintf("%s · %d × %d", f.Name, cfg.Width, cfg.Height),
		Hash:      hex.EncodeToString(sum[:16]),
		Size:      len(raw),
		SourceApp: foregroundAppName(),
	}
}
