# Gate 2.5：Wails 版"弹窗闲置即销毁"的开关验证 + 对照测量
#
#   pwsh -File scratch\gate25-idle.ps1
#
# 它做两次同样的流程（一次关着开关、一次开着），比三组数：
#   1) 启动后的常驻内存（进程树总和）
#   2) 热键 → picker 可见的延迟（冷/热）
#   3) 闲置销毁后 WebView2 进程数 / 内存
#
# 收尾一定会把安装版拉回来（try/finally）。

param(
    [string]$Root = "E:\default\clipnest\.agents\worktrees\gate2-5-idle-windows"
)
$ErrorActionPreference = "Stop"
function Say([string]$m) { Write-Host $m }
Add-Type @'
using System;using System.Text;using System.Runtime.InteropServices;
public class GW {
  [DllImport("user32.dll")] public static extern bool EnumWindows(P cb, IntPtr l); public delegate bool P(IntPtr h, IntPtr l);
  [DllImport("user32.dll")] public static extern uint GetWindowThreadProcessId(IntPtr h, out uint pid);
  [DllImport("user32.dll")] public static extern int GetWindowText(IntPtr h, StringBuilder s, int n);
  [DllImport("user32.dll")] public static extern bool IsWindowVisible(IntPtr h);
  [DllImport("user32.dll")] public static extern void keybd_event(byte vk, byte scan, uint flags, UIntPtr e);
  [DllImport("user32.dll")] public static extern bool PostMessage(IntPtr h,uint m,IntPtr w,IntPtr l);
  public static IntPtr Find(uint pid,string title){ IntPtr f=IntPtr.Zero; EnumWindows((h,l)=>{uint p;GetWindowThreadProcessId(h,out p);if(p==pid){var sb=new StringBuilder(256);GetWindowText(h,sb,256);if(sb.ToString()==title){f=h;return false;}}return true;},IntPtr.Zero);return f; }
  public static void Key(byte vk,bool up){ keybd_event(vk,0,up?2u:0u,UIntPtr.Zero); }
  public static void Hot(byte letter){ Key(0x11,false);Key(0x10,false);Key(letter,false);Key(letter,true);Key(0x10,true);Key(0x11,true); }
  public static void WmClose(IntPtr h){ PostMessage(h,0x0010,IntPtr.Zero,IntPtr.Zero); }
}
'@

$devExe      = Join-Path $Root "bin\clipnest-dev.exe"
$installed   = "C:\Program Files\bbqisbbq\ClipNest\clipnest.exe"
$sandboxBase = Join-Path $Root "scratch\appdata-idle"

function Stop-All {
    Get-Process clipnest -ErrorAction SilentlyContinue | ForEach-Object { Stop-Process -Id $_.Id -Force }
    Start-Sleep -Milliseconds 800
}

function Start-Dev([bool]$idleDestroy, [int]$graceMs) {
    $env:APPDATA = $sandboxBase
    if ($idleDestroy) { $env:CLIPNEST_IDLE_DESTROY = "1"; $env:CLIPNEST_IDLE_MS = "$graceMs" }
    else { Remove-Item Env:\CLIPNEST_IDLE_DESTROY -ErrorAction SilentlyContinue; Remove-Item Env:\CLIPNEST_IDLE_MS -ErrorAction SilentlyContinue }
    $p = Start-Process -FilePath $devExe -ArgumentList "--background" -PassThru
    Start-Sleep -Milliseconds 2500
    return $p
}

function Tree-Memory([int]$rootPid) {
    $procs = Get-CimInstance Win32_Process | Select-Object ProcessId, ParentProcessId, Name
    $map = @{}; foreach ($x in $procs) { $map[[int]$x.ProcessId] = $x }
    function Anc([int]$id) { $c=@(); $cur=$id; for($i=0;$i -lt 10;$i++){ if(-not $map.ContainsKey($cur)){break}; $par=[int]$map[$cur].ParentProcessId; if($par -eq 0 -or $par -eq $cur){break}; $c+=$par; $cur=$par }; $c }
    $ids = @($rootPid)
    foreach ($x in $procs) {
        if ($x.Name -ne 'msedgewebview2.exe') { continue }
        if ((Anc ([int]$x.ProcessId)) -contains $rootPid) { $ids += [int]$x.ProcessId }
    }
    $g = Get-Process -Id $ids -ErrorAction SilentlyContinue
    $renderers = @($procs | Where-Object { $_.Name -eq 'msedgewebview2.exe' -and ((Anc ([int]$_.ProcessId)) -contains $rootPid) }).Count
    return [pscustomobject]@{
        procs = $g.Count
        ws    = [math]::Round((($g | Measure-Object WorkingSet64 -Sum).Sum)/1MB,1)
        priv  = [math]::Round((($g | Measure-Object PrivateMemorySize64 -Sum).Sum)/1MB,1)
    }
}

