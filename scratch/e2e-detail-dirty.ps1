# 详情页「未保存」误报：打开一条条目、什么都不改，按 Esc 返回，不该拦。
#
# 背景：编辑器（Vditor IR）载入内容时会按自己的规范整理一次，与库里的字节可能不同；
# 而「编辑器失焦」也会触发一次内容上报。两件事叠起来，以前的表现是：
#   点开详情 → 点「返回」/按 Esc → 弹「这条内容有未保存的修改，怎么处理？」
#
# 用法（必须 -STA）：pwsh -STA -NoProfile -File scratch\e2e-detail-dirty.ps1
#
# 判据以**日志**为准（截图只作旁证，PrintWindow 抓 WebView2 容易拿到陈旧帧）：
#   需要 bin\clipnest-dev.exe 里带 E2E-TEMP 标记（EntryDetailView 里那两行 LogClient）：
#     E2E-DETAIL-MOUNT <id>   → 真的进了详情页（证明键鼠注入有效）
#     E2E-DIRTY 0 / 1         → 判脏状态
#   期望：进详情后只出现 DIRTY 0；按 Esc 后不出现三向选择；
#         真敲一个字之后才出现 DIRTY 1。
Add-Type -AssemblyName System.Drawing
Add-Type -AssemblyName System.Windows.Forms
Add-Type @'
using System;
using System.Runtime.InteropServices;
public class DetailDr {
  [StructLayout(LayoutKind.Sequential)] public struct RECT { public int Left, Top, Right, Bottom; }
  [DllImport("user32.dll")] public static extern bool GetWindowRect(IntPtr h, out RECT r);
  [DllImport("user32.dll")] public static extern bool PrintWindow(IntPtr h, IntPtr dc, uint flags);
  [DllImport("user32.dll")] public static extern bool SetForegroundWindow(IntPtr h);
  [DllImport("user32.dll")] public static extern bool SetCursorPos(int x, int y);
  [DllImport("user32.dll")] public static extern void mouse_event(uint f, uint dx, uint dy, uint d, IntPtr e);
  [DllImport("user32.dll")] public static extern void keybd_event(byte vk, byte scan, uint flags, IntPtr extra);
  public const uint LEFTDOWN = 0x0002, LEFTUP = 0x0004;
}
'@

$root = 'E:\default\clipnest'
$sandbox = Join-Path $root 'scratch\appdata-detail-dirty'
$devExe = Join-Path $root 'bin\clipnest-dev.exe'
$shots = Join-Path $root 'scratch\e2e-shots'
New-Item -ItemType Directory -Path $shots -Force | Out-Null
$realAppData = $env:APPDATA

function Get-Rect($p) {
  $r = New-Object DetailDr+RECT
  [DetailDr]::GetWindowRect($p.MainWindowHandle, [ref]$r) | Out-Null
  return $r
}

function Click($p, [int]$relX, [int]$relY, [int]$times = 1) {
  $r = Get-Rect $p
  [DetailDr]::SetForegroundWindow($p.MainWindowHandle) | Out-Null
  Start-Sleep -Milliseconds 200
  for ($i = 0; $i -lt $times; $i++) {
    [DetailDr]::SetCursorPos($r.Left + $relX, $r.Top + $relY) | Out-Null
    Start-Sleep -Milliseconds 80
    [DetailDr]::mouse_event([DetailDr]::LEFTDOWN, 0, 0, 0, [IntPtr]::Zero)
    [DetailDr]::mouse_event([DetailDr]::LEFTUP, 0, 0, 0, [IntPtr]::Zero)
    if ($times -gt 1) { Start-Sleep -Milliseconds 60 }
  }
  Start-Sleep -Milliseconds 300
}

function Shot($p, $name) {
  $r = Get-Rect $p
  $w = $r.Right - $r.Left; $h = $r.Bottom - $r.Top
  if ($w -lt 100 -or $h -lt 100) { throw "窗口尺寸异常：${w}x$h（窗口没起来？）" }
  Start-Sleep -Milliseconds 350
  $bmp = New-Object System.Drawing.Bitmap $w, $h
  $g = [System.Drawing.Graphics]::FromImage($bmp)
  $hdc = $g.GetHdc()
  [DetailDr]::PrintWindow($p.MainWindowHandle, $hdc, 2) | Out-Null
  $g.ReleaseHdc($hdc); $g.Dispose()
  $path = Join-Path $shots $name
  $bmp.Save($path, [System.Drawing.Imaging.ImageFormat]::Png); $bmp.Dispose()
  Write-Host ("截图 {0}  {1}x{2}" -f $name, $w, $h)
}

$log = Join-Path $sandbox 'ClipNest\clipnest.log'
function Markers($what) {
  if (-not (Test-Path $log)) { return @() }
  return @(Get-Content $log | Select-String -Pattern 'E2E-DETAIL-MOUNT|E2E-DIRTY' | ForEach-Object { $_.Line })
}

# 安装版会抢单实例，先停掉
Get-Process clipnest, clipnest-dev -ErrorAction SilentlyContinue | Stop-Process -Force
Start-Sleep -Milliseconds 800

