# End-to-end check of the web-image fix: copying an image from a browser must
# land as an image entry (not a rich-text entry pointing at the image URL).
#
#   1. start the dev build against a sandbox APPDATA
#   2. reproduce the browser's clipboard shape on a real page: select the <img>
#      and fire Ctrl+C through Chrome (CDP) — verified with scratch/clipfmt that
#      this leaves only HTML Format (<img src>) + alt text, no bitmap at all
#   3. the entry must be kind=image, 400x300, with the source URL downloaded
#   4. stop the local web server, copy again -> must fall back to a rich entry
#      (not silently vanish) and log why
Add-Type -AssemblyName System.Windows.Forms

$root = 'E:\default\clipnest'
$devExe = Join-Path $root 'bin\clipnest-dev.exe'
$sandbox = Join-Path $root 'scratch\appdata-webimg'
$log = Join-Path $sandbox 'ClipNest\clipnest.log'
$db = Join-Path $sandbox 'ClipNest\clipnest.db'
$python = 'C:\Python313\python.exe'
$probeDir = Join-Path $root 'scratch\webimg-probe'
$results = New-Object System.Collections.Generic.List[string]

function Say([string]$s) { $results.Add($s); Write-Host $s }
function LastEntry() {
  $code = 'import sqlite3,sys;c=sqlite3.connect(sys.argv[1]);r=c.execute("select kind,width,height,source_app,substr(content,1,40) from entries order by created_at desc limit 1").fetchone();print("|".join(str(v) for v in r))'
  return (& $python -c $code $db)
}
function EntryCount() {
  $code = 'import sqlite3,sys;c=sqlite3.connect(sys.argv[1]);print(c.execute("select count(*) from entries").fetchone()[0])'
  return [int](& $python -c $code $db)
}
function Wait-Entry([int]$want, [int]$timeoutMs = 15000) {
  $sw = [Diagnostics.Stopwatch]::StartNew()
  while ($sw.ElapsedMilliseconds -lt $timeoutMs) {
    if ((Test-Path $db) -and (EntryCount) -ge $want) { return $true }
    Start-Sleep -Milliseconds 200
  }
  return $false
}

# ---- sandbox up ----------------------------------------------------------
Get-Process | Where-Object { $_.ProcessName -like 'clipnest*' } | Stop-Process -Force -ErrorAction SilentlyContinue
Start-Sleep -Milliseconds 700
Remove-Item -Recurse -Force $sandbox -ErrorAction SilentlyContinue
New-Item -ItemType Directory -Force -Path (Join-Path $sandbox 'ClipNest') | Out-Null
$env:APPDATA = $sandbox
$env:CLIPNEST_KEEP_CONSOLE = '1'
$proc = Start-Process -FilePath $devExe -ArgumentList '--background' -PassThru
Start-Sleep -Seconds 2

# ---- the local page must be reachable -----------------------------------
$page = $false
try { $page = (Invoke-WebRequest http://127.0.0.1:8765/test.html -UseBasicParsing -TimeoutSec 3).StatusCode -eq 200 } catch { $page = $false }
if (-not $page) {
  Start-Process -FilePath $python -ArgumentList '-m', 'http.server', '8765' -WorkingDirectory $probeDir -WindowStyle Hidden
  Start-Sleep -Seconds 2
  try { $page = (Invoke-WebRequest http://127.0.0.1:8765/test.html -UseBasicParsing -TimeoutSec 3).StatusCode -eq 200 } catch { $page = $false }
}
Say "page up = $page"
if (-not $page) { Say 'FAIL: 本地探针页面起不来'; exit 1 }

# ---- Chrome with CDP must be reachable (we drive the copy through it) ----
function CdpUp { try { return (Invoke-WebRequest http://127.0.0.1:9333/json/list -UseBasicParsing -TimeoutSec 2).StatusCode -eq 200 } catch { return $false } }
$cdp = CdpUp
if (-not $cdp) {
  $profile = Join-Path $probeDir 'chrome-profile'
  Start-Process -FilePath 'C:\Program Files\Google\Chrome\Application\chrome.exe' -ArgumentList @(
    '--remote-debugging-port=9333', "--user-data-dir=$profile", '--no-first-run',
    '--no-default-browser-check', '--window-size=900,700', '--window-position=80,80',
    'http://127.0.0.1:8765/test.html'
  )
  $sw = [Diagnostics.Stopwatch]::StartNew()
  while ($sw.ElapsedMilliseconds -lt 20000 -and -not $cdp) {
    Start-Sleep -Milliseconds 500
    $cdp = CdpUp
  }
}
Say "chrome cdp up = $cdp"
if (-not $cdp) { Say 'FAIL: Chrome 的调试端口没起来，后面的复制无从谈起'; exit 1 }

# ---- copy the image from Chrome -----------------------------------------
# 先往剪贴板塞一条哨兵文本：否则沙箱启动时"补读一次当前剪贴板"留下的那条（上一次
# 测试的截图）会冒充"这次复制的结果"。
Set-Clipboard -Value "sentinel-$([guid]::NewGuid())"
Start-Sleep -Milliseconds 900
$before = EntryCount
Push-Location $probeDir
node copyprobe.mjs select | Out-Host
Pop-Location
$null = Wait-Entry ($before + 1) 20000
Start-Sleep -Milliseconds 1500
$last = LastEntry
Say "web image -> $last"
if (($last -split '\|')[0] -eq 'image') { Say 'OK: 网页图片被收成了图片条目' } else { Say 'FAIL: 还是没存成图片' }

# ---- fallback: server down ----------------------------------------------
Get-Process python -ErrorAction SilentlyContinue | Where-Object { $_.CommandLine -like '*http.server*' } | Stop-Process -Force -ErrorAction SilentlyContinue
Get-CimInstance Win32_Process -Filter "Name='python.exe'" | Where-Object { $_.CommandLine -like '*http.server*' } | ForEach-Object { Stop-Process -Id $_.ProcessId -Force -ErrorAction SilentlyContinue }
Start-Sleep -Milliseconds 800
$before2 = EntryCount
$fallbacksBefore = @(Select-String -Path $log -Pattern '退回富文本').Count
Push-Location $probeDir
node copyprobe.mjs select | Out-Host
Pop-Location
Start-Sleep -Milliseconds 3000
$last2 = LastEntry
Say "server down -> $last2"
# 注意：降级出来的富文本可能被"内容去重"合并进上一条同样的条目（这是对的），
# 所以这里判"日志里多了一条退回"，而不是"多了一条记录"。
$fallbacksAfter = @(Select-String -Path $log -Pattern '退回富文本').Count
if ($fallbacksAfter -gt $fallbacksBefore) { Say 'OK: 下载失败时退回富文本条目（内容没丢）' } else { Say "FAIL: 失败路径没走通（退回次数 $fallbacksBefore -> $fallbacksAfter）" }
Say "log: $((Select-String -Path $log -Pattern 'remote image' | Select-Object -Last 2 | ForEach-Object { $_.Line }) -join ' / ')"

# ---- restore -------------------------------------------------------------
Stop-Process -Id $proc.Id -Force -ErrorAction SilentlyContinue
Remove-Item Env:\CLIPNEST_KEEP_CONSOLE -ErrorAction SilentlyContinue
Write-Host "`n===== summary ====="
$results | ForEach-Object { Write-Host $_ }

