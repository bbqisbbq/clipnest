# 给 ClipNest 的可执行文件与安装包做 Authenticode 签名。
#
# 用法：
#   # 1) 自签名（个人/内网分发，无需证书、不花钱）
#   powershell -File scripts\sign.ps1 -Path bin\clipnest-0.5.0-amd64-setup.exe, bin\clipnest-0.5.0-amd64-setup-per-user.exe, bin\clipnest.exe -SelfSigned
#
#   # 2) 正式证书（买了 OV/EV 代码签名证书之后）
#   $env:CLIPNEST_SIGN_PFX = "D:\certs\clipnest.pfx"
#   $env:CLIPNEST_SIGN_PASSWORD = "******"
#   powershell -File scripts\sign.ps1 -Path bin\*.exe -Timestamp http://timestamp.digicert.com
#
# 自签名说明：签名后文件会显示发布者为证书里的 CN（而不是"未知发布者"），并且被改动后
# 系统会立即发现。但 Windows 仍会提示"无法验证发布者"，除非把 build\signing\clipnest-selfsigned.cer
# 导入到「受信任的根证书颁发机构」+「受信任的发布者」各一次（见 README「签名」一节）。

[CmdletBinding()]
param(
  [Parameter(Mandatory = $true, Position = 0)]
  [string[]]$Path,

  # 自签名证书（默认在当前用户证书库中查找，没有就创建）
  [switch]$SelfSigned,

  # 正式证书路径与密码（也可用环境变量提供）
  [string]$Pfx = $env:CLIPNEST_SIGN_PFX,
  [string]$Password = $env:CLIPNEST_SIGN_PASSWORD,

  # 时间戳服务；离线签名时留空
  [string]$Timestamp = ""
)

$ErrorActionPreference = 'Stop'
$repo = Split-Path -Parent $PSScriptRoot
$subject = 'CN=ClipNest (self-signed), O=ClipNest, C=CN'
$signingDir = Join-Path $repo 'build\signing'

function Get-SelfSignedCert {
  $cert = Get-ChildItem Cert:\CurrentUser\My -CodeSigningCert |
    Where-Object { $_.Subject -eq $subject } |
    Sort-Object NotAfter -Descending |
    Select-Object -First 1

  if (-not $cert) {
    Write-Host "创建自签名代码签名证书…" -ForegroundColor Yellow
    $cert = New-SelfSignedCertificate `
      -Type CodeSigningCert `
      -Subject $subject `
      -KeyUsage DigitalSignature `
      -KeyAlgorithm RSA `
      -KeyLength 3072 `
      -CertStoreLocation Cert:\CurrentUser\My `
      -NotAfter (Get-Date).AddYears(5) `
      -TextExtension @('2.5.29.37={text}1.3.6.1.5.5.7.3.3')   # EKU: codeSigning
  }

  # 导出 .pfx（可搬去别的机器签名）与 .cer（分发给用户信任）
  if (-not (Test-Path $signingDir)) { New-Item -ItemType Directory -Force $signingDir | Out-Null }
  $pfxPath = Join-Path $signingDir 'clipnest-selfsigned.pfx'
  $cerPath = Join-Path $signingDir 'clipnest-selfsigned.cer'
  if (-not (Test-Path $cerPath)) {
    Export-Certificate -Cert $cert -FilePath $cerPath | Out-Null
  }
  if (-not (Test-Path $pfxPath)) {
    # 自用场景给个固定口令，方便脚本无交互；文件本身在 .gitignore 里
    $pw = ConvertTo-SecureString -String 'clipnest' -Force -AsPlainText
    Export-PfxCertificate -Cert $cert -FilePath $pfxPath -Password $pw | Out-Null
  }
  Write-Host "证书: $($cert.Thumbprint)  有效期至 $($cert.NotAfter.ToString('yyyy-MM-dd'))"
  Write-Host "公开证书（给用户导入信任）: $cerPath"
  return $cert
}

if ($Pfx) {
  if (-not (Test-Path $Pfx)) { throw "找不到证书文件: $Pfx" }
  $secure = ConvertTo-SecureString -String $Password -Force -AsPlainText
  $cert = Import-PfxCertificate -FilePath $Pfx -CertStoreLocation Cert:\CurrentUser\My -Password $secure
  Write-Host "导入正式证书: $($cert.Subject)" -ForegroundColor Green
} elseif ($SelfSigned) {
  $cert = Get-SelfSignedCert
} else {
  # 不指定就默认自签名，避免"什么都没签"却被当成签过
  Write-Host "未提供 -Pfx，按自签名处理（想用正式证书请传 -Pfx/-Password 或环境变量）" -ForegroundColor Yellow
  $cert = Get-SelfSignedCert
}

# powershell -File 传进来的数组其实是 "a,b,c" 一个字符串，这里拆开并去掉引号
$items = @()
foreach ($raw in $Path) {
  foreach ($piece in ([string]$raw -split ',')) {
    $clean = $piece.Trim().Trim("'").Trim('"').Trim()
    if ($clean) { $items += $clean }
  }
}

$targets = @()
foreach ($item in $items) {
  $resolved = @(Resolve-Path $item -ErrorAction SilentlyContinue)
  if (-not $resolved) { Write-Warning "跳过（不存在）: $item"; continue }
  foreach ($r in $resolved) { $targets += Get-Item $r.Path }
}

if ($targets.Count -eq 0) { throw '没有可签名的文件' }

$failed = 0
foreach ($file in $targets) {
  Write-Host "签名 $($file.Name) ($([math]::Round($file.Length / 1MB, 1)) MB)…" -NoNewline
  try {
    $params = @{
      FilePath      = $file.FullName
      Certificate   = $cert
      HashAlgorithm = 'SHA256'
    }
    if ($Timestamp) { $params.TimestampServer = $Timestamp }
    $sig = Set-AuthenticodeSignature @params
    if ($sig.Status -eq 'Valid' -or $sig.Status -eq 'UnknownError' -or $sig.Status -eq 'NotTrusted') {
      Write-Host " 完成（$($sig.Status)）" -ForegroundColor Green
    } else {
      Write-Host " 失败: $($sig.Status) $($sig.StatusMessage)" -ForegroundColor Red
      $failed++
    }
  } catch {
    Write-Host " 异常: $_" -ForegroundColor Red
    $failed++
  }
}

Write-Host ""
Write-Host "=== 验证结果 ==="
foreach ($file in $targets) {
  $sig = Get-AuthenticodeSignature $file.FullName
  $who = if ($sig.SignerCertificate) { $sig.SignerCertificate.Subject } else { '（未签名）' }
  Write-Host ("{0,-46} {1,-12} {2}" -f $file.Name, $sig.Status, $who)
}

if ($failed -gt 0) {
  Write-Host ""
  Write-Host "$failed 个文件签名失败" -ForegroundColor Red
  exit 1
}
Write-Host ""
Write-Host "全部完成。自签名证书在别人机器上需要先导入 $signingDir\clipnest-selfsigned.cer 才不报警。" -ForegroundColor Cyan