function Picker-Latency([int]$pid2, [int]$rounds = 3, [int]$graceSettleMs = 0) {
    $lat = @()
    for ($i = 0; $i -lt $rounds; $i++) {
        # 懒建的窗口此刻可能根本不存在：先按热键，再等"窗口出现且可见"
        $h = [GW]::Find([uint32]$pid2, "ClipNest")
        if ($h -ne [IntPtr]::Zero -and [GW]::IsWindowVisible($h)) { [GW]::WmClose($h); Start-Sleep -Milliseconds 600 }
        if ($graceSettleMs -gt 0) { Start-Sleep -Milliseconds $graceSettleMs }
        $sw = [Diagnostics.Stopwatch]::StartNew()
        [GW]::Hot(0x56)  # Ctrl+Shift+V
        $seen = -1.0
        while ($sw.Elapsed.TotalMilliseconds -lt 8000) {
            $h = [GW]::Find([uint32]$pid2, "ClipNest")
            if ($h -ne [IntPtr]::Zero -and [GW]::IsWindowVisible($h)) { $seen = $sw.Elapsed.TotalMilliseconds; break }
            [Threading.Thread]::Sleep(1)
        }
        $lat += [math]::Round($seen,1)
        if ($h -ne [IntPtr]::Zero) { [GW]::WmClose($h) }   # 关闭请求 → 隐藏（+ 闲置时销毁）
        Start-Sleep -Milliseconds 500
    }
    return $lat
}

$out = [ordered]@{}
try {
    Say "=== A. 现状（不开开关）==="
    Stop-All
    $a = Start-Dev $false 0
    $memA = Tree-Memory $a.Id
    Say ("  常驻：{0} 进程  WS={1}MB  私有={2}MB" -f $memA.procs, $memA.ws, $memA.priv)
    $latA = Picker-Latency $a.Id 3
    Say ("  picker 热键→可见：{0} ms" -f ($latA -join ", "))
    Start-Sleep -Milliseconds 2500
    $memA2 = Tree-Memory $a.Id
    Say ("  闲置 2.5s 后：{0} 进程  WS={1}MB" -f $memA2.procs, $memA2.ws)
    Stop-Process -Id $a.Id -Force; Start-Sleep -Milliseconds 800
    $out["baseline"] = @{ mem = $memA; mem_idle = $memA2; latency = $latA }

    Say "=== B. 开 CLIPNEST_IDLE_DESTROY=1（grace 600ms）==="
    $b = Start-Dev $true 600
    $memB = Tree-Memory $b.Id
    Say ("  启动后（只有主窗）：{0} 进程  WS={1}MB  私有={2}MB" -f $memB.procs, $memB.ws, $memB.priv)
    $latB = Picker-Latency $b.Id 3 900
    Say ("  picker 热键→可见（第 1 次要重建，后面是冷页面）：{0} ms" -f ($latB -join ", "))
    Start-Sleep -Milliseconds 3500
    $memB2 = Tree-Memory $b.Id
    Say ("  闲置 3s 后（应已销毁）：{0} 进程  WS={1}MB" -f $memB2.procs, $memB2.ws)
    $logPath = Join-Path $sandboxBase "ClipNest\clipnest.log"
    if (Test-Path $logPath) {
        $destroyed = @(Get-Content $logPath | Select-String -Pattern "idle: (picker|capture|shot) window destroyed")
        Say ("  日志里销毁记录：{0} 条" -f $destroyed.Count)
    }
    Stop-Process -Id $b.Id -Force; Start-Sleep -Milliseconds 800
    $out["idle_destroy"] = @{ mem = $memB; mem_idle = $memB2; latency = $latB }

    Say "=== C. 只销毁 capture/shot，picker 常驻（CLIPNEST_IDLE_SKIP=picker）==="
    Stop-All
    $env:APPDATA = $sandboxBase
    $env:CLIPNEST_IDLE_DESTROY = "1"; $env:CLIPNEST_IDLE_MS = "600"; $env:CLIPNEST_IDLE_SKIP = "picker"
    $c = Start-Process -FilePath $devExe -ArgumentList "--background" -PassThru
    Start-Sleep -Milliseconds 2500
    $memC = Tree-Memory $c.Id
    Say ("  常驻：{0} 进程  WS={1}MB  私有={2}MB" -f $memC.procs, $memC.ws, $memC.priv)
    $latC = Picker-Latency $c.Id 3
    Say ("  picker 热键→可见：{0} ms" -f ($latC -join ", "))
    Start-Sleep -Milliseconds 2500
    $memC2 = Tree-Memory $c.Id
    Say ("  闲置后：{0} 进程  WS={1}MB" -f $memC2.procs, $memC2.ws)
    Stop-Process -Id $c.Id -Force; Start-Sleep -Milliseconds 800
    $out["keep_picker"] = @{ mem = $memC; mem_idle = $memC2; latency = $latC }
}
finally {
    Stop-All
    Remove-Item Env:\CLIPNEST_IDLE_DESTROY -ErrorAction SilentlyContinue
    Remove-Item Env:\CLIPNEST_IDLE_MS -ErrorAction SilentlyContinue
    Remove-Item Env:\CLIPNEST_IDLE_SKIP -ErrorAction SilentlyContinue
    $env:APPDATA = "$env:USERPROFILE\AppData\Roaming"
    if (Test-Path $installed) { Start-Process -FilePath $installed -ArgumentList "--background" | Out-Null; Say "  已把安装版拉回来" }
}

$out | ConvertTo-Json -Depth 5 | Set-Content (Join-Path $Root "scratch\gate25-result.json") -Encoding utf8
Say "=== 汇总 ==="
Say ("  基线：  WS {0}MB / {1} 进程；picker {2}ms" -f $out["baseline"].mem.ws, $out["baseline"].mem.procs, (($out["baseline"].latency) -join "/"))
Say ("  全销毁：WS {0}MB / {1} 进程；picker {2}ms" -f $out["idle_destroy"].mem.ws, $out["idle_destroy"].mem.procs, (($out["idle_destroy"].latency) -join "/"))
Say ("  留 picker：WS {0}MB / {1} 进程；picker {2}ms" -f $out["keep_picker"].mem.ws, $out["keep_picker"].mem.procs, (($out["keep_picker"].latency) -join "/"))



