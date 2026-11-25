param([string]$Exe = '')
# 两件事一起验（沙箱 APPDATA + 真实鼠标键盘）：
#   1. 复制内容后，列表**不切页面**就能看到新条目（App 监听 clip:new）；
#   2. 快捷输入框草稿：敲完 → 记录 → 强杀进程 / 点× 收起 → 重启后草稿还在。
#
# 用法（必须 -STA）：pwsh -STA -NoProfile -File scratch\e2e-live-and-draft.ps1
Add-Type -AssemblyName System.Drawing
Add-Type -AssemblyName System.Windows.Forms
Add-Type @'
using System;
using System.Runtime.InteropServices;
public class LiveDr {
  [StructLayout(LayoutKind.Sequential)] public struct RECT { public int Left, Top, Right, Bottom; }
  [DllImport("user32.dll")] public static extern bool GetWindowRect(IntPtr h, out RECT r);
  [DllImport("user32.dll")] public static extern bool PrintWindow(IntPtr h, IntPtr dc, uint flags);
  [DllImport("user32.dll")] public static extern bool SetForegroundWindow(IntPtr h);
  [DllImport("user32.dll")] public static extern bool SetCursorPos(int x, int y);
  [DllImport("user32.dll")] public static extern void mouse_event(uint f, uint dx, uint dy, uint d, IntPtr e);
  public const uint LEFTDOWN = 0x0002, LEFTUP = 0x0004;
}
'@

# 闲置销毁会把这些脚本正拿着的窗口收掉（产品默认行为），测试期间关掉。
$env:CLIPNEST_IDLE_DESTROY = '0'
$root = 'E:\default\clipnest'
$sandbox = Join-Path $root 'scratch\appdata-live-draft'
$devExe = if ($Exe) { $Exe } else { Join-Path $root 'bin\clipnest-dev.exe' }
$procName = [IO.Path]::GetFileNameWithoutExtension($devExe)
$installedExe = 'C:\Program Files\bbqisbbq\ClipNest\clipnest.exe'
$shots = Join-Path $root 'scratch\e2e-shots'
$realAppData = $env:APPDATA
New-Item -ItemType Directory -Path $shots -Force | Out-Null

function Get-Rect($p) {
  $r = New-Object LiveDr+RECT
  [LiveDr]::GetWindowRect($p.MainWindowHandle, [ref]$r) | Out-Null
  return $r
}

function Shot($p, $name) {
  $r = Get-Rect $p
  $bmp = New-Object System.Drawing.Bitmap ($r.Right - $r.Left), ($r.Bottom - $r.Top)
  $g = [System.Drawing.Graphics]::FromImage($bmp)
  $hdc = $g.GetHdc()
  [LiveDr]::PrintWindow($p.MainWindowHandle, $hdc, 2) | Out-Null
  $g.ReleaseHdc($hdc); $g.Dispose()
  $path = Join-Path $shots "$name.png"
  $bmp.Save($path, [System.Drawing.Imaging.ImageFormat]::Png); $bmp.Dispose()
  return $path
}

function Click($p, [int]$relX, [int]$relY) {
  $r = Get-Rect $p
  [LiveDr]::SetForegroundWindow($p.MainWindowHandle) | Out-Null
  Start-Sleep -Milliseconds 250
  [LiveDr]::SetCursorPos($r.Left + $relX, $r.Top + $relY) | Out-Null
  Start-Sleep -Milliseconds 120
  [LiveDr]::mouse_event([LiveDr]::LEFTDOWN, 0, 0, 0, [IntPtr]::Zero)
  Start-Sleep -Milliseconds 60
  [LiveDr]::mouse_event([LiveDr]::LEFTUP, 0, 0, 0, [IntPtr]::Zero)
  Start-Sleep -Milliseconds 500
}

function Type-Text($text) {
  [System.Windows.Forms.SendKeys]::SendWait($text)
}

function Stop-Sandbox {
  Get-Process $procName -ErrorAction SilentlyContinue | Stop-Process -Force
  Start-Sleep -Milliseconds 900
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
    $c = Get-Process $procName -ErrorAction SilentlyContinue | Select-Object -First 1
    if ($c) { $c.Refresh(); if ($c.MainWindowHandle -ne 0) { Start-Sleep -Milliseconds 5000; return $c } }
    Start-Sleep -Milliseconds 250
  }
  throw 'sandbox 窗口没起来'
}

function Dump-Draft($label) {
  $copy = Join-Path $root 'scratch\draft-snap'
  Remove-Item $copy -Recurse -Force -ErrorAction SilentlyContinue
  New-Item -ItemType Directory -Path $copy -Force | Out-Null
  Copy-Item (Join-Path $sandbox 'ClipNest\clipnest.db*') $copy -Force
  "--- $label ---"
  Push-Location $root
  & go run ./scratch/dbdraft $copy 2>&1 | Select-Object -Last 3
  Pop-Location
}

try {
  Get-Process clipnest -ErrorAction SilentlyContinue | Stop-Process -Force
  Start-Sleep -Milliseconds 900

  # ---- 阶段 1：敲草稿 → 1.2s 后强杀（模拟崩溃），草稿应该已经落库 ----
  $p = Start-Sandbox
  Shot $p 'live-1-composer-empty' | Out-Null
  Click $p 600 620
  Type-Text 'draft-e2e-alpha'
  Start-Sleep -Milliseconds 1200
  Stop-Sandbox
  Dump-Draft '阶段1：敲完 1.2s 后强杀'

  # ---- 阶段 2：重启，草稿应该回到编辑器里 ----
  $p = Start-Sandbox -KeepData
  "shot 2: " + (Shot $p 'live-2-draft-restored')

  # ---- 阶段 3：再敲 → 立刻点×收起窗口 → 强杀；草稿应该也在 ----
  Click $p 600 620
  Type-Text ' plus-beta'
  Start-Sleep -Milliseconds 120
  Click $p 1095 18            # 标题栏 ×（= 收起到托盘）
  Start-Sleep -Milliseconds 900
  Stop-Sandbox
  Dump-Draft '阶段3：敲完 0.15s 点×，再强杀'

  # ---- 阶段 4：重启，看草稿是否完整；顺便验"复制完立刻出现" ----
  $p = Start-Sandbox -KeepData
  "shot 4: " + (Shot $p 'live-3-restored2')
  Set-Clipboard -Value 'live-refresh-e2e-123'
  Start-Sleep -Milliseconds 2500
  "shot 5: " + (Shot $p 'live-4-after-copy')
  Dump-Draft '阶段4：复制一条之后'
}
finally {
  Stop-Sandbox
  $env:APPDATA = $realAppData
  Start-Process -FilePath $installedExe -ArgumentList '--show' | Out-Null
  Start-Sleep -Milliseconds 1200
}



