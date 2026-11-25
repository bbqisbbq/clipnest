# 用 MyGo 构建打一个**本地安装包**（Gate 2 Step 4）。
#
#   pwsh -File scripts\package-mygo.ps1                    # 默认 2.12.0
#   pwsh -File scripts\package-mygo.ps1 -Version 2.12.1
#
# 和 wails3 task windows:package 的差别只有一处：Go 那边由我们自己 go build（MyGo 宿主），
# 其余（syso 资源、NSIS 脚本、ed25519 签名）全部沿用 docs/RELEASE.md 那一套。
#
# 产物：bin\clipnest-<版本>-amd64-setup.exe（+ .sig）
param(
    [string]$Version = "2.12.0",
    [switch]$SkipSign,
    [switch]$SkipFrontend
)
$ErrorActionPreference = "Stop"
$root = Split-Path -Parent $PSScriptRoot
Push-Location $root
try {
    Write-Host "=== 1. 版本号 → $Version（service.go / config.yml / windows/info.json）==="
    $svc = "internal\clip\service.go"
    (Get-Content $svc -Raw) -replace 'const Version = "[^"]+"', "const Version = `"$Version`"" | Set-Content $svc -Encoding utf8
    $cfg = "build\config.yml"
    (Get-Content $cfg -Raw) -replace "(?m)^version: `"[^`"]+`"", "version: `"$Version`"" | Set-Content $cfg -Encoding utf8
    # 安装包的版本号只认这个（wails3 task common:update:build-assets 生成的）
    $nsh = "build\windows\nsis\wails_tools.nsh"
    (Get-Content $nsh -Raw) -replace '!define INFO_PRODUCTVERSION "[^"]+"', "!define INFO_PRODUCTVERSION `"$Version`"" | Set-Content $nsh -Encoding utf8
    $info = Get-Content "build\windows\info.json" -Raw
    $info = $info -replace '("file_version": ")2\.[0-9.]+(")', "`${1}$Version`${2}" -replace '("ProductVersion": ")2\.[0-9.]+(")', "`${1}$Version`${2}"
    Set-Content "build\windows\info.json" $info -Encoding utf8
    Select-String -Path $svc -Pattern "const Version" | ForEach-Object { Write-Host "   $($_.Line.Trim())" }

    if (-not $SkipFrontend) {
        Write-Host "=== 2. 前端产物（vite build）==="
        Push-Location frontend
        try { npm run build 2>&1 | Select-Object -Last 3 | ForEach-Object { Write-Host "   $_" } } finally { Pop-Location }
    }

    Write-Host "=== 3. Windows 资源（wails3 generate syso：图标/manifest/版本）==="
    Push-Location build
    try {
        & wails3 generate syso -arch amd64 -icon windows/icon.ico -manifest windows/wails.exe.manifest -info windows/info.json -out ../wails_windows_amd64.syso
        if ($LASTEXITCODE -ne 0) { throw "wails3 generate syso 失败" }
    } finally { Pop-Location }

    Write-Host "=== 4. go build（MyGo 宿主，CGO_ENABLED=1 才有 ppocr）==="
    $env:CGO_ENABLED = "1"
    $env:GOOS = "windows"
    $env:GOARCH = "amd64"
    & go build -tags production -trimpath -buildvcs=false `
        -ldflags="-w -s -H windowsgui -linkmode=internal" -o bin/clipnest.exe .
    if ($LASTEXITCODE -ne 0) { throw "go build 失败" }
    Remove-Item wails_windows_amd64.syso -ErrorAction SilentlyContinue
    $exe = Get-Item bin\clipnest.exe
    Write-Host ("   bin\clipnest.exe  {0:N1} MB  {1}" -f ($exe.Length / 1MB), $exe.LastWriteTime)
    if ($exe.LastWriteTime -lt (Get-Date).AddMinutes(-5)) { throw "exe 不是刚构建出来的" }
    if (-not (Select-String -Path bin\clipnest.exe -Pattern 'releases/download/ocr-assets-v1' -Quiet)) {
        Write-Warning "exe 里没有 ocr-assets 字符串：cgo/ppocr 可能没编进去"
    }

    Write-Host "=== 5. NSIS 安装包 ==="
    Push-Location build\windows\nsis
    try {
        & makensis -DARG_WAILS_AMD64_BINARY="$root\bin\clipnest.exe" project.nsi | Select-Object -Last 6 | ForEach-Object { Write-Host "   $_" }
        if ($LASTEXITCODE -ne 0) { throw "makensis 失败" }
    } finally { Pop-Location }

    $built = "bin\clipnest-amd64-installer.exe"
    if (-not (Test-Path $built)) { throw "没找到 $built" }
    $setup = "bin\clipnest-$Version-amd64-setup.exe"
    Move-Item $built $setup -Force
    Write-Host ("   安装包：{0}  {1:N1} MB" -f $setup, ((Get-Item $setup).Length / 1MB))

    if (-not $SkipSign) {
        Write-Host "=== 6. ed25519 签名（自动更新验签用）==="
        $key = "build\signing\ed25519.key"
        if (-not (Test-Path $key)) {
            Write-Warning "没有 $key，跳过签名（本机自测不影响安装；发布必须签）"
        } else {
            & go run ./scratch/signinstaller sign $setup 2>&1 | Select-Object -Last 3 | ForEach-Object { Write-Host "   $_" }
            if ($LASTEXITCODE -ne 0) { throw "签名失败" }
            & go run ./scratch/signinstaller verify $setup "$setup.sig" 2>&1 | Select-Object -Last 2 | ForEach-Object { Write-Host "   $_" }
        }
    }

    Write-Host "=== 7. 自检 ==="
    $vi = (Get-Item $setup).VersionInfo
    Write-Host ("   版本信息：{0} / {1}" -f $vi.ProductName, $vi.ProductVersion)
    Get-ChildItem bin\clipnest-$Version-amd64-setup.exe*, bin\clipnest.exe |
        Select-Object Name, @{n = "MB"; e = { [math]::Round($_.Length / 1MB, 1) } }, LastWriteTime | Format-Table -AutoSize
    Write-Host "`n装它：右键「以管理员身份运行」→ 选目录 → 装完立即启动（托盘常驻）。"
    Write-Host "想验证自动更新：把 .exe 和 .sig 一起传到 Releases，应用里「检查更新」。"
}
finally {
    Remove-Item Env:\CGO_ENABLED, Env:\GOOS, Env:\GOARCH -ErrorAction SilentlyContinue
    Pop-Location
}


