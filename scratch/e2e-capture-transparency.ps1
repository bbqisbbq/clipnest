# 「记一笔」弹窗透明度的实测脚本（2026-11-20）。
#
# 验证两件事，靠屏幕像素说话（不看截图印象）：
#   1. 窗口本身真透明 —— 圆角外侧那一点应该是「背后的白窗口」的颜色，
#      而不是窗口底色 rgb(14,16,21)。（改动前那里是一块矩形实底。）
#   2. 卡片 alpha 跟着 Settings.CaptureTransparency 走 —— 15% / 30% 时卡片
#      在白色背景上按比例变浅：surface(#0e1015) 与白底混合。
#
# 做法：沙箱 APPDATA（不碰本机真数据）+ dev 版 exe，背后摆一个白色文本框窗口
# （scratch/paste-target.ps1），热键弹出后按窗口矩形截图取像素。
# 运行前会停掉安装版 ClipNest（单实例锁按 app id 抢），跑完再把它拉起来。
#
# 用法：pwsh -NoProfile -File scratch\e2e-capture-transparency.ps1
Add-Type -AssemblyName System.Drawing
Add-Type -AssemblyName System.Windows.Forms
Add-Type @'
using System;
using System.Runtime.InteropServices;
public class CNT {
  [StructLayout(LayoutKind.Sequential)] public struct RECT { public int Left, Top, Right, Bottom; }
  [DllImport("user32.dll")] public static extern bool GetWindowRect(IntPtr h, out RECT r);
  [DllImport("user32.dll")] public static extern bool SetForegroundWindow(IntPtr h);
  [DllImport("user32.dll")] public static extern bool IsWindowVisible(IntPtr h);
}
'@

$root = 'E:\default\clipnest'
$devExe = Join-Path $root 'bin\clipnest-dev.exe'
$installedExe = 'C:\Program Files\bbqisbbq\ClipNest\clipnest.exe'
$sandboxAppData = Join-Path $root 'scratch\tmp-profile-transparency\APPDATA'
$shots = Join-Path $root 'scratch\transparency-shots'
New-Item -ItemType Directory -Force -Path $sandboxAppData, $shots | Out-Null

$pwshPath = (Get-Process -Id $PID).Path
$results = New-Object System.Collections.Generic.List[string]
$logLineRe = 'capture window: pos=\((-?\d+),(-?\d+)\) size=\((\d+)x(\d+)\) hwnd=(0x[0-9a-fA-F]+)'

function Wait-For([scriptblock]$cond, [int]$timeoutMs = 8000) {
  $sw = [Diagnostics.Stopwatch]::StartNew()
  while ($sw.ElapsedMilliseconds -lt $timeoutMs) {
    $v = & $cond
    if ($null -ne $v -and $v -ne $false) { return $v }
    Start-Sleep -Milliseconds 120
  }
  return $null
}
function Stop-AllApp {
  Get-Process | Where-Object { $_.ProcessName -like 'clipnest*' } | ForEach-Object {
    try { $_.Kill(); $_.WaitForExit(3000) | Out-Null } catch {}
  }
  Start-Sleep -Milliseconds 800
}
function Get-Rect([IntPtr]$h) {
  $r = New-Object CNT+RECT
  if (-not [CNT]::GetWindowRect($h, [ref]$r)) { return $null }
  return [pscustomobject]@{ X = $r.Left; Y = $r.Top; W = $r.Right - $r.Left; H = $r.Bottom - $r.Top }
}

# 用沙箱设置启动 dev 版；返回它的日志路径。
function Start-DevApp([int]$transparency) {
  Stop-AllApp
  $dataDir = Join-Path $sandboxAppData 'ClipNest'
  New-Item -ItemType Directory -Force -Path $dataDir | Out-Null
  $settings = @{
    capture             = $true
    hotkey              = 'Ctrl+Shift+V'
    captureHotkey       = 'Ctrl+Shift+N'
    themePreset         = 'default-dark'
    theme               = 'dark'
    showOnLaunch        = $false
    autoStart           = $false
    updateCheck         = $false
    syncEnabled         = $false
    captureTransparency = $transparency
  } | ConvertTo-Json
  Set-Content -Path (Join-Path $dataDir 'settings.json') -Value $settings -Encoding utf8
  $env:APPDATA = $sandboxAppData
  Start-Process -FilePath $devExe | Out-Null
  if (-not (Wait-For { Get-Process clipnest-dev -ErrorAction SilentlyContinue } 20000)) {
    throw "dev 版没起来：$devExe"
  }
  Start-Sleep -Seconds 4
  return (Join-Path $dataDir 'clipnest.log')
}

