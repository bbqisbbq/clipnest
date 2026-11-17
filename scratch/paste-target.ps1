# A throwaway paste target: a WinForms window with a focused TextBox that keeps
# writing its content to a file, so the e2e test can verify what was pasted.
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
$form.Controls.Add($box)

$timer = New-Object System.Windows.Forms.Timer
$timer.Interval = 200
$timer.Add_Tick({
  try { Set-Content -Path $out -Value $box.Text -Encoding utf8 } catch {}
})
$timer.Start()

$form.Add_Shown({ $form.Activate(); $box.Focus() })
[System.Windows.Forms.Application]::Run($form)
