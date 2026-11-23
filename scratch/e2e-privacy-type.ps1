# 隐私模式 e2e（真正打字那条路）：空草稿 → 点进输入框 → 敲三段 → 看是不是只剩最后一段清楚。
#
# 用法（必须 -STA）：pwsh -STA -NoProfile -File scratch\e2e-privacy-type.ps1
# 只敲 ASCII（A-Z / 空格 / 回车），不碰输入法 —— 这是唯一稳的自动化路径。
Add-Type -AssemblyName System.Drawing
Add-Type @'
using System; using System.Runtime.InteropServices;
public class E2EType {
  [StructLayout(LayoutKind.Sequential)] public struct RECT { public int Left, Top, Right, Bottom; }
  [DllImport("user32.dll")] public static extern bool GetWindowRect(IntPtr h, out RECT r);
  [DllImport("user32.dll")] public static extern bool PrintWindow(IntPtr h, IntPtr dc, uint flags);
  [DllImport("user32.dll")] public static extern bool SetForegroundWindow(IntPtr h);
  [DllImport("user32.dll")] public static extern bool SetCursorPos(int x, int y);
  [DllImport("user32.dll")] public static extern void mouse_event(uint f, uint dx, uint dy, uint d, IntPtr e);
  [DllImport("user32.dll")] public static extern void keybd_event(byte vk, byte scan, uint flags, IntPtr extra);
  public const uint LEFTDOWN = 0x0002, LEFTUP = 0x0004, KEYUP = 0x0002;
}
'@

$root = 'E:\default\clipnest'
$sandbox = Join-Path $root 'scratch\appdata-privacy-type'
$devExe = Join-Path $root 'bin\clipnest-dev.exe'
$installedExe = 'C:\Program Files\bbqisbbq\ClipNest\clipnest.exe'
$shots = Join-Path $root 'scratch\privacy-shots-type'
$realAppData = $env:APPDATA
$dataDir = Join-Path $sandbox 'ClipNest'
New-Item -ItemType Directory -Path $shots -Force | Out-Null

function Get-Rect($p) {
  $r = New-Object E2EType+RECT
  [E2EType]::GetWindowRect($p.MainWindowHandle, [ref]$r) | Out-Null
  return $r
}
function Shot($p, $name) {
  $r = Get-Rect $p
  $bmp = New-Object System.Drawing.Bitmap ($r.Right - $r.Left), ($r.Bottom - $r.Top)
  $g = [System.Drawing.Graphics]::FromImage($bmp)
  $hdc = $g.GetHdc()
  [E2EType]::PrintWindow($p.MainWindowHandle, $hdc, 2) | Out-Null
  $g.ReleaseHdc($hdc); $g.Dispose()
  $path = Join-Path $shots "$name.png"
  $bmp.Save($path, [System.Drawing.Imaging.ImageFormat]::Png); $bmp.Dispose()
  return $path
}
function Click($p, [int]$relX, [int]$relY) {
  $r = Get-Rect $p
  [E2EType]::SetForegroundWindow($p.MainWindowHandle) | Out-Null
  Start-Sleep -Milliseconds 350
  [E2EType]::SetCursorPos($r.Left + $relX, $r.Top + $relY) | Out-Null
  Start-Sleep -Milliseconds 250
  [E2EType]::mouse_event([E2EType]::LEFTDOWN, 0, 0, 0, [IntPtr]::Zero)
  Start-Sleep -Milliseconds 80
  [E2EType]::mouse_event([E2EType]::LEFTUP, 0, 0, 0, [IntPtr]::Zero)
  Start-Sleep -Milliseconds 500
}
function Key([int]$vk) {
  [E2EType]::keybd_event([byte]$vk, 0, 0, [IntPtr]::Zero)
  Start-Sleep -Milliseconds 25
  [E2EType]::keybd_event([byte]$vk, 0, [E2EType]::KEYUP, [IntPtr]::Zero)
  Start-Sleep -Milliseconds 45
}
function TypeText([string]$s) {
  foreach ($ch in $s.ToCharArray()) {
    if ($ch -eq ' ') { Key 0x20; continue }
    $u = [int][char]([string]$ch).ToUpper()
    if ($u -ge 0x41 -and $u -le 0x5A) { Key $u; continue }
    if ($u -ge 0x30 -and $u -le 0x39) { Key $u; continue }
    # 只支持 ASCII 字母/数字/空格，其余跳过（自动化里不碰输入法，也不碰标点）
  }
}

$result = @{}
Push-Location $root
try {
  Get-Process clipnest -ErrorAction SilentlyContinue | Stop-Process -Force
  Start-Sleep -Milliseconds 900

  Remove-Item $sandbox -Recurse -Force -ErrorAction SilentlyContinue
  New-Item -ItemType Directory -Path $dataDir -Force | Out-Null

  $env:APPDATA = $sandbox
  Start-Process -FilePath $devExe -ArgumentList '--show' | Out-Null
  $p = $null
  $deadline = (Get-Date).AddSeconds(30)
  while ((Get-Date) -lt $deadline) {
    $c = Get-Process clipnest-dev -ErrorAction SilentlyContinue | Select-Object -First 1
    if ($c) { $c.Refresh(); if ($c.MainWindowHandle -ne 0) { $p = $c; Start-Sleep -Milliseconds 2600; break } }
    Start-Sleep -Milliseconds 250
  }
  if (-not $p) { throw 'sandbox 窗口没起来' }
  Get-Process clipnest-dev | Stop-Process -Force
  Start-Sleep -Milliseconds 1500

  # 打开隐私模式（开关本身的点击另有 e2e 验，这里只验遮罩行为）
  $path = Join-Path $dataDir 'settings.json'
  $j = Get-Content $path -Raw | ConvertFrom-Json
  $j | Add-Member -NotePropertyName privacyInput -NotePropertyValue $true -Force
  $j | ConvertTo-Json -Depth 12 | Set-Content $path -Encoding utf8

  Start-Process -FilePath $devExe -ArgumentList '--show' | Out-Null
  $deadline = (Get-Date).AddSeconds(30)
  $p = $null
  while ((Get-Date) -lt $deadline) {
    $c = Get-Process clipnest-dev -ErrorAction SilentlyContinue | Select-Object -First 1
    if ($c) { $c.Refresh(); if ($c.MainWindowHandle -ne 0) { $p = $c; Start-Sleep -Milliseconds 2600; break } }
    Start-Sleep -Milliseconds 250
  }
  if (-not $p) { throw 'sandbox 窗口没起来（第二轮）' }

  Click $p 600 665
  Start-Sleep -Milliseconds 700
  Shot $p '1-empty-focused' | Out-Null

  TypeText 'alpha line one'
  Key 0x0D
  TypeText 'bravo line two'
  Key 0x0D
  TypeText 'charlie line three'
  Start-Sleep -Milliseconds 1200
  Shot $p '2-three-lines' | Out-Null

  Key 0x0D
  TypeText 'delta line four'
  Start-Sleep -Milliseconds 1200
  Shot $p '3-four-lines' | Out-Null

  $result['typed'] = 'ok'
} finally {
  Get-Process clipnest-dev -ErrorAction SilentlyContinue | Stop-Process -Force
  Start-Sleep -Milliseconds 900
  $env:APPDATA = $realAppData
  Start-Process -FilePath $installedExe -ArgumentList '--background' | Out-Null
  Pop-Location
}

$result.GetEnumerator() | ForEach-Object { "{0}: {1}" -f $_.Key, $_.Value }
"screenshots: $shots"
