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
param(
  # 传别的构建产物时用（换壳验证：bin\clipnest.exe 可能指 MyGo 构建）
  [string]$Exe = '',

  # 主题预设（lib/themes.ts 里的 id）：测浅色主题时传 default-light / pure-light
  [string]$Preset = 'default-dark',
  # 深浅方案：留空则按预设自己的明暗（settings.theme = system 时以预设为准）
  [string]$Theme = 'system',
  # 背后那块窗口的底色（#rrggbb）：白色会让你看不出浅色主题的透明（白叠白）
  [string]$Backdrop = '#ffffff',
  # 第二档透明度（默认 30；传 100 可验证卡片完全透掉那一端）
  [int]$Mid = 30
)

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

# 闲置销毁会把这些脚本正拿着的窗口收掉（产品默认行为），测试期间关掉。
$env:CLIPNEST_IDLE_DESTROY = '0'
$root = 'E:\default\clipnest'
$devExe = if ($Exe) { $Exe } else { Join-Path $root 'bin\clipnest-dev.exe' }
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
    themePreset         = $Preset
    theme               = $Theme
    showOnLaunch        = $false
    autoStart           = $false
    updateCheck         = $false
    syncEnabled         = $false
    captureTransparency = $transparency
  } | ConvertTo-Json
  Set-Content -Path (Join-Path $dataDir 'settings.json') -Value $settings -Encoding utf8
  $env:APPDATA = $sandboxAppData
  Start-Process -FilePath $devExe | Out-Null
  $procName = [IO.Path]::GetFileNameWithoutExtension($devExe)   # 传 -Exe 时进程名可能不是 clipnest-dev
