# 「弹窗跟随主题/透明度」的实时链路验证：
#   托盘「暂停收集」会走 SetCapture → SaveSettings → emit("settings:changed")，
#   是唯一不需要在 webview 里点东西就能触发的真实设置变更。
#   两个弹窗（picker / capture）如果订阅正确，就会各回一行 `[theme-sync] …` 进日志。
#
# 用法：pwsh -STA -NoProfile -File scratch\check-theme-sync.ps1
Add-Type -AssemblyName System.Drawing
Add-Type -AssemblyName System.Windows.Forms
Add-Type -AssemblyName UIAutomationClient
Add-Type -AssemblyName UIAutomationTypes
Add-Type @'
using System;
using System.Text;
using System.Runtime.InteropServices;
public class ThemeSync {
  [StructLayout(LayoutKind.Sequential)] public struct RECT { public int Left, Top, Right, Bottom; }
  [DllImport("user32.dll")] public static extern bool GetWindowRect(IntPtr h, out RECT r);
  [DllImport("user32.dll")] public static extern bool SetCursorPos(int x, int y);
  [DllImport("user32.dll")] public static extern void mouse_event(uint f, uint dx, uint dy, uint d, IntPtr e);
  [DllImport("user32.dll")] public static extern bool IsWindowVisible(IntPtr h);
  [DllImport("user32.dll", CharSet=CharSet.Unicode)] public static extern int GetClassName(IntPtr h, StringBuilder sb, int n);
  [DllImport("user32.dll")] public static extern bool EnumWindows(EnumProc cb, IntPtr p);
  public delegate bool EnumProc(IntPtr h, IntPtr p);
  public static IntPtr FindMenu() {
    IntPtr found = IntPtr.Zero;
    EnumWindows((h, p) => {
      if (!IsWindowVisible(h)) return true;
      var cls = new StringBuilder(64); GetClassName(h, cls, 64);
      if (cls.ToString() == "#32768") { found = h; return false; }
      return true;
    }, IntPtr.Zero);
    return found;
  }
}
'@

$root = 'E:\default\clipnest'
$sandbox = Join-Path $root 'scratch\tmp-profile-theme-sync\APPDATA'
$dataDir = Join-Path $sandbox 'ClipNest'
$devExe = Join-Path $root 'bin\clipnest-dev.exe'
$installedExe = 'C:\Program Files\bbqisbbq\ClipNest\clipnest.exe'
$installedWasRunning = [bool](Get-Process clipnest -ErrorAction SilentlyContinue)
$AE = [System.Windows.Automation.AutomationElement]
$desc = [System.Windows.Automation.TreeScope]::Descendants
$btnCond = New-Object System.Windows.Automation.PropertyCondition($AE::ControlTypeProperty, [System.Windows.Automation.ControlType]::Button)

function Click([int]$x, [int]$y) {
  [ThemeSync]::SetCursorPos($x, $y) | Out-Null
  Start-Sleep -Milliseconds 200
  [ThemeSync]::mouse_event(0x0002, 0, 0, 0, [IntPtr]::Zero)
  Start-Sleep -Milliseconds 80
  [ThemeSync]::mouse_event(0x0004, 0, 0, 0, [IntPtr]::Zero)
  Start-Sleep -Milliseconds 700
}

