# 把 vditor 的运行时资源从 node_modules 同步到 frontend/public/vditor。
#
# 为什么要有它：Vditor 默认从 unpkg CDN 拉 lute（Markdown 引擎）和图标，
# 这是个离线桌面应用，断网就等于编辑器白屏。所以必须把 dist 拷进 public，
# 并用 options.cdn 指过去。
#
# 不做的事：不提交这堆文件（22MB，且是 npm 包的纯拷贝），已加进 .gitignore，
# 由 predev / prebuild 在每次构建前重新同步 —— 版本永远跟 package.json 一致。
#
# 用法：
#   pwsh -File scripts\sync-vditor.ps1          # 同步
#   pwsh -File scripts\sync-vditor.ps1 -Check   # 只校验，不动文件（CI / 构建前用）

param([switch]$Check)

$ErrorActionPreference = "Stop"
$root = Split-Path -Parent $PSScriptRoot
$src = Join-Path $root "frontend\node_modules\vditor\dist"
$dstRoot = Join-Path $root "frontend\public\vditor"
$dst = Join-Path $dstRoot "dist"

# 少一个都不行：少了 lute 编辑器起不来，少了图标工具栏会变大字母。
$required = @(
  "index.min.js",
  "index.css",
  "js\lute\lute.min.js",
  "js\icons\ant.js",
  "js\i18n\zh_CN.js"
)

if (-not (Test-Path $src)) {
  throw "找不到 $src —— 先在 frontend 里跑 npm install"
}

if ($Check) {
  $missing = $required | Where-Object { -not (Test-Path (Join-Path $dst $_)) }
  if ($missing) { throw "vditor 资源缺失：$($missing -join ', ')；先跑 scripts\sync-vditor.ps1" }
  Write-Host "vditor 资源齐全"
  exit 0
}

if (Test-Path $dst) { Remove-Item -Recurse -Force $dst }
New-Item -ItemType Directory -Force -Path $dstRoot | Out-Null
Copy-Item -Recurse -Force $src $dst

$missing = $required | Where-Object { -not (Test-Path (Join-Path $dst $_)) }
if ($missing) { throw "同步后仍缺：$($missing -join ', ')" }

$size = (Get-ChildItem $dst -Recurse -File | Measure-Object Length -Sum).Sum / 1MB
Write-Host ("vditor 资源已同步：{0:N1} MB -> {1}" -f $size, $dst)
