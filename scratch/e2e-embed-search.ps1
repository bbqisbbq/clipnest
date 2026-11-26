param([string]$Exe = '')
# 语义搜索的界面级验证（2.13.0）。
#
# 数据用 internal/clip 的 live 用例刚跑出来的那份（5 条、向量已算好）——
# 于是这里验的是**界面真的把语义结果画出来了**，而不是再算一遍向量。
#
# 三步：
#   1. 起应用（沙箱 APPDATA，showOnLaunch），截图：主窗正常、条目在；
#   2. Ctrl+F 聚焦搜索框，敲一句"和任何条目都不共享词"的话，截图：语义结果在列表里；
#   3. CLIPNEST_EVAL_JS2 点开「设置」，截图：新增的「搜索」分区渲染正常。
#
# 用法（必须 -STA，要 SendKeys）：
#   pwsh -STA -NoProfile -File scratch\e2e-embed-search.ps1
Add-Type -AssemblyName System.Drawing
Add-Type -AssemblyName System.Windows.Forms
Add-Type @'
using System;
using System.Runtime.InteropServices;
public class ESr {
  [StructLayout(LayoutKind.Sequential)] public struct RECT { public int Left, Top, Right, Bottom; }
  [DllImport("user32.dll")] public static extern bool GetWindowRect(IntPtr h, out RECT r);
  [DllImport("user32.dll")] public static extern bool SetForegroundWindow(IntPtr h);
  [DllImport("user32.dll")] public static extern bool IsWindowVisible(IntPtr h);
  [DllImport("user32.dll")] public static extern IntPtr GetForegroundWindow();
  [DllImport("user32.dll")] public static extern bool EnumWindows(EnumProc cb, IntPtr p);
  [DllImport("user32.dll")] public static extern uint GetWindowThreadProcessId(IntPtr h, out uint pid);
  [DllImport("user32.dll")] public static extern int GetWindowTextLength(IntPtr h);
  public delegate bool EnumWindowsProc(IntPtr h, IntPtr p);
  public delegate bool EnumProc(IntPtr h, IntPtr p);
}
'@

$ErrorActionPreference = 'Stop'
$root = 'E:\default\clipnest'
$src = Join-Path $root 'scratch\embed-model\live-data'
$sandbox = Join-Path $root 'scratch\appdata-embed-search'
$shots = Join-Path $root 'scratch\e2e-shots'
$devExe = if ($Exe) { Join-Path $root $Exe } else { Join-Path $root 'bin\clipnest-dev.exe' }
$procName = [IO.Path]::GetFileNameWithoutExtension($devExe)
New-Item -ItemType Directory -Force -Path $shots | Out-Null

if (-not (Test-Path (Join-Path $src 'clipnest.db'))) {
  Write-Host "E2E-RESULT: SKIP — 没有 live 数据（先跑：`$env:CLIPNEST_EMBED_LIVE='1'; go test ./internal/clip -run TestEmbedLiveSearch）"
  exit 1
}

# 闲置销毁会把这些脚本正拿着的窗口收掉（产品默认行为），测试期间关掉
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
# 主窗口句柄：三个窗口（主窗 / picker / capture）同属一个进程且都可能在，
# 而且 MyGo 的自绘窗口有时候拿不到 MainWindowHandle —— 所以按**尺寸**认：
# 主窗最宽最高，两个弹窗是 620×220 级别的面板。
function Get-MainHwnd([int]$pid_) {
  $script:hits = New-Object System.Collections.Generic.List[object]
  $cb = [ESr+EnumProc]{
    param($h, $p)
    $wpid = 0
    [ESr]::GetWindowThreadProcessId($h, [ref]$wpid) | Out-Null
    if ($wpid -eq $pid_ -and [ESr]::IsWindowVisible($h)) {
      $r = New-Object ESr+RECT
      if ([ESr]::GetWindowRect($h, [ref]$r)) {
        $script:hits.Add([pscustomobject]@{ H = $h; W = $r.Right - $r.Left; T = $r.Top; B = $r.Bottom })
      }
    }
    return $true
  }
  [ESr]::EnumWindows($cb, [IntPtr]::Zero) | Out-Null
  $best = $script:hits | Sort-Object -Property @{Expression = { $_.W * ($_.B - $_.T) }} -Descending | Select-Object -First 1
  if (-not $best) { return [IntPtr]::Zero }
  return $best.H
}
# 发键之前必须确认前台就是目标窗口：否则 Ctrl+F 会打到用户的浏览器里去
# （第一次跑就是这么把 "云端传不上去" 敲进 Chrome 的查找框的）。
function Assert-Foreground([IntPtr]$hwnd) {
  for ($i = 0; $i -lt 8; $i++) {
    [ESr]::SetForegroundWindow($hwnd) | Out-Null
    Start-Sleep -Milliseconds 350
    if ([ESr]::GetForegroundWindow() -eq $hwnd) { return $true }
  }
  return $false
}
function Shot([string]$name) {
  $screen = [System.Windows.Forms.Screen]::PrimaryScreen.Bounds
  $bmp = New-Object System.Drawing.Bitmap $screen.Width, $screen.Height
  $g = [System.Drawing.Graphics]::FromImage($bmp)
  $g.CopyFromScreen($screen.X, $screen.Y, 0, 0, $bmp.Size)
  $g.Dispose()
  $bmp.Save((Join-Path $shots $name), [System.Drawing.Imaging.ImageFormat]::Png)
  $bmp.Dispose()
  Write-Host "   截图：scratch\e2e-shots\$name"
}

