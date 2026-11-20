# 托盘菜单快照：点开 Win11 溢出面板 → 找到 ClipNest 图标（名字里带 clipnest）→
# 右键 → 把弹出菜单窗口（类名 #32768）PrintWindow 下来。
#
# 用法（必须 -STA）：pwsh -STA -NoProfile -File scratch\shot-tray-menu.ps1
Add-Type -AssemblyName System.Drawing
Add-Type -AssemblyName System.Windows.Forms
Add-Type -AssemblyName UIAutomationClient
Add-Type -AssemblyName UIAutomationTypes
Add-Type @'
using System;
using System.Text;
using System.Runtime.InteropServices;
public class TrayShot {
  [StructLayout(LayoutKind.Sequential)] public struct RECT { public int Left, Top, Right, Bottom; }
  [DllImport("user32.dll")] public static extern bool GetWindowRect(IntPtr h, out RECT r);
  [DllImport("user32.dll")] public static extern bool PrintWindow(IntPtr h, IntPtr dc, uint flags);
  [DllImport("user32.dll")] public static extern bool SetCursorPos(int x, int y);
  [DllImport("user32.dll")] public static extern void mouse_event(uint f, uint dx, uint dy, uint d, IntPtr e);
  [DllImport("user32.dll")] public static extern bool IsWindowVisible(IntPtr h);
  [DllImport("user32.dll", CharSet=CharSet.Unicode)] public static extern int GetClassName(IntPtr h, StringBuilder sb, int n);
  [DllImport("user32.dll")] public static extern bool EnumWindows(EnumProc cb, IntPtr p);
  public delegate bool EnumProc(IntPtr h, IntPtr p);
  public const uint RIGHTDOWN = 0x0008, RIGHTUP = 0x0010;
  // 找可见的 #32768（弹出菜单）窗口；返回第一个命中
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
$sandbox = Join-Path $root 'scratch\appdata-tray-shot'
$devExe = Join-Path $root 'bin\clipnest-dev.exe'
$installedExe = 'C:\Program Files\bbqisbbq\ClipNest\clipnest.exe'
$shots = Join-Path $root 'scratch\e2e-shots'
$realAppData = $env:APPDATA
New-Item -ItemType Directory -Path $shots -Force | Out-Null

$AE = [System.Windows.Automation.AutomationElement]
$desc = [System.Windows.Automation.TreeScope]::Descendants
$btnCond = New-Object System.Windows.Automation.PropertyCondition($AE::ControlTypeProperty, [System.Windows.Automation.ControlType]::Button)

try {
  Get-Process clipnest -ErrorAction SilentlyContinue | Stop-Process -Force
  Start-Sleep -Milliseconds 900
  Remove-Item $sandbox -Recurse -Force -ErrorAction SilentlyContinue
  New-Item -ItemType Directory -Path $sandbox -Force | Out-Null
  $env:APPDATA = $sandbox
  Start-Process -FilePath $devExe -ArgumentList '--show' | Out-Null
  $deadline = (Get-Date).AddSeconds(25)
  while ((Get-Date) -lt $deadline) {
    $c = Get-Process clipnest-dev -ErrorAction SilentlyContinue | Select-Object -First 1
    if ($c) { $c.Refresh(); if ($c.MainWindowHandle -ne 0) { break } }
    Start-Sleep -Milliseconds 250
  }
  Start-Sleep -Milliseconds 2000

  # 1. 打开溢出面板（托盘图标平时藏在里面）
  $taskbar = $AE::RootElement.FindFirst([System.Windows.Automation.TreeScope]::Children,
    (New-Object System.Windows.Automation.PropertyCondition($AE::ClassNameProperty, 'Shell_TrayWnd')))
  $chevron = $null
  foreach ($b in $taskbar.FindAll($desc, $btnCond)) {
    if ($b.Current.Name -match '显示隐藏的图标|隐藏的图标|显示隐藏') { $chevron = $b; break }
  }
  if (-not $chevron) { "找不到溢出面板按钮"; return }
  $chevron.GetCurrentPattern([System.Windows.Automation.InvokePattern]::Pattern).Invoke()
  Start-Sleep -Milliseconds 1200

  # 2. 面板里找 ClipNest 图标
  $fly = $AE::RootElement.FindFirst([System.Windows.Automation.TreeScope]::Children,
    (New-Object System.Windows.Automation.PropertyCondition($AE::ClassNameProperty, 'TopLevelWindowForOverflowXamlIsland')))
  if (-not $fly) { "溢出面板窗口没出现"; return }
  $icon = $null
  foreach ($b in $fly.FindAll($desc, $btnCond)) {
    if ($b.Current.Name -match 'clipnest') { $icon = $b; break }
  }
  if (-not $icon) { "溢出面板里没有 clipnest 图标"; return }

  # 3. 右键弹菜单
  $r = $icon.Current.BoundingRectangle
  "icon: $($icon.Current.Name) at $($r.X),$($r.Y)"
  [TrayShot]::SetCursorPos([int]($r.X + $r.Width / 2), [int]($r.Y + $r.Height / 2)) | Out-Null
  Start-Sleep -Milliseconds 300
  [TrayShot]::mouse_event([TrayShot]::RIGHTDOWN, 0, 0, 0, [IntPtr]::Zero)
  Start-Sleep -Milliseconds 120
  [TrayShot]::mouse_event([TrayShot]::RIGHTUP, 0, 0, 0, [IntPtr]::Zero)
  Start-Sleep -Milliseconds 1000

  # 4. 截菜单
  $menu = [TrayShot]::FindMenu()
  if ($menu -eq [IntPtr]::Zero) { "没找到弹出菜单窗口"; return }
  $mr = New-Object TrayShot+RECT
  [TrayShot]::GetWindowRect($menu, [ref]$mr) | Out-Null
  $w = $mr.Right - $mr.Left; $h = $mr.Bottom - $mr.Top
  "menu: ${w}x${h}"
  $bmp = New-Object System.Drawing.Bitmap $w, $h
  $g = [System.Drawing.Graphics]::FromImage($bmp)
  $hdc = $g.GetHdc()
  [TrayShot]::PrintWindow($menu, $hdc, 2) | Out-Null
  $g.ReleaseHdc($hdc); $g.Dispose()
  $out = Join-Path $shots 'tray-menu.png'
  $bmp.Save($out, [System.Drawing.Imaging.ImageFormat]::Png)
  $bmp.Dispose()
  "shot: $out"

  # 5. 点菜单里的「打开设置」（原生菜单不进 UIA 树，只能按像素点）：
  #    菜单项行高约 24px，第 5 项中心在 top+94。
  [TrayShot]::SetCursorPos([int]($mr.Left + $w / 2), [int]($mr.Top + 94)) | Out-Null
  Start-Sleep -Milliseconds 250
  [TrayShot]::mouse_event(0x0002, 0, 0, 0, [IntPtr]::Zero)   # LEFTDOWN
  Start-Sleep -Milliseconds 80
  [TrayShot]::mouse_event(0x0004, 0, 0, 0, [IntPtr]::Zero)   # LEFTUP
  Start-Sleep -Milliseconds 1500

  $win = Get-Process clipnest-dev | Where-Object { $_.MainWindowHandle -ne 0 } | Select-Object -First 1
  $wr = New-Object TrayShot+RECT
  [TrayShot]::GetWindowRect($win.MainWindowHandle, [ref]$wr) | Out-Null
  $ww = $wr.Right - $wr.Left; $wh = $wr.Bottom - $wr.Top
  $w2 = New-Object System.Drawing.Bitmap $ww, $wh
  $g2 = [System.Drawing.Graphics]::FromImage($w2)
  $hdc2 = $g2.GetHdc()
  [TrayShot]::PrintWindow($win.MainWindowHandle, $hdc2, 2) | Out-Null
  $g2.ReleaseHdc($hdc2); $g2.Dispose()
  $out2 = Join-Path $shots 'tray-open-settings.png'
  $w2.Save($out2, [System.Drawing.Imaging.ImageFormat]::Png)
  $w2.Dispose()
  "shot: $out2"
}
finally {
  [System.Windows.Forms.SendKeys]::SendWait('{ESC}')
  Start-Sleep -Milliseconds 200
  [System.Windows.Forms.SendKeys]::SendWait('{ESC}')
  Get-Process clipnest-dev -ErrorAction SilentlyContinue | Stop-Process -Force
  Start-Sleep -Milliseconds 900
  $env:APPDATA = $realAppData
  Start-Process -FilePath $installedExe -ArgumentList '--show' | Out-Null
  Start-Sleep -Milliseconds 1200
}
