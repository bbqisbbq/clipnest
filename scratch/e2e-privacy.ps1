# 隐私模式 e2e：预写三段草稿 → 打开隐私模式 → 截图验证"只有光标所在段可读"。
#
# 用法（必须 -STA）：pwsh -STA -NoProfile -File scratch\e2e-privacy.ps1
# 前提：bin\clipnest-dev.exe 已用当前前端产物重新构建（先 cd frontend; vite build 再 go build）。
#
# 全程**不打字**（输入法会把自动化搅乱）：草稿用 scratch/setdraft 预写进库，
# 隐私开关用 settings.json + 界面点一次两条路各验一遍。
Add-Type -AssemblyName System.Drawing
Add-Type -AssemblyName System.Windows.Forms
Add-Type @'
using System;
using System.Runtime.InteropServices;
public class E2EPriv {
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
$sandbox = Join-Path $root 'scratch\appdata-privacy'
$devExe = Join-Path $root 'bin\clipnest-dev.exe'
$installedExe = 'C:\Program Files\bbqisbbq\ClipNest\clipnest.exe'
$shots = Join-Path $root 'scratch\privacy-shots'
$realAppData = $env:APPDATA
$dataDir = Join-Path $sandbox 'ClipNest'
New-Item -ItemType Directory -Path $shots -Force | Out-Null

function Get-Rect($p) {
  $r = New-Object E2EPriv+RECT
  [E2EPriv]::GetWindowRect($p.MainWindowHandle, [ref]$r) | Out-Null
  return $r
}
function Shot($p, $name) {
  $r = Get-Rect $p
  $bmp = New-Object System.Drawing.Bitmap ($r.Right - $r.Left), ($r.Bottom - $r.Top)
  $g = [System.Drawing.Graphics]::FromImage($bmp)
  $hdc = $g.GetHdc()
  [E2EPriv]::PrintWindow($p.MainWindowHandle, $hdc, 2) | Out-Null
  $g.ReleaseHdc($hdc); $g.Dispose()
  $path = Join-Path $shots "$name.png"
  $bmp.Save($path, [System.Drawing.Imaging.ImageFormat]::Png); $bmp.Dispose()
  return $path
}
function Click($p, [int]$relX, [int]$relY) {
  $r = Get-Rect $p
  [E2EPriv]::SetForegroundWindow($p.MainWindowHandle) | Out-Null
  Start-Sleep -Milliseconds 300
  [E2EPriv]::SetCursorPos($r.Left + $relX, $r.Top + $relY) | Out-Null
  Start-Sleep -Milliseconds 200
  [E2EPriv]::mouse_event([E2EPriv]::LEFTDOWN, 0, 0, 0, [IntPtr]::Zero)
  Start-Sleep -Milliseconds 70
  [E2EPriv]::mouse_event([E2EPriv]::LEFTUP, 0, 0, 0, [IntPtr]::Zero)
  Start-Sleep -Milliseconds 700
}
function Wait-Window() {
  $p = $null
  $deadline = (Get-Date).AddSeconds(30)
  while ((Get-Date) -lt $deadline) {
    $c = Get-Process clipnest-dev -ErrorAction SilentlyContinue | Select-Object -First 1
    if ($c) { $c.Refresh(); if ($c.MainWindowHandle -ne 0) { $p = $c; Start-Sleep -Milliseconds 2400; break } }
    Start-Sleep -Milliseconds 250
  }
  if (-not $p) { throw 'sandbox 窗口没起来' }
  return $p
}
function Set-Privacy([bool]$on) {
  $path = Join-Path $dataDir 'settings.json'
  $json = Get-Content $path -Raw | ConvertFrom-Json
  $json | Add-Member -NotePropertyName privacyInput -NotePropertyValue $on -Force
  $json | ConvertTo-Json -Depth 12 | Set-Content $path -Encoding utf8
}

$draft = @'
第一段：旁边有人走过，这行字不该被看清。
第二段：这里也是已经写好的内容，隐私模式下只留一团灰影。
第三段：#work 光标所在的这一段才是清楚的。
'@ -replace "`r`n", "`n"

$result = @{}
Push-Location $root
try {
  Get-Process clipnest -ErrorAction SilentlyContinue | Stop-Process -Force
  Start-Sleep -Milliseconds 900

  Remove-Item $sandbox -Recurse -Force -ErrorAction SilentlyContinue
  New-Item -ItemType Directory -Path $dataDir -Force | Out-Null
  $sd = & go run ./scratch/setdraft $dataDir $draft 2>&1
  $result['draft_seed'] = ($sd -join ' | ')

  # 第一次启动只为让应用写出 settings.json（顺便确认默认关）
  $env:APPDATA = $sandbox
  Start-Process -FilePath $devExe -ArgumentList '--show' | Out-Null
  $p = Wait-Window
  Shot $p '0-default-off' | Out-Null
  Get-Process clipnest-dev -ErrorAction SilentlyContinue | Stop-Process -Force
  Start-Sleep -Milliseconds 1500

  Set-Privacy $true
  Start-Process -FilePath $devExe -ArgumentList '--show' | Out-Null
  $p = Wait-Window
  Shot $p '1-privacy-on' | Out-Null

  # 点第一段（坐标取自截图：正文三行在 y≈663/687/711，正文从 x≈344 起）
  Click $p 500 663
  Start-Sleep -Milliseconds 800
  Shot $p '2-caret-first-paragraph' | Out-Null

  # 点一下开关（.composer-btns 里，眼睛图标在 x≈903 y≈696），确认点得动、能关掉
  Click $p 903 696
  Start-Sleep -Milliseconds 900
  Shot $p '3-toggled-off' | Out-Null

  Get-Process clipnest-dev -ErrorAction SilentlyContinue | Stop-Process -Force
  Start-Sleep -Milliseconds 1200
  $saved = Get-Content (Join-Path $dataDir 'settings.json') -Raw | ConvertFrom-Json
  $result['privacyInput_after_toggle'] = $saved.privacyInput
} finally {
  Get-Process clipnest-dev -ErrorAction SilentlyContinue | Stop-Process -Force
  Start-Sleep -Milliseconds 800
  $env:APPDATA = $realAppData
  Start-Process -FilePath $installedExe -ArgumentList '--background' | Out-Null
  Pop-Location
}

$result.GetEnumerator() | ForEach-Object { "{0}: {1}" -f $_.Key, $_.Value }
"screenshots: $shots"
