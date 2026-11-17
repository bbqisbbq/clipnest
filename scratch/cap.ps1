# Screenshot helper for the ClipNest window.
#   -Click "x,y"  click at window-relative coordinates
#   -Hover "x,y"  move the cursor there (for tooltips)
#   -Wheel n      scroll n notches (negative = down)
#   -Out name     output file name under scratch\
param(
  [string]$Click = "",
  [string]$Hover = "",
  [int]$Wheel = 0,
  [string]$Out = "shot.png",
  [int]$WaitMs = 700,
  [switch]$NoFocus
)

Add-Type -AssemblyName System.Drawing
if (-not ("CapX" -as [type])) {
  Add-Type @"
using System;
using System.Collections.Generic;
using System.Runtime.InteropServices;
using System.Text;
public class CapX {
  [StructLayout(LayoutKind.Sequential)] public struct RECT { public int Left, Top, Right, Bottom; }
  public delegate bool EnumProc(IntPtr h, IntPtr l);
  [DllImport("user32.dll")] public static extern bool EnumWindows(EnumProc cb, IntPtr p);
  [DllImport("user32.dll")] public static extern uint GetWindowThreadProcessId(IntPtr h, out uint pid);
  [DllImport("user32.dll", CharSet = CharSet.Unicode)] public static extern int GetWindowTextW(IntPtr h, StringBuilder s, int n);
  [DllImport("user32.dll")] public static extern bool GetWindowRect(IntPtr h, out RECT r);
  [DllImport("user32.dll")] public static extern bool SetForegroundWindow(IntPtr h);
  [DllImport("user32.dll")] public static extern bool ShowWindow(IntPtr h, int c);
  [DllImport("user32.dll")] public static extern bool SetCursorPos(int x, int y);
  [DllImport("user32.dll")] public static extern void mouse_event(uint f, int dx, int dy, int d, IntPtr e);
  [DllImport("user32.dll")] public static extern void keybd_event(byte vk, byte scan, uint flags, IntPtr extra);

  // Finds the app's main window by title, visible or not.
  public static IntPtr FindMain(uint pid, string titlePart) {
    IntPtr best = IntPtr.Zero;
    long bestArea = -1;
    EnumWindows((h, l) => {
      uint p; GetWindowThreadProcessId(h, out p);
      if (p != pid) return true;
      var sb = new StringBuilder(256); GetWindowTextW(h, sb, 256);
      if (sb.ToString().IndexOf(titlePart, StringComparison.OrdinalIgnoreCase) < 0) return true;
      RECT r; if (!GetWindowRect(h, out r)) return true;
      long area = (long)(r.Right - r.Left) * (r.Bottom - r.Top);
      if (area > bestArea) { bestArea = area; best = h; }
      return true;
    }, IntPtr.Zero);
    return best;
  }
}
"@
}

$p = Get-Process clipnest -ErrorAction Stop | Select-Object -First 1
$h = [CapX]::FindMain([uint32]$p.Id, "ClipNest")
if ($h -eq [IntPtr]::Zero) { throw "ClipNest window not found for pid $($p.Id)" }
if ($NoFocus) {
  Start-Sleep -Milliseconds 300
} else {
  [CapX]::ShowWindow($h, 9) | Out-Null
  [CapX]::keybd_event(0x12, 0, 0, [IntPtr]::Zero)
  [CapX]::keybd_event(0x12, 0, 2, [IntPtr]::Zero)
  [CapX]::SetForegroundWindow($h) | Out-Null
  Start-Sleep -Milliseconds 700
}

$r = New-Object CapX+RECT
[CapX]::GetWindowRect($h, [ref]$r) | Out-Null
$w = $r.Right - $r.Left
$ht = $r.Bottom - $r.Top
Write-Host "window rect: $($r.Left),$($r.Top) ${w}x${ht}"

if ($Wheel -ne 0) {
  [CapX]::SetCursorPos($r.Left + [int]($w / 2), $r.Top + 300) | Out-Null
  Start-Sleep -Milliseconds 180
  # Real micro-movement so the webview registers a hover before scrolling.
  [CapX]::mouse_event(0x0001, 2, 0, 0, [IntPtr]::Zero)
  Start-Sleep -Milliseconds 60
  [CapX]::mouse_event(0x0001, -2, 0, 0, [IntPtr]::Zero)
  Start-Sleep -Milliseconds 120
  $steps = [Math]::Abs($Wheel)
  $delta = if ($Wheel -lt 0) { -120 } else { 120 }
  for ($i = 0; $i -lt $steps; $i++) {
    [CapX]::mouse_event(0x0800, 0, 0, $delta, [IntPtr]::Zero)
    Start-Sleep -Milliseconds 90
  }
  Start-Sleep -Milliseconds 400
}

if ($Click) {
  $xy = $Click.Split(',')
  [CapX]::SetCursorPos($r.Left + [int]$xy[0], $r.Top + [int]$xy[1]) | Out-Null
  Start-Sleep -Milliseconds 150
  [CapX]::mouse_event(0x0002, 0, 0, 0, [IntPtr]::Zero)
  [CapX]::mouse_event(0x0004, 0, 0, 0, [IntPtr]::Zero)
  Start-Sleep -Milliseconds 600
}

if ($Hover) {
  $xy = $Hover.Split(',')
  [CapX]::SetCursorPos($r.Left + [int]$xy[0], $r.Top + [int]$xy[1]) | Out-Null
  Start-Sleep -Milliseconds 120
  # A tiny real movement makes the webview emit mouseover/hover events; SetCursorPos
  # alone can leave the page without a hover state.
  [CapX]::mouse_event(0x0001, 1, 0, 0, [IntPtr]::Zero)
  Start-Sleep -Milliseconds 60
  [CapX]::mouse_event(0x0001, -1, 0, 0, [IntPtr]::Zero)
  Start-Sleep -Milliseconds 60
  [CapX]::mouse_event(0x0001, 0, 1, 0, [IntPtr]::Zero)
  Start-Sleep -Milliseconds 60
  [CapX]::mouse_event(0x0001, 0, -1, 0, [IntPtr]::Zero)
  Start-Sleep -Milliseconds 900
}

Start-Sleep -Milliseconds $WaitMs
$bmp = New-Object System.Drawing.Bitmap $w, $ht
$g = [System.Drawing.Graphics]::FromImage($bmp)
$g.CopyFromScreen($r.Left, $r.Top, 0, 0, $bmp.Size)
$g.Dispose()
$path = "E:\default\clipnest\scratch\$Out"
$bmp.Save($path, [System.Drawing.Imaging.ImageFormat]::Png)
$bmp.Dispose()
Write-Host "saved $path"
