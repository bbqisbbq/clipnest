# 探针 v3：热键唤起 picker，轮询等到真正可见的那一帧立刻屏幕抓取
param([string]$Out = "$PSScriptRoot\probe-picker.png")
Add-Type -AssemblyName System.Drawing
Add-Type @'
using System;
using System.Text;
using System.Runtime.InteropServices;
public class PK3 {
  [StructLayout(LayoutKind.Sequential)] public struct RECT { public int Left, Top, Right, Bottom; }
  [DllImport("user32.dll")] public static extern bool GetWindowRect(IntPtr h, out RECT r);
  [DllImport("user32.dll", CharSet=CharSet.Unicode)] public static extern int GetWindowTextW(IntPtr h, StringBuilder sb, int n);
  [DllImport("user32.dll")] public static extern bool EnumWindows(EnumProc cb, IntPtr p);
  [DllImport("user32.dll")] public static extern bool IsWindowVisible(IntPtr h);
  [DllImport("user32.dll")] public static extern void keybd_event(byte vk, byte scan, uint flags, IntPtr extra);
  [DllImport("user32.dll")] public static extern bool SetForegroundWindow(IntPtr h);
  public delegate bool EnumProc(IntPtr h, IntPtr p);
  public const byte VK_CONTROL = 0x11, VK_SHIFT = 0x10, VK_V = 0x56;
  public const uint KEYUP = 0x0002;
  public static IntPtr FindByTitleSize(string title, int width) {
    IntPtr found = IntPtr.Zero;
    EnumWindows((h, p) => {
      var sb = new StringBuilder(256); GetWindowTextW(h, sb, 256);
      if (sb.ToString() != title) return true;
      RECT r; if (!GetWindowRect(h, out r)) return true;
      if (Math.Abs((r.Right - r.Left) - width) > 40) return true;
      found = h; return false;
    }, IntPtr.Zero);
    return found;
  }
}
'@

$picker = [PK3]::FindByTitleSize('ClipNest', 468)
if ($picker -eq [IntPtr]::Zero) { throw '找不到 picker 窗口' }
Write-Host "picker hwnd=$('{0:x}' -f $picker.ToInt64()) visible(before)=$([PK3]::IsWindowVisible($picker))"

[PK3]::keybd_event([PK3]::VK_CONTROL, 0, 0, [IntPtr]::Zero)
[PK3]::keybd_event([PK3]::VK_SHIFT, 0, 0, [IntPtr]::Zero)
[PK3]::keybd_event([PK3]::VK_V, 0, 0, [IntPtr]::Zero)
Start-Sleep -Milliseconds 40
[PK3]::keybd_event([PK3]::VK_V, 0, [PK3]::KEYUP, [IntPtr]::Zero)
[PK3]::keybd_event([PK3]::VK_SHIFT, 0, [PK3]::KEYUP, [IntPtr]::Zero)
[PK3]::keybd_event([PK3]::VK_CONTROL, 0, [PK3]::KEYUP, [IntPtr]::Zero)

$seen = $false; $elapsed = 0
while ($elapsed -lt 3000) {
  if ([PK3]::IsWindowVisible($picker)) { $seen = $true; break }
  Start-Sleep -Milliseconds 50; $elapsed += 50
}
Write-Host "visible-after=$seen after ${elapsed}ms"
if (-not $seen) { throw 'picker 一直没可见（热键可能没触发，或已因失焦隐藏）' }

$r = New-Object PK3+RECT
[PK3]::GetWindowRect($picker, [ref]$r) | Out-Null
$w = $r.Right - $r.Left; $h = $r.Bottom - $r.Top
Start-Sleep -Milliseconds 120
[PK3]::SetForegroundWindow($picker) | Out-Null
Start-Sleep -Milliseconds 60

$bmp = New-Object System.Drawing.Bitmap $w, $h
$g = [System.Drawing.Graphics]::FromImage($bmp)
$g.CopyFromScreen($r.Left, $r.Top, 0, 0, (New-Object System.Drawing.Size $w, $h))
$g.Dispose()
$bmp.Save($Out, [System.Drawing.Imaging.ImageFormat]::Png)
function Px([int]$x, [int]$y) { $c = $bmp.GetPixel($x, $y); "#{0:x2}{1:x2}{2:x2}" -f $c.R, $c.G, $c.B }
Write-Host ("pos=$($r.Left),$($r.Top) size=${w}x${h} 卡片(6,6)={0} 中心={1} 列表行(12,120)={2}" -f (Px 6 6), (Px ([int]($w/2)) ([int]($h/2))), (Px 12 120))
Write-Host "saved: $Out"
$bmp.Dispose()

[PK3]::keybd_event(0x1B, 0, 0, [IntPtr]::Zero)
Start-Sleep -Milliseconds 40
[PK3]::keybd_event(0x1B, 0, [PK3]::KEYUP, [IntPtr]::Zero)
