# 统计脚本：有多少人在用 ClipNest
#
# 不装服务器、不发遥测，全部用 GitHub 公开/只读数据：
#   1. Releases 资产下载次数（任何人都能查，最接近"装了多少次"）
#   2. 仓库 traffic（浏览量 / 独立访客 / clone 次数，最近 14 天，每天一个点）
#
# traffic 需要写权限的 token。脚本会先试着从 git credential helper 里取，
# 也可以显式传 -Token，或设置环境变量 GITHUB_TOKEN。
#
#   pwsh -File scripts\stats.ps1
#   pwsh -File scripts\stats.ps1 -Json          # 输出 JSON，方便别的脚本消费
#
# 注意：traffic 只保留最近 14 天，且需要 token；下载量是累计值。

[CmdletBinding()]
param(
  [string]$Repo = "bbqisbbq/clipnest",
  [string]$Token,
  [switch]$Json
)

$ErrorActionPreference = "Stop"

function Get-Token {
  param([string]$Explicit)
  if ($Explicit) { return $Explicit }
  if ($env:GITHUB_TOKEN) { return $env:GITHUB_TOKEN }
  try {
    $lines = "protocol=https`nhost=github.com`n`n" | git credential fill 2>$null
    foreach ($line in ($lines -split "`n")) {
      if ($line -like "password=*") { return ($line -replace "^password=", "") }
    }
  } catch { }
  return $null
}

function Invoke-Api {
  param([string]$Url, [string]$Token)
  $headers = @{
    "Accept"     = "application/vnd.github+json"
    "User-Agent" = "clipnest-stats"
  }
  if ($Token) { $headers["Authorization"] = "Bearer $Token" }
  try {
    return Invoke-RestMethod -Uri $Url -Headers $headers -Method Get -TimeoutSec 30
  } catch {
    return $null
  }
}

$token = Get-Token -Explicit $Token

Write-Host "仓库: $Repo" -ForegroundColor Cyan
Write-Host ("token: " + $(if ($token) { "已获取（可查 traffic）" } else { "没有（只能查下载量）" })) -ForegroundColor DarkGray

# ---- 1. 下载量 ----
$releases = Invoke-Api -Url "https://api.github.com/repos/$Repo/releases?per_page=100" -Token $token
$assets = @()
$total = 0
foreach ($rel in @($releases)) {
  foreach ($asset in @($rel.assets)) {
    $assets += [pscustomobject]@{
      release  = $rel.tag_name
      name     = $asset.name
      downloads = [int]$asset.download_count
      published = ($rel.published_at -as [datetime])
    }
    $total += [int]$asset.download_count
  }
}

# ---- 2. traffic ----
$views = $null
$clones = $null
if ($token) {
  $views = Invoke-Api -Url "https://api.github.com/repos/$Repo/traffic/views" -Token $token
  $clones = Invoke-Api -Url "https://api.github.com/repos/$Repo/traffic/clones" -Token $token
}

if ($Json) {
  [pscustomobject]@{
    repo            = $Repo
    totalDownloads  = $total
    assets          = $assets
    viewsCount      = if ($views) { $views.count } else { $null }
    viewsUniques    = if ($views) { $views.uniques } else { $null }
    viewsDaily      = if ($views) { $views.views } else { $null }
    clonesCount     = if ($clones) { $clones.count } else { $null }
    clonesUniques   = if ($clones) { $clones.uniques } else { $null }
    clonesDaily     = if ($clones) { $clones.clones } else { $null }
  } | ConvertTo-Json -Depth 5
  return
}

Write-Host ""
Write-Host "== 下载量 ==" -ForegroundColor Green
if ($assets.Count -eq 0) {
  Write-Host "  （没有发布资产，或 API 限流）" -ForegroundColor Yellow
} else {
  $assets |
    Sort-Object release, name |
    Group-Object release |
    ForEach-Object {
      Write-Host ("  {0}" -f $_.Name) -ForegroundColor White
      $_.Group | ForEach-Object { Write-Host ("    {0,-46} {1,6}" -f $_.name, $_.downloads) }
    }
  Write-Host ("  合计: {0}" -f $total) -ForegroundColor Cyan
}

if ($views) {
  Write-Host ""
  Write-Host "== 最近 14 天浏览 ==" -ForegroundColor Green
  Write-Host ("  总计 {0} 次 / 独立访客 {1}" -f $views.count, $views.uniques) -ForegroundColor Cyan
  $views.views | ForEach-Object {
    $day = ([datetime]$_.timestamp).ToString("MM-dd")
    $bar = "█" * [math]::Min(40, [int]$_.count)
    Write-Host ("  {0}  {1,4}  {2}" -f $day, $_.count, $bar) -ForegroundColor DarkGray
  }
}

if ($clones) {
  Write-Host ""
  Write-Host "== 最近 14 天 clone ==" -ForegroundColor Green
  Write-Host ("  总计 {0} 次 / 独立 {1}" -f $clones.count, $clones.uniques) -ForegroundColor Cyan
}

if (-not $views) {
  Write-Host ""
  Write-Host "提示：想看浏览量/独立访客，需要一个带 repo 权限的 token：" -ForegroundColor Yellow
  Write-Host "  pwsh -File scripts\stats.ps1 -Token ghp_xxx" -ForegroundColor DarkGray
}

Write-Host ""
Write-Host "口径提醒：下载量 ≠ 用户数（同一人可能装多次、或装了又卸；" -ForegroundColor DarkGray
Write-Host "自动更新也会产生下载）。真实「用过多少人」只能靠遥测，那需要服务器。" -ForegroundColor DarkGray
