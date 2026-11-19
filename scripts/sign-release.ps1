# 给安装包签名并准备好发布所需的两个资产。
#
# 用法：
#   pwsh -File scripts\sign-release.ps1 -Installer bin\clipnest-2.7.1-amd64-setup.exe
#
# 产物：
#   <installer>.sig   ed25519 detached 签名（base64 单行）
#
# 发布时必须把 **exe 和 .sig 一起**上传到同一个 GitHub Release：
# app 的自动更新做强制验签，缺少 .sig 的发布会被直接拒绝（宁可停旧版也不装来源不明的包）。
param(
  [Parameter(Mandatory = $true)][string]$Installer,
  [string]$GoRoot = "."
)

$ErrorActionPreference = "Stop"

if (-not (Test-Path $Installer)) { throw "找不到安装包：$Installer" }
$keyPath = Join-Path $GoRoot "build\signing\ed25519.key"
if (-not (Test-Path $keyPath)) {
  throw "找不到私钥 $keyPath`n先跑： go run ./scratch/signinstaller keygen  （并把打印出的公钥粘进 internal/clip/update_sign.go）"
}

Push-Location $GoRoot
try {
  Write-Host "签名: $Installer" -ForegroundColor Cyan
  & go run ./scratch/signinstaller sign $Installer
  if ($LASTEXITCODE -ne 0) { throw "签名失败" }

  $sigPath = "$Installer.sig"
  Write-Host "验签自检: $sigPath" -ForegroundColor Cyan
  & go run ./scratch/signinstaller verify $Installer $sigPath
  if ($LASTEXITCODE -ne 0) { throw "验签自检失败——不要把这份签名发出去" }

  $hash = (Get-FileHash -Algorithm SHA256 -Path $Installer).Hash.ToLower()
  Write-Host ""
  Write-Host "SHA256  $hash" -ForegroundColor DarkGray
  Write-Host "产物：$((Resolve-Path $sigPath).Path)" -ForegroundColor Green
  Write-Host ""
  Write-Host "发布（两个资产都要传）：" -ForegroundColor Yellow
  Write-Host "  gh release create v<版本> `"$Installer`" `"$sigPath`" --title `"ClipNest <版本>`" --notes-file docs\release-notes-v<版本>.md"
}
finally {
  Pop-Location
}
