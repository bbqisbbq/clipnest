# End-to-end check of the region-screenshot feature.
#
#   1. stop every ClipNest process, start the dev build against a sandbox APPDATA
#   2. fire the hotkey with real input -> the overlay must appear over the monitor
#      under the cursor (geometry comes back from the app log)
#   3. drag a rectangle -> an image entry with the dragged size lands in the
#      sandbox DB, the crop math shows up in the log, and the PNG is on the clipboard
#   4. fire it again and press Esc -> no new entry
#   5. put the installed build back
#
# Real input (SetCursorPos + mouse_event + keybd_event), so the hotkey path, the
# Win32 overlay placement and the WebView drag/keyboard handling are all exercised.
#
# 用 Ctrl+Shift+Z 而不是默认的 Ctrl+Shift+A：这台机器上后者已被别的程序占用
# （热键预检会如实报错，见 internal/clip/hotkey_windows.go）。
#
# 参数 -Exe 可以指向发布构建（bin\clipnest.exe）做"装之前先按真包验一遍"。
param([string]$Exe = "")
Add-Type -AssemblyName System.Drawing
Add-Type -AssemblyName System.Windows.Forms
Add-Type @'
using System;
using System.Runtime.InteropServices;
public class ShotWin {
  [DllImport("user32.dll")] public static extern bool SetCursorPos(int x, int y);
  [DllImport("user32.dll")] public static extern void mouse_event(uint f, uint dx, uint dy, uint d, IntPtr e);
  [DllImport("user32.dll")] public static extern void keybd_event(byte vk, byte scan, uint flags, UIntPtr extra);
  public const uint LEFTDOWN = 0x0002, LEFTUP = 0x0004;
  public const byte VK_CONTROL = 0x11, VK_SHIFT = 0x10, VK_Z = 0x7A, VK_ESCAPE = 0x1B;
}
'@

$root = 'E:\default\clipnest'
$devExe = if ($Exe) { $Exe } else { Join-Path $root 'bin\clipnest-dev.exe' }
$installedExe = 'C:\Program Files\bbqisbbq\ClipNest\clipnest.exe'
$sandbox = Join-Path $root 'scratch\appdata-shot25'
$log = Join-Path $sandbox 'ClipNest\clipnest.log'
$db = Join-Path $sandbox 'ClipNest\clipnest.db'
$shotDir = Join-Path $root 'scratch'
$python = 'C:\Python313\python.exe'
$results = New-Object System.Collections.Generic.List[string]

function Say([string]$s) { $results.Add($s); Write-Host $s }
function Wait-For([scriptblock]$cond, [int]$timeoutMs = 12000) {
  $sw = [Diagnostics.Stopwatch]::StartNew()
  while ($sw.ElapsedMilliseconds -lt $timeoutMs) {
    $v = & $cond
    if ($null -ne $v -and $v -ne $false) { return $v }
    Start-Sleep -Milliseconds 120
  }
  return $null
}
function TapHotkey() {
  [ShotWin]::keybd_event([ShotWin]::VK_CONTROL, 0, 0, [UIntPtr]::Zero)
  [ShotWin]::keybd_event([ShotWin]::VK_SHIFT, 0, 0, [UIntPtr]::Zero)
  [ShotWin]::keybd_event([ShotWin]::VK_Z, 0, 0, [UIntPtr]::Zero)
  Start-Sleep -Milliseconds 60
  [ShotWin]::keybd_event([ShotWin]::VK_Z, 0, 2, [UIntPtr]::Zero)
  [ShotWin]::keybd_event([ShotWin]::VK_SHIFT, 0, 2, [UIntPtr]::Zero)
  [ShotWin]::keybd_event([ShotWin]::VK_CONTROL, 0, 2, [UIntPtr]::Zero)
}
function DragRect([int]$x1, [int]$y1, [int]$x2, [int]$y2) {
  [ShotWin]::SetCursorPos($x1, $y1) | Out-Null
  Start-Sleep -Milliseconds 200
  [ShotWin]::mouse_event([ShotWin]::LEFTDOWN, 0, 0, 0, [IntPtr]::Zero)
  Start-Sleep -Milliseconds 100
  for ($i = 1; $i -le 12; $i++) {
    $x = [int]($x1 + ($x2 - $x1) * $i / 12)
    $y = [int]($y1 + ($y2 - $y1) * $i / 12)
    [ShotWin]::SetCursorPos($x, $y) | Out-Null
    Start-Sleep -Milliseconds 45
  }
  Start-Sleep -Milliseconds 150
  # 松手前先拍一张：这正是用户框完还没松手时的样子（遮罩 + 选区 + 尺寸气泡）
  Screenshot 'shot-e2e-1b-selecting.png'
  [ShotWin]::mouse_event([ShotWin]::LEFTUP, 0, 0, 0, [IntPtr]::Zero)
  Start-Sleep -Milliseconds 800
}
function Screenshot([string]$name) {
  $screen = [System.Windows.Forms.Screen]::PrimaryScreen.Bounds
  $bmp = New-Object System.Drawing.Bitmap $screen.Width, $screen.Height
  $g = [System.Drawing.Graphics]::FromImage($bmp)
  $g.CopyFromScreen($screen.X, $screen.Y, 0, 0, $bmp.Size)
  $g.Dispose()
  $bmp.Save((Join-Path $shotDir $name), [System.Drawing.Imaging.ImageFormat]::Png)
  $bmp.Dispose()
}
function EntryCount() {
  if (-not (Test-Path $db)) { return 0 }
  $code = 'import sqlite3,sys;c=sqlite3.connect(sys.argv[1]);print(c.execute("select count(*) from entries").fetchone()[0])'
  return [int](& $python -c $code $db)
}
function LastEntry() {
  $code = 'import sqlite3,sys;c=sqlite3.connect(sys.argv[1]);r=c.execute("select kind,width,height,source_app from entries order by created_at desc limit 1").fetchone();print(",".join(str(v) for v in r))'
  return (& $python -c $code $db)
}
function LastLogMatch([string]$pattern) {
  if (-not (Test-Path $log)) { return $null }
  return (Select-String -Path $log -Pattern $pattern | Select-Object -Last 1).Line
}