# 面板区域截图：窗口矩形 + 6px 余量（余量里那圈就是「窗口外」）。
function Shot-Region([IntPtr]$hwnd, [string]$name) {
  $rect = Get-Rect $hwnd
  $pad = 6
  $bmp = New-Object System.Drawing.Bitmap ($rect.W + 2 * $pad), ($rect.H + 2 * $pad)
  $g = [System.Drawing.Graphics]::FromImage($bmp)
  $g.CopyFromScreen($rect.X - $pad, $rect.Y - $pad, 0, 0, $bmp.Size)
  $g.Dispose()
  $bmp.Save((Join-Path $shots $name), [System.Drawing.Imaging.ImageFormat]::Png)
  return [pscustomobject]@{ Bitmap = $bmp; Pad = $pad; Rect = $rect }
}

# 拍一张"卡片真的画上去了"的截图：卡片是深色的，若中心点还是浅色（WebView2 还没画完）
# 就等一会儿重拍。不做这一步会量到"窗口还没渲染"的全透明白。
function Shot-Card([IntPtr]$hwnd, [string]$name) {
  for ($i = 0; $i -lt 6; $i++) {
    $shot = Shot-Region $hwnd $name
    $center = $shot.Bitmap.GetPixel([int]($shot.Bitmap.Width / 2), [int]($shot.Bitmap.Height / 2))
    if ($center.R -lt 140) { return $shot }
    $shot.Bitmap.Dispose()
    Start-Sleep -Milliseconds 700
  }
  throw "截图里看不到卡片（$name 一直是浅色）：窗口没渲染出来"
}

$installedWasRunning = [bool](Get-Process clipnest -ErrorAction SilentlyContinue)

# 起一个 dev 实例 → 让白色文本框拿到焦点 → 热键弹出 → 截一张「卡片已画好」的图
# → 返回三个采样点：圆角外侧（= 背后的桌面/窗口）、卡片空白处、卡片中心。
function Measure-Capture([int]$transparency, [string]$shotName) {
  $log = Start-DevApp $transparency

  $target = Wait-For {
    $c = Get-Process | Where-Object { $_.MainWindowTitle -eq 'ClipNest paste target' } | Select-Object -First 1
    if ($c) { $c.Refresh(); if ($c.MainWindowHandle -ne 0) { $c } }
  } 3000
  if (-not $target) {
    Start-Process -FilePath $pwshPath -ArgumentList '-STA', '-NoProfile', '-File', (Join-Path $root 'scratch\paste-target.ps1') | Out-Null
    $target = Wait-For {
      $c = Get-Process | Where-Object { $_.MainWindowTitle -eq 'ClipNest paste target' } | Select-Object -First 1
      if ($c) { $c.Refresh(); if ($c.MainWindowHandle -ne 0) { $c } }
    } 20000
  }
  if (-not $target) { throw 'paste target 窗口没起来' }
  [CNT]::SetForegroundWindow($target.MainWindowHandle) | Out-Null
  Start-Sleep -Milliseconds 700

  $skip = (Get-Content $log).Count
  [System.Windows.Forms.SendKeys]::SendWait('^+n')
  $m = Wait-For {
    $lines = Get-Content $log
    if ($lines.Count -le $skip) { return $null }
    $hit = $lines[$skip..($lines.Count - 1)] | Where-Object { $_ -match $logLineRe } | Select-Object -Last 1
    if ($hit) { return $hit }
    return $null
  } 10000
  if (-not $m) { throw "热键没弹出 capture 窗口（transparency=$transparency 的日志里没有 capture window 行）" }
  $null = $m -match $logLineRe
  $hwnd = [IntPtr][Convert]::ToInt64($matches[5], 16)
  Wait-For { if ([CNT]::IsWindowVisible($hwnd)) { $true } else { $null } } 5000 | Out-Null
  Start-Sleep -Milliseconds 600

  $shot = Shot-Card $hwnd $shotName
  $pad = $shot.Pad
  # 圆角外侧：窗口左上角往外 3px（半径 10px 的圆弧之外），那里应该就是背后的白文本框。
  $corner = $shot.Bitmap.GetPixel(3, 3)
  # 卡片空白处：底栏左侧（footer 按钮靠右，左边是空的）。
  $card = $shot.Bitmap.GetPixel($pad + 30, $pad + $shot.Rect.H - 12)
  $shot.Bitmap.Dispose()
  return [pscustomobject]@{ Corner = $corner; Card = $card; Size = "$($shot.Rect.W)x$($shot.Rect.H)" }
}

