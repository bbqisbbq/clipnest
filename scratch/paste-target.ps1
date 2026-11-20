# A throwaway paste target: a WinForms window with a focused TextBox that keeps
# writing its content to a file, so the e2e test can verify what was pasted.
#
# 背景色可以用 $env:CLIPNEST_TARGET_BG 指定（HTML 颜色名或 #rrggbb）：透明弹窗的
# e2e 需要一块**有颜色**的背景才量得出来 —— 米白卡片压在白色文档上，半透明和实心
# 本来就差不了几个像素。
Add-Type -AssemblyName System.Windows.Forms
Add-Type -AssemblyName System.Drawing

$out = Join-Path $env:TEMP 'clipnest-target.txt'
Set-Content -Path $out -Value '' -Encoding utf8

$form = New-Object System.Windows.Forms.Form
$form.Text = 'ClipNest paste target'
$form.Width = 540
$form.Height = 320
$form.StartPosition = 'Manual'
$form.Left = 620
$form.Top = 260

$box = New-Object System.Windows.Forms.TextBox
$box.Multiline = $true
$box.Dock = 'Fill'
$box.Font = New-Object System.Drawing.Font('Consolas', 12)
$box.Text = ''

$bg = $env:CLIPNEST_TARGET_BG
if ($bg) {
  try {
    $colour = [System.Drawing.ColorTranslator]::FromHtml($bg)
    $box.BackColor = $colour
    $form.BackColor = $colour
  } catch {
    Write-Host "忽略无效的 CLIPNEST_TARGET_BG=$bg"
  }
}

$form.Controls.Add($box)

$timer = New-Object System.Windows.Forms.Timer
$timer.Interval = 200
$timer.Add_Tick({
  try { Set-Content -Path $out -Value $box.Text -Encoding utf8 } catch {}
})
$timer.Start()

$form.Add_Shown({ $form.Activate(); $box.Focus() })
[System.Windows.Forms.Application]::Run($form)
