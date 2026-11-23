// ocrprobe：验证「Go 起一个 PowerShell 进程，用系统自带 WinRT OCR 认图」这条路。
//
//	go run ./scratch/ocrprobe <png 路径>
//
// 关注三件事：① 中文能不能正确带回来（stdout 编码）；② 一次往返多少毫秒；
// ③ 语言包的选择顺序（zh-Hans-CN → zh-Hans → en-US → 用户配置）。
package main

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
	"unicode/utf16"
)

// 脚本刻意保持**纯 ASCII**：PowerShell 5.1 会按 ANSI 读文件/命令行，
// 里面只要有一个中文字符就可能整段解析失败（踩过）。图片路径走环境变量传。
const ocrScript = `
$ErrorActionPreference='Stop'
try { [Console]::OutputEncoding=[Text.Encoding]::UTF8 } catch {}
Add-Type -AssemblyName System.Runtime.WindowsRuntime
$asTask=([System.WindowsRuntimeSystemExtensions].GetMethods()|Where-Object{$_.Name -eq 'AsTask' -and $_.GetParameters().Count -eq 1 -and $_.GetParameters()[0].ParameterType.Name -eq 'IAsyncOperation` + "`" + `1'})[0]
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
[Console]::Out.Write($result.Text)
`

func encodeCommand(script string) string {
	u := utf16.Encode([]rune(script))
	buf := make([]byte, 0, len(u)*2)
	for _, r := range u {
		buf = append(buf, byte(r), byte(r>>8))
	}
	return base64.StdEncoding.EncodeToString(buf)
}

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "用法: go run ./scratch/ocrprobe <png>")
		os.Exit(2)
	}
	start := time.Now()
	cmd := exec.Command("powershell.exe",
		"-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass",
		"-EncodedCommand", encodeCommand(ocrScript))
	cmd.Env = append(os.Environ(), "CLIPNEST_OCR_IMAGE="+os.Args[1])
	var out, errb bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errb
	err := cmd.Run()
	took := time.Since(start)
	if err != nil {
		fmt.Printf("失败(%v): %s\n", err, strings.TrimSpace(errb.String()))
		os.Exit(1)
	}
	text := strings.TrimSpace(out.String())
	fmt.Printf("往返 %d ms，%d 字\n", took.Milliseconds(), len([]rune(text)))
	fmt.Println("--- 识别结果 ---")
	fmt.Println(text)
}
