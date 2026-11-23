package clip

// 图片 OCR：把图里的文字抽出来进搜索索引。
//
// ## 为什么走「Go 起一个 PowerShell 进程」调用 Windows 自带引擎
//
// 目标引擎是 Windows 自带的 `Windows.Media.Ocr`（离线、零依赖、无需下载模型，
// 中文 Windows 上自带 zh-Hans 语言包）。问题是**怎么从 Go 调 WinRT**：
//
//   · 直接在 Go 里调：`IAsyncOperation<T>` 要手搓一堆 COM vtable + 完成回调，
//     代码量与出错面都远超这个功能的价值；
//   · C# sidecar（最干净）：`.NET Framework 4.8 + WinRT` 需要 Windows SDK 的
//     UnionMetadata 才能编译 —— 本机只有 .NET Core 版 winmd，csc 直接报
//     `System.Runtime 缺引用`（实测）；要通就得额外装 Windows SDK；
//   · PowerShell 5.1：Win10/11 自带，脚本用 `-EncodedCommand`（base64 UTF-16LE）传，
//     绕开"文件编码被当 ANSI"与"引号"两个坑。实测一次往返约 1.2 秒（大头是
//     PowerShell 冷启动），所以它只能跑在后台队列里，绝不挡采集。
//
// 认不出、没有语言包、PowerShell 被策略禁掉，一律返回 (\"\", err)，调用方把
// `ocr_state` 标成 unsupported/failed —— 功能缺失要能被看见，不能假装成功。

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"
	"unicode"
	"unicode/utf16"
)

// ocrScript 是 PowerShell 侧的全部逻辑。**保持纯 ASCII**：
// PowerShell 5.1 按 ANSI 读命令行/脚本文件，混进一个多字节字符就可能整段解析失败。
// 图片路径通过环境变量 CLIPNEST_OCR_IMAGE 传，结果按行输出到 stdout（UTF-8）。
const ocrScript = `
$ErrorActionPreference='Stop'
try { [Console]::OutputEncoding=[Text.Encoding]::UTF8 } catch {}
Add-Type -AssemblyName System.Runtime.WindowsRuntime
$asTask=([System.WindowsRuntimeSystemExtensions].GetMethods()|Where-Object{$_.Name -eq 'AsTask' -and $_.GetParameters().Count -eq 1 -and $_.GetParameters()[0].ParameterType.Name -eq 'IAsyncOperation' + [char]96 + '1'})[0]
function Await($op,$t){$task=$asTask.MakeGenericMethod($t).Invoke($null,@($op));$task.Wait(-1)|Out-Null;$task.Result}
[Windows.Media.Ocr.OcrEngine,Windows.Media.Ocr,ContentType=WindowsRuntime]|Out-Null
[Windows.Globalization.Language,Windows.Globalization,ContentType=WindowsRuntime]|Out-Null
[Windows.Graphics.Imaging.BitmapDecoder,Windows.Graphics.Imaging,ContentType=WindowsRuntime]|Out-Null
[Windows.Storage.Streams.InMemoryRandomAccessStream,Windows.Storage.Streams,ContentType=WindowsRuntime]|Out-Null
[Windows.Storage.Streams.DataWriter,Windows.Storage.Streams,ContentType=WindowsRuntime]|Out-Null
$engine=$null
foreach($tag in @('zh-Hans-CN','zh-Hans','en-US')){
  try { $lang=[Windows.Globalization.Language]::new($tag); $engine=[Windows.Media.Ocr.OcrEngine]::TryCreateFromLanguage($lang) } catch { $engine=$null }
  if($engine){ break }
}
if(-not $engine){ try { $engine=[Windows.Media.Ocr.OcrEngine]::TryCreateFromUserProfileLanguages() } catch {} }
if(-not $engine){ [Console]::Error.Write('no-ocr-engine'); exit 3 }
$bytes=[IO.File]::ReadAllBytes($env:CLIPNEST_OCR_IMAGE)
$stream=New-Object Windows.Storage.Streams.InMemoryRandomAccessStream
$writer=New-Object Windows.Storage.Streams.DataWriter($stream)
$writer.WriteBytes([byte[]]$bytes)
Await ($writer.StoreAsync()) ([uint32])|Out-Null
$writer.DetachStream()|Out-Null
$stream.Seek(0)
$decoder=Await ([Windows.Graphics.Imaging.BitmapDecoder]::CreateAsync($stream)) ([Windows.Graphics.Imaging.BitmapDecoder])
$bitmap=Await ($decoder.GetSoftwareBitmapAsync()) ([Windows.Graphics.Imaging.SoftwareBitmap])
$result=Await ($engine.RecognizeAsync($bitmap)) ([Windows.Media.Ocr.OcrResult])
$lines=@()
foreach($line in $result.Lines){ $lines += $line.Text }
[Console]::Out.Write([string]::Join([char]10, $lines))
`

