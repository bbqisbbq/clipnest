# End-to-end check of the quick-capture popup ("记一笔") move / resize support:
#   1. (re)start ClipNest and give the desktop a focused text box (so there is a caret)
#   2. fire Ctrl+Shift+N; the popup's hwnd + geometry come from the app log
#      (`capture window: pos=(x,y) size=(WxH) hwnd=0x…`)
#   3. drag its header      -> window position must follow the mouse
#   4. drag its right edge  -> width must grow
#   5. drag its bottom edge -> height must grow
#   6. Esc, restart the app, fire the hotkey again -> same size comes back
#
# Input is real (SetCursorPos + mouse_event), not window messages, so the Wails
# runtime's --wails-draggable / edge-resize paths are genuinely exercised.
Add-Type -AssemblyName System.Drawing
Add-Type -AssemblyName System.Windows.Forms
Add-Type @'
using System;
using System.Runtime.InteropServices;
public class CN {
  [StructLayout(LayoutKind.Sequential)] public struct RECT { public int Left, Top, Right, Bottom; }
  [DllImport("user32.dll")] public static extern bool GetWindowRect(IntPtr h, out RECT r);
  [DllImport("user32.dll")] public static extern bool SetForegroundWindow(IntPtr h);
  [DllImport("user32.dll")] public static extern bool SetCursorPos(int x, int y);
  [DllImport("user32.dll")] public static extern void mouse_event(uint f, uint dx, uint dy, uint d, IntPtr e);
  [DllImport("user32.dll")] public static extern bool IsWindowVisible(IntPtr h);
  [DllImport("user32.dll")] public static extern uint GetDpiForWindow(IntPtr h);
  public const uint LEFTDOWN = 0x0002, LEFTUP = 0x0004;
}
'@

$root = 'E:\default\clipnest'
$log = Join-Path $env:APPDATA 'ClipNest\clipnest.log'
$shotDir = Join-Path $root 'scratch'
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
function Get-Rect([IntPtr]$h) {
  $r = New-Object CN+RECT
  if (-not [CN]::GetWindowRect($h, [ref]$r)) { return $null }
  return [pscustomobject]@{ X = $r.Left; Y = $r.Top; W = $r.Right - $r.Left; H = $r.Bottom - $r.Top }
}
function Shot([string]$name) {
  $screen = [System.Windows.Forms.Screen]::PrimaryScreen.Bounds
  $bmp = New-Object System.Drawing.Bitmap $screen.Width, $screen.Height
  $g = [System.Drawing.Graphics]::FromImage($bmp)
  $g.CopyFromScreen($screen.X, $screen.Y, 0, 0, $bmp.Size)
  $g.Dispose()
  $p = Join-Path $shotDir $name
  $bmp.Save($p, [System.Drawing.Imaging.ImageFormat]::Png)
  $bmp.Dispose()
}
function Drag([int]$fromX, [int]$fromY, [int]$dx, [int]$dy) {
  [CN]::SetCursorPos($fromX, $fromY) | Out-Null
  Start-Sleep -Milliseconds 250
  [CN]::mouse_event([CN]::LEFTDOWN, 0, 0, 0, [IntPtr]::Zero)
  Start-Sleep -Milliseconds 180
  $steps = 12
  for ($i = 1; $i -le $steps; $i++) {
    [CN]::SetCursorPos([int]($fromX + $dx * $i / $steps), [int]($fromY + $dy * $i / $steps)) | Out-Null
    Start-Sleep -Milliseconds 45
  }
  Start-Sleep -Milliseconds 150
  [CN]::mouse_event([CN]::LEFTUP, 0, 0, 0, [IntPtr]::Zero)
  Start-Sleep -Milliseconds 450
}
function Start-App([switch]$ResetSize) {
  if (Get-Process clipnest -ErrorAction SilentlyContinue) {
    Get-Process clipnest | Stop-Process -Force
    Start-Sleep -Milliseconds 1500
  }
  if ($ResetSize) {
    # The remembered size lives in the db meta table; drop it so the run starts
    # from the shipped default and the first assertion means something.
    Push-Location $root
    try { & go run ./scratch/metadump -del capture.size 2>&1 | Out-Null } finally { Pop-Location }
  }
  Start-Process -FilePath (Join-Path $root 'bin\clipnest.exe') | Out-Null
  Wait-For { Get-Process clipnest -ErrorAction SilentlyContinue } 15000 | Out-Null
  Start-Sleep -Seconds 4
}
# Fire the hotkey and pick up the popup from the log line the app just wrote.
function Open-Capture([IntPtr]$focusHwnd) {
  $skip = (Get-Content $log).Count
  [CN]::SetForegroundWindow($focusHwnd) | Out-Null
  Start-Sleep -Milliseconds 500
  [System.Windows.Forms.SendKeys]::SendWait('^+n')
  $m = Wait-For {
    $lines = Get-Content $log
    if ($lines.Count -le $skip) { return $null }
    $hit = $lines[$skip..($lines.Count - 1)] | Where-Object { $_ -match $logLineRe } | Select-Object -Last 1
    if ($hit) { return $hit }
    return $null
  } 10000
  if (-not $m) { return $null }
  $null = $m -match $logLineRe
  $hwnd = [IntPtr][Convert]::ToInt64($matches[5], 16)
  Wait-For { if ([CN]::IsWindowVisible($hwnd)) { $true } else { $null } } 5000 | Out-Null
  Start-Sleep -Milliseconds 900
  return [pscustomobject]@{ Hwnd = $hwnd; LogX = [int]$matches[1]; LogY = [int]$matches[2]; LogW = [int]$matches[3]; LogH = [int]$matches[4] }
}

