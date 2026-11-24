# 多平台编译门禁：确认 internal/... 在没有 Windows 头文件的情况下也能编译。
#
# 为什么需要它：业务包里一旦有人写了平台专属调用（Win32、某个 _windows.go 里定义的
# 纯函数、某个只在 Windows 存在的类型），Windows 上照样一路绿灯，直到有人真的为
# macOS/Linux 开工才发现整个包编不过。这个脚本把"非 Windows 能编译"变成一个可以
# 每次改动后跑的命令。
#
# 用法：
#   pwsh -File scripts\check-platforms.ps1            # 全部目标
#   pwsh -File scripts\check-platforms.ps1 -Targets linux/amd64
#
# 说明：
#   - 只检查 internal/...（不含 main.go）：整包交叉编译要 cgo 与各平台工具链，
#     那属于真机 CI 的活，不在这里伪造。
#   - CGO_ENABLED=0 是故意的：ppocr 的 engine_stub.go 就是为这个组合写的。
#   - internal/clip 现在**能**在非 Windows 编译（平台层是 platform_other.go 里的桩）。

[CmdletBinding()]
param(
    [string[]]$Targets = @('linux/amd64', 'darwin/arm64', 'windows/arm64'),
    [string[]]$Packages = @(
        './internal/clip',
        './internal/expand',
        './internal/winplat',
        './internal/ppocr'
    )
)

$ErrorActionPreference = 'Stop'
Push-Location (Join-Path $PSScriptRoot '..')
try {
    $saved = @{
        GOOS        = $env:GOOS
        GOARCH      = $env:GOARCH
        CGO_ENABLED = $env:CGO_ENABLED
    }
    $failed = @()

    foreach ($t in $Targets) {
        $parts = $t -split '/'
        $env:GOOS = $parts[0]
        $env:GOARCH = $parts[1]
        $env:CGO_ENABLED = '0'

        Write-Host "=== $t ===" -ForegroundColor Cyan
        # vet 比 build 严格一点：它还会检查测试文件能不能编译。
        $out = & go vet $Packages 2>&1
        if ($LASTEXITCODE -ne 0) {
            $failed += $t
            Write-Host $out -ForegroundColor Red
        } else {
            Write-Host "  ok" -ForegroundColor Green
        }
    }

    if ($failed.Count -gt 0) {
        Write-Host ""
        Write-Host "失败目标：$($failed -join ', ')" -ForegroundColor Red
        Write-Host "修法：把纯函数从 *_windows.go 挪到中立文件，或在 *_other.go / platform_other.go 里补桩。" -ForegroundColor Yellow
        exit 1
    }
    Write-Host ""
    Write-Host "全部通过：这些包在非 Windows 上都能编译（平台能力是空实现，不是编译失败）。" -ForegroundColor Green
}
finally {
    $env:GOOS = $saved.GOOS
    $env:GOARCH = $saved.GOARCH
    $env:CGO_ENABLED = $saved.CGO_ENABLED
    Pop-Location
}
