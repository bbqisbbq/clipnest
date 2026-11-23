# 验"回车之后立刻生效"：输入 aaa → 回车 → **不再打字** → 截图（上一行应当已变淡）。
#
# 用法（必须 -STA）：pwsh -STA -NoProfile -File scratch\e2e-privacy-enter.ps1
# 只敲 ASCII，不碰输入法；每步都等够，避免 Vditor 的异步重排还在跑。
Add-Type -AssemblyName System.Drawing
Add-Type @'
using System; using System.Runtime.InteropServices;
public class E2EEnter {
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
$sandbox = Join-Path $root 'scratch\appdata-privacy-enter'
$devExe = Join-Path $root 'bin\clipnest-dev.exe'
$installedExe = 'C:\Program Files\bbqisbbq\ClipNest\clipnest.exe'
$shots = Join-Path $root 'scratch\privacy-shots-enter'
$realAppData = $env:APPDATA
$dataDir = Join-Path $sandbox 'ClipNest'
New-Item -ItemType Directory -Path $shots -Force | Out-Null

function Get-Rect($p) {
  $r = New-Object E2EEnter+RECT
  [E2EEnter]::GetWindowRect($p.MainWindowHandle, [ref]$r) | Out-Null
  return $r
}
function Shot($p, $name) {
  $r = Get-Rect $p
  $bmp = New-Object System.Drawing.Bitmap ($r.Right - $r.Left), ($r.Bottom - $r.Top)
  $g = [System.Drawing.Graphics]::FromImage($bmp)
  $hdc = $g.GetHdc()
  [E2EEnter]::PrintWindow($p.MainWindowHandle, $hdc, 2) | Out-Null
  $g.ReleaseHdc($hdc); $g.Dispose()
  $path = Join-Path $shots "$name.png"
  $bmp.Save($path, [System.Drawing.Imaging.ImageFormat]::Png); $bmp.Dispose()
  return $path
}
function Click($p, [int]$relX, [int]$relY) {
  $r = Get-Rect $p
  [E2EEnter]::SetForegroundWindow($p.MainWindowHandle) | Out-Null
  Start-Sleep -Milliseconds 400
  [E2EEnter]::SetCursorPos($r.Left + $relX, $r.Top + $relY) | Out-Null
  Start-Sleep -Milliseconds 250
  [E2EEnter]::mouse_event([E2EEnter]::LEFTDOWN, 0, 0, 0, [IntPtr]::Zero)
  Start-Sleep -Milliseconds 80
  [E2EEnter]::mouse_event([E2EEnter]::LEFTUP, 0, 0, 0, [IntPtr]::Zero)
  Start-Sleep -Milliseconds 500
}
function Key([int]$vk) {
  [E2EEnter]::keybd_event([byte]$vk, 0, 0, [IntPtr]::Zero)
  Start-Sleep -Milliseconds 40
  [E2EEnter]::keybd_event([byte]$vk, 0, [E2EEnter]::KEYUP, [IntPtr]::Zero)
  Start-Sleep -Milliseconds 90
}
function TypeWord([string]$s) {
  foreach ($ch in $s.ToCharArray()) { Key ([int][char]([string]$ch).ToUpper()) }
}

Push-Location $root
try {
  Get-Process clipnest -ErrorAction SilentlyContinue | Stop-Process -Force
  Start-Sleep -Milliseconds 900
  Remove-Item $sandbox -Recurse -Force -ErrorAction SilentlyContinue
  New-Item -ItemType Directory -Path $dataDir -Force | Out-Null

  $env:APPDATA = $sandbox
  Start-Process -FilePath $devExe -ArgumentList '--show' | Out-Null
  Start-Sleep -Seconds 4
  Get-Process clipnest-dev | Stop-Process -Force
  Start-Sleep -Milliseconds 1600

  $path = Join-Path $dataDir 'settings.json'
  $j = Get-Content $path -Raw | ConvertFrom-Json
  $j | Add-Member -NotePropertyName privacyInput -NotePropertyValue $true -Force
  $j | ConvertTo-Json -Depth 12 | Set-Content $path -Encoding utf8

  Start-Process -FilePath $devExe -ArgumentList '--show' | Out-Null
  $p = $null
  $deadline = (Get-Date).AddSeconds(30)
  while ((Get-Date) -lt $deadline) {
    $c = Get-Process clipnest-dev -ErrorAction SilentlyContinue | Select-Object -First 1
    if ($c) { $c.Refresh(); if ($c.MainWindowHandle -ne 0) { $p = $c; Start-Sleep -Milliseconds 2600; break } }
    Start-Sleep -Milliseconds 250
  }
  if (-not $p) { throw 'sandbox 窗口没起来' }

  Click $p 600 665
  TypeWord 'aaa'
  Start-Sleep -Milliseconds 900
  Shot $p '1-one-line' | Out-Null

  Key 0x0D      # Enter
  Start-Sleep -Milliseconds 1500
  Shot $p '2-after-enter-no-typing' | Out-Null

  TypeWord 'bbb'
  Start-Sleep -Milliseconds 900
  Shot $p '3-second-line' | Out-Null

  Key 0x0D
  Start-Sleep -Milliseconds 1500
  Shot $p '4-after-second-enter' | Out-Null
} finally {
  Get-Process clipnest-dev -ErrorAction SilentlyContinue | Stop-Process -Force
  Start-Sleep -Milliseconds 900
  $env:APPDATA = $realAppData
  Start-Process -FilePath $installedExe -ArgumentList '--background' | Out-Null
  Pop-Location
}
"screenshots: $shots"
