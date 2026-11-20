# 2.8.3 冒烟：沙箱 APPDATA 起一次开发构建，截一张主窗、看日志有没有报错。
#
# 用法（必须 -STA）：pwsh -STA -NoProfile -File scratch\smoke-2.8.3.ps1
Add-Type -AssemblyName System.Drawing
Add-Type @'
using System;
using System.Runtime.InteropServices;
public class SmokeShot {
  [StructLayout(LayoutKind.Sequential)] public struct RECT { public int Left, Top, Right, Bottom; }
  [DllImport("user32.dll")] public static extern bool GetWindowRect(IntPtr h, out RECT r);
  [DllImport("user32.dll")] public static extern bool PrintWindow(IntPtr h, IntPtr dc, uint flags);
}
'@

$root = 'E:\default\clipnest'
$sandbox = Join-Path $root 'scratch\appdata-smoke'
$exe = Join-Path $root 'bin\clipnest-dev.exe'
$shots = Join-Path $root 'scratch\e2e-shots'
$realAppData = $env:APPDATA
New-Item -ItemType Directory -Path $shots -Force | Out-Null

# 安装版会抢单实例，先停掉
Get-Process clipnest -ErrorAction SilentlyContinue | Stop-Process -Force
Get-Process clipnest-dev -ErrorAction SilentlyContinue | Stop-Process -Force
Start-Sleep -Milliseconds 800

Get-ChildItem $sandbox -Recurse -Force -ErrorAction SilentlyContinue | Remove-Item -Recurse -Force -ErrorAction SilentlyContinue
$cfg = Join-Path $sandbox 'ClipNest\settings.json'
New-Item -ItemType Directory -Path (Split-Path $cfg) -Force | Out-Null
@{ capture = $true; syncEnabled = $false; showOnLaunch = $true } | ConvertTo-Json | Set-Content -Path $cfg -Encoding UTF8

$env:APPDATA = $sandbox
$p = Start-Process -FilePath $exe -ArgumentList '--show' -PassThru
Start-Sleep -Seconds 5

if (-not $p.HasExited) {
  $r = New-Object SmokeShot+RECT
  [SmokeShot]::GetWindowRect($p.MainWindowHandle, [ref]$r) | Out-Null
  $w = $r.Right - $r.Left; $h = $r.Bottom - $r.Top
  if ($w -gt 100 -and $h -gt 100) {
    $bmp = New-Object System.Drawing.Bitmap $w, $h
    $g = [System.Drawing.Graphics]::FromImage($bmp)
    $hdc = $g.GetHdc()
    [SmokeShot]::PrintWindow($p.MainWindowHandle, $hdc, 2) | Out-Null
    $g.ReleaseHdc($hdc); $g.Dispose()
    $shot = Join-Path $shots 'smoke-2.8.3-main.png'
    $bmp.Save($shot, [System.Drawing.Imaging.ImageFormat]::Png); $bmp.Dispose()
    "截图：$shot"
  } else {
    "窗口尺寸异常：${w}x$h"
  }
} else {
  "进程已退出，退出码 $($p.ExitCode)"
}

$log = Join-Path $sandbox 'ClipNest\clipnest.log'
if (Test-Path $log) {
  "--- 日志（最后 15 行）---"
  Get-Content $log | Select-Object -Last 15
  "--- 含 error/warn 的行 ---"
  Select-String -Path $log -Pattern 'error|warn|failed' -SimpleMatch | ForEach-Object { $_.Line }
}

$db = Join-Path $sandbox 'ClipNest\clipnest.db'
"数据库：$(if (Test-Path $db) { '已创建 ' + (Get-Item $db).Length + ' B' } else { '缺失' })"

Get-Process clipnest-dev -ErrorAction SilentlyContinue | Stop-Process -Force
$env:APPDATA = $realAppData