# 1. App + a focused text box.
Start-App -ResetSize
if (-not (Get-Process powershell, pwsh -ErrorAction SilentlyContinue | Where-Object { $_.MainWindowTitle -eq 'ClipNest paste target' })) {
  Start-Process -FilePath $pwshPath -ArgumentList '-STA', '-NoProfile', '-File', (Join-Path $root 'scratch\paste-target.ps1') | Out-Null
}
$target = Wait-For {
  $c = Get-Process | Where-Object { $_.MainWindowTitle -eq 'ClipNest paste target' } | Select-Object -First 1
  if ($c) { $c.Refresh(); if ($c.MainWindowHandle -ne 0) { $c } }
} 20000
if (-not $target) { throw 'paste target window never appeared' }
$targetHwnd = $target.MainWindowHandle
[CN]::SetForegroundWindow($targetHwnd) | Out-Null
Start-Sleep -Milliseconds 600

# 2. Open the popup.
$cap = Open-Capture $targetHwnd
if (-not $cap) {
  Write-Host 'E2E-RESULT: FAIL — capture window never appeared (no log line)'
  Shot 'shot-capture-missing.png'
  exit 1
}
$hwnd = $cap.Hwnd
$dpi = [CN]::GetDpiForWindow($hwnd)
$scale = $dpi / 96.0
$r0 = Get-Rect $hwnd
Write-Host "dpi=$dpi scale=$scale log=(pos $($cap.LogX),$($cap.LogY) size $($cap.LogW)x$($cap.LogH)) rect=(x=$($r0.X) y=$($r0.Y) w=$($r0.W) h=$($r0.H))"
Shot 'shot-capture-before.png'
$expectW = [int](620 * $scale); $expectH = [int](220 * $scale)
if ([Math]::Abs($r0.W - $expectW) -le 6 -and [Math]::Abs($r0.H - $expectH) -le 6) {
  $results.Add("PASS 首次尺寸 = $($r0.W)x$($r0.H)，即默认 620x220 逻辑像素")
} else {
  $results.Add("FAIL 首次尺寸 $($r0.W)x$($r0.H)，期望 ${expectW}x${expectH}")
}

