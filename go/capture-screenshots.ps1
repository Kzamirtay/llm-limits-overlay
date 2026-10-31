$ErrorActionPreference = "Stop"
$env:SEE_MASK_NOZONECHECKS = "1"

Add-Type -AssemblyName System.Drawing
Add-Type -AssemblyName System.Windows.Forms

$code = @"
using System;
using System.Text;
using System.Runtime.InteropServices;

public class Win32Capture {
    [DllImport("user32.dll")] public static extern bool EnumWindows(EnumWindowsProc lpEnumFunc, IntPtr lParam);
    public delegate bool EnumWindowsProc(IntPtr hWnd, IntPtr lParam);
    [DllImport("user32.dll")] public static extern uint GetWindowThreadProcessId(IntPtr hWnd, out uint lpdwProcessId);
    [DllImport("user32.dll", SetLastError = true)] public static extern bool GetWindowRect(IntPtr hWnd, out RECT lpRect);
    [DllImport("user32.dll")] public static extern bool SetWindowPos(IntPtr hWnd, IntPtr hWndInsertAfter, int X, int Y, int cx, int cy, uint uFlags);
    [DllImport("user32.dll")] public static extern bool PostMessage(IntPtr hWnd, uint Msg, IntPtr wParam, IntPtr lParam);
    public const uint WM_DESTROY = 0x0002;

    public static IntPtr FindOverlayWindow(uint pid) {
        IntPtr found = IntPtr.Zero;
        EnumWindows((hWnd, lParam) => {
            uint procId;
            GetWindowThreadProcessId(hWnd, out procId);
            if (procId == pid) {
                found = hWnd;
                return false;
            }
            return true;
        }, IntPtr.Zero);
        return found;
    }
}

[StructLayout(LayoutKind.Sequential)]
public struct RECT {
    public int Left;
    public int Top;
    public int Right;
    public int Bottom;
}
"@
Add-Type -TypeDefinition $code

$RepoRoot = "\\wsl.localhost\Ubuntu-22.04\home\kzamirtay\agy-limits-overlay"
$ExeSrc = "$RepoRoot\go\dist\llm-limits-overlay.exe"
$CfgSrc = "$RepoRoot\overlay.config.json"
$EnvSrc = "$RepoRoot\go\dist\.env"
$TempDir = "C:\Users\User\AppData\Local\Temp\llmo-capture"

if (Test-Path $TempDir) { Remove-Item -Recurse -Force $TempDir }
New-Item -ItemType Directory -Path $TempDir | Out-Null
Copy-Item $ExeSrc (Join-Path $TempDir "llm-limits-overlay.exe") -Force
Copy-Item $CfgSrc (Join-Path $TempDir "overlay.config.json") -Force
if (Test-Path $EnvSrc) { Copy-Item $EnvSrc (Join-Path $TempDir ".env") -Force }
Unblock-File (Join-Path $TempDir "llm-limits-overlay.exe") -ErrorAction SilentlyContinue

$ExePath = Join-Path $TempDir "llm-limits-overlay.exe"
$LightPath = "$RepoRoot\docs\screenshots\light.png"
$DarkPath = "$RepoRoot\docs\screenshots\dark.png"

Write-Output "Starting fresh Go overlay..."
$proc = Start-Process -FilePath $ExePath -WorkingDirectory $TempDir -PassThru

# Wait for initial fetch to complete and snapshot to update
$logPath = Join-Path $TempDir "overlay32.log"
for ($i = 0; $i -lt 20; $i++) {
    Start-Sleep -Milliseconds 500
    if (Test-Path $logPath) {
        $content = Get-Content $logPath -Raw -ErrorAction SilentlyContinue
        if ($content -match "snapshot updated") {
            Write-Output "Snapshot updated in log."
            break
        }
    }
}
Start-Sleep -Milliseconds 800

$hwnd = [Win32Capture]::FindOverlayWindow($proc.Id)
if ($hwnd -eq [IntPtr]::Zero) {
    Stop-Process -Id $proc.Id -Force
    throw "Could not find Go overlay window for PID $($proc.Id)."
}

Write-Output "Found overlay window HWND: $hwnd"

