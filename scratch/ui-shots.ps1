# Drives the running ClipNest window for visual verification:
#   1. seeds clipboard content of every kind
#   2. screenshots the history view
#   3. clicks "设置" and screenshots the settings view
# Output: scratch/shot-*.png
Add-Type -AssemblyName System.Drawing
Add-Type -AssemblyName System.Windows.Forms
Add-Type @'
using System;
using System.Runtime.InteropServices;
public class W {
  [StructLayout(LayoutKind.Sequential)] public struct RECT { public int Left, Top, Right, Bottom; }
  [DllImport("user32.dll")] public static extern bool GetWindowRect(IntPtr h, out RECT r);
  [DllImport("user32.dll")] public static extern bool SetForegroundWindow(IntPtr h);
  [DllImport("user32.dll")] public static extern bool ShowWindow(IntPtr h, int c);
  [DllImport("user32.dll")] public static extern bool SetCursorPos(int x, int y);
  [DllImport("user32.dll")] public static extern void mouse_event(uint f, uint dx, uint dy, uint d, IntPtr e);
}
'@

$root = 'E:\default\clipnest'
$proc = Get-Process clipnest -ErrorAction SilentlyContinue | Select-Object -First 1
if (-not $proc) { throw 'clipnest is not running' }
$h = $proc.MainWindowHandle
[W]::ShowWindow($h, 9) | Out-Null
[W]::SetForegroundWindow($h) | Out-Null
Start-Sleep -Milliseconds 700

function Shot([string]$name) {
  $r = New-Object W+RECT
  [W]::GetWindowRect($h, [ref]$r) | Out-Null
  $w = $r.Right - $r.Left; $ht = $r.Bottom - $r.Top
  $bmp = New-Object System.Drawing.Bitmap $w, $ht
  $g = [System.Drawing.Graphics]::FromImage($bmp)
  $g.CopyFromScreen($r.Left, $r.Top, 0, 0, $bmp.Size)
  $g.Dispose()
  $path = Join-Path $root "scratch\$name.png"
  $bmp.Save($path, [System.Drawing.Imaging.ImageFormat]::Png)
  $bmp.Dispose()
  Write-Host "saved $path"
  return [pscustomobject]@{ Left = $r.Left; Top = $r.Top; Width = $w; Height = $ht }
}

function ClickAt([int]$x, [int]$y) {
  [W]::SetCursorPos($x, $y) | Out-Null
  Start-Sleep -Milliseconds 120
  [W]::mouse_event(0x0002, 0, 0, 0, [IntPtr]::Zero)
  [W]::mouse_event(0x0004, 0, 0, 0, [IntPtr]::Zero)
  Start-Sleep -Milliseconds 450
}

$rect = Shot 'shot-history'
Write-Host "rect: $($rect.Left),$($rect.Top) $($rect.Width)x$($rect.Height)"

# 设置 is the last item in the sidebar footer.
ClickAt ($rect.Left + 63) ($rect.Top + 699)
[System.Windows.Forms.SendKeys]::SendWait('{PGUP}{PGUP}{PGUP}') | Out-Null
Start-Sleep -Milliseconds 500
Shot 'shot-settings' | Out-Null

# Back to the history list and open an entry's detail.
ClickAt ($rect.Left + 63) ($rect.Top + 131)
ClickAt ($rect.Left + 460) ($rect.Top + 190)
Shot 'shot-detail' | Out-Null
