# 前后对照图：改动前的 dist CSS vs 改动后的 dist CSS，同一个标本页、同一套数据。
#
# 前置：dist-before.css 是改造前那次构建留下的产物（已归档在本目录）。
# 现在的 dist CSS 由 npx vite build 生成。主题 token 直接从 lib/themes.ts 的
# default-light 预设里解析，避免手工抄一份又和代码走散。
#
# 用法：pwsh -File scratch\ui-review\build-compare.ps1

param(
  [string]$Chrome = "C:\Program Files\Google\Chrome\Application\chrome.exe",
  [int]$Width = 1120,
  [int]$Height = 740
)

$ErrorActionPreference = "Stop"
$root = Split-Path -Parent (Split-Path -Parent $PSScriptRoot)
$review = Join-Path $root "scratch\ui-review"
$assets = Join-Path $root "frontend\dist\assets"

# ---- 从 themes.ts 抽出 default-light 的 token，生成注入用的 CSS ----
$themes = Get-Content (Join-Path $root "frontend\src\lib\themes.ts") -Raw
$m = [regex]::Match($themes, 'id: "default-light",[\s\S]*?tokens: \{([\s\S]*?)\n    \},')
if (-not $m.Success) { throw "解析不到 default-light 预设" }
$tokenCSS = ":root {`n" + (($m.Groups[1].Value -split "`n" | ForEach-Object {
  $line = $_.Trim()
  if ($line -match '^"(--[a-z0-9-]+)":\s*"(.+?)",?$') { "  $($Matches[1]): $($Matches[2]);" }
}) -join "`n") + "`n}"
Set-Content -Path (Join-Path $review "tokens-default-light.css") -Value $tokenCSS -Encoding UTF8

$scaffold = Get-Content (Join-Path $review "scaffold.css") -Raw
$body = Get-Content (Join-Path $review "mock-body.html") -Raw

$runs = @(
  @{ id = "before"; css = (Join-Path $review "dist-before.css"); tokens = (Join-Path $review "variant-0-current.css"); label = "改造前" },
  @{ id = "after";  css = (Get-ChildItem -Path $assets -Filter "index-*.css" | Sort-Object LastWriteTime -Descending | Select-Object -First 1).FullName; tokens = (Join-Path $review "tokens-default-light.css"); label = "改造后" }
)

foreach ($r in $runs) {
  $baseCSS = (Get-Content $r.css -Raw) -replace 'url\(/assets/', 'url(./'
  $tokenCSS = Get-Content $r.tokens -Raw

  $html = @"
<!doctype html>
<html lang="zh-CN" data-theme="light"><head><meta charset="utf-8">
<title>$($r.label)</title>
<style>$baseCSS</style>
<style>$scaffold</style>
<style>$tokenCSS</style>
</head><body>
$body
</body></html>
"@

  $tmp = Join-Path $assets ("_ui-compare-" + $r.id + ".html")
  Set-Content -Path $tmp -Value $html -Encoding UTF8

  $out = Join-Path $review ($r.id + ".png")
  & $Chrome --headless=new --disable-gpu --hide-scrollbars --force-device-scale-factor=1 `
    --virtual-time-budget=3000 "--window-size=$Width,$Height" --screenshot="$out" `
    ("file:///" + $tmp.Replace('\', '/')) 2>&1 | Out-Null
  Remove-Item $tmp -ErrorAction SilentlyContinue
  if (-not (Test-Path $out)) { throw "截图失败：$out" }
  Write-Output ("{0} -> {1}" -f $r.label, $out)
}
