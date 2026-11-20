# 主题巡检：同一份沙箱数据，按 3 个预设各起一次，截整窗对比
# （看弱化文字的对比、类型 chip 的底色是否跟着主题、logo 方块上的字色）。
#
# 用法（必须 -STA）：pwsh -STA -NoProfile -File scratch\e2e-theme-shots.ps1
Add-Type -AssemblyName System.Drawing
Add-Type @'
using System;
using System.Runtime.InteropServices;
public class ThShot {
  [StructLayout(LayoutKind.Sequential)] public struct RECT { public int Left, Top, Right, Bottom; }
  [DllImport("user32.dll")] public static extern bool GetWindowRect(IntPtr h, out RECT r);
  [DllImport("user32.dll")] public static extern bool PrintWindow(IntPtr h, IntPtr dc, uint flags);
  public const uint LEFTDOWN = 0x0002;
}
'@

$root = 'E:\default\clipnest'
$sandbox = Join-Path $root 'scratch\appdata-theme-shots'
$devExe = Join-Path $root 'bin\clipnest-dev.exe'
$installedExe = 'C:\Program Files\bbqisbbq\ClipNest\clipnest.exe'
$shots = Join-Path $root 'scratch\e2e-shots'
$realAppData = $env:APPDATA
New-Item -ItemType Directory -Path $shots -Force | Out-Null

function Shot($p, $name) {
  $r = New-Object ThShot+RECT
  [ThShot]::GetWindowRect($p.MainWindowHandle, [ref]$r) | Out-Null
  $bmp = New-Object System.Drawing.Bitmap ($r.Right - $r.Left), ($r.Bottom - $r.Top)
  $g = [System.Drawing.Graphics]::FromImage($bmp)
  $hdc = $g.GetHdc()
  [ThShot]::PrintWindow($p.MainWindowHandle, $hdc, 2) | Out-Null
  $g.ReleaseHdc($hdc); $g.Dispose()
  $path = Join-Path $shots "$name.png"
  $bmp.Save($path, [System.Drawing.Imaging.ImageFormat]::Png); $bmp.Dispose()
  return $path
}

function Run-Preset([string]$preset, [string]$scheme) {
  Get-Process clipnest-dev -ErrorAction SilentlyContinue | Stop-Process -Force
  Start-Sleep -Milliseconds 900
  $cfg = Join-Path $sandbox 'ClipNest\settings.json'
  New-Item -ItemType Directory -Path (Split-Path $cfg) -Force | Out-Null
  $json = @{ themePreset = $preset; theme = $scheme; capture = $true; syncEnabled = $false } | ConvertTo-Json
  Set-Content -Path $cfg -Value $json -Encoding UTF8
  $env:APPDATA = $sandbox
  Start-Process -FilePath $devExe -ArgumentList '--show' | Out-Null
  $deadline = (Get-Date).AddSeconds(25)
  $p = $null
  while ((Get-Date) -lt $deadline) {
    $c = Get-Process clipnest-dev -ErrorAction SilentlyContinue | Select-Object -First 1
    if ($c) { $c.Refresh(); if ($c.MainWindowHandle -ne 0) { $p = $c; break } }
    Start-Sleep -Milliseconds 250
  }
  if (-not $p) { throw "窗口没起来（$preset）" }
  Start-Sleep -Milliseconds 4500
  Shot $p "theme-$preset"
}

try {
  Get-Process clipnest -ErrorAction SilentlyContinue | Stop-Process -Force
  Start-Sleep -Milliseconds 900
  Remove-Item $sandbox -Recurse -Force -ErrorAction SilentlyContinue
  New-Item -ItemType Directory -Path $sandbox -Force | Out-Null

  # 先灌几条不同类型的内容，chip 颜色才看得出来
  Run-Preset 'default-dark' 'dark'
  Set-Clipboard -Value 'https://example.com/theme-audit'
  Start-Sleep -Milliseconds 800
  Set-Clipboard -Value '- [ ] 主题巡检用的待办一条'
  Start-Sleep -Milliseconds 800
  Run-Preset 'default-light' 'light'
  Run-Preset 'paper' 'light'
  Run-Preset 'ink' 'dark'
}
finally {
  Get-Process clipnest-dev -ErrorAction SilentlyContinue | Stop-Process -Force
  Start-Sleep -Milliseconds 800
  $env:APPDATA = $realAppData
  Start-Process -FilePath $installedExe -ArgumentList '--show' | Out-Null
  Start-Sleep -Milliseconds 1000
}
