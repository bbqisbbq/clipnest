# 「打 # 弹已有标签 → 选中插入」的真实行为验证（沙箱数据 + 全局热键 + 真键盘）。
#
# 为什么走热键而不是点鼠标：SetForegroundWindow/SetCursorPos 在无人值守时经常不生效
# （六张截图字节数完全一样 = 输入压根没进窗口）。Ctrl+Shift+N 是应用注册的**全局热键**，
# 注入按键就会弹「记一笔」窗口并自己抢占焦点，之后 SendKeys 一定落在那个输入框里。
#
# 步骤：
#   1. 沙箱 APPDATA 里塞几条带 #tag 的条目（work / writing / reading）；
#   2. Ctrl+Shift+N 打开记一笔 → 敲 `#`（应弹出 3 个候选）→ 敲 `w`（过滤到 2 个）；
#   3. Enter 插入（应变成 `#writing `）→ Ctrl+Enter 存成条目；
#   4. 停实例，从库里读最新条目，断言正文里是 `#writing`。
#
# 用法（必须 -STA）：pwsh -STA -NoProfile -File scratch\e2e-tag-suggest.ps1
Add-Type -AssemblyName System.Drawing
Add-Type -AssemblyName System.Windows.Forms

$root = 'E:\default\clipnest'
$sandbox = Join-Path $root 'scratch\appdata-e2e-tags'
$devExe = Join-Path $root 'bin\clipnest-dev.exe'
$installedExe = 'C:\Program Files\bbqisbbq\ClipNest\clipnest.exe'
$shots = Join-Path $root 'scratch\e2e-tags-shots'
$realAppData = $env:APPDATA
New-Item -ItemType Directory -Path $shots -Force | Out-Null

# 整屏截图：弹窗是独立的置顶小窗，整屏抓最省事，也不依赖 hwnd
function Grab($name) {
  $b = [System.Windows.Forms.SystemInformation]::VirtualScreen
  $bmp = New-Object System.Drawing.Bitmap $b.Width, $b.Height
  $g = [System.Drawing.Graphics]::FromImage($bmp)
  $g.CopyFromScreen($b.Left, $b.Top, 0, 0, $bmp.Size)
  $g.Dispose()
  $path = Join-Path $shots "$name.png"
  $bmp.Save($path, [System.Drawing.Imaging.ImageFormat]::Png)
  $bmp.Dispose()
  return $path
}

$result = @{}
Push-Location $root
try {
  Get-Process clipnest -ErrorAction SilentlyContinue | Stop-Process -Force
  Start-Sleep -Milliseconds 900

  Remove-Item $sandbox -Recurse -Force -ErrorAction SilentlyContinue
  New-Item -ItemType Directory -Path (Join-Path $sandbox 'ClipNest') -Force | Out-Null
  $seed = & go run ./scratch/seedtags (Join-Path $sandbox 'ClipNest') 2>&1
  $result['seed'] = ($seed -join ' | ')

  $env:APPDATA = $sandbox
  Start-Process -FilePath $devExe -ArgumentList '--background' | Out-Null
  $deadline = (Get-Date).AddSeconds(30)
  while ((Get-Date) -lt $deadline) {
    $c = Get-Process clipnest-dev -ErrorAction SilentlyContinue | Select-Object -First 1
    if ($c) { $c.Refresh(); if ($c.MainWindowHandle -ne 0) { Start-Sleep -Milliseconds 2500; break } }
    Start-Sleep -Milliseconds 250
  }

  # 1. 全局热键打开「记一笔」（它自己会抢焦点）
  [System.Windows.Forms.SendKeys]::SendWait('^+n')
  Start-Sleep -Milliseconds 1800
  Grab 'a-capture-open' | Out-Null

  # 2. 敲 `#`：应弹出 work / writing / reading
  [System.Windows.Forms.SendKeys]::SendWait('#')
  Start-Sleep -Milliseconds 1100
  Grab 'b-hash' | Out-Null

  # 3. 敲 w：过滤成 work / writing
  [System.Windows.Forms.SendKeys]::SendWait('w')
  Start-Sleep -Milliseconds 1100
  Grab 'c-filtered' | Out-Null

  # 4. 敲 r：只剩 writing；Enter 插入候选
  [System.Windows.Forms.SendKeys]::SendWait('r')
  Start-Sleep -Milliseconds 1100
  Grab 'd-one' | Out-Null
  [System.Windows.Forms.SendKeys]::SendWait('{ENTER}')
  Start-Sleep -Milliseconds 1200
  Grab 'e-accepted' | Out-Null

  # 5. Ctrl+Enter 存成条目（记一笔的保存键）
  [System.Windows.Forms.SendKeys]::SendWait('^~')
  Start-Sleep -Milliseconds 1800
  Grab 'f-saved' | Out-Null

  # 6. 停实例再读库（避开 WAL 快照问题），断言最新条目正文
  Get-Process clipnest-dev -ErrorAction SilentlyContinue | Stop-Process -Force
  Start-Sleep -Milliseconds 1000
  $latest = & go run ./scratch/lastentries (Join-Path $sandbox 'ClipNest') 3 2>&1
  $result['latest'] = ($latest -join ' ; ')
  $result['tag_inserted'] = [bool]($latest -match '#writing')
} finally {
  Get-Process clipnest-dev -ErrorAction SilentlyContinue | Stop-Process -Force
  Start-Sleep -Milliseconds 800
  $env:APPDATA = $realAppData
  Start-Process -FilePath $installedExe -ArgumentList '--background' | Out-Null
  Pop-Location
}

$result.GetEnumerator() | ForEach-Object { "{0}: {1}" -f $_.Key, $_.Value }
"screenshots: $shots"