if (-not (Wait-For { Get-Process $procName -ErrorAction SilentlyContinue } 20000)) {
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

# 拍一张"卡片真的画上去了"的截图。判据不能是"中心点够不够暗"——浅色主题下卡片是米白的，
# 那条会把画好的窗口当成没渲染。改成看**画面里有没有内容**：卡片上有标题、占位符、按钮，
# 整幅图的亮度标准差远大于"一片纯色桌面"。标准差太小就等一会儿重拍。
function Shot-Card([IntPtr]$hwnd, [string]$name) {
  for ($i = 0; $i -lt 6; $i++) {
    $shot = Shot-Region $hwnd $name
    $lum = New-Object System.Collections.Generic.List[double]
    $stepX = [Math]::Max(1, [int]($shot.Bitmap.Width / 16))
    $stepY = [Math]::Max(1, [int]($shot.Bitmap.Height / 10))
    for ($y = 0; $y -lt $shot.Bitmap.Height; $y += $stepY) {
      for ($x = 0; $x -lt $shot.Bitmap.Width; $x += $stepX) {
        $c = $shot.Bitmap.GetPixel($x, $y)
        $lum.Add(0.299 * $c.R + 0.587 * $c.G + 0.114 * $c.B)
      }
    }
    $mean = ($lum | Measure-Object -Average).Average
    $var = 0.0; foreach ($v in $lum) { $var += ($v - $mean) * ($v - $mean) }
    $sd = [Math]::Sqrt($var / $lum.Count)
    if ($sd -ge 12) { return $shot }
    $shot.Bitmap.Dispose()
    Start-Sleep -Milliseconds 700
  }
  throw "截图里看不到卡片（$name 一直是一片纯色，标准差 < 12）：窗口没渲染出来"
}

$installedWasRunning = [bool](Get-Process clipnest -ErrorAction SilentlyContinue)

# 起一个 dev 实例 → 让白色文本框拿到焦点 → 热键弹出 → 截一张「卡片已画好」的图
# → 返回三个采样点：圆角外侧（= 背后的桌面/窗口）、卡片空白处、卡片中心。
function Measure-Capture([int]$transparency, [string]$shotName) {
  # 每次都用指定底色的新目标窗（复用上一次的白窗会让 -Backdrop 失效）
  Get-Process | Where-Object { $_.MainWindowTitle -eq 'ClipNest paste target' } | ForEach-Object { try { $_.Kill() } catch {} }
  $env:CLIPNEST_TARGET_BG = $Backdrop
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
  $a = Measure-Capture 0 "capture-transparency-0-$Preset.png"
  $b = Measure-Capture $Mid "capture-transparency-$Mid-$Preset.png"

  # 背后那块窗口的预期颜色（就是 -Backdrop 给的那个）
  $bgExpected = [System.Drawing.ColorTranslator]::FromHtml($Backdrop)
  $fmt = { param(
  # 传别的构建产物时用（换壳验证：bin\clipnest.exe 可能指 MyGo 构建）
  [string]$Exe = '',
$c) "$($c.R),$($c.G),$($c.B)" }
  Write-Host "0%   : 圆角外侧=$(& $fmt $a.Corner)  卡片=$(& $fmt $a.Card)   ($($a.Size))"
  Write-Host "30%  : 圆角外侧=$(& $fmt $b.Corner)  卡片=$(& $fmt $b.Card)   ($($b.Size))"

  # 判定不写死任何主题的具体色值（浅色/深色预设的 --surface 差着十万八千里），
  # 只认三件物理事实：
  #   ① 圆角外侧 = 背后那块窗口的颜色（窗口真透明）
  #   ② 0% 时卡片与背后明显不同（卡片不透明）
  #   ③ 30% 时卡片朝背后的方向移动了一截、但没走到头（真混合，且混合层还压着自己的底色）
  # 换底色（白/蓝）、换主题预设时这些都成立；具体色值打印出来供人看。
  $pick = @{ Name = 'R'; C0 = $a.Card.R; C30 = $b.Card.R; BG = $a.Corner.R }
  foreach ($c in @(
      @{ Name = 'G'; C0 = $a.Card.G; C30 = $b.Card.G; BG = $a.Corner.G },
      @{ Name = 'B'; C0 = $a.Card.B; C30 = $b.Card.B; BG = $a.Corner.B })) {
    if ([Math]::Abs($c.BG - $c.C0) -gt [Math]::Abs($pick.BG - $pick.C0)) { $pick = $c }
  }
  $gap = $pick.BG - $pick.C0          # 不透明卡片与背后的差
  $moved = $pick.C30 - $pick.C0       # 半透明之后卡片移动了多少
  Write-Host ("主通道 {0}: 实心 {1} → 半透明 {2}，背后 {3}（跨度 {4}，移动 {5}）" -f `
    $pick.Name, $pick.C0, $pick.C30, $pick.BG, $gap, $moved)

  # 1) 圆角外侧 = 背后窗口（±25，抗抗锯齿与 DWM 阴影）
  $cornerOK = [Math]::Abs($a.Corner.R - $bgExpected.R) -le 25 -and
              [Math]::Abs($a.Corner.G - $bgExpected.G) -le 25 -and
              [Math]::Abs($a.Corner.B - $bgExpected.B) -le 25
  if ($cornerOK) {
    $results.Add("PASS 圆角外侧 = 背后窗口的颜色 ($(& $fmt $a.Corner))，不是窗口底色——窗口真透明")
  } else {
    $results.Add("FAIL 圆角外侧 $(& $fmt $a.Corner) ≠ 背后窗口 $(& $fmt $bgExpected)：窗口还是实底")
  }

  # 背后与卡片本身几乎同色（例如浅色主题 + 白文档）时，"半透明"在物理上就看不出差别：
  # 白叠白。这种情况不算失败，但要明确说出来，别让人以为开关没生效。
  if ([Math]::Abs($gap) -lt 40) {
    $results.Add(("NOTE 卡片与背后只差 {0}（$Backdrop）：这个组合本来就看不出透明，换彩色/深色背景再测" -f [int][Math]::Abs($gap)))
  }

  # 2) 0% 时卡片与背后明显不同（不透明的卡片）。背景与卡片同色时这条量不出来，跳过。
  if ([Math]::Abs($gap) -lt 40) {
    $results.Add("SKIP 0% 的卡片不透明判定：背后与卡片太接近，量不出来")
  } else {
    $results.Add(("PASS 0% 时卡片 {0} 与背后 {1} 明显不同（差 {2}）：卡片不透明" -f `
      (& $fmt $a.Card), (& $fmt $a.Corner), [int][Math]::Abs($gap)))
  }
  # 3) 第二档：默认 30% 时卡片朝背后移动了、且没走到头；-Mid 100 则要求卡片几乎完全透掉
  $towardBackdrop = [Math]::Sign($moved) -eq [Math]::Sign($gap)
  $visiblyMoved = [Math]::Abs($moved) -ge 8
  $notSwallowed = [Math]::Abs($moved) -lt [Math]::Abs($gap) - 8
  if ([Math]::Abs($gap) -lt 40) {
    $results.Add("SKIP 30% 的混合判定：背后与卡片太接近，量不出来（见上面的 NOTE）")
  } elseif ($Mid -ge 95) {
    # 最大档不苛求"像素等于背后"：卡片完全透掉之后，露出来的是 DWM 的 Acrylic 层，
    # 它自己带一层底色（白底下量到 146,146,146，蓝底下接近原色）。能证明"卡片自己不再
    # 画东西"就够——也就是离实心卡片足够远、并且朝背后方向走了大半。
    if ([Math]::Abs($moved) -ge ([Math]::Abs($gap) * 0.5) -and [Math]::Abs($moved) -ge 60) {
      $results.Add(("PASS 100% 时卡片 {0} 已基本透掉（实心 {1}，背后 {2}，移动 {3}/{4}）" -f `
        (& $fmt $b.Card), (& $fmt $a.Card), (& $fmt $a.Corner), [int]$moved, [int]$gap))
    } else {
      $results.Add(("FAIL 100% 时卡片 {0} 还是实心的样子（实心 {1}，移动 {2}）" -f `
        (& $fmt $b.Card), (& $fmt $a.Card), [int]$moved))
    }
  } elseif ($towardBackdrop -and $visiblyMoved -and $notSwallowed) {
    $results.Add(("PASS 30% 时卡片 {0} 朝背后移动了 {1}（跨度 {2}）——半透明真的与背后混合" -f `
      (& $fmt $b.Card), [int]$moved, [int]$gap))
  } else {
    $results.Add(("FAIL 30% 时卡片 {0} 没有与背后混合（实心 {1}，背后 {2}，移动 {3}）" -f `
      (& $fmt $b.Card), (& $fmt $a.Card), (& $fmt $a.Corner), [int]$moved))
  }

  # 4) 半透明之后圆角外侧仍然透明
  $cornerStillOK = [Math]::Abs($b.Corner.R - $bgExpected.R) -le 25 -and
                   [Math]::Abs($b.Corner.G - $bgExpected.G) -le 25 -and
                   [Math]::Abs($b.Corner.B - $bgExpected.B) -le 25
  if ($cornerStillOK) {
    $results.Add("PASS 30% 时圆角外侧仍是背后窗口 ($(& $fmt $b.Corner))")
  } else {
    $results.Add("FAIL 30% 时圆角外侧 $(& $fmt $b.Corner) 变实了（背后是 $(& $fmt $bgExpected)）")
  }  $results | ForEach-Object { Write-Host $_ }
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








