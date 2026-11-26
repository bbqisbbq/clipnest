param([string]$Exe = '')
# 「记一笔」草稿持久化 e2e（2.12.6）。
#
# 复现的正是用户报的那个 BUG：
#   打了一段字没保存 → 按 Esc 关窗 → 窗口闲置被销毁（idle.go，默认 1.5s）
#   → 再按热键打开 → 什么都没有了。
#
# 判据（三条，缺一不可）：
#   1. 日志里出现 `idle: capture window destroyed` —— 说明走的确实是"销毁"那条路，
#      而不是窗口没关所以状态还在内存里；
#   2. 库里 meta 的 `capture.draft` 就是打进去的那段字（关掉进程后读，等价于重启）；
#   3. 重开窗口后从编辑器里把内容**拷出来**逐字比对 —— 不只是"库里有"，而是真的
#      渲染回输入框了（否则用户还是看不见）。顺便留一张截图当证据。
#
# 用法（必须 -STA，要 SendKeys / 剪贴板）：
#   pwsh -STA -NoProfile -File scratch\e2e-capture-draft.ps1
#   pwsh -STA -NoProfile -File scratch\e2e-capture-draft.ps1 -Exe bin\clipnest.exe
Add-Type -AssemblyName System.Drawing
Add-Type -AssemblyName System.Windows.Forms
Add-Type @'
using System;
using System.Runtime.InteropServices;
public class CDr {
  [StructLayout(LayoutKind.Sequential)] public struct RECT { public int Left, Top, Right, Bottom; }
  [DllImport("user32.dll")] public static extern bool GetWindowRect(IntPtr h, out RECT r);
  [DllImport("user32.dll")] public static extern bool SetForegroundWindow(IntPtr h);
  [DllImport("user32.dll")] public static extern bool IsWindowVisible(IntPtr h);
}
'@

$ErrorActionPreference = 'Stop'
$root = 'E:\default\clipnest'
$sandbox = Join-Path $root 'scratch\appdata-capture-draft'
$shots = Join-Path $root 'scratch\e2e-shots'
$devExe = if ($Exe) { Join-Path $root $Exe } else { Join-Path $root 'bin\clipnest-dev.exe' }
$procName = [IO.Path]::GetFileNameWithoutExtension($devExe)
New-Item -ItemType Directory -Force -Path $shots | Out-Null

# 闲量销毁**保持产品默认（开）**：这次要测的就是"窗口被销毁之后草稿还在"。
# 只把闲置时间调短一点，免得每次等 1.5 秒。
Remove-Item Env:CLIPNEST_IDLE_DESTROY -ErrorAction SilentlyContinue
Remove-Item Env:CLIPNEST_IDLE_SKIP -ErrorAction SilentlyContinue
$env:CLIPNEST_IDLE_MS = '700'
$env:APPDATA = $sandbox

$typed = '记一笔草稿没有保存就关窗了' + "`n" + '明天接着写第二行'
$logLineRe = 'capture window: pos=\((-?\d+),(-?\d+)\) size=\((\d+)x(\d+)\) hwnd=(0x[0-9a-fA-F]+)'
$results = New-Object System.Collections.Generic.List[string]

function Wait-For([scriptblock]$cond, [int]$timeoutMs = 8000) {
  $sw = [Diagnostics.Stopwatch]::StartNew()
  while ($sw.ElapsedMilliseconds -lt $timeoutMs) {
    $v = & $cond
    if ($null -ne $v -and $v -ne $false) { return $v }
    Start-Sleep -Milliseconds 120
  }
  return $null
}
function Stop-AllApp {
  Get-Process | Where-Object { $_.ProcessName -like 'clipnest*' } | ForEach-Object {
    try { $_.Kill(); $_.WaitForExit(3000) | Out-Null } catch {}
  }
  Start-Sleep -Milliseconds 800
}
function Get-Rect([IntPtr]$h) {
  $r = New-Object CDr+RECT
  if (-not [CDr]::GetWindowRect($h, [ref]$r)) { return $null }
  return [pscustomobject]@{ X = $r.Left; Y = $r.Top; W = $r.Right - $r.Left; H = $r.Bottom - $r.Top }
}
function Shot-Region([IntPtr]$hwnd, [string]$name) {
  $rect = Get-Rect $hwnd
  if (-not $rect) { return }
  $bmp = New-Object System.Drawing.Bitmap $rect.W, $rect.H
  $g = [System.Drawing.Graphics]::FromImage($bmp)
  $g.CopyFromScreen($rect.X, $rect.Y, 0, 0, $bmp.Size)
  $g.Dispose()
  $bmp.Save((Join-Path $shots $name), [System.Drawing.Imaging.ImageFormat]::Png)
  $bmp.Dispose()
  Write-Host "   截图：scratch\e2e-shots\$name"
}
# 读库里的 meta（进程必须已退出：WAL 下的只读连接在写者消失后最稳）。
# 注意返回**整段**输出而不是某一行：草稿是多行的，按行切会把第二行以后丢掉
# （第一版就是这样，断言差点变成"只验了第一行"）。
function Get-MetaDraft {
  Push-Location $root
  try { return (& go run ./scratch/dbdraft "$sandbox\ClipNest" capture 2>&1 | Out-String) }
  finally { Pop-Location }
}

