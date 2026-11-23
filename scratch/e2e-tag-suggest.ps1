# 标签联想 e2e：点进「记一条」→ 敲 `#`/`w`/`r` → Enter 插入 → 读库断言正文。
#
# 用法（必须 -STA）：pwsh -STA -NoProfile -File scratch\e2e-tag-suggest.ps1
# 前提：bin\clipnest-dev.exe 已用当前前端产物重新构建（先 cd frontend; vite build 再 go build）。
Add-Type -AssemblyName System.Drawing
Add-Type -AssemblyName System.Windows.Forms
Add-Type @'
using System;
using System.Runtime.InteropServices;
public class E2ETag {
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
$sandbox = Join-Path $root 'scratch\appdata-e2e-tags'
$devExe = Join-Path $root 'bin\clipnest-dev.exe'
$installedExe = 'C:\Program Files\bbqisbbq\ClipNest\clipnest.exe'
$shots = Join-Path $root 'scratch\e2e-tags-shots'
$realAppData = $env:APPDATA
New-Item -ItemType Directory -Path $shots -Force | Out-Null

function Get-Rect($p) {
  $r = New-Object E2ETag+RECT
  [E2ETag]::GetWindowRect($p.MainWindowHandle, [ref]$r) | Out-Null
  return $r
}

function Shot($p, $name) {
  $r = Get-Rect $p
  $bmp = New-Object System.Drawing.Bitmap ($r.Right - $r.Left), ($r.Bottom - $r.Top)
  $g = [System.Drawing.Graphics]::FromImage($bmp)
  $hdc = $g.GetHdc()
  [E2ETag]::PrintWindow($p.MainWindowHandle, $hdc, 2) | Out-Null
  $g.ReleaseHdc($hdc); $g.Dispose()
  $path = Join-Path $shots "$name.png"
  $bmp.Save($path, [System.Drawing.Imaging.ImageFormat]::Png); $bmp.Dispose()
  return $path
}

function Click($p, [int]$relX, [int]$relY) {
  $r = Get-Rect $p
  [E2ETag]::SetForegroundWindow($p.MainWindowHandle) | Out-Null
  Start-Sleep -Milliseconds 300
  [E2ETag]::SetCursorPos($r.Left + $relX, $r.Top + $relY) | Out-Null
  Start-Sleep -Milliseconds 200
  [E2ETag]::mouse_event([E2ETag]::LEFTDOWN, 0, 0, 0, [IntPtr]::Zero)
  Start-Sleep -Milliseconds 70
  [E2ETag]::mouse_event([E2ETag]::LEFTUP, 0, 0, 0, [IntPtr]::Zero)
  Start-Sleep -Milliseconds 700
}

$result = @{}
Push-Location $root
try {
  Get-Process clipnest -ErrorAction SilentlyContinue | Stop-Process -Force
  Start-Sleep -Milliseconds 900

  Remove-Item $sandbox -Recurse -Force -ErrorAction SilentlyContinue
  New-Item -ItemType Directory -Path (Join-Path $sandbox 'ClipNest') -Force | Out-Null
  $seed = & go run ./scratch/seedtags (Join-Path $sandbox 'ClipNest') 2>&1
  $sd = & go run ./scratch/setdraft (Join-Path $sandbox 'ClipNest') '#wr' 2>&1
  $result['draft_seed'] = ($sd -join ' | ')
  $result['seed'] = ($seed -join ' | ')

  $env:APPDATA = $sandbox
  Start-Process -FilePath $devExe -ArgumentList '--show' | Out-Null
  $p = $null
  $deadline = (Get-Date).AddSeconds(30)
  while ((Get-Date) -lt $deadline) {
    $c = Get-Process clipnest-dev -ErrorAction SilentlyContinue | Select-Object -First 1
    if ($c) { $c.Refresh(); if ($c.MainWindowHandle -ne 0) { $p = $c; Start-Sleep -Milliseconds 2200; break } }
    Start-Sleep -Milliseconds 250
  }
  if (-not $p) { throw 'sandbox 窗口没起来' }
  Shot $p '0-start' | Out-Null

  # **完全不打字**（输入法会把自动化搅乱）：草稿 `#wr` 已经预写进库，应用起来就带着它。
  # 点一下输入框末尾把光标放到 `#wr` 之后 → 触发联想；再点第一条候选 → 插入。
  $r = Get-Rect $p
  $cx = 450
  $cy = [int](($r.Bottom - $r.Top) - 120)
  Click $p $cx $cy
  Start-Sleep -Milliseconds 900
  Shot $p '1-caret' | Out-Null

  # 弹窗在光标下方 ~6px：光标在 (450, ~620)，候选第一条大约在 (490, 540)
  Click $p 490 540
  Start-Sleep -Milliseconds 1200
  Shot $p '2-accepted' | Out-Null

  # 停实例（草稿在 Shutdown 兜底 flush），读库断言
  Get-Process clipnest-dev -ErrorAction SilentlyContinue | Stop-Process -Force
  Start-Sleep -Milliseconds 1200
  $draftOut = & go run ./scratch/dbdraft (Join-Path $sandbox 'ClipNest') 2>&1
  $result['draft'] = ($draftOut -join ' | ')
  $result['tag_inserted'] = [bool](($draftOut -join ' ') -match '#writing')
} finally {
  Get-Process clipnest-dev -ErrorAction SilentlyContinue | Stop-Process -Force
  Start-Sleep -Milliseconds 800
  $env:APPDATA = $realAppData
  Start-Process -FilePath $installedExe -ArgumentList '--background' | Out-Null
  Pop-Location
}

$result.GetEnumerator() | ForEach-Object { "{0}: {1}" -f $_.Key, $_.Value }
"screenshots: $shots"
