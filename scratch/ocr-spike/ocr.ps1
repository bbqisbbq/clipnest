# Windows 自带 OCR（Windows.Media.Ocr）可行性探针
#
# 用法：powershell.exe -NoProfile -ExecutionPolicy Bypass -File scratch\ocr-spike\ocr.ps1 [图片路径]
#
# 三条踩过的坑，记在这儿：
#   ① WinRT 类型投影的第二个参数必须是**契约程序集名**（OcrEngine 在 Windows.Media.Ocr，
#      不在 Windows.Foundation）。写错了会 "Unable to find type"，且有时又能过 —— 别赌。
#   ② 异步 IO 要手工 Await（下面这段 AsTask 反射样板是通用写法）。
#   ③ 结果写 out.txt（UTF-8）：控制台代码页会把中文吞成乱码。
# 用 Windows PowerShell 5.1 跑。

param([string]$Path = 'E:\default\clipnest\scratch\ocr-spike\test-zh.png')

$ErrorActionPreference = 'Stop'
$outFile = Join-Path (Split-Path -Parent $MyInvocation.MyCommand.Path) 'out.txt'
$lines = New-Object System.Collections.Generic.List[string]
function Say([string]$s) { $lines.Add($s) }

Add-Type -AssemblyName System.Runtime.WindowsRuntime
$asTaskGeneric = ([System.WindowsRuntimeSystemExtensions].GetMethods() | Where-Object {
  $_.Name -eq 'AsTask' -and $_.GetParameters().Count -eq 1 -and
  $_.GetParameters()[0].ParameterType.Name -eq 'IAsyncOperation`1'
})[0]
function Await($op, $resultType) {
  $task = $asTaskGeneric.MakeGenericMethod($resultType).Invoke($null, @($op))
  $task.Wait(-1) | Out-Null
  $task.Result
}

[Windows.Media.Ocr.OcrEngine, Windows.Media.Ocr, ContentType = WindowsRuntime] | Out-Null
[Windows.Globalization.Language, Windows.Globalization, ContentType = WindowsRuntime] | Out-Null
[Windows.Graphics.Imaging.BitmapDecoder, Windows.Graphics.Imaging, ContentType = WindowsRuntime] | Out-Null
[Windows.Storage.Streams.InMemoryRandomAccessStream, Windows.Storage.Streams, ContentType = WindowsRuntime] | Out-Null
[Windows.Storage.Streams.DataWriter, Windows.Storage.Streams, ContentType = WindowsRuntime] | Out-Null

Say '--- 系统可用的 OCR 语言'
$langs = [Windows.Media.Ocr.OcrEngine]::AvailableRecognizerLanguages
foreach ($l in $langs) { Say ('  {0}  ({1})' -f $l.LanguageTag, $l.NativeName) }
if ($langs.Count -eq 0) {
  Say '  （一个都没有）'
  $lines | Out-File -FilePath $outFile -Encoding utf8
  exit 2
}

# 图片经内存流喂给 BitmapDecoder，绕开 StorageFile 的投影问题
$bytes = [System.IO.File]::ReadAllBytes($Path)
$stream = New-Object Windows.Storage.Streams.InMemoryRandomAccessStream
$writer = New-Object Windows.Storage.Streams.DataWriter($stream)
$writer.WriteBytes([byte[]]$bytes)
Await ($writer.StoreAsync()) ([uint32]) | Out-Null
$writer.DetachStream() | Out-Null
$stream.Seek(0)
$decoder = Await ([Windows.Graphics.Imaging.BitmapDecoder]::CreateAsync($stream)) ([Windows.Graphics.Imaging.BitmapDecoder])
$bitmap = Await ($decoder.GetSoftwareBitmapAsync()) ([Windows.Graphics.Imaging.SoftwareBitmap])
Say ('--- 图片 {0} × {1}' -f $bitmap.PixelWidth, $bitmap.PixelHeight)

foreach ($lang in $langs) {
  $engine = [Windows.Media.Ocr.OcrEngine]::TryCreateFromLanguage($lang)
  if (-not $engine) { continue }
  # 第一次含引擎冷启动，第二次取稳态耗时
  $cold = [System.Diagnostics.Stopwatch]::StartNew()
  Await ($engine.RecognizeAsync($bitmap)) ([Windows.Media.Ocr.OcrResult]) | Out-Null
  $cold.Stop()
  $sw = [System.Diagnostics.Stopwatch]::StartNew()
  $result = Await ($engine.RecognizeAsync($bitmap)) ([Windows.Media.Ocr.OcrResult])
  $sw.Stop()
  Say ''
  Say ('=== {0}  冷启 {1} ms / 稳态 {2} ms  行数 {3}' -f $lang.LanguageTag, $cold.ElapsedMilliseconds, $sw.ElapsedMilliseconds, $result.Lines.Count)
  Say $result.Text
}

$lines | Out-File -FilePath $outFile -Encoding utf8
exit 0