try {
  $a = Measure-Capture 0 'capture-transparency-0.png'
  $b = Measure-Capture 30 'capture-transparency-30.png'

  $fmt = { param($c) "$($c.R),$($c.G),$($c.B)" }
  Write-Host "0%   : 圆角外侧=$(& $fmt $a.Corner)  卡片=$(& $fmt $a.Card)   ($($a.Size))"
  Write-Host "30%  : 圆角外侧=$(& $fmt $b.Corner)  卡片=$(& $fmt $b.Card)   ($($b.Size))"

  # 1) 圆角外侧不是窗口底色（14,16,21），而是背后的浅色窗口 → 窗口真的透明
  if ($a.Corner.R -gt 150 -and $a.Corner.R - $a.Card.R -gt 100) {
    $results.Add("PASS 0% 时圆角外侧是背后的浅色窗口 ($(& $fmt $a.Corner))，不是窗口底色——窗口真透明")
  } else {
    $results.Add("FAIL 0% 时圆角外侧 $(& $fmt $a.Corner) 看起来还是实底（卡片 $(& $fmt $a.Card)）")
  }

  # 2) 0% 时卡片是实心 surface（#0e1015 = 14,16,21）
  if ([Math]::Abs($a.Card.R - 14) -le 6 -and [Math]::Abs($a.Card.G - 16) -le 6 -and [Math]::Abs($a.Card.B - 21) -le 6) {
    $results.Add("PASS 0% 时卡片是实心 surface ($(& $fmt $a.Card))")
  } else {
    $results.Add("FAIL 0% 时卡片 $(& $fmt $a.Card)，期望 14,16,21")
  }

  # 3) 30% 时卡片明显变浅 = 真的与背后的东西混合了。不锁死具体数值：混合层里还有
  #    一层 DWM backdrop（Acrylic）自己的底色，算不出一个能写死的期望值。
  #    但方向必须对：比实心 surface 浅一大截，同时又还没浅到跟背后一样。
  if ($b.Card.R -ge $a.Card.R + 25 -and $b.Card.R -le $b.Corner.R - 25) {
    $results.Add(("PASS 30% 时卡片 {0} 比实心 {1} 明显更浅，且仍比背后 {2} 深——半透明真的生效" -f `
      (& $fmt $b.Card), (& $fmt $a.Card), (& $fmt $b.Corner)))
  } else {
    $results.Add(("FAIL 30% 时卡片 {0} 没有与背后混合（实心 {1}，背后 {2}）：alpha 被合到黑底上了" -f `
      (& $fmt $b.Card), (& $fmt $a.Card), (& $fmt $b.Corner)))
  }

  # 4) 半透明之后圆角外侧仍然透明（别用"把整窗改成不透明 + 浅色"来糊弄）
  if ($b.Corner.R -gt 150) {
    $results.Add("PASS 30% 时圆角外侧仍是背后的窗口 ($(& $fmt $b.Corner))")
  } else {
    $results.Add("FAIL 30% 时圆角外侧 $(& $fmt $b.Corner) 变实了")
  }
  $results | ForEach-Object { Write-Host $_ }
  $failed = @($results | Where-Object { $_ -like 'FAIL*' }).Count
  Write-Host ("E2E-RESULT: {0} (shots: {1})" -f $(if ($failed -eq 0) { 'PASS' } else { "FAIL x$failed" }), $shots)
  exit $(if ($failed -eq 0) { 0 } else { 1 })
}
finally {
  Stop-AllApp
  Get-Process | Where-Object { $_.MainWindowTitle -eq 'ClipNest paste target' } | ForEach-Object { try { $_.Kill() } catch {} }
  if ($installedWasRunning -and (Test-Path $installedExe)) {
    Start-Process -FilePath $installedExe -ArgumentList '--background' | Out-Null
    Write-Host "已把安装版 ClipNest 拉起来：$installedExe --background"
  }
}