# 3. Drag by the header: the window must move with the mouse.
[CN]::SetForegroundWindow($hwnd) | Out-Null
Start-Sleep -Milliseconds 300
Drag ($r0.X + [int](40 * $scale)) ($r0.Y + [int](14 * $scale)) ([int](-140 * $scale)) ([int](-90 * $scale))
$r1 = Get-Rect $hwnd
$movedX = $r1.X - $r0.X
$movedY = $r1.Y - $r0.Y
Write-Host "after header drag: x=$($r1.X) y=$($r1.Y) (Δ=$movedX,$movedY)"
if ([Math]::Abs($movedX) -gt 60 -and [Math]::Abs($movedY) -gt 30) {
  $results.Add("PASS 拖动标题条移动了窗口（Δ=$movedX,$movedY）")
} else {
  $results.Add("FAIL 拖动标题条没有移动窗口（Δ=$movedX,$movedY）")
}

# 4. Drag the right edge: the window must get wider.
Drag ($r1.X + $r1.W - 3) ($r1.Y + [int]($r1.H / 2)) ([int](220 * $scale)) 0
$r2 = Get-Rect $hwnd
$grewBy = $r2.W - $r1.W
Write-Host "after right-edge drag: w=$($r2.W) h=$($r2.H) (Δw=$grewBy)"
if ($grewBy -gt 120) {
  $results.Add("PASS 拖右边缘把宽度拉大了 ${grewBy}px（$($r1.W) → $($r2.W)）")
} else {
  $results.Add("FAIL 拖右边缘没有改变宽度（Δw=$grewBy）")
}

# 5. Drag the bottom edge: the window must get taller.
Drag ($r2.X + [int]($r2.W / 2)) ($r2.Y + $r2.H - 3) 0 ([int](160 * $scale))
$r3 = Get-Rect $hwnd
$grewH = $r3.H - $r2.H
Write-Host "after bottom-edge drag: w=$($r3.W) h=$($r3.H) (Δh=$grewH)"
if ($grewH -gt 80) {
  $results.Add("PASS 拖下边缘把高度拉大了 ${grewH}px（$($r2.H) → $($r3.H)）")
} else {
  $results.Add("FAIL 拖下边缘没有改变高度（Δh=$grewH）")
}
Shot 'shot-capture-resized.png'
$finalDipW = [int][Math]::Round($r3.W / $scale)
$finalDipH = [int][Math]::Round($r3.H / $scale)

# 6. Close, restart, reopen: the size must stick.
[CN]::SetForegroundWindow($hwnd) | Out-Null
Start-Sleep -Milliseconds 300
[System.Windows.Forms.SendKeys]::SendWait('{ESC}')
Start-Sleep -Milliseconds 1200
if (-not [CN]::IsWindowVisible($hwnd)) { $results.Add('PASS Esc 关闭了窗口') } else { $results.Add('FAIL Esc 没有关闭窗口') }

