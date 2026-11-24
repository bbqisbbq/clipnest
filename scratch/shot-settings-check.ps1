# 看一眼设置页：新增的「截图快捷键 / 截图后复制到剪贴板 / 网页图片自动下载」三行。
# 起沙箱实例（--show），点侧栏齿轮进设置，滚到「交互与启动」和「采集」，各截一张。
Add-Type -AssemblyName System.Drawing
Add-Type -AssemblyName System.Windows.Forms
Add-Type @'
using System;
using System.Text;
using System.Runtime.InteropServices;
public class WinShot {
  public delegate bool EnumProc(IntPtr h, IntPtr l);
  [StructLayout(LayoutKind.Sequential)] public struct RECT { public int Left, Top, Right, Bottom; }
  [DllImport("user32.dll")] public static extern bool EnumWindows(EnumProc cb, IntPtr p);
  [DllImport("user32.dll", CharSet=CharSet.Unicode)] public static extern int GetWindowTextW(IntPtr h, StringBuilder s, int n);
  [DllImport("user32.dll")] public static extern bool IsWindowVisible(IntPtr h);
  [DllImport("user32.dll")] public static extern bool GetWindowRect(IntPtr h, out RECT r);
  [DllImport("user32.dll")] public static extern bool SetForegroundWindow(IntPtr h);
  [DllImport("user32.dll")] public static extern bool SetCursorPos(int x, int y);
  [DllImport("user32.dll")] public static extern void mouse_event(uint f, uint dx, uint dy, uint d, IntPtr e);
  public static IntPtr Find(string title) {
    IntPtr found = IntPtr.Zero;
    EnumWindows((h, l) => {
      if (!IsWindowVisible(h)) return true;
      var sb = new StringBuilder(256);
      GetWindowTextW(h, sb, 256);
      if (sb.ToString() == title) found = h;
      return true;
    }, IntPtr.Zero);
    return found;
  }
}
'@

$root = 'E:\default\clipnest'
$sandbox = Join-Path $root 'scratch\appdata-settings-shot'
Get-Process | Where-Object { $_.ProcessName -like 'clipnest*' } | Stop-Process -Force -ErrorAction SilentlyContinue
Start-Sleep -Milliseconds 600
New-Item -ItemType Directory -Force -Path (Join-Path $sandbox 'ClipNest') | Out-Null
$env:APPDATA = $sandbox
$env:CLIPNEST_KEEP_CONSOLE = '1'
$proc = Start-Process -FilePath (Join-Path $root 'bin\clipnest-dev.exe') -ArgumentList '--show' -PassThru
Start-Sleep -Seconds 4

$h = [WinShot]::Find('ClipNest 剪贴板')
if ($h -eq [IntPtr]::Zero) { Write-Host 'FAIL: 没找到主窗口'; exit 1 }
$r = New-Object WinShot+RECT
[WinShot]::GetWindowRect($h, [ref]$r) | Out-Null
[WinShot]::SetForegroundWindow($h) | Out-Null
Start-Sleep -Milliseconds 500
Write-Host "main window at $($r.Left),$($r.Top) size $($r.Right - $r.Left)x$($r.Bottom - $r.Top)"

function Click([int]$x, [int]$y) {
  [WinShot]::SetCursorPos($x, $y) | Out-Null
  Start-Sleep -Milliseconds 200
  [WinShot]::mouse_event(0x0002, 0, 0, 0, [IntPtr]::Zero)
  [WinShot]::mouse_event(0x0004, 0, 0, 0, [IntPtr]::Zero)
  Start-Sleep -Milliseconds 700
}
function Shot([string]$name) {
  $bmp = New-Object System.Drawing.Bitmap ($r.Right - $r.Left), ($r.Bottom - $r.Top)
  $g = [System.Drawing.Graphics]::FromImage($bmp)
  $g.CopyFromScreen($r.Left, $r.Top, 0, 0, $bmp.Size)
  $g.Dispose()
  $bmp.Save((Join-Path (Join-Path $root 'scratch') $name), [System.Drawing.Imaging.ImageFormat]::Png)
  $bmp.Dispose()
  Write-Host "screenshot: $name"
}

Shot 'settings-check-1-main.png'
# 侧栏底部的齿轮/设置入口：按现有布局在左下角偏上（每次布局都在这附近）
Click ($r.Left + 46) ($r.Top + 713)
Shot 'settings-check-2.png'
# 滚到「交互与启动」：新增的截图快捷键 / 截图后复制到剪贴板 就在这几行
[WinShot]::SetCursorPos($r.Left + 650, $r.Top + 450) | Out-Null
for ($i = 0; $i -lt 6; $i++) {
  [WinShot]::mouse_event(0x0800, 0, 0, [uint32]4294967176, [IntPtr]::Zero)
  Start-Sleep -Milliseconds 120
}
Start-Sleep -Milliseconds 600
Shot 'settings-check-3-interaction.png'

Stop-Process -Id $proc.Id -Force -ErrorAction SilentlyContinue
Remove-Item Env:\CLIPNEST_KEEP_CONSOLE -ErrorAction SilentlyContinue
Write-Host 'done'