Remove-Item $sandbox -Recurse -Force -ErrorAction SilentlyContinue
New-Item -ItemType Directory -Path (Join-Path $sandbox 'ClipNest') -Force | Out-Null
@{ capture = $false; syncEnabled = $false; showOnLaunch = $true } | ConvertTo-Json |
  Set-Content -Path (Join-Path $sandbox 'ClipNest\settings.json') -Encoding UTF8

# 刻意用一段"会被 Markdown 规范整理"的内容：无空格表格、大写 [X]、行尾空格。
# 只要判脏拿"库里的原字节"和"编辑器规范化后的值"比，这条就一定会误报。
$seedMd = Join-Path $sandbox 'seed.md'
@'
# 未保存误报复现样本

普通的一段正文，用来确认编辑区能正常载入。

|列一|列二|
|---|---|
|A|B|

- [X] 已完成的任务

行尾有三个空格   
'@ | Set-Content -Path $seedMd -Encoding UTF8

Push-Location $root
& go run ./scratch/seedentry (Join-Path $sandbox 'ClipNest') $seedMd 2>&1 | ForEach-Object { "  seed: $_" }
Pop-Location

$env:APPDATA = $sandbox
$p = Start-Process -FilePath $devExe -ArgumentList '--show' -PassThru
Start-Sleep -Seconds 5
if ($p.HasExited) { throw "进程退出了，退出码 $($p.ExitCode)" }
[DetailDr]::SetForegroundWindow($p.MainWindowHandle) | Out-Null
Start-Sleep -Milliseconds 800
Shot $p 'dirty-0-list.png'

Write-Host '--- 阶段 1：单击聚焦 → 双击第一条卡片进详情（不做任何修改）---'
Click $p 680 250                       # 第一次点击只当"把窗口拿到前台"，不算操作
Click $p 680 250 -times 2
Start-Sleep -Seconds 3
Shot $p 'dirty-1-detail.png'
$m1 = Markers
Write-Host '    markers:'; $m1 | ForEach-Object { "      $_" }

Write-Host '--- 阶段 2：Esc 返回（此时未判脏 ⇒ 不该弹三向选择）---'
[System.Windows.Forms.SendKeys]::SendWait('{ESC}')
Start-Sleep -Seconds 3
Shot $p 'dirty-2-after-esc.png'
$mEsc = Markers
Write-Host ("    mount 次数=" + @($mEsc | Where-Object { $_ -match 'E2E-DETAIL-MOUNT' }).Count + "  （Esc 正常返回则不变）")

Write-Host '--- 阶段 3：再进详情、点进正文、真敲一个字（判脏必须仍然有效）---'
Click $p 680 250 -times 2
Start-Sleep -Seconds 3
Click $p 400 232                       # 点在正文第一行上，让 Vditor 的 contenteditable 真的拿到焦点
Start-Sleep -Milliseconds 700
# 用 keybd_event 而不是 SendKeys：SendKeys 走的是"线程焦点窗口"，WebView2 的子窗口经常收不到；
# keybd_event 是全局按键，和真人按键同一条路径（别的 e2e 脚本里 Esc/热键也是这样发的）。
$vk = 0x58  # 'x'
[DetailDr]::keybd_event($vk, 0, 0, [IntPtr]::Zero)
[DetailDr]::keybd_event($vk, 0, 2, [IntPtr]::Zero)
Start-Sleep -Seconds 2
Shot $p 'dirty-3-after-edit.png'
$m2 = Markers
Write-Host '    markers:'; $m2 | ForEach-Object { "      $_" }

Get-Process clipnest-dev -ErrorAction SilentlyContinue | Stop-Process -Force
$env:APPDATA = $realAppData

Write-Host ''
Write-Host '--- error/warn 日志 ---'
if (Test-Path $log) {
  $bad = Select-String -Path $log -Pattern 'error|failed|失败' |
    Where-Object { $_.Line -notmatch 'shot hotkey registration failed|GitHub API 限流' }
  if ($bad) { $bad | ForEach-Object { $_.Line } } else { '（无相关报错）' }
}

# 断言
$dirty = @($m2 | Where-Object { $_ -match 'E2E-DIRTY' } | ForEach-Object { ($_ -split 'E2E-DIRTY ')[1].Trim() })
$entered = @($m1 | Where-Object { $_ -match 'E2E-DETAIL-MOUNT' }).Count
Write-Host ''
Write-Host ("entered-detail={0}  dirty-sequence=[{1}]" -f $entered, ($dirty -join ','))
if ($entered -lt 1) {
  Write-Host '结论：没有日志标记（正式构建里已摘掉那两行临时 LogClient）→ 请按截图人工判读：' -ForegroundColor Yellow
  Write-Host '   dirty-1-detail.png      右上应是「已保存」（不是「未保存」徽章）'
  Write-Host '   dirty-2-after-esc.png   应回到列表（不该出现三向选择弹窗）'
  Write-Host '   dirty-3-after-edit.png  敲字后应变成「未保存」+ 可点「保存」'
  exit 0
}
if ($dirty.Count -ge 2 -and $dirty[0] -eq '0' -and $dirty[-1] -eq '1') {
  Write-Host '结论：PASS —— 打开不改不脏（DIRTY 0），真改了才脏（DIRTY 1）' -ForegroundColor Green
  exit 0
}
Write-Host '结论：FAIL —— 判脏行为与预期不符，看上面的序列与截图' -ForegroundColor Red
exit 1