function Start-App {
  Stop-AllApp
  if (Test-Path $sandbox) { Remove-Item $sandbox -Recurse -Force -ErrorAction SilentlyContinue }
  $dataDir = Join-Path $sandbox 'ClipNest'
  New-Item -ItemType Directory -Force -Path $dataDir | Out-Null
  @{
    capture       = $true
    hotkey        = 'Ctrl+Shift+F11'    # 这台机器上 Ctrl+Shift+A/Z 被别的程序占了
    captureHotkey = 'Ctrl+Shift+N'
    showOnLaunch  = $false
    autoStart     = $false
    updateCheck   = $false
    syncEnabled   = $false
    themePreset   = 'default-dark'
    theme         = 'dark'
  } | ConvertTo-Json | Set-Content -Path (Join-Path $dataDir 'settings.json') -Encoding utf8
  Start-Process -FilePath $devExe | Out-Null
  if (-not (Wait-For { Get-Process $procName -ErrorAction SilentlyContinue } 20000)) {
    throw "没起来：$devExe"
  }
  Start-Sleep -Seconds 4
  return (Join-Path $dataDir 'clipnest.log')
}

# 敲热键并从来日志里捞窗口句柄（面板窗的 MainWindowHandle 是 0，只能这么找）
function Open-Capture([string]$logPath) {
  $skip = if (Test-Path $logPath) { @(Get-Content $logPath).Count } else { 0 }
  [System.Windows.Forms.SendKeys]::SendWait('^+n')
  $m = Wait-For {
    if (-not (Test-Path $logPath)) { return $null }
    $lines = @(Get-Content $logPath)
    if ($lines.Count -le $skip) { return $null }
    $hit = $lines[$skip..($lines.Count - 1)] | Where-Object { $_ -match $logLineRe } | Select-Object -Last 1
    if ($hit) { return $hit }
    return $null
  } 10000
  if (-not $m) { return $null }
  $null = $m -match $logLineRe
  $hwnd = [IntPtr][Convert]::ToInt64($matches[5], 16)
  Wait-For { if ([CDr]::IsWindowVisible($hwnd)) { $true } else { $null } } 5000 | Out-Null
  Start-Sleep -Milliseconds 900
  return $hwnd
}

# 从编辑器里把内容拷出来（编辑器是 contenteditable，^a ^c 走的是浏览器自己的复制）
function Read-Editor {
  [System.Windows.Forms.SendKeys]::SendWait('^a')
  Start-Sleep -Milliseconds 200
  [System.Windows.Forms.SendKeys]::SendWait('^c')
  Start-Sleep -Milliseconds 350
  try { return (Get-Clipboard -Raw) } catch { return '' }
}

function Check([string]$name, [bool]$ok, [string]$detail = '') {
  $tag = if ($ok) { 'PASS' } else { 'FAIL' }
  $results.Add("$tag  $name")
  Write-Host ("  [{0}] {1} {2}" -f $tag, $name, $detail)
}

$savedClip = ''
try { $savedClip = Get-Clipboard -Raw } catch {}