try {
  Get-Process | Where-Object { $_.ProcessName -like 'clipnest*' } | ForEach-Object { try { $_.Kill(); $_.WaitForExit(3000) | Out-Null } catch {} }
  Start-Sleep -Milliseconds 1200
  New-Item -ItemType Directory -Force -Path $dataDir | Out-Null
  @{ capture = $true; updateCheck = $false; hotkey = ''; captureHotkey = ''; showOnLaunch = $false
     autoStart = $false; themePreset = 'default-dark'; theme = 'dark'; captureTransparency = 0 } |
    ConvertTo-Json | Set-Content (Join-Path $dataDir 'settings.json') -Encoding utf8

  $env:APPDATA = $sandbox
  Start-Process -FilePath $devExe -ArgumentList '--show' | Out-Null
  $deadline = (Get-Date).AddSeconds(25)
  while ((Get-Date) -lt $deadline) {
    $c = Get-Process clipnest-dev -ErrorAction SilentlyContinue | Select-Object -First 1
    if ($c) { $c.Refresh(); if ($c.MainWindowHandle -ne 0) { break } }
    Start-Sleep -Milliseconds 250
  }
  Start-Sleep -Seconds 3
  $log = Join-Path $dataDir 'clipnest.log'
  $before = (Get-Content $log).Count

  # 打开溢出面板 → 找到 ClipNest 图标 → 右键 → 点第 2 项（暂停收集）
  $taskbar = $AE::RootElement.FindFirst([System.Windows.Automation.TreeScope]::Children,
    (New-Object System.Windows.Automation.PropertyCondition($AE::ClassNameProperty, 'Shell_TrayWnd')))
  $chevron = $null
  foreach ($b in $taskbar.FindAll($desc, $btnCond)) {
    if ($b.Current.Name -match '显示隐藏的图标|隐藏的图标|显示隐藏') { $chevron = $b; break }
  }
  if (-not $chevron) { throw '找不到溢出面板按钮（托盘图标藏在里面）' }
  $chevron.GetCurrentPattern([System.Windows.Automation.InvokePattern]::Pattern).Invoke()
  Start-Sleep -Milliseconds 1200
  $fly = $AE::RootElement.FindFirst([System.Windows.Automation.TreeScope]::Children,
    (New-Object System.Windows.Automation.PropertyCondition($AE::ClassNameProperty, 'TopLevelWindowForOverflowXamlIsland')))
  if (-not $fly) { throw '溢出面板没出现' }
  $icon = $null
  foreach ($b in $fly.FindAll($desc, $btnCond)) {
    if ($b.Current.Name -match 'clipnest') { $icon = $b; break }
  }
  if (-not $icon) { throw '溢出面板里没有 clipnest 图标' }
  $r = $icon.Current.BoundingRectangle
  [ThemeSync]::SetCursorPos([int]($r.X + $r.Width / 2), [int]($r.Y + $r.Height / 2)) | Out-Null
  Start-Sleep -Milliseconds 300
  [ThemeSync]::mouse_event(0x0008, 0, 0, 0, [IntPtr]::Zero)   # RIGHTDOWN
  Start-Sleep -Milliseconds 120
  [ThemeSync]::mouse_event(0x0010, 0, 0, 0, [IntPtr]::Zero)   # RIGHTUP
  Start-Sleep -Milliseconds 900
  $menu = [ThemeSync]::FindMenu()
  if ($menu -eq [IntPtr]::Zero) { throw '托盘菜单没弹出来' }
  $mr = New-Object ThemeSync+RECT
  [ThemeSync]::GetWindowRect($menu, [ref]$mr) | Out-Null
  # 菜单：快速粘贴 / 暂停收集 ｜ 开机自启 / 打开设置 / 打开数据目录 ｜ 退出
  # 行高约 24px，第一项中心 ≈ top+14，第二项 ≈ top+38
  Click ([int]($mr.Left + 30)) ([int]($mr.Top + 38))
  Start-Sleep -Seconds 2
  # 再点一次恢复收集（别把沙箱实例留在暂停态）
  [ThemeSync]::mouse_event(0x0008, 0, 0, 0, [IntPtr]::Zero)
  Start-Sleep -Milliseconds 120
  [ThemeSync]::mouse_event(0x0010, 0, 0, 0, [IntPtr]::Zero)
  Start-Sleep -Milliseconds 900
  $menu2 = [ThemeSync]::FindMenu()
  if ($menu2 -ne [IntPtr]::Zero) {
    $mr2 = New-Object ThemeSync+RECT
    [ThemeSync]::GetWindowRect($menu2, [ref]$mr2) | Out-Null
    Click ([int]($mr2.Left + 30)) ([int]($mr2.Top + 38))
    Start-Sleep -Seconds 1
  }

  $lines = Get-Content $log | Select-Object -Skip $before
  $sync = $lines | Where-Object { $_ -match '\[theme-sync\]' }
  $toggled = $lines | Where-Object { $_ -match 'settings:changed|capture (enabled|disabled)|collection' }
  Write-Host "--- 设置变更相关日志（$($lines.Count) 行新增）"
  $toggled | ForEach-Object { Write-Host "  $_" }
  Write-Host "--- [theme-sync] 行（弹窗各自的上报）"
  $sync | ForEach-Object { Write-Host "  $_" }
  $capture = @($sync | Where-Object { $_ -match 'mode=capture' }).Count
  $picker = @($sync | Where-Object { $_ -match 'mode=picker' }).Count
  $main = @($sync | Where-Object { $_ -match 'mode=main' }).Count
  Write-Host "E2E-RESULT: main=$main capture=$capture picker=$picker"
  if ($capture -ge 1 -and $picker -ge 1) { Write-Host 'PASS 两个弹窗都收到了 settings:changed' }
  else { Write-Host 'FAIL 弹窗没收到 settings:changed（托盘菜单可能没点中，看上面的日志）' }
}
finally {
  Get-Process | Where-Object { $_.ProcessName -like 'clipnest*' } | ForEach-Object { try { $_.Kill() } catch {} }
  if ($installedWasRunning -and (Test-Path $installedExe)) {
    Start-Process -FilePath $installedExe -ArgumentList '--background' | Out-Null
    Write-Host "已把安装版 ClipNest 拉起来"
  }
}