$rect = New-Object RECT
[Win32Capture]::GetWindowRect($hwnd, [ref]$rect) | Out-Null
$width = $rect.Right - $rect.Left
$height = $rect.Bottom - $rect.Top
$left = $rect.Left
$top = $rect.Top

Write-Output "Overlay rect: $left, $top, ${width}x$height"

# Backdrop form
$bgForm = New-Object System.Windows.Forms.Form
$bgForm.FormBorderStyle = [System.Windows.Forms.FormBorderStyle]::None
$bgForm.StartPosition = [System.Windows.Forms.FormStartPosition]::Manual
$bgForm.Location = [System.Drawing.Point]::new(($left - 20), ($top - 20))
$bgForm.Size = [System.Drawing.Size]::new(($width + 40), ($height + 40))
$bgForm.TopMost = $true
$bgForm.ShowInTaskbar = $false
$bgForm.BackColor = [System.Drawing.Color]::White
$bgForm.Show()

[Win32Capture]::SetWindowPos($bgForm.Handle, $hwnd, 0, 0, 0, 0, 0x0013) | Out-Null
$bgForm.Refresh()
[System.Windows.Forms.Application]::DoEvents()
Start-Sleep -Milliseconds 600

# Capture White
$bmpWhite = New-Object System.Drawing.Bitmap($width, $height)
$gWhite = [System.Drawing.Graphics]::FromImage($bmpWhite)
$gWhite.CopyFromScreen($left, $top, 0, 0, $bmpWhite.Size)
$gWhite.Dispose()

# Change to Dark
$bgForm.BackColor = [System.Drawing.Color]::FromArgb(18, 18, 18)
$bgForm.Refresh()
[System.Windows.Forms.Application]::DoEvents()
Start-Sleep -Milliseconds 600

# Capture Dark
$bmpDark = New-Object System.Drawing.Bitmap($width, $height)
$gDark = [System.Drawing.Graphics]::FromImage($bmpDark)
$gDark.CopyFromScreen($left, $top, 0, 0, $bmpDark.Size)
$gDark.Dispose()

# Clean up backdrop and close overlay
$bgForm.Close()
$bgForm.Dispose()
[Win32Capture]::PostMessage($hwnd, [Win32Capture]::WM_DESTROY, [IntPtr]::Zero, [IntPtr]::Zero) | Out-Null
Start-Sleep -Seconds 1
Stop-Process -Id $proc.Id -Force -ErrorAction SilentlyContinue
Remove-Item -Recurse -Force $TempDir -ErrorAction SilentlyContinue

# Scale to 420 px width
$targetWidth = 420
$targetHeight = [int][Math]::Round($height * ($targetWidth / $width))

$scaledWhite = New-Object System.Drawing.Bitmap($targetWidth, $targetHeight)
$gScaleW = [System.Drawing.Graphics]::FromImage($scaledWhite)
$gScaleW.InterpolationMode = [System.Drawing.Drawing2D.InterpolationMode]::HighQualityBicubic
$gScaleW.DrawImage($bmpWhite, 0, 0, $targetWidth, $targetHeight)
$gScaleW.Dispose()
$scaledWhite.Save($LightPath, [System.Drawing.Imaging.ImageFormat]::Png)
$scaledWhite.Dispose()
$bmpWhite.Dispose()

$scaledDark = New-Object System.Drawing.Bitmap($targetWidth, $targetHeight)
$gScaleD = [System.Drawing.Graphics]::FromImage($scaledDark)
$gScaleD.InterpolationMode = [System.Drawing.Drawing2D.InterpolationMode]::HighQualityBicubic
$gScaleD.DrawImage($bmpDark, 0, 0, $targetWidth, $targetHeight)
$gScaleD.Dispose()
$scaledDark.Save($DarkPath, [System.Drawing.Imaging.ImageFormat]::Png)
$scaledDark.Dispose()
$bmpDark.Dispose()

$whiteMd5 = (Get-FileHash -Path $LightPath -Algorithm MD5).Hash
$darkMd5  = (Get-FileHash -Path $DarkPath -Algorithm MD5).Hash
Write-Output "Light screenshot: $LightPath (MD5: $whiteMd5)"
Write-Output "Dark screenshot:  $DarkPath (MD5: $darkMd5)"
Write-Output "Checksums differ: $($whiteMd5 -ne $darkMd5)"
