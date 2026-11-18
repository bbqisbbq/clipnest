# 在我们的窗口内部相对坐标点击（不碰其他程序）
param(
  [string]$Title = "ClipNest",
  [int]$X = 60,
  [int]$Y = 100,
  [string]$Out = "",
  [int]$WaitMs = 800,
  [int]$Wheel = 0
)

Add-Type -AssemblyName System.Drawing
Add-Type -AssemblyName System.Windows.Forms
Add-Type @"
using System;
using System.Text;
using System.Runtime.InteropServices;
public class Win32Click {
  public delegate bool EnumProc(IntPtr h, IntPtr l);
  [StructLayout(LayoutKind.Sequential)] public struct RECT { public int Left, Top, Right, Bottom; }
  [DllImport("user32.dll")] public static extern bool EnumWindows(EnumProc cb, IntPtr p);
  [DllImport("user32.dll", CharSet=CharSet.Unicode)] public static extern int GetWindowTextW(IntPtr h, StringBuilder s, int n);
  [DllImport("user32.dll")] public static extern bool IsWindowVisible(IntPtr h);
  [DllImport("user32.dll")] public static extern bool GetWindowRect(IntPtr h, out RECT r);
  [DllImport("user32.dll")] public static extern bool SetForegroundWindow(IntPtr h);
  [DllImport("user32.dll")] public static extern bool SetCursorPos(int x, int y);
  [DllImport("user32.dll")] public static extern void mouse_event(uint flags, uint dx, uint dy, uint data, UIntPtr extra);
  public static IntPtr Find(string title) {
    IntPtr found = IntPtr.Zero;
    EnumWindows((h, l) => {
      if (!IsWindowVisible(h)) return true;
      var sb = new StringBuilder(256);
      GetWindowTextW(h, sb, 256);
      if (sb.ToString() == title || sb.ToString().StartsWith(title)) found = h;
      return true;
    }, IntPtr.Zero);
    return found;
  }
}
"@

$h = [Win32Click]::Find($Title)
if ($h -eq [IntPtr]::Zero) { Write-Host "window '$Title' not found"; exit 1 }

[Win32Click]::SetForegroundWindow($h) | Out-Null
Start-Sleep -Milliseconds 250

$r = New-Object Win32Click+RECT
[Win32Click]::GetWindowRect($h, [ref]$r) | Out-Null
$px = $r.Left + $X
$py = $r.Top + $Y

[Win32Click]::SetCursorPos($px, $py) | Out-Null
Start-Sleep -Milliseconds 120
[Win32Click]::mouse_event(0x0002, 0, 0, 0, [UIntPtr]::Zero)  # left down
Start-Sleep -Milliseconds 40
[Win32Click]::mouse_event(0x0004, 0, 0, 0, [UIntPtr]::Zero)  # left up
if ($Wheel -ne 0) {
  $times = [math]::Abs($Wheel)
  $dir = if ($Wheel -lt 0) { 0xFFFFFFFF } else { 0 }
  for ($i = 0; $i -lt $times; $i++) {
    [Win32Click]::mouse_event(0x0800, 0, 0, [uint32]::MaxValue * 0 + $dir + 120 * 0 + ([int]($Wheel -band 0)), [UIntPtr]::Zero)
    Start-Sleep -Milliseconds 60
  }
}
Start-Sleep -Milliseconds $WaitMs

if ($Out) {
  $r2 = New-Object Win32Click+RECT
  [Win32Click]::GetWindowRect($h, [ref]$r2) | Out-Null
  $w = $r2.Right - $r2.Left
  $ht = $r2.Bottom - $r2.Top
  $bmp = New-Object System.Drawing.Bitmap $w, $ht
  $g = [System.Drawing.Graphics]::FromImage($bmp)
  $g.CopyFromScreen($r2.Left, $r2.Top, 0, 0, $bmp.Size)
  $g.Dispose()
  $bmp.Save($Out, [System.Drawing.Imaging.ImageFormat]::Png)
  $bmp.Dispose()
  Write-Host "saved $Out (${w}x${ht}) at $($r2.Left),$($r2.Top)"
}
