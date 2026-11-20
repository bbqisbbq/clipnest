# 样式回归：把构建产物 CSS 内联进一张卡片标本页，headless Chrome 截图。
#
# 为什么要有它：样式拆成 src/styles/*.css 之后，靠肉眼很难确认"拼接顺序没变、
# 折叠/预览这些依赖层叠的地方还正常"。这个脚本不依赖 vite dev server，也不依赖
# 具体产物文件名（会自动挑最新的 index-*.css），适合改完 CSS 跑一次看眼。
#
# 用法：
#   pwsh -File scripts\visual-check\check-css.ps1                 # 默认输出到 scratch\visual-check.png
#   pwsh -File scripts\visual-check\check-css.ps1 -Out out.png    # 指定输出
#
# 前提：先跑过 cd frontend && npx vite build（需要 dist/assets/index-*.css）。

param(
  [string]$Out = "",
  [string]$Chrome = "C:\Program Files\Google\Chrome\Application\chrome.exe",
  [int]$Width = 640,
  [int]$Height = 1500
)

$ErrorActionPreference = "Stop"
$root = Split-Path -Parent (Split-Path -Parent $PSScriptRoot)

$css = Get-ChildItem -Path (Join-Path $root "frontend\dist\assets") -Filter "index-*.css" -ErrorAction SilentlyContinue |
  Sort-Object LastWriteTime -Descending | Select-Object -First 1
if (-not $css) { throw "找不到 frontend\dist\assets\index-*.css，先跑：cd frontend; npx vite build" }

if (-not $Out) { $Out = Join-Path $root "scratch\visual-check.png" }

# 深色预设 token：运行时由 lib/themes.ts 写到 :root 行内样式，这里手工补一份
$tokens = @"
--bg:#0a0b0f; --surface:#0e1015; --surface-2:#13161c; --surface-3:#191d25;
--border:#21252e; --border-soft:#1a1e26; --border-strong:#2c313b;
--text:#e9ebf0; --text-2:#a4abba; --text-3:#7e8593;
--accent:#7d8cff; --accent-2:#9d7bff;
--accent-tint:rgba(125,140,255,.14); --accent-line:rgba(125,140,255,.42);
--ok:#45c78d; --warn:#e0a253; --danger:#ee6a6a;
"@

# 样板：一张高图（用来验证折叠高度）用 data URI 现画，避免额外素材文件
$svg = '<svg xmlns="http://www.w3.org/2000/svg" width="1200" height="1600"><rect width="1200" height="1600" fill="#1b2436"/><rect width="1200" height="200" fill="#2f6fed"/><rect y="1400" width="1200" height="200" fill="#e0a253"/><text x="60" y="130" font-family="sans-serif" font-size="72" fill="#fff">TOP</text><text x="60" y="1540" font-family="sans-serif" font-size="72" fill="#20180f">BOTTOM</text></svg>'
$img = "data:image/svg+xml;base64," + [Convert]::ToBase64String([Text.Encoding]::UTF8.GetBytes($svg))

# Vite 打出来的 @font-face 是绝对路径 /assets/xxx.woff2（app 里从根路径服务）。
# 标本页是本地文件，得换成相对路径，字体才会真的加载 —— 否则量出来的行高、
# 折叠高度都是回退字体的，改了字体以后这个脚本就失去意义了。
$cssText = ($css | Get-Content -Raw) -replace 'url\(/assets/', 'url(./'

$html = @"
<!doctype html>
<html lang="zh-CN" data-theme="dark"><head><meta charset="utf-8"><title>visual check</title>
<style>$cssText</style>
<style>:root { $tokens } body { margin:0; padding:20px; } .lbl { color: var(--text-3); font-size:12px; margin:16px 0 6px; } .wrap { max-width:560px; }</style>
</head><body><div class="wrap">
  <div class="lbl">A 图片条目 · 折叠：应为一截预览 + 底部渐变，不该顶满屏</div>
  <article class="memo-card entry-card is-collected">
    <div class="entry-image clampable is-clipped"><p><img alt="" src="$img" /></p></div>
    <footer class="memo-meta"><span>09:41</span><span class="entry-autotag">剪贴板</span></footer>
  </article>
  <div class="lbl">B 图片条目 · 展开：应露出整张图（含 BOTTOM 色块）</div>
  <article class="memo-card entry-card is-collected is-expanded">
    <div class="entry-image clampable"><p><img alt="" src="$img" /></p></div>
    <footer class="memo-meta"><span>09:42</span><span class="entry-autotag">剪贴板</span></footer>
  </article>
  <div class="lbl">C 文字条目 · 短：不该有底部遮罩</div>
  <article class="memo-card entry-card"><div class="memo-body clampable"><p>短句一条，全部可见。</p></div>
    <footer class="memo-meta"><span>09:43</span></footer></article>
  <div class="lbl">D 文字条目 · 长 · 折叠：应被 7.2em 截断、有遮罩、右下角「展开全文」</div>
  <article class="memo-card entry-card is-collapsible"><div class="memo-body clampable is-clipped">
    <p>第一行文字，用来撑开卡片高度。</p><p>第二行文字，继续堆内容。</p><p>第三行文字，继续堆内容。</p>
    <p>第四行文字，继续堆内容。</p><p>第五行文字，继续堆内容。</p><p>第六行文字，继续堆内容。</p></div>
    <button class="entry-toggle">展开全文 ⌄</button>
    <footer class="memo-meta"><span>09:44</span></footer></article>
  <div class="lbl">E 文字条目 · 长 · 已展开：提示应弱化成「收起」</div>
  <article class="memo-card entry-card is-collapsible is-expanded"><div class="memo-body clampable">
    <p>展开之后不该有遮罩，提示变成弱化的「收起」。</p></div>
    <button class="entry-toggle is-open">收起 ⌃</button>
    <footer class="memo-meta"><span>09:45</span></footer></article>
</div></body></html>
"@

# 写在 dist\assets 里而不是 %TEMP%：上面的 ./xxx.woff2 要能找到同目录的字体文件。
$tmp = Join-Path (Join-Path $root "frontend\dist\assets") ("_visual-check-" + [Guid]::NewGuid().ToString("N") + ".html")
Set-Content -Path $tmp -Value $html -Encoding UTF8

if (-not (Test-Path $Chrome)) { throw "找不到 Chrome：$Chrome（可用 -Chrome 指定路径）" }
$Out = [IO.Path]::GetFullPath($Out)
& $Chrome --headless=new --disable-gpu --hide-scrollbars --virtual-time-budget=3000 `
  "--window-size=$Width,$Height" --screenshot="$Out" ("file:///" + $tmp.Replace('\', '/')) 2>&1 | Out-Null
Remove-Item $tmp -ErrorAction SilentlyContinue

if (-not (Test-Path $Out)) { throw "截图失败：$Out" }
Write-Output ("截图已生成：{0}（CSS 来源：{1}）" -f $Out, $css.Name)
