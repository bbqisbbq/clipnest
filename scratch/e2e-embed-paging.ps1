param([string]$Exe = '')
# 语义搜索分页的回归验证（2.13.2）：搜完之后**条数不许越滚越多**。
#
# 复现的是用户报的："搜一句，右上角显示 560+ 条，可库里只有 82 条"。
# 根因：applyHybrid 把 Before 游标清掉了，而 ListEntries 正是靠"ranked 且带游标
# → 返回空"来喊停；于是每次"上滑加载更早"都拿到同一页，前端 lại 不去重 → 无限累加。
#
# 判据（读 .memo-count 的文本，不靠看图）：
#   1. 搜完之后条数 ≤ 50（一页）；
#   2. 连续触发 4 次"加载更早"之后，条数**不变**。
#
# 用法（必须 -STA）：pwsh -STA -NoProfile -File scratch\e2e-embed-paging.ps1
Add-Type -AssemblyName System.Drawing
Add-Type -AssemblyName System.Windows.Forms
Add-Type @'
using System;
using System.Runtime.InteropServices;
public class EPg {
  [StructLayout(LayoutKind.Sequential)] public struct RECT { public int Left, Top, Right, Bottom; }
  [DllImport("user32.dll")] public static extern bool GetWindowRect(IntPtr h, out RECT r);
  [DllImport("user32.dll")] public static extern bool SetForegroundWindow(IntPtr h);
  [DllImport("user32.dll")] public static extern bool IsWindowVisible(IntPtr h);
  [DllImport("user32.dll")] public static extern IntPtr GetForegroundWindow();
  [DllImport("user32.dll")] public static extern bool EnumWindows(EnumProc cb, IntPtr p);
  [DllImport("user32.dll")] public static extern uint GetWindowThreadProcessId(IntPtr h, out uint pid);
  public delegate bool EnumProc(IntPtr h, IntPtr p);
}
'@

$ErrorActionPreference = 'Stop'
$root = 'E:\default\clipnest'
$sandbox = Join-Path $root 'scratch\appdata-embed-fix'
$shots = Join-Path $root 'scratch\e2e-shots'
$devExe = if ($Exe) { Join-Path $root $Exe } else { Join-Path $root 'bin\clipnest-dev.exe' }
$procName = [IO.Path]::GetFileNameWithoutExtension($devExe)
New-Item -ItemType Directory -Force -Path $shots | Out-Null
if (-not (Test-Path (Join-Path $sandbox 'ClipNest\clipnest.db'))) {
  Write-Host "E2E-RESULT: SKIP — 没有沙箱数据（$sandbox）"
  exit 1
}

$env:CLIPNEST_IDLE_DESTROY = '0'
$env:APPDATA = $sandbox

function Wait-For([scriptblock]$cond, [int]$timeoutMs = 10000) {
  $sw = [Diagnostics.Stopwatch]::StartNew()
  while ($sw.ElapsedMilliseconds -lt $timeoutMs) {
    $v = & $cond
    if ($null -ne $v -and $v -ne $false) { return $v }
    Start-Sleep -Milliseconds 150
  }
  return $null
}
function Stop-AllApp {
  Get-Process | Where-Object { $_.ProcessName -like 'clipnest*' } | ForEach-Object {
    try { $_.Kill(); $_.WaitForExit(3000) | Out-Null } catch {}
  }
  Start-Sleep -Milliseconds 800
}
function Get-MainHwnd([int]$thePid) {
  $script:hits = New-Object System.Collections.Generic.List[object]
  $cb = [EPg+EnumProc]{
    param($h, $p)
    $wpid = 0
    [EPg]::GetWindowThreadProcessId($h, [ref]$wpid) | Out-Null
    if ($wpid -eq $thePid -and [EPg]::IsWindowVisible($h)) {
      $r = New-Object EPg+RECT
      if ([EPg]::GetWindowRect($h, [ref]$r)) {
        $script:hits.Add([pscustomobject]@{ H = $h; A = ($r.Right - $r.Left) * ($r.Bottom - $r.Top) })
      }
    }
    return $true
  }
  [EPg]::EnumWindows($cb, [IntPtr]::Zero) | Out-Null
  $best = $script:hits | Sort-Object A -Descending | Select-Object -First 1
  if (-not $best) { return [IntPtr]::Zero }
  return $best.H
}

# 页面里干三件事：搜一句 → 读条数 → 连点四次"加载更早" → 再读条数。
# 结果写进 document.title，让 9 秒那次 EVAL_JS2 把它读回来（EVAL_JS 只返回一次，
# 而我们要读的是"过了一会儿之后"的值）。
$js = @'
(()=>{setTimeout(()=>{
  const input=document.querySelector('.memo-head input.input');
  if(!input) return;
  const set=Object.getOwnPropertyDescriptor(window.HTMLInputElement.prototype,'value').set;
  set.call(input,'打包');
  input.dispatchEvent(new Event('input',{bubbles:true}));
  setTimeout(()=>{
    const read=()=>{const el=document.querySelector('.memo-count');return el?el.textContent.replace(/\s+/g,''):'?'};
    const first=read();
    const tl=document.querySelector('.memo-timeline');
    let n=0;
    const tick=()=>{
      if(tl){tl.scrollTop=0;tl.dispatchEvent(new Event('scroll'));}
      if(++n<4){setTimeout(tick,450);return;}
      document.body.dataset.paging='PAGING|'+first+'|'+read();
    };
    tick();
  },900);
},800);return 'scheduled';})()
'@
$env:CLIPNEST_EVAL_JS = $js -replace "`r?`n", ""
$env:CLIPNEST_EVAL_JS2 = "(document.body.dataset.paging||'none')"

