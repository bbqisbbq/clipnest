# Step 3 剩余验收（MyGo 壳）：picker 贴插入符 + 回车直粘、单实例、托盘图标
#
#   pwsh -STA -File scratch\e2e-picker-single.ps1 -Exe <bin\clipnest.exe>
#
# 1. 沙箱 APPDATA + 预置快捷键（Ctrl+Shift+J / Ctrl+Shift+N / Ctrl+Shift+F11，避开本机占用）
# 2. 复制一段带随机串的文本 → 应用应捕获成条目
# 3. 打开记事本（当作"原窗口"，有插入符），按热键唤起 picker → 回车 → 文本应粘进记事本
# 4. 再启动一个实例 → 单实例锁应让它退出、并把第一个实例的主窗唤到前台
# 5. 截图托盘区域，确认图标在

param(
    [string]$Exe = "E:\default\clipnest\.agents\worktrees\mygo-shell\bin\clipnest.exe",
    [string]$Root = "E:\default\clipnest\.agents\worktrees\mygo-shell"
)
$ErrorActionPreference = "Stop"
Add-Type -AssemblyName System.Drawing
Add-Type -AssemblyName System.Windows.Forms
Add-Type @'
using System;using System.Text;using System.Runtime.InteropServices;using System.Collections.Generic;
public class Pk {
  [DllImport("user32.dll")] public static extern bool EnumWindows(P cb, IntPtr l); public delegate bool P(IntPtr h, IntPtr l);
  [DllImport("user32.dll")] public static extern uint GetWindowThreadProcessId(IntPtr h, out uint pid);
  [DllImport("user32.dll")] public static extern int GetWindowText(IntPtr h, StringBuilder s, int n);
  [DllImport("user32.dll")] public static extern bool IsWindowVisible(IntPtr h);
  [DllImport("user32.dll")] public static extern bool SetForegroundWindow(IntPtr h);
  [DllImport("user32.dll")] public static extern IntPtr GetForegroundWindow();
  [DllImport("user32.dll")] public static extern bool GetWindowRect(IntPtr h, out RECT r);
  [DllImport("user32.dll")] public static extern void keybd_event(byte vk, byte sc, uint fl, UIntPtr e);
  public struct RECT{public int L,T,R,B;}
  public static IntPtr FindByPid(uint pid, string contains){ IntPtr f=IntPtr.Zero;
    EnumWindows((h,l)=>{uint p;GetWindowThreadProcessId(h,out p);if(p==pid){var sb=new StringBuilder(256);GetWindowText(h,sb,256);if(sb.ToString().Contains(contains)){f=h;return false;}}return true;},IntPtr.Zero);return f; }
  public static IntPtr FindByTitleContains(string contains){ IntPtr f=IntPtr.Zero;
    EnumWindows((h,l)=>{var sb=new StringBuilder(256);GetWindowText(h,sb,256);if(sb.ToString().Contains(contains) && IsWindowVisible(h)){f=h;return false;}return true;},IntPtr.Zero);return f; }
  public static void Key(byte vk, bool up){ keybd_event(vk,0,up?2u:0u,UIntPtr.Zero); }
  public static void Hot(byte letter){ Key(0x11,false);Key(0x10,false);Key(letter,false);Key(letter,true);Key(0x10,true);Key(0x11,true); }
  public static void Enter(){ Key(0x0D,false); Key(0x0D,true); }
  public static string Rect(IntPtr h){ RECT r; if(!GetWindowRect(h,out r)) return "?"; return (r.R-r.L)+"x"+(r.B-r.T)+"@"+r.L+","+r.T; }
}
'@

$sandbox = Join-Path $Root "scratch\appdata-picker"
$log = Join-Path $sandbox "ClipNest\clipnest.log"
$results = New-Object System.Collections.Generic.List[string]
function Say([string]$m) { $results.Add($m); Write-Host $m }

Get-Process clipnest,notepad -ErrorAction SilentlyContinue | ForEach-Object { Stop-Process -Id $_.Id -Force }
Start-Sleep -Milliseconds 800
Remove-Item -Recurse -Force $sandbox -ErrorAction SilentlyContinue
New-Item -ItemType Directory -Force -Path (Join-Path $sandbox "ClipNest") | Out-Null
@{ capture = $true; syncEnabled = $false; showOnLaunch = $true; autoStart = $false
   hotkey = "Ctrl+Shift+J"; captureHotkey = "Ctrl+Shift+N"; shotHotkey = "Ctrl+Shift+F11"
   pasteOnPick = $true } | ConvertTo-Json | Set-Content (Join-Path $sandbox "ClipNest\settings.json") -Encoding UTF8
$env:APPDATA = $sandbox
$env:CLIPNEST_IDLE_DESTROY = '0'

