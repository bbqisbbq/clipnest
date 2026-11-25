# 默认配置（不开任何环境变量）下的验证：picker 预建是否生效、capture 用完是否被销毁
#
#   pwsh -File scratch\gate25-default.ps1

param([string]$Root = "E:\default\clipnest\.agents\worktrees\gate2-5-idle-windows", [string]$Exe = "")
$ErrorActionPreference = "Stop"
function Say([string]$m) { Write-Host $m }

Add-Type @'
using System;using System.Text;using System.Runtime.InteropServices;
public class DF {
  [DllImport("user32.dll")] public static extern bool EnumWindows(P cb, IntPtr l); public delegate bool P(IntPtr h, IntPtr l);
  [DllImport("user32.dll")] public static extern uint GetWindowThreadProcessId(IntPtr h, out uint pid);
  [DllImport("user32.dll")] public static extern int GetWindowText(IntPtr h, StringBuilder s, int n);
  [DllImport("user32.dll")] public static extern bool IsWindowVisible(IntPtr h);
  [DllImport("user32.dll")] public static extern void keybd_event(byte vk, byte scan, uint flags, UIntPtr e);
  public static IntPtr Find(uint pid,string title){ IntPtr f=IntPtr.Zero; EnumWindows((h,l)=>{uint p;GetWindowThreadProcessId(h,out p);if(p==pid){var sb=new StringBuilder(256);GetWindowText(h,sb,256);if(sb.ToString()==title){f=h;return false;}}return true;},IntPtr.Zero);return f; }
  public static void Key(byte vk,bool up){ keybd_event(vk,0,up?2u:0u,UIntPtr.Zero); }
  public static void Hot(byte letter){ Key(0x11,false);Key(0x10,false);Key(letter,false);Key(letter,true);Key(0x10,true);Key(0x11,true); }
  public static void Esc(){ Key(0x1B,false); Key(0x1B,true); }
}
'@

$devExe = if ($Exe) { $Exe } else { Join-Path $Root "bin\clipnest-dev.exe" }
$installed = "C:\Program Files\bbqisbbq\ClipNest\clipnest.exe"
$sandbox = Join-Path $Root "scratch\appdata-default"

function Tree([int]$pid2) {
  $procs = Get-CimInstance Win32_Process | Select-Object ProcessId, ParentProcessId, Name
  $map = @{}; foreach ($x in $procs) { $map[[int]$x.ProcessId] = $x }
  function Anc([int]$id) { $c=@(); $cur=$id; for($i=0;$i -lt 10;$i++){ if(-not $map.ContainsKey($cur)){break}; $par=[int]$map[$cur].ParentProcessId; if($par -eq 0 -or $par -eq $cur){break}; $c+=$par; $cur=$par }; $c }
  function Kids([int]$id) { $c=@($id); $grown=$true; while($grown){ $grown=$false; foreach($x in $procs){ if($c -contains [int]$x.ParentProcessId -and -not ($c -contains [int]$x.ProcessId)){ $c += [int]$x.ProcessId; $grown=$true } } }; $c }
  $ids = Kids $pid2
  $g = Get-Process -Id $ids -ErrorAction SilentlyContinue
  return [pscustomobject]@{ procs=$g.Count; ws=[math]::Round((($g|Measure-Object WorkingSet64 -Sum).Sum)/1MB,1); priv=[math]::Round((($g|Measure-Object PrivateMemorySize64 -Sum).Sum)/1MB,1) }
}

Get-Process clipnest -ErrorAction SilentlyContinue | ForEach-Object { Stop-Process -Id $_.Id -Force }
Start-Sleep -Milliseconds 800
Remove-Item -Recurse -Force $sandbox -ErrorAction SilentlyContinue
$env:APPDATA = $sandbox
Remove-Item Env:\CLIPNEST_IDLE_DESTROY -ErrorAction SilentlyContinue
Remove-Item Env:\CLIPNEST_IDLE_MS -ErrorAction SilentlyContinue
Remove-Item Env:\CLIPNEST_IDLE_SKIP -ErrorAction SilentlyContinue
$p = Start-Process -FilePath $devExe -ArgumentList "--background" -PassThru
try {
    Start-Sleep -Milliseconds 1500
    $m1 = Tree $p.Id
    Say ("启动 1.5s（picker 还没预建）：{0} 进程 WS={1}MB 私有={2}MB" -f $m1.procs, $m1.ws, $m1.priv)
    Start-Sleep -Milliseconds 3000
    $m2 = Tree $p.Id
    Say ("启动 4.5s（picker 已预建）：  {0} 进程 WS={1}MB 私有={2}MB" -f $m2.procs, $m2.ws, $m2.priv)

    # picker 延迟：预建过，应该和"常驻隐藏"一样快
    $h = [DF]::Find([uint32]$p.Id, "ClipNest")
    $lat = @()
    for ($i=0; $i -lt 3; $i++) {
        if ([DF]::IsWindowVisible($h)) { [DF]::Hot(0x56); Start-Sleep -Milliseconds 400 }
        $sw=[Diagnostics.Stopwatch]::StartNew(); [DF]::Hot(0x56)
        while ($sw.Elapsed.TotalMilliseconds -lt 5000 -and -not [DF]::IsWindowVisible($h)) { [Threading.Thread]::Sleep(1) }
        $lat += [math]::Round($sw.Elapsed.TotalMilliseconds,1)
        [DF]::Hot(0x56); Start-Sleep -Milliseconds 400   # 再按一次收起
        if ([DF]::IsWindowVisible($h)) { [DF]::Esc(); Start-Sleep -Milliseconds 300 }
    }
    Say ("picker 热键→可见：{0} ms" -f ($lat -join ", "))

    # capture：唤起一次 → Esc 收起 → 等闲置销毁
    [DF]::Hot(0x4E)   # Ctrl+Shift+N
    Start-Sleep -Milliseconds 900
    $hc = [DF]::Find([uint32]$p.Id, "记一笔")
    Say ("记一笔 vis=$([DF]::IsWindowVisible($hc))")
    [DF]::Esc(); Start-Sleep -Milliseconds 400
    if ([DF]::IsWindowVisible($hc)) { [DF]::Esc(); Start-Sleep -Milliseconds 300 }
    $m3 = Tree $p.Id
    Say ("用完立刻：{0} 进程 WS={1}MB" -f $m3.procs, $m3.ws)
    Start-Sleep -Milliseconds 2500
    $m4 = Tree $p.Id
    Say ("闲置 2.5s 后（capture 应已销毁）：{0} 进程 WS={1}MB" -f $m4.procs, $m4.ws)
    $logPath = Join-Path $sandbox "ClipNest\clipnest.log"
    if (Test-Path $logPath) {
        $d = @(Get-Content $logPath | Select-String -Pattern "idle: \w+ window destroyed")
        Say ("销毁日志 {0} 条：{1}" -f $d.Count, (($d | ForEach-Object { ($_ -split 'idle: ')[1] }) -join "; "))
    }
}
finally {
    Stop-Process -Id $p.Id -Force -ErrorAction SilentlyContinue
    $env:APPDATA = "$env:USERPROFILE\AppData\Roaming"
    if (Test-Path $installed) { Start-Process -FilePath $installed -ArgumentList "--background" | Out-Null; Say "已把安装版拉回来" }
}