try {
  Write-Host '=== 1. 起应用（沙箱 APPDATA，闲置销毁保持默认开）==='
  $logPath = Start-App
  Write-Host "   log: $logPath"

  Write-Host '=== 2. 打开「记一笔」，打一段字，按 Esc 关窗 ==='
  $hwnd = Open-Capture $logPath
  if (-not $hwnd) { throw '「记一笔」没弹出来（日志里没有 capture window 行）' }
  Start-Sleep -Milliseconds 700
  # 逐行敲：SendKeys 的 \n 在 contenteditable 里不一定换行，回车用 {ENTER}。
  # 别在文案里放 `[ ]`：SendKeys 把它们当分组符号，而中文输入法又会把半角方括号
  # 上屏成全角 `【】`（实测），两件事都会让"到底存进去的是什么"变得说不清。
  [System.Windows.Forms.SendKeys]::SendWait('记一笔草稿没有保存就关窗了')
  [System.Windows.Forms.SendKeys]::SendWait('{ENTER}')
  [System.Windows.Forms.SendKeys]::SendWait('明天接着写第二行')
  Start-Sleep -Milliseconds 900        # 让 400ms 的防抖落库窗口先过
  Shot-Region $hwnd 'capture-draft-1-typed.png'
  [System.Windows.Forms.SendKeys]::SendWait('{ESC}')

  Write-Host '=== 3. 等闲置销毁 ==='
  $destroyed = Wait-For {
    if ((Get-Content $logPath) -match 'idle: capture window destroyed') { return $true }
    return $null
  } 12000
  Check '窗口确实被闲置销毁（不是留在内存里）' ([bool]$destroyed)

  Write-Host '=== 4. 重开窗口：内容应该自己回来 ==='
  $hwnd2 = Open-Capture $logPath
  if (-not $hwnd2) { throw '第二次没弹出「记一笔」' }
  Start-Sleep -Milliseconds 1200
  Shot-Region $hwnd2 'capture-draft-2-restored.png'
  $got = Read-Editor
  $normalized = ($got -replace "`r`n", "`n").Trim()
  Check '重开后编辑器里就是那段字' ($normalized -match '记一笔草稿没有保存就关窗了' -and $normalized -match '明天接着写第二行') "读回=$(($normalized -replace "`n", '\n'))"

  Write-Host '=== 5. 关掉进程后读库（等价于重启）==='
  Stop-AllApp
  $draft = Get-MetaDraft
  Check '库里 capture.draft 落住了（两行都在）' ($draft -match '记一笔草稿没有保存就关窗了' -and $draft -match '明天接着写第二行') "draft=$(($draft.Trim() -replace "`n", '\n'))"

  Write-Host '=== 6. 保存之后草稿要清干净（否则下次开窗又冒出已保存的内容）==='
  Write-Host '   重起一轮干净的沙箱，打一行字，Ctrl+Enter 存成条目'
  $logPath = Start-App
  $hwnd3 = Open-Capture $logPath
  if (-not $hwnd3) { throw '第三轮没弹出「记一笔」' }
  Start-Sleep -Milliseconds 900
  [System.Windows.Forms.SendKeys]::SendWait('这一条要真的存下去')
  Start-Sleep -Milliseconds 800
  [System.Windows.Forms.SendKeys]::SendWait('^{ENTER}')
  Start-Sleep -Milliseconds 1500
  Stop-AllApp
  $afterSave = Get-MetaDraft
  Check '保存后 capture.draft 被清空' ($afterSave -match 'captureDraft=<>') "draft=$(($afterSave.Trim() -replace "`n", '\n'))"
} catch {
  Write-Host ("E2E-ERROR: " + $_.Exception.Message)
  $results.Add('FAIL  脚本异常')
} finally {
  Stop-AllApp
  if ($savedClip) { try { Set-Clipboard -Value $savedClip } catch {} }
  Remove-Item Env:CLIPNEST_IDLE_MS -ErrorAction SilentlyContinue
}

$failed = @($results | Where-Object { $_ -like 'FAIL*' }).Count
Write-Host ''
Write-Host ("E2E-RESULT: {0} 项通过，{1} 项失败" -f ($results.Count - $failed), $failed)
if ($failed -eq 0) { Write-Host 'PASS 「记一笔」草稿：关窗 + 闲置销毁 + 重开，一个字都没丢' } else { exit 1 }

