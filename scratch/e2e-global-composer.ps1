# End-to-end check of 2.8.0 的两件事：
#   1. 底部输入框是全局的：换到待办、设置、再换回来，它的内容都不变；
#   2. 草稿落在库里：打字 → 检查 meta.composer.draft；重启应用 → 内容还在。
#
# 做法：用沙箱 APPDATA 起一个干净实例（不碰真实数据），真实鼠标点击侧栏，
# 每步 PrintWindow 截图，末尾恢复用户原本的安装版。
#
# 用法（必须 -STA）：pwsh -STA -NoProfile -File scratch\e2e-global-composer.ps1
Add-Type -AssemblyName System.Drawing
Add-Type -AssemblyName System.Windows.Forms
Add-Type @'
using System;
using System.Runtime.InteropServices;
public class E2EG {
  [StructLayout(LayoutKind.Sequential)] public struct RECT { public int Left, Top, Right, Bottom; }
  [DllImport("user32.dll")] public static extern bool GetWindowRect(IntPtr h, out RECT r);
  [DllImport("user32.dll")] public static extern bool PrintWindow(IntPtr h, IntPtr dc, uint flags);
  [DllImport("user32.dll")] public static extern bool SetForegroundWindow(IntPtr h);
  [DllImport("user32.dll")] public static extern bool ShowWindow(IntPtr h, int c);
  [DllImport("user32.dll")] public static extern IntPtr GetForegroundWindow();
  [DllImport("user32.dll")] public static extern bool SetCursorPos(int x, int y);
  [DllImport("user32.dll")] public static extern void mouse_event(uint f, uint dx, uint dy, uint d, IntPtr e);
  public const uint LEFTDOWN = 0x0002, LEFTUP = 0x0004;
}
'@

$root = 'E:\default\clipnest'
$sandbox = Join-Path $root 'scratch\appdata-e2e-composer'
$devExe = Join-Path $root 'bin\clipnest-dev.exe'
$installedExe = 'C:\Program Files\bbqisbbq\ClipNest\clipnest.exe'
$shots = Join-Path $root 'scratch\e2e-shots'
# 用户真实的 APPDATA（沙箱会把它改掉，收尾要还回去，否则恢复的安装版会去读沙箱数据）
$realAppData = $env:APPDATA
New-Item -ItemType Directory -Path $shots -Force | Out-Null

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
    if ($c) { $c.Refresh(); if ($c.MainWindowHandle -ne 0) { Start-Sleep -Milliseconds 1800; return $c } }
    Start-Sleep -Milliseconds 250
  }
  throw 'sandbox 窗口没起来'
}

function Stop-Sandbox {
  Get-Process clipnest-dev -ErrorAction SilentlyContinue | Stop-Process -Force
  Start-Sleep -Milliseconds 900
}

function Get-Rect($p) {
  $r = New-Object E2EG+RECT
  [E2EG]::GetWindowRect($p.MainWindowHandle, [ref]$r) | Out-Null
  return $r
}

function Shot($p, $name) {
  $r = Get-Rect $p
  $bmp = New-Object System.Drawing.Bitmap ($r.Right - $r.Left), ($r.Bottom - $r.Top)
  $g = [System.Drawing.Graphics]::FromImage($bmp)
  $hdc = $g.GetHdc()
  [E2EG]::PrintWindow($p.MainWindowHandle, $hdc, 2) | Out-Null
  $g.ReleaseHdc($hdc); $g.Dispose()
  $path = Join-Path $shots "$name.png"
  $bmp.Save($path, [System.Drawing.Imaging.ImageFormat]::Png); $bmp.Dispose()
  return $path
}

function Click($p, [int]$relX, [int]$relY) {
  $r = Get-Rect $p
  [E2EG]::SetForegroundWindow($p.MainWindowHandle) | Out-Null
  Start-Sleep -Milliseconds 250
  [E2EG]::SetCursorPos($r.Left + $relX, $r.Top + $relY) | Out-Null
  Start-Sleep -Milliseconds 150
  [E2EG]::mouse_event([E2EG]::LEFTDOWN, 0, 0, 0, [IntPtr]::Zero)
  Start-Sleep -Milliseconds 60
  [E2EG]::mouse_event([E2EG]::LEFTUP, 0, 0, 0, [IntPtr]::Zero)
  Start-Sleep -Milliseconds 700
}

function Meta {
  & (Join-Path $root 'bin\metadump.exe')
}

$result = @{}
try {
  Get-Process clipnest -ErrorAction SilentlyContinue | Stop-Process -Force
  Start-Sleep -Milliseconds 900

  $p = Start-Sandbox
  Shot $p '0-start' | Out-Null

  # 1. 点输入区并打字
  Click $p 600 620
  [System.Windows.Forms.SendKeys]::SendWait('E2E-DRAFT-A1')
  Start-Sleep -Milliseconds 1400
  Shot $p '1-typed' | Out-Null
  $meta1 = Meta
  $result['draft_in_db'] = ($meta1 -match 'E2E-DRAFT-A1')

  # 2. 切到待办
  Click $p 45 92
  Shot $p '2-todo' | Out-Null

  # 3. 切到设置
  Click $p 40 715
  Shot $p '3-settings' | Out-Null

  # 4. 从设置返回（点设置页左上「← 返回」）
  Click $p 60 60
  Shot $p '4-back' | Out-Null

  # 5. 回全部内容
  Click $p 50 126
  Shot $p '5-entries' | Out-Null

  # 6. 重启：草稿应该还在（-KeepData 才能验证这一点：别把库擦掉）
  Stop-Sandbox
  $p = Start-Sandbox -KeepData
  Shot $p '6-restarted' | Out-Null
  $result['meta_after_restart'] = (Meta) -join ' | '

  $result['window'] = "hwnd=$($p.MainWindowHandle)"
}
finally {
  Stop-Sandbox
  $env:APPDATA = $realAppData
  Start-Process -FilePath $installedExe -ArgumentList '--show' | Out-Null
}

$result.GetEnumerator() | ForEach-Object { "{0}: {1}" -f $_.Key, $_.Value }
"screenshots: $shots"
