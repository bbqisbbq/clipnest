# 草稿健壮性验证：**敲完立刻收起窗口（点 ×）→ 200ms 后强杀**，草稿是否还在。
# 这是旧实现（防抖在浏览器里）最容易丢字的那条路径：400ms 静默期没跑完就没了。
# 现在后端在 WindowHide 时收到 app:hidden → 立刻 flush。
#
# 用法（必须 -STA）：pwsh -STA -NoProfile -File scratch\e2e-draft-robust.ps1
Add-Type -AssemblyName System.Drawing
Add-Type -AssemblyName System.Windows.Forms
Add-Type @'
using System;
using System.Runtime.InteropServices;
public class DrRo {
  [StructLayout(LayoutKind.Sequential)] public struct RECT { public int Left, Top, Right, Bottom; }
  [DllImport("user32.dll")] public static extern bool GetWindowRect(IntPtr h, out RECT r);
  [DllImport("user32.dll")] public static extern bool PrintWindow(IntPtr h, IntPtr dc, uint flags);
  [DllImport("user32.dll")] public static extern bool SetForegroundWindow(IntPtr h);
  [DllImport("user32.dll")] public static extern bool SetCursorPos(int x, int y);
  [DllImport("user32.dll")] public static extern void mouse_event(uint f, uint dx, uint dy, uint d, IntPtr e);
  public const uint LEFTDOWN = 0x0002, LEFTUP = 0x0004;
}
'@

$root = 'E:\default\clipnest'
$sandbox = Join-Path $root 'scratch\appdata-draft-robust'
$devExe = Join-Path $root 'bin\clipnest-dev.exe'
$installedExe = 'C:\Program Files\bbqisbbq\ClipNest\clipnest.exe'
$shots = Join-Path $root 'scratch\e2e-shots'
$realAppData = $env:APPDATA

function Shot($p, $name) {
  $r = New-Object DrRo+RECT
  [DrRo]::GetWindowRect($p.MainWindowHandle, [ref]$r) | Out-Null
  $bmp = New-Object System.Drawing.Bitmap ($r.Right - $r.Left), ($r.Bottom - $r.Top)
  $g = [System.Drawing.Graphics]::FromImage($bmp)
  $hdc = $g.GetHdc()
  [DrRo]::PrintWindow($p.MainWindowHandle, $hdc, 2) | Out-Null
  $g.ReleaseHdc($hdc); $g.Dispose()
  $path = Join-Path $shots "$name.png"
  $bmp.Save($path, [System.Drawing.Imaging.ImageFormat]::Png); $bmp.Dispose()
  return $path
}

function Click($p, [int]$relX, [int]$relY) {
  [DrRo]::SetForegroundWindow($p.MainWindowHandle) | Out-Null
  Start-Sleep -Milliseconds 200
  $r = New-Object DrRo+RECT
  [DrRo]::GetWindowRect($p.MainWindowHandle, [ref]$r) | Out-Null
  [DrRo]::SetCursorPos($r.Left + $relX, $r.Top + $relY) | Out-Null
  Start-Sleep -Milliseconds 120
  [DrRo]::mouse_event([DrRo]::LEFTDOWN, 0, 0, 0, [IntPtr]::Zero)
  Start-Sleep -Milliseconds 60
  [DrRo]::mouse_event([DrRo]::LEFTUP, 0, 0, 0, [IntPtr]::Zero)
  Start-Sleep -Milliseconds 350
}

function Dump($label) {
  $copy = Join-Path $root 'scratch\draft-snap'
  Remove-Item $copy -Recurse -Force -ErrorAction SilentlyContinue
  New-Item -ItemType Directory -Path $copy -Force | Out-Null
  Copy-Item (Join-Path $sandbox 'ClipNest\clipnest.db*') $copy -Force
  "--- $label ---"
  Push-Location $root
  & go run ./scratch/dbdraft $copy 2>&1 | Select-Object -Last 3
  Pop-Location
}

function Start-Sandbox {
  param([switch]$KeepData)
  if (-not $KeepData) {
    Remove-Item $sandbox -Recurse -Force -ErrorAction SilentlyContinue
    New-Item -ItemType Directory -Path $sandbox -Force | Out-Null
  }
  $env:APPDATA = $sandbox
  Start-Process -FilePath $devExe -ArgumentList '--show' | Out-Null
  $deadline = (Get-Date).AddSeconds(25)
  while ((Get-Date) -lt $deadline) {
    $c = Get-Process clipnest-dev -ErrorAction SilentlyContinue | Select-Object -First 1
    if ($c) { $c.Refresh(); if ($c.MainWindowHandle -ne 0) { Start-Sleep -Milliseconds 5000; return $c } }
    Start-Sleep -Milliseconds 250
  }
  throw 'sandbox 窗口没起来'
}

try {
  Get-Process clipnest -ErrorAction SilentlyContinue | Stop-Process -Force
  Start-Sleep -Milliseconds 900

  $p = Start-Sandbox
  # 第一下点只激活窗口，第二下点才把光标放进编辑器
  Click $p 600 620
  Click $p 600 620
  [System.Windows.Forms.SendKeys]::SendWait('hide-flush-marker')
  # 敲完立刻收起窗口（不等 400ms 静默期）
  Click $p 1095 18
  Start-Sleep -Milliseconds 300
  Get-Process clipnest-dev -ErrorAction SilentlyContinue | Stop-Process -Force
  Start-Sleep -Milliseconds 700
  Dump '收起窗口后 300ms 强杀'

  $p = Start-Sandbox -KeepData
  "shot: " + (Shot $p 'draft-robust-1-restored')
  Dump '重启后'
}
finally {
  Get-Process clipnest-dev -ErrorAction SilentlyContinue | Stop-Process -Force
  Start-Sleep -Milliseconds 700
  $env:APPDATA = $realAppData
  Start-Process -FilePath $installedExe -ArgumentList '--show' | Out-Null
  Start-Sleep -Milliseconds 1000
}
