# 侧栏「同时选中两个」回归检查：起一个沙箱实例（不碰真实数据），
# 截图侧栏 → 点「待办」→ 再截侧栏，看是否只剩一行高亮。
#
# 和 e2e-global-composer.ps1 一样：先停掉用户正在跑的安装版（否则
# SingleInstance 会让沙箱实例直接退出），收尾再把安装版拉起来。
#
# 用法（必须 -STA）：pwsh -STA -NoProfile -File scratch\check-sidebar-active.ps1
Add-Type -AssemblyName System.Drawing
Add-Type -AssemblyName System.Windows.Forms
Add-Type @'
using System;
using System.Runtime.InteropServices;
public class SideChk {
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
$sandbox = Join-Path $root 'scratch\appdata-sidebar-check'
$devExe = Join-Path $root 'bin\clipnest-dev.exe'
$installedExe = 'C:\Program Files\bbqisbbq\ClipNest\clipnest.exe'
$shots = Join-Path $root 'scratch\e2e-shots'
$realAppData = $env:APPDATA
New-Item -ItemType Directory -Path $shots -Force | Out-Null

function Get-Rect($p) {
  $r = New-Object SideChk+RECT
  [SideChk]::GetWindowRect($p.MainWindowHandle, [ref]$r) | Out-Null
  return $r
}

# 只截侧栏那一条（宽 218，标题栏下沿开始）
function ShotSide($p, $name) {
  $w = 218; $h = 430
  $bmp = New-Object System.Drawing.Bitmap $w, $h
  $g = [System.Drawing.Graphics]::FromImage($bmp)
  $hdc = $g.GetHdc()
  [SideChk]::PrintWindow($p.MainWindowHandle, $hdc, 2) | Out-Null
  $g.ReleaseHdc($hdc); $g.Dispose()
  $cropH = $h - 40
  $crop = New-Object System.Drawing.Bitmap $w, $cropH
  $g2 = [System.Drawing.Graphics]::FromImage($crop)
  $g2.DrawImage($bmp, (New-Object System.Drawing.Rectangle 0, 0, $w, $cropH), (New-Object System.Drawing.Rectangle 0, 40, $w, $cropH), [System.Drawing.GraphicsUnit]::Pixel)
  $g2.Dispose()
  $path = Join-Path $shots "$name.png"
  $crop.Save($path, [System.Drawing.Imaging.ImageFormat]::Png)
  $bmp.Dispose(); $crop.Dispose()
  return $path
}

function Click($p, [int]$relX, [int]$relY) {
  $r = Get-Rect $p
  [SideChk]::SetForegroundWindow($p.MainWindowHandle) | Out-Null
  Start-Sleep -Milliseconds 250
  [SideChk]::SetCursorPos($r.Left + $relX, $r.Top + $relY) | Out-Null
  Start-Sleep -Milliseconds 150
  [SideChk]::mouse_event([SideChk]::LEFTDOWN, 0, 0, 0, [IntPtr]::Zero)
  Start-Sleep -Milliseconds 60
  [SideChk]::mouse_event([SideChk]::LEFTUP, 0, 0, 0, [IntPtr]::Zero)
  Start-Sleep -Milliseconds 700
}

function Stop-Sandbox {
  Get-Process clipnest-dev -ErrorAction SilentlyContinue | Stop-Process -Force
  Start-Sleep -Milliseconds 900
}

function Start-Sandbox {
  if (-not $args.Contains('-KeepData')) {
    Remove-Item $sandbox -Recurse -Force -ErrorAction SilentlyContinue
    New-Item -ItemType Directory -Path $sandbox -Force | Out-Null
  }
  $env:APPDATA = $sandbox
  Start-Process -FilePath $devExe -ArgumentList '--show' | Out-Null
  $deadline = (Get-Date).AddSeconds(25)
  while ((Get-Date) -lt $deadline) {
    $c = Get-Process clipnest-dev -ErrorAction SilentlyContinue | Select-Object -First 1
    if ($c) { $c.Refresh(); if ($c.MainWindowHandle -ne 0) { Start-Sleep -Milliseconds 1800; return $c } }
    Start-Sleep -Milliseconds 250
  }
  throw 'sandbox 窗口没起来'
}

try {
  Get-Process clipnest -ErrorAction SilentlyContinue | Stop-Process -Force
  Start-Sleep -Milliseconds 900

  $p = Start-Sandbox
  "shot 1: " + (ShotSide $p 'side-01-entries')
  Click $p 45 86                       # 「待办」
  "shot 2: " + (ShotSide $p 'side-02-todo')
  Click $p 50 122                      # 「全部内容」：从待办点回内容
  "shot 3: " + (ShotSide $p 'side-03-back-to-all')
}
finally {
  Stop-Sandbox
  $env:APPDATA = $realAppData
  Start-Process -FilePath $installedExe -ArgumentList '--show' | Out-Null
  Start-Sleep -Milliseconds 1200
  Get-Process clipnest -ErrorAction SilentlyContinue | Select-Object Id,ProcessName | Format-Table -AutoSize | Out-String
}