Start-App
$cap2 = Open-Capture $targetHwnd
if (-not $cap2) {
  $results.Add('FAIL 重启后弹窗没出现')
} else {
  $r4 = Get-Rect $cap2.Hwnd
  Write-Host "after restart: log size $($cap2.LogW)x$($cap2.LogH), rect $($r4.W)x$($r4.H), expected $finalDipW x $finalDipH (dip)"
  if ([Math]::Abs($cap2.LogW - $finalDipW) -le 4 -and [Math]::Abs($cap2.LogH - $finalDipH) -le 4) {
    $results.Add("PASS 重启后仍是上次拖出来的尺寸 $($cap2.LogW)x$($cap2.LogH)")
  } else {
    $results.Add("FAIL 重启后尺寸是 $($cap2.LogW)x$($cap2.LogH)，期望 ${finalDipW}x${finalDipH}")
  }
  if ([Math]::Abs($r4.W - $r3.W) -le 4 -and [Math]::Abs($r4.Y - $r0.Y) -ge 0) {
    $results.Add('PASS 位置仍跟着光标（不是上次拖走的位置）')
  }
  [CN]::SetForegroundWindow($cap2.Hwnd) | Out-Null
  Start-Sleep -Milliseconds 400
  Shot 'shot-capture-after-restart.png'

  # 7. Shrink it hard: the minimum size must hold (a 60px editor is unusable).
  $r5 = Get-Rect $cap2.Hwnd
  Drag ($r5.X + $r5.W - 3) ($r5.Y + [int]($r5.H / 2)) ([int](-600 * $scale)) 0
  Drag ($r5.X + [int]($r5.W / 2)) ($r5.Y + (Get-Rect $cap2.Hwnd).H - 3) 0 ([int](-400 * $scale))
  $r6 = Get-Rect $cap2.Hwnd
  Write-Host "after shrinking hard: w=$($r6.W) h=$($r6.H) (min is 420x160 dip)"
  if ([Math]::Abs($r6.W - [int](420 * $scale)) -le 6 -and [Math]::Abs($r6.H - [int](160 * $scale)) -le 6) {
    $results.Add("PASS 缩到最小停在 420x160（实际 $([int]($r6.W / $scale))x$([int]($r6.H / $scale))）")
  } else {
    $results.Add("FAIL 最小尺寸没兜住：$([int]($r6.W / $scale))x$([int]($r6.H / $scale))，期望 420x160")
  }
  Shot 'shot-capture-min.png'

  # 8. Double-click the header: it must NOT maximise (Windows only emulates the
  #    caption drag here; a maximise would leave the popup the size of the screen).
  $r7 = Get-Rect $cap2.Hwnd
  $hx = $r7.X + [int](40 * $scale); $hy = $r7.Y + [int](14 * $scale)
  [CN]::SetCursorPos($hx, $hy) | Out-Null
  Start-Sleep -Milliseconds 200
  [CN]::mouse_event([CN]::LEFTDOWN, 0, 0, 0, [IntPtr]::Zero)
  [CN]::mouse_event([CN]::LEFTUP, 0, 0, 0, [IntPtr]::Zero)
  Start-Sleep -Milliseconds 90
  [CN]::mouse_event([CN]::LEFTDOWN, 0, 0, 0, [IntPtr]::Zero)
  [CN]::mouse_event([CN]::LEFTUP, 0, 0, 0, [IntPtr]::Zero)
  Start-Sleep -Milliseconds 900
  $r8 = Get-Rect $cap2.Hwnd
  Write-Host "after header double-click: w=$($r8.W) h=$($r8.H)"
  if ([Math]::Abs($r8.W - $r7.W) -le 6 -and [Math]::Abs($r8.H - $r7.H) -le 6) {
    $results.Add('PASS 双击标题条没有把窗口最大化')
  } else {
    $results.Add("FAIL 双击标题条把窗口改成了 $($r8.W)x$($r8.H)")
  }
  [CN]::SetForegroundWindow($cap2.Hwnd) | Out-Null
  Start-Sleep -Milliseconds 300
  [System.Windows.Forms.SendKeys]::SendWait('{ESC}')
}

Start-Sleep -Milliseconds 600
Write-Host '---- log: capture lines ----'
Get-Content $log -Tail 40 | Where-Object { $_ -match $logLineRe }
Write-Host '---- results ----'
$results | ForEach-Object { Write-Host $_ }
$failed = ($results | Where-Object { $_ -like 'FAIL*' }).Count
if ($failed -eq 0) { Write-Host 'E2E-RESULT: PASS' } else { Write-Host "E2E-RESULT: FAIL ($failed)" }

if ($env:E2E_KEEP_TARGET -ne '1') {
  Get-Process | Where-Object { $_.MainWindowTitle -eq 'ClipNest paste target' } | Stop-Process -Force
}
# Leave the machine in the shipped state: keep the app running at the default size.
Start-App -ResetSize
