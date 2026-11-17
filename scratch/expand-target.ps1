# 缩写展开的端到端靶子：一个只有文本框的小窗口，会把内容定期写到文件。
#
#   powershell -STA -File scratch\expand-target.ps1 -Out C:\tmp\dump.txt
Add-Type -AssemblyName System.Windows.Forms
Add-Type -AssemblyName System.Drawing

$out = "$env:TEMP\expand-dump.txt"
if ($args.Length -ge 1) { $out = $args[0] }
Set-Content -Path $out -Value '' -Encoding utf8

$form = New-Object System.Windows.Forms.Form
$form.Text = 'ClipNest expand target'
$form.Width = 620
$form.Height = 220
$form.StartPosition = 'CenterScreen'

$box = New-Object System.Windows.Forms.TextBox
$box.Multiline = $true
$box.Dock = 'Fill'
$box.Font = New-Object System.Drawing.Font('Consolas', 12)
$form.Controls.Add($box)

$timer = New-Object System.Windows.Forms.Timer
$timer.Interval = 250
$timer.Add_Tick({
    try { Set-Content -Path $out -Value $script:box.Text -Encoding utf8 -NoNewline } catch { }
})
$timer.Start()

$form.Add_Shown({
    $form.Activate()
    $box.Focus()
})
[System.Windows.Forms.Application]::Run($form)