Write-Host '=== 起应用（沙箱：81 条向量已就绪）==='
Stop-AllApp
# 沙箱里那份 settings.json 是从真实库拷来的（showOnLaunch=false），改一下免得窗口不出现。
# **顺手必须把同步关掉**：拷来的那个文件带着真实 WebDAV 账号，测试实例一起跑就会
# 真的往用户云端推包（踩过：6 次 push 把用户线上的包换成了沙箱库打的包）。
$settingsPath = Join-Path $sandbox 'ClipNest\settings.json'
$cfg = Get-Content $settingsPath -Raw | ConvertFrom-Json
$cfg | Add-Member -NotePropertyName showOnLaunch -NotePropertyValue $true -Force
$cfg | Add-Member -NotePropertyName syncEnabled -NotePropertyValue $false -Force
$cfg | Add-Member -NotePropertyName syncUrl -NotePropertyValue '' -Force
$cfg | Add-Member -NotePropertyName syncUser -NotePropertyValue '' -Force
$cfg | Add-Member -NotePropertyName syncPass -NotePropertyValue '' -Force
$cfg | ConvertTo-Json -Depth 6 | Set-Content $settingsPath -Encoding utf8
Start-Process -FilePath $devExe | Out-Null
if (-not (Wait-For { Get-Process $procName -ErrorAction SilentlyContinue } 20000)) { throw "没起来：$devExe" }
$app = Get-Process $procName
Start-Sleep -Seconds 5
$hwnd = Get-MainHwnd $app.Id
if ($hwnd -eq [IntPtr]::Zero) { Write-Host 'E2E-RESULT: FAIL — 没找到主窗口'; exit 1 }
[EPg]::SetForegroundWindow($hwnd) | Out-Null
Start-Sleep -Seconds 9      # 等 2.5+1.5+4×0.7+1.2 秒的脚本跑完，再等 EVAL_JS2（9 秒）读回来

$logPath = Join-Path $sandbox 'ClipNest\clipnest.log'
$line = Select-String -Path $logPath -Pattern 'debug eval\(CLIPNEST_EVAL_JS2\)' | Select-Object -Last 1
$title = if ($line) { ($line.Line -split '=>')[-1].Trim() } else { '（没有）' }
Write-Host ("   title => $title")

$rect = New-Object EPg+RECT
[EPg]::GetWindowRect($hwnd, [ref]$rect) | Out-Null
$bmp = New-Object System.Drawing.Bitmap ($rect.Right - $rect.Left), ($rect.Bottom - $rect.Top)
$g = [System.Drawing.Graphics]::FromImage($bmp)
$g.CopyFromScreen($rect.Left, $rect.Top, 0, 0, $bmp.Size)
$g.Dispose()
$bmp.Save((Join-Path $shots 'embed-paging.png'), [System.Drawing.Imaging.ImageFormat]::Png)
$bmp.Dispose()
Write-Host '   截图：scratch\e2e-shots\embed-paging.png'

Stop-AllApp
Remove-Item Env:CLIPNEST_EVAL_JS -ErrorAction SilentlyContinue
Remove-Item Env:CLIPNEST_EVAL_JS2 -ErrorAction SilentlyContinue
Remove-Item Env:CLIPNEST_IDLE_DESTROY -ErrorAction SilentlyContinue

if ($title -notmatch '^PAGING\|(.+)\|(.+)$') {
  Write-Host 'E2E-RESULT: FAIL — 没拿到两次读数'
  exit 1
}
$first = $matches[1]
$second = $matches[2]
Write-Host ("   搜索后条数：{0}    连点四次后：{1}" -f $first, $second)
if ($first -ne $second) {
  Write-Host 'E2E-RESULT: FAIL — 条数在"加载更早"之后变了（分页没停住）'
  exit 1
}
if ($first -notmatch '·\s*(\d+)\+?条') {
  Write-Host "E2E-RESULT: FAIL — 条数文本没法解析：$first"
  exit 1
}
$n = [int]$matches[1]
if ($n -gt 50) {
  Write-Host "E2E-RESULT: FAIL — 一页就超过 50 条（$n）"
  exit 1
}
if ($n -lt 50) {
  # 结果本来就不到一页 → hasMore 是 false，"上滑加载更早"这条路径根本没被走到。
  # 这时唯一有意义的断言就是"没有越滚越多"（上面已经比过）。真正卡死这个 bug 的是
  # Go 侧的 TestEmbedLiveSearch：它直接断言带游标的相关性查询必须返回空。
  Write-Host "   （只有 $n 条，不到一页 —— 分页路径未被触发，靠 Go 侧的游标断言兜底）"
}
Write-Host 'PASS 语义搜索的条数不会在"加载更早"之后越滚越多'
