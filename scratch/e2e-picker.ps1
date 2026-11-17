# End-to-end check of the quick-paste popup:
#   1. start (or reuse) ClipNest
#   2. start a WinForms paste target with a focused text box
#   3. put a unique sentinel on the clipboard so it becomes the newest entry
#   4. fire the global hotkey, screenshot the popup, press Enter
#   5. assert the sentinel landed inside the target's text box
Add-Type -AssemblyName System.Drawing
Add-Type -AssemblyName System.Windows.Forms
Add-Type @'
using System;
using System.Runtime.InteropServices;
public class E2E {
  [StructLayout(LayoutKind.Sequential)] public struct RECT { public int Left, Top, Right, Bottom; }
  [DllImport("user32.dll")] public static extern bool GetWindowRect(IntPtr h, out RECT r);
  [DllImport("user32.dll")] public static extern bool SetForegroundWindow(IntPtr h);
  [DllImport("user32.dll")] public static extern bool ShowWindow(IntPtr h, int c);
  [DllImport("user32.dll")] public static extern IntPtr GetForegroundWindow();
  [DllImport("user32.dll")] public static extern bool SetCursorPos(int x, int y);
  [DllImport("user32.dll")] public static extern void mouse_event(uint f, uint dx, uint dy, uint d, IntPtr e);
}
'@

$root = 'E:\default\clipnest'
$pwshPath = (Get-Process -Id $PID).Path
$targetFile = Join-Path $env:TEMP 'clipnest-target.txt'
$sentinel = "E2E-SENTINEL-$([guid]::NewGuid().ToString('N').Substring(0,8))"

function Wait-For([scriptblock]$cond, [int]$timeoutMs = 8000) {
  $sw = [Diagnostics.Stopwatch]::StartNew()
  while ($sw.ElapsedMilliseconds -lt $timeoutMs) {
    if (& $cond) { return $true }
    Start-Sleep -Milliseconds 150
  }
  return $false
}

# 1. ClipNest must be running.
if (-not (Get-Process clipnest -ErrorAction SilentlyContinue)) {
  Start-Process -FilePath (Join-Path $root 'bin\clipnest.exe') | Out-Null
  Start-Sleep -Seconds 4
}
Write-Host "clipnest: $((Get-Process clipnest | Select-Object -First 1).Id)"

# 2. Paste target.
if (-not (Get-Process powershell -ErrorAction SilentlyContinue | Where-Object { $_.MainWindowTitle -eq 'ClipNest paste target' })) {
  Start-Process -FilePath $pwshPath -ArgumentList '-STA','-NoProfile','-File',(Join-Path $root 'scratch\paste-target.ps1') | Out-Null
}
$target = $null
$deadline = (Get-Date).AddSeconds(20)
while ((Get-Date) -lt $deadline) {
  $cand = Get-Process | Where-Object { $_.MainWindowTitle -eq 'ClipNest paste target' } | Select-Object -First 1
  if ($cand) {
    $cand.Refresh()
    if ($cand.MainWindowHandle -ne 0) { $target = $cand; break }
  }
  Start-Sleep -Milliseconds 200
}
if (-not $target) { throw 'paste target window never appeared' }
Write-Host "target hwnd=$($target.MainWindowHandle)"
[E2E]::SetForegroundWindow($target.MainWindowHandle) | Out-Null
Start-Sleep -Milliseconds 600

# 3. Sentinel on the clipboard → newest history entry.
Set-Clipboard -Value $sentinel
Start-Sleep -Milliseconds 900

# 4. Fire the global hotkey (Ctrl+Shift+V).
[E2E]::SetForegroundWindow($target.MainWindowHandle) | Out-Null
Start-Sleep -Milliseconds 400
[System.Windows.Forms.SendKeys]::SendWait('^+v')
Start-Sleep -Milliseconds 1200

# Screenshot whatever is on screen now (the popup should sit at the caret).
$screen = [System.Windows.Forms.Screen]::PrimaryScreen.Bounds
$bmp = New-Object System.Drawing.Bitmap $screen.Width, $screen.Height
$g = [System.Drawing.Graphics]::FromImage($bmp)
$g.CopyFromScreen($screen.X, $screen.Y, 0, 0, $bmp.Size)
$g.Dispose()
$shot = Join-Path $root 'scratch\shot-picker.png'
$bmp.Save($shot, [System.Drawing.Imaging.ImageFormat]::Png)
$bmp.Dispose()
Write-Host "picker shot: $shot"

# 5. Enter picks the active (top) entry and pastes it.
[System.Windows.Forms.SendKeys]::SendWait('{ENTER}')
Start-Sleep -Milliseconds 1400

$got = (Get-Content -Path $targetFile -Raw -ErrorAction SilentlyContinue)
Write-Host "target content: [$($got -replace "`r?`n", '\n')]"
if ($got -and $got.Contains($sentinel)) {
  Write-Host 'E2E-RESULT: PASS — 粘贴落到了目标窗口'
} else {
  Write-Host 'E2E-RESULT: FAIL — 目标窗口没有收到内容'
}

# Leave the target running so the next test can reuse it; kill it on request.
if ($env:E2E_KEEP_TARGET -ne '1') {
  Get-Process | Where-Object { $_.MainWindowTitle -eq 'ClipNest paste target' } | Stop-Process -Force
}
