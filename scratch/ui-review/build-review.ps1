# 生成「现状 / A 暖白 / B 冷灰 / C 深色」四张同屏对比图。
#
# 做法：把 vite 打出来的 dist CSS 内联进标本页（类名与真实组件一致），
# 再叠一层变量与舒适化覆盖，headless Chrome 按真实窗口尺寸 1120x740 截图。
#
# 用法：pwsh -File scratch\ui-review\build-review.ps1

param(
  [string]$Chrome = "C:\Program Files\Google\Chrome\Application\chrome.exe",
  [int]$Width = 1120,
  [int]$Height = 740
)

$ErrorActionPreference = "Stop"
$root = Split-Path -Parent (Split-Path -Parent $PSScriptRoot)
$review = Join-Path $root "scratch\ui-review"
$assets = Join-Path $root "frontend\dist\assets"

$cssFile = Get-ChildItem -Path $assets -Filter "index-*.css" | Sort-Object LastWriteTime -Descending | Select-Object -First 1
if (-not $cssFile) { throw "找不到 frontend\dist\assets\index-*.css，先跑：cd frontend; npx vite build" }

# Vite 打出来的是绝对路径 /assets/xxx.woff2，标本页在 assets 目录里，换成相对路径
$baseCSS = (Get-Content $cssFile.FullName -Raw) -replace 'url\(/assets/', 'url(./'
$scaffold = Get-Content (Join-Path $review "scaffold.css") -Raw
$comfort = Get-Content (Join-Path $review "comfort.css") -Raw
$body = Get-Content (Join-Path $review "mock-body.html") -Raw

$variants = @(
  @{ id = "0-current"; scheme = "light"; css = "variant-0-current.css"; label = "现状（default-light）"; comfort = $false },
  @{ id = "a-paper";   scheme = "light"; css = "variant-a-paper.css";   label = "A 暖白纸感";            comfort = $true },
  @{ id = "b-cool";    scheme = "light"; css = "variant-b-cool.css";    label = "B 冷灰软 UI";           comfort = $true },
  @{ id = "c-dark";    scheme = "dark";  css = "variant-c-dark.css";    label = "C 柔和深色";            comfort = $true }
)

foreach ($v in $variants) {
  $variantCSS = Get-Content (Join-Path $review $v.css) -Raw
  $extra = if ($v.comfort) { $comfort } else { "" }

  $html = @"
<!doctype html>
<html lang="zh-CN" data-theme="$($v.scheme)"><head><meta charset="utf-8">
<title>$($v.label)</title>
<style>$baseCSS</style>
<style>$scaffold</style>
<style>$extra</style>
<style>$variantCSS</style>
<style>html, body { overflow: hidden; }</style>
</head><body>
$body
</body></html>
"@

  $tmp = Join-Path $assets ("_ui-review-" + $v.id + ".html")
  Set-Content -Path $tmp -Value $html -Encoding UTF8

  $out = Join-Path $review ($v.id + ".png")
  & $Chrome --headless=new --disable-gpu --hide-scrollbars --force-device-scale-factor=1 `
    --virtual-time-budget=3000 "--window-size=$Width,$Height" --screenshot="$out" `
    ("file:///" + $tmp.Replace('\', '/')) 2>&1 | Out-Null
  Remove-Item $tmp -ErrorAction SilentlyContinue

  if (-not (Test-Path $out)) { throw "截图失败：$out" }
  Write-Output ("{0} -> {1}" -f $v.label, $out)
}