# 1. 准备沙箱数据（live 用例那份库 + 资源），只改设置里的显示/开关
Stop-AllApp
if (Test-Path $sandbox) { Remove-Item $sandbox -Recurse -Force -ErrorAction SilentlyContinue }
$dataDir = Join-Path $sandbox 'ClipNest'
New-Item -ItemType Directory -Force -Path $dataDir | Out-Null
Copy-Item (Join-Path $src '*') $dataDir -Recurse -Force
@{
  capture     = $true
  hotkey      = 'Ctrl+Shift+F11'
  captureHotkey = 'Ctrl+Shift+N'
  showOnLaunch = $true
  autoStart    = $false
  updateCheck  = $false
  syncEnabled  = $false
  themePreset  = 'default-dark'
  theme        = 'dark'
  embedSearch  = $true
} | ConvertTo-Json | Set-Content -Path (Join-Path $dataDir 'settings.json') -Encoding utf8

# 3 秒时排一个定时任务：8 秒后点开「设置」，再把「搜索」这一节的标题滚进视野。
# 为什么用 setTimeout 而不是分两次 eval：EVAL_JS 只有一个射程（3 秒），
# 而"点开设置之后再滚"必须在同一次注入里排好时序。
# 为什么用 scrollIntoView 而不是 scrollTop=常数：分区顺序会变，写死像素迟早滚过头
# （第一版就是 1700 滚到了同步、900 滚到了交互，改了两轮）。
$js = @'
(()=>{setTimeout(()=>{
  const b=[...document.querySelectorAll('.side-item')].find(e=>e.textContent.includes('设置'));
  if(b)b.click();
  setTimeout(()=>{
    const all=[...document.querySelectorAll('body *')].filter(e=>e.children.length===0&&e.textContent.trim()==='搜索');
    const h=all[all.length-1];
    if(!h)return;
    h.scrollIntoView({block:'start'});
  },1500);
},5000);return 'scheduled';})()
'@
$env:CLIPNEST_EVAL_JS = $js -replace "`r?`n", ""

Write-Host '=== 1. 起应用（沙箱数据：5 条条目，向量已算好）==='
Start-Process -FilePath $devExe | Out-Null
if (-not (Wait-For { Get-Process $procName -ErrorAction SilentlyContinue } 20000)) { throw "没起来：$devExe" }
$app = Wait-For { Get-Process $procName -ErrorAction SilentlyContinue } 20000
Start-Sleep -Seconds 5
$hwnd = Get-MainHwnd $app.Id
if ($hwnd -eq [IntPtr]::Zero) { Write-Host 'E2E-RESULT: FAIL — 没找到主窗口'; exit 1 }
Write-Host ('   主窗口 hwnd={0}（候选可见窗口 {1} 个）' -f $hwnd, $script:hits.Count)
if (-not (Assert-Foreground $hwnd)) {
  Write-Host 'E2E-RESULT: FAIL — 拿不到前台焦点，不发键（免得敲进别的程序）'
  Stop-AllApp
  exit 1
}
Start-Sleep -Milliseconds 800
Shot 'embed-1-boot.png'

Write-Host '=== 2. Ctrl+F 搜一句"没有任何共同用词"的话 ==='
[System.Windows.Forms.SendKeys]::SendWait('^f')
Start-Sleep -Milliseconds 400
[System.Windows.Forms.SendKeys]::SendWait('云端传不上去')
Start-Sleep -Milliseconds 1500     # 输入防抖 + 一次检索
Shot 'embed-2-semantic.png'

Write-Host '=== 3. 打开设置并滚到新增的「搜索」分区 ==='
Start-Sleep -Seconds 8             # 等 EVAL_JS 里那个 5+1.2 秒的定时任务
Shot 'embed-3-settings.png'

$logPath = Join-Path $dataDir 'clipnest.log'
if (Test-Path $logPath) {
  $eval = Select-String -Path $logPath -Pattern 'debug eval\(CLIPNEST_EVAL_JS\)' | Select-Object -Last 1
  Write-Host ("   EVAL_JS => " + $(if ($eval) { $eval.Line.Trim() } else { '（没有）' }))
  $emb = Select-String -Path $logPath -Pattern 'embed:' | Select-Object -Last 3
  if ($emb) { $emb | ForEach-Object { Write-Host ("   " + $_.Line.Trim()) } }
}

Stop-AllApp
Remove-Item Env:CLIPNEST_EVAL_JS -ErrorAction SilentlyContinue
Remove-Item Env:CLIPNEST_EVAL_JS2 -ErrorAction SilentlyContinue
Remove-Item Env:CLIPNEST_IDLE_DESTROY -ErrorAction SilentlyContinue
Write-Host ''
Write-Host 'E2E-RESULT: 截图已产出（embed-1-boot / embed-2-semantic / embed-3-settings）—— 看画面确认'