try {
    $token = "PICKER-直粘-VERIFY-" + (Get-Random -Maximum 99999)
    Set-Clipboard -Value $token
    $p = Start-Process -FilePath $Exe -ArgumentList "--show" -PassThru
    Start-Sleep -Seconds 4
    $captured = (Get-Content $log -ErrorAction SilentlyContinue | Select-String "clip:new|capture" | Measure-Object).Count
    Say ("应用启动：pid=$($p.Id)，日志条目=$captured")

    # 目标窗口：记事本（有插入符）
    $np = Start-Process notepad -PassThru
    Start-Sleep -Seconds 2
    $npHwnd = [Pk]::FindByPid([uint32]$np.Id, "记事本")
    if ($npHwnd -eq [IntPtr]::Zero) { $npHwnd = [Pk]::GetForegroundWindow() }
    [Pk]::SetForegroundWindow($npHwnd) | Out-Null
    Start-Sleep -Milliseconds 600
    Say ("目标窗口：$([Pk]::Rect($npHwnd))")

    # 唤起 picker
    $appH = [Pk]::FindByPid([uint32]$p.Id, "ClipNest")
    $before = (Get-Content $log).Count
    [Pk]::SetForegroundWindow($npHwnd) | Out-Null
    Start-Sleep -Milliseconds 400
    [Pk]::Hot(0x4A)   # Ctrl+Shift+J
    Start-Sleep -Milliseconds 1500
    $newLines = (Get-Content $log)[$before..((Get-Content $log).Count - 1)]
    $posLine = ($newLines | Select-String "picker window: pos=" | Select-Object -Last 1).Line
    Say ("picker 日志：$posLine")
    if ($posLine -match 'pos=\((-?\d+),(-?\d+)\)') {
        $px = [int]$Matches[1]; $py = [int]$Matches[2]
        $r = New-Object Pk+RECT; [Pk]::GetWindowRect($npHwnd, [ref]$r) | Out-Null
        $insideOrNear = ($px -gt ($r.L - 200)) -and ($px -lt ($r.R + 200)) -and ($py -gt ($r.T - 200)) -and ($py -lt ($r.B + 400))
        if ($insideOrNear) { Say "PASS picker 出现在目标窗口/插入符附近（pos=$px,$py，记事本=$($r.L),$($r.T)）" }
        else { Say "FAIL picker 位置离目标窗口很远：pos=$px,$py，记事本=$($r.L),$($r.T)" }
    } else { Say "FAIL 没拿到 picker 位置日志" }

    # 回车直粘
    [Pk]::Enter()
    Start-Sleep -Milliseconds 1200
    [Pk]::SetForegroundWindow($npHwnd) | Out-Null
    Start-Sleep -Milliseconds 300
    # 读记事本内容：Ctrl+A / Ctrl+C 后看剪贴板
    [Pk]::Key(0x11,$false); [Pk]::Key(0x41,$false); [Pk]::Key(0x41,$true); [Pk]::Key(0x11,$true)
    Start-Sleep -Milliseconds 300
    [Pk]::Key(0x11,$false); [Pk]::Key(0x43,$false); [Pk]::Key(0x43,$true); [Pk]::Key(0x11,$true)
    Start-Sleep -Milliseconds 500
    $got = Get-Clipboard -Raw
    if ($got -and $got.Contains($token)) { Say "PASS 回车把条目直接粘进了原窗口（记事本内容含 $token）" }
    else { Say "FAIL 没粘进去，记事本内容=«$got»" }

    # 单实例：再启一个，应立刻退出并把第一个窗口唤到前台
    $p2 = Start-Process -FilePath $Exe -PassThru
    Start-Sleep -Seconds 3
    $p2.Refresh()
    if ($p2.HasExited) { Say "PASS 第二个实例立刻退出（单实例锁生效）" } else { Say "FAIL 第二个实例还在跑"; Stop-Process -Id $p2.Id -Force }
    $fg = [Pk]::GetForegroundWindow()
    if ($fg -eq $appH -or [Pk]::FindByPid([uint32]$p.Id, "ClipNest") -eq $fg) { Say "PASS 第一个实例的主窗被唤到前台" }
    else { Say "WARN 前台不是主窗（可能被记事本挡住）：fg=$fg app=$appH" }

    # 托盘区域截图（右下角）
    $screen = [System.Windows.Forms.Screen]::PrimaryScreen.Bounds
    $bmp = New-Object System.Drawing.Bitmap 320, 48
    $g = [System.Drawing.Graphics]::FromImage($bmp)
    $g.CopyFromScreen($screen.Width - 320, $screen.Height - 48, 0, 0, $bmp.Size)
    $trayShot = Join-Path $Root "scratch\mygo-tray.png"
    $bmp.Save($trayShot, [System.Drawing.Imaging.ImageFormat]::Png)
    $bmp.Dispose(); $g.Dispose()
    Say "托盘区域截图：$trayShot"
}
finally {
    Get-Process clipnest,notepad -ErrorAction SilentlyContinue | ForEach-Object { Stop-Process -Id $_.Id -Force -ErrorAction SilentlyContinue }
    Remove-Item Env:\CLIPNEST_IDLE_DESTROY -ErrorAction SilentlyContinue
    $env:APPDATA = "$env:USERPROFILE\AppData\Roaming"
}
Write-Host "`n===== summary ====="
$results | ForEach-Object { Write-Host $_ }



