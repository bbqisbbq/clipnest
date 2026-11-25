# 从 Wails 生成的 clipservice.ts 机械生成 MyGo 适配层（src/lib/clipservice.gen.ts）。
#
#   pwsh -File scripts\gen-mygo-clipservice.ps1
#
# 为什么要有这个脚本：换壳后前端不再用 Wails 生成的 $Call.ByID，而是 call("ClipService.X")。
# 方法名保持 Go 的 PascalCase，所以组件零改动；类型（Models.*）继续复用 bindings 里生成的 models.ts。
#
# 需要特别处理的一类：Wails 把 `func() (A, B, C)` 这类**多返回值**映射成 TS 元组
# （`Promise<[A, B, C]>`），而 MyGo 只支持 `(T, error)`，于是 Go 侧用 bindings.go 的结构体代替，
# 这里再把它转回元组 —— 否则前端 `const [, ready, reason] = await ...` 会报
# "TypeError: (intermediate value) is not iterable"（2.12.0 装出来就是这个坑）。
param(
    [string]$Root = (Split-Path -Parent $PSScriptRoot)
)
$ErrorActionPreference = "Stop"
$src = Join-Path $Root "scripts\mygo-bindings\clipservice.signatures.txt"
$out = Join-Path $Root "frontend\src\lib\clipservice.gen.ts"
$lines = Get-Content $src

# 多返回值方法：TS 侧要还原成元组，字段名来自 bindings.go 里的结构体。
$tuples = @{
    "CaptureSize"     = @{ ret = "[number, number]"; fields = @("W", "H"); types = @("number", "number") }
    "ShotBounds"      = @{ ret = "[number, number, number, number]"; fields = @("Left", "Top", "Width", "Height"); types = @("number", "number", "number", "number") }
    "OcrEngineStatus" = @{ ret = "[string, boolean, string]"; fields = @("Chosen", "PPOCRReady", "Reason"); types = @("string", "boolean", "string") }
    "Match"           = @{ ret = "[any, number, boolean]"; fields = @("Trigger", "ConsumedLen", "Ok"); types = @("any", "number", "boolean") }
    "Render"          = @{ ret = "[string, number]"; fields = @("Text", "Caret"); types = @("string", "number") }
}

$emit = New-Object System.Collections.Generic.List[string]
$emit.Add("// 由 scripts/gen-mygo-clipservice.ps1 从 scripts/mygo-bindings/clipservice.signatures.txt 生成，别手改。")
$emit.Add("// 方法名保持 Go 的 PascalCase，Wails 版组件因此一行都不用改。")
$emit.Add('import { call } from "mygo-runtime";')
$emit.Add('import type * as Models from "./models";')
$emit.Add("")

$names = New-Object System.Collections.Generic.List[string]
foreach ($line in $lines) {
    if ($line -match '^\s*#' -or $line -notmatch "`t") { continue }
    $parts = $line.Split("`t")
    $m = [regex]::Match("export function $($parts[0])(): `$CancellablePromise<$($parts[1])> {", '^export function (\w+)\(([^)]*)\): \$CancellablePromise<(.*)> \{$')
    if (-not $m.Success) { continue }
    $name = $m.Groups[1].Value
    $ret = $m.Groups[3].Value -replace '\$models\.', 'Models.' -replace 'expand\$0\.', 'any'
    $names.Add($name)

    if ($tuples.ContainsKey($name)) {
        $t = $tuples[$name]
        $pairs = foreach ($f in $t.fields) { "r?.$f" }
        $emit.Add("export const $name = async (..._args: any[]): Promise<$($t.ret)> => {")
        $emit.Add("  const r: any = await call(`"ClipService.$name`", ..._args);")
        $emit.Add("  // 运行时按 Go 结构体的字段名取值，形状再断言回元组（类型由上面的 fields 保证）")
        $emit.Add("  return [$($pairs -join ', ')] as $($t.ret);")
        $emit.Add("};")
    }
    else {
        $emit.Add("export const $name = (...args: any[]): Promise<$ret> => call<$ret>(`"ClipService.$name`", ...args);")
    }
}

$emit.Add("")
$emit.Add("export const ClipService = {")
foreach ($n in $names) { $emit.Add("  $n,") }
$emit.Add("};")

Set-Content $out -Value $emit -Encoding utf8
Write-Host ("生成 {0} 个方法（其中 {1} 个元组还原）→ {2}" -f $names.Count, $tuples.Count, $out)