# ---- 1. sandbox up -------------------------------------------------------
Get-Process | Where-Object { $_.ProcessName -like 'clipnest*' } | Stop-Process -Force -ErrorAction SilentlyContinue
Start-Sleep -Milliseconds 700
Remove-Item -Recurse -Force $sandbox -ErrorAction SilentlyContinue
New-Item -ItemType Directory -Force -Path (Join-Path $sandbox 'ClipNest') | Out-Null
Remove-Item (Join-Path $shotDir 'shot-e2e-*.png') -ErrorAction SilentlyContinue

$env:APPDATA = $sandbox
$env:CLIPNEST_KEEP_CONSOLE = '1'
'{"shotHotkey":"Ctrl+Shift+F11"}' | Set-Content -Encoding utf8 (Join-Path $sandbox 'ClipNest\settings.json')
$proc = Start-Process -FilePath $devExe -ArgumentList '--background' -PassThru
$hot = Wait-For { $l = LastLogMatch 'shot hotkey registered: (.+)'; if ($l) { $l } }
if (-not $hot) { Say 'FAIL: 截图热键没注册上'; Get-Content $log -ErrorAction SilentlyContinue | Select-Object -Last 25; exit 1 }
Say "OK: $hot"
if (LastLogMatch 'panic error') { Say 'FAIL: 启动就崩了（日志里有 panic）'; exit 1 }

# ---- 2. fire the hotkey --------------------------------------------------
$before = EntryCount
TapHotkey
$geomLine = Wait-For { $l = LastLogMatch 'shot window: pos='; if ($l) { $l } } 10000
if (-not $geomLine) { Say 'FAIL: 遮罩窗没出现'; Get-Content $log | Select-Object -Last 20; exit 1 }
$m = [regex]::Match($geomLine, 'pos=\((-?\d+),(-?\d+)\) size=\((\d+)x(\d+)\)')
$gx = [int]$m.Groups[1].Value; $gy = [int]$m.Groups[2].Value
$gw = [int]$m.Groups[3].Value; $gh = [int]$m.Groups[4].Value
Say "OK: overlay at ($gx,$gy) size ${gw}x${gh}"
Start-Sleep -Milliseconds 900
Screenshot 'shot-e2e-1-overlay.png'

# ---- 3. drag a rectangle -------------------------------------------------
$sx = $gx + [int]($gw * 0.20)
$sy = $gy + [int]($gh * 0.25)
$ex = $gx + [int]($gw * 0.60)
$ey = $gy + [int]($gh * 0.65)
$wantW = $ex - $sx; $wantH = $ey - $sy
Say "drag: ($sx,$sy) -> ($ex,$ey) = ${wantW}x${wantH}"
DragRect $sx $sy $ex $ey
$after = EntryCount
$last = LastEntry
Say "entries before=$before after=$after"
Say "last entry = $last"
Say "crop log: $(LastLogMatch 'shot crop:')"

$parts = $last -split ','
if ($parts[0] -ne 'image') {
  Say "FAIL: 最后一条不是图片条目（$last）"
} else {
  $gotW = [int]$parts[1]; $gotH = [int]$parts[2]
  if ([Math]::Abs($gotW - $wantW) -le 3 -and [Math]::Abs($gotH - $wantH) -le 3) {
    Say "OK: 截到 ${gotW}x${gotH}，与拖出来的 ${wantW}x${wantH} 一致"
  } else {
    Say "FAIL: 尺寸对不上：截到 ${gotW}x${gotH}，拖的是 ${wantW}x${wantH}"
  }
}

# ---- clipboard should hold that image ------------------------------------
$img = $null
for ($i = 0; $i -lt 10 -and -not $img; $i++) {
  try { $img = [System.Windows.Forms.Clipboard]::GetImage() } catch { Start-Sleep -Milliseconds 250 }
}
if ($img) { Say "OK: 剪贴板里有图片 $($img.Width)x$($img.Height)"; $img.Dispose() } else { Say 'FAIL: 剪贴板里没有图片' }

# ---- 4. Esc cancels ------------------------------------------------------
$beforeEsc = EntryCount
TapHotkey
Start-Sleep -Milliseconds 1500
[ShotWin]::keybd_event([ShotWin]::VK_ESCAPE, 0, 0, [UIntPtr]::Zero)
[ShotWin]::keybd_event([ShotWin]::VK_ESCAPE, 0, 2, [UIntPtr]::Zero)
Start-Sleep -Milliseconds 900
if ((EntryCount) -eq $beforeEsc) { Say 'OK: Esc 取消没有多出条目' } else { Say 'FAIL: Esc 之后多出了条目' }
Screenshot 'shot-e2e-2-after-esc.png'

# ---- 5. restore ----------------------------------------------------------
Stop-Process -Id $proc.Id -Force -ErrorAction SilentlyContinue
Start-Sleep -Milliseconds 700
Remove-Item Env:\CLIPNEST_KEEP_CONSOLE -ErrorAction SilentlyContinue
if (Test-Path $installedExe) { Start-Process -FilePath $installedExe -ArgumentList '--background' }
Write-Host "`n===== summary ====="
$results | ForEach-Object { Write-Host $_ }

