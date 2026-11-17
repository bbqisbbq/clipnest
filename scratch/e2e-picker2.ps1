# Second e2e pass for the popup: Esc must dismiss without touching the target,
# and Ctrl+Enter must copy without pasting.
Add-Type -AssemblyName System.Windows.Forms
Add-Type @'
using System;
using System.Runtime.InteropServices;
public class E2EB {
  [DllImport("user32.dll")] public static extern bool SetForegroundWindow(IntPtr h);
}
'@

$root = 'E:\default\clipnest'
$pwshPath = (Get-Process -Id $PID).Path
$targetFile = Join-Path $env:TEMP 'clipnest-target.txt'
$sentinelA = "E2E-ESC-A-$([guid]::NewGuid().ToString('N').Substring(0,6))"
$sentinelB = "E2E-COPY-B-$([guid]::NewGuid().ToString('N').Substring(0,6))"

function Get-Target {
  $deadline = (Get-Date).AddSeconds(20)
  while ((Get-Date) -lt $deadline) {
    $p = Get-Process | Where-Object { $_.MainWindowTitle -eq 'ClipNest paste target' } | Select-Object -First 1
    if ($p) { $p.Refresh(); if ($p.MainWindowHandle -ne 0) { return $p } }
    Start-Sleep -Milliseconds 200
  }
  throw 'paste target window never appeared'
}

function Get-TargetText {
  if (-not (Test-Path $targetFile)) { return '' }
  return (Get-Content $targetFile -Raw -ErrorAction SilentlyContinue) -replace "`r", ''
}

if (-not (Get-Process clipnest -ErrorAction SilentlyContinue)) {
  Start-Process -FilePath (Join-Path $root 'bin\clipnest.exe') | Out-Null
  Start-Sleep -Seconds 4
}
$existing = Get-Process | Where-Object { $_.MainWindowTitle -eq 'ClipNest paste target' } | Select-Object -First 1
if (-not $existing) {
  Start-Process -FilePath $pwshPath -ArgumentList '-STA','-NoProfile','-File',(Join-Path $root 'scratch\paste-target.ps1') | Out-Null
}
$target = Get-Target

function Focus-Target {
  [E2EB]::SetForegroundWindow($target.MainWindowHandle) | Out-Null
  Start-Sleep -Milliseconds 350
}

function Reset-TargetText {
  Focus-Target
  [System.Windows.Forms.SendKeys]::SendWait('^a{BACKSPACE}')
  Start-Sleep -Milliseconds 400
}

# ---------- A. Esc dismisses without pasting ----------
Set-Clipboard -Value $sentinelA
Start-Sleep -Milliseconds 800
Reset-TargetText
$before = Get-TargetText

[System.Windows.Forms.SendKeys]::SendWait('^+v')
Start-Sleep -Milliseconds 900
[System.Windows.Forms.SendKeys]::SendWait('{ESC}')
Start-Sleep -Milliseconds 600
$afterEsc = Get-TargetText
$clipAfterEsc = Get-Clipboard -Raw

Write-Host "A) text before=[$($before -replace "`n",'\n')] afterEsc=[$($afterEsc -replace "`n",'\n')]"
Write-Host "A) clipboard still sentinel: $($clipAfterEsc.Trim() -eq $sentinelA)"

# Enter must now reach the target again (proves focus went back and popup is gone).
[System.Windows.Forms.SendKeys]::SendWait('{ENTER}')
Start-Sleep -Milliseconds 500
$afterEnter = Get-TargetText
$focusBack = $afterEnter.Contains("`n") -and -not $afterEnter.Contains($sentinelA)
Write-Host "A) focus returned to target: $focusBack"

# ---------- B. Ctrl+Enter copies only ----------
Set-Clipboard -Value $sentinelB
Start-Sleep -Milliseconds 800
Reset-TargetText

Focus-Target
[System.Windows.Forms.SendKeys]::SendWait('^+v')
Start-Sleep -Milliseconds 900
# items are newest-first: [B, A, ...] → one Down selects A, then Ctrl+Enter copies it.
[System.Windows.Forms.SendKeys]::SendWait('{DOWN}')
Start-Sleep -Milliseconds 250
[System.Windows.Forms.SendKeys]::SendWait('^{ENTER}')
Start-Sleep -Milliseconds 900

$clipAfterCopy = (Get-Clipboard -Raw).Trim()
$textAfterCopy = Get-TargetText
Write-Host "B) clipboard=[$clipAfterCopy]"
Write-Host "B) target text=[$($textAfterCopy -replace "`n",'\n')] (must not contain the copied value)"
$copyOnlyOK = ($clipAfterCopy -eq $sentinelA) -and (-not $textAfterCopy.Contains($sentinelA))

if ($focusBack -and $copyOnlyOK) {
  Write-Host 'E2E-RESULT: PASS — Esc 只关闭，Ctrl+Enter 只复制'
} else {
  Write-Host "E2E-RESULT: FAIL (focusBack=$focusBack copyOnly=$copyOnlyOK)"
}

if ($env:E2E_KEEP_TARGET -ne '1') {
  Get-Process | Where-Object { $_.MainWindowTitle -eq 'ClipNest paste target' } | Stop-Process -Force
}
