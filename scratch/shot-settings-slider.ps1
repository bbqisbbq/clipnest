# 设置页里「记一笔」弹窗透明滑块的样子（视觉检查用）。
#   起 dev 版（沙箱 APPDATA）→ 点侧栏「设置」→ 滚到「交互与启动」→ 截图。
# 用法：pwsh -STA -NoProfile -File scratch\shot-settings-slider.ps1
Add-Type -AssemblyName System.Drawing
Add-Type -AssemblyName System.Windows.Forms
Add-Type @'
using System;
using System.Runtime.InteropServices;
public class SW {
  [StructLayout(LayoutKind.Sequential)] public struct RECT { public int Left, Top, Right, Bottom; }
  [DllImport("user32.dll")] public static extern bool GetWindowRect(IntPtr h, out RECT r);
  [DllImport("user32.dll")] public static extern bool SetForegroundWindow(IntPtr h);
  [DllImport("user32.dll")] public static extern bool SetCursorPos(int x, int y);
  [DllImport("user32.dll")] public static extern void mouse_event(uint f, uint dx, uint dy, uint d, IntPtr e);
}
'@

$root = 'E:\default\clipnest'
$sandbox = Join-Path $root 'scratch\tmp-profile-slider-shot\APPDATA'
$dataDir = Join-Path $sandbox 'ClipNest'
$out = Join-Path $root 'scratch\slider-shot.png'

Get-Process | Where-Object { $_.ProcessName -like 'clipnest*' } | ForEach-Object { try { $_.Kill(); $_.WaitForExit(3000) | Out-Null } catch {} }
Start-Sleep -Milliseconds 1200
New-Item -ItemType Directory -Force -Path $dataDir | Out-Null
@{ capture = $true; updateCheck = $false; hotkey = ''; captureHotkey = ''; showOnLaunch = $true
   autoStart = $true; themePreset = 'default-dark'; theme = 'dark'; captureTransparency = 40 } |
  ConvertTo-Json | Set-Content (Join-Path $dataDir 'settings.json') -Encoding utf8

$env:APPDATA = $sandbox
Start-Process -FilePath (Join-Path $root 'bin\clipnest-dev.exe') -ArgumentList '--show' | Out-Null
$deadline = (Get-Date).AddSeconds(25)
$hwnd = [IntPtr]::Zero
while ((Get-Date) -lt $deadline) {
  $c = Get-Process clipnest-dev -ErrorAction SilentlyContinue | Select-Object -First 1
  if ($c) { $c.Refresh(); if ($c.MainWindowHandle -ne 0) { $hwnd = $c.MainWindowHandle; break } }
  Start-Sleep -Milliseconds 250
}
if ($hwnd -eq [IntPtr]::Zero) { throw 'dev 版窗口没起来' }
[SW]::SetForegroundWindow($hwnd) | Out-Null
Start-Sleep -Milliseconds 900

$r = New-Object SW+RECT
[SW]::GetWindowRect($hwnd, [ref]$r) | Out-Null
$left = $r.Left; $top = $r.Top

function Click([int]$x, [int]$y) {
  [SW]::SetCursorPos($x, $y) | Out-Null
  Start-Sleep -Milliseconds 180
  [SW]::mouse_event(0x0002, 0, 0, 0, [IntPtr]::Zero)
  Start-Sleep -Milliseconds 70
  [SW]::mouse_event(0x0004, 0, 0, 0, [IntPtr]::Zero)
  Start-Sleep -Milliseconds 900
}
function Wheel([int]$x, [int]$y, [int]$notches) {
  [SW]::SetCursorPos($x, $y) | Out-Null
  Start-Sleep -Milliseconds 150
  for ($i = 0; $i -lt [Math]::Abs($notches); $i++) {
    $delta = if ($notches -gt 0) { [uint32]120 } else { [uint32]4294967176 }  # 4294967176 = -120 的补码
    [SW]::mouse_event(0x0800, 0, 0, $delta, [IntPtr]::Zero)
    Start-Sleep -Milliseconds 90
  }
  Start-Sleep -Milliseconds 500
}

# 侧栏「设置」（1120x740 窗口里在左下角）
Click ($left + 64) ($top + 710)
Start-Sleep -Milliseconds 700
# 内容区往下滚到「交互与启动」
Wheel ($left + 672) ($top + 300) -6
Start-Sleep -Milliseconds 600

$bmp = New-Object System.Drawing.Bitmap ($r.Right - $r.Left), ($r.Bottom - $r.Top)
$g = [System.Drawing.Graphics]::FromImage($bmp)
$g.CopyFromScreen($left, $top, 0, 0, $bmp.Size)
$g.Dispose()
$bmp.Save($out, [System.Drawing.Imaging.ImageFormat]::Png)
$bmp.Dispose()
Write-Host "shot: $out"

Get-Process | Where-Object { $_.ProcessName -like 'clipnest*' } | ForEach-Object { try { $_.Kill() } catch {} }