// encodePowerShellCommand turns a script into the `-EncodedCommand` payload
// (base64 of UTF-16LE), which sidesteps both quoting and file-encoding problems.
func encodePowerShellCommand(script string) string {
	units := utf16.Encode([]rune(script))
	buf := make([]byte, 0, len(units)*2)
	for _, u := range units {
		buf = append(buf, byte(u), byte(u>>8))
	}
	return base64.StdEncoding.EncodeToString(buf)
}

// ocrAvailable reports whether this machine can attempt OCR at all.
func ocrAvailable() bool {
	if runtime.GOOS != "windows" {
		return false
	}
	_, err := exec.LookPath("powershell.exe")
	return err == nil
}

// runSystemOCR recognizes text in a PNG and returns it line by line.
//
// 一次调用一个进程：慢（约 1.2 秒）但极稳，也没有常驻进程要管。后台队列串行跑，
// 用户感知不到。
func runSystemOCR(png []byte) (string, error) {
	if !ocrAvailable() {
		return "", fmt.Errorf("当前系统没有可用的 OCR 引擎")
	}
	if len(png) == 0 {
		return "", fmt.Errorf("图片是空的")
	}
	dir := filepath.Join(os.TempDir(), "clipnest-ocr")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	file := filepath.Join(dir, fmt.Sprintf("ocr-%d-%d.png", os.Getpid(), time.Now().UnixNano()))
	if err := os.WriteFile(file, png, 0o600); err != nil {
		return "", err
	}
	defer func() { _ = os.Remove(file) }()

	cmd := exec.Command("powershell.exe",
		"-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass",
		"-EncodedCommand", encodePowerShellCommand(ocrScript))
	cmd.Env = append(os.Environ(), "CLIPNEST_OCR_IMAGE="+file)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return "", fmt.Errorf("OCR 失败：%s", msg)
	}
	return normalizeOCRText(stdout.String()), nil
}

// ---- 识别结果后处理 ------------------------------------------------------
//
// 系统 OCR 的两个通病，必须在入库前修掉，否则搜索和复制都会被它们毁掉：
//  1. **中文单字之间被塞空格**：`坚 果 云` —— 检索"坚果云"就搜不到了；
//  2. 常见符号误识别：`https.//`、`://` 变 `.//`，长破折号/全角空格等。

var (
	// 两个 CJK 字符之间的**横向空白**：直接去掉。
	// 注意别用 `\s+`：它把换行也算进去，会把两行中文粘成一行（测试抓到过）。
	ocrCJKSpaceRE = regexp.MustCompile(`([\p{Han}\p{Hiragana}\p{Katakana}])[ \t\x{00A0}\x{3000}]+([\p{Han}\p{Hiragana}\p{Katakana}])`)
	// URL 协议头的误识别（系统 OCR 很爱把 `://` 认成 `.//` 或 `;//`）。
	ocrBadSchemeRE  = regexp.MustCompile(`(?i)\b(https?|ftp)([.;,:])(//)`)
	ocrMultiBlankRE = regexp.MustCompile(`\n{3,}`)
)

// normalizeOCRText 修掉系统 OCR 的固定毛病，其余原样保留（别自作聪明改内容）。
func normalizeOCRText(s string) string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "\n")
	s = ocrBadSchemeRE.ReplaceAllString(s, "$1://")
	// 反复替换：`中 文 字` 里一次替换会吃掉相邻的配对
	for {
		next := ocrCJKSpaceRE.ReplaceAllString(s, "$1$2")
		if next == s {
			break
		}
		s = next
	}
	s = ocrMultiBlankRE.ReplaceAllString(s, "\n\n")
	// 去掉行首尾空白与全角空格，但保留行结构（行 = 版面换行，有用）
	lines := strings.Split(s, "\n")
	for i, line := range lines {
		lines[i] = strings.TrimFunc(line, func(r rune) bool {
			return r == ' ' || r == '\t' || r == '\u3000' || unicode.IsSpace(r)
		})
	}
	return strings.TrimSpace(strings.Join(lines, "\n"))
}
