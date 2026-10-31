param(
    [string]$ExePath = "\\wsl.localhost\Ubuntu-22.04\home\kzamirtay\agy-limits-overlay\go\dist\llm-limits-overlay-poc.exe",
    [string]$OutputDir = "\\wsl.localhost\Ubuntu-22.04\home\kzamirtay\agy-limits-overlay\go\dist"
)

Add-Type -AssemblyName System.Windows.Forms
Add-Type -AssemblyName System.Drawing

$code = @"
using System;
using System.Runtime.InteropServices;

public static class Win32 {
    [StructLayout(LayoutKind.Sequential)]
    public struct RECT {
        public int Left;
        public int Top;
        public int Right;
        public int Bottom;
    }

    [StructLayout(LayoutKind.Sequential)]
    public struct POINT {
        public int X;
        public int Y;
        public POINT(int x, int y) { X = x; Y = y; }
    }

    [DllImport("user32.dll", SetLastError = true)]
    public static extern bool GetWindowRect(IntPtr hWnd, out RECT lpRect);

    [DllImport("user32.dll", EntryPoint = "GetWindowLongPtr")]
    public static extern IntPtr GetWindowLongPtr(IntPtr hWnd, int nIndex);

    [DllImport("user32.dll", EntryPoint = "GetWindowLong")]
    public static extern int GetWindowLong(IntPtr hWnd, int nIndex);

    [DllImport("user32.dll")]
    public static extern IntPtr WindowFromPoint(POINT Point);

    [DllImport("user32.dll")]
    public static extern uint GetWindowThreadProcessId(IntPtr hWnd, out uint lpdwProcessId);

    [DllImport("user32.dll")]
    public static extern void keybd_event(byte bVk, byte bScan, uint dwFlags, int dwExtraInfo);

    [DllImport("user32.dll")]
    public static extern IntPtr FindWindow(string lpClassName, string lpWindowName);

    [DllImport("user32.dll")]
    public static extern bool SetWindowPos(IntPtr hWnd, IntPtr hWndInsertAfter, int X, int Y, int cx, int cy, uint uFlags);
}
"@
Add-Type -TypeDefinition $code

Write-Output "=== Starting LLM Limits Overlay PoC Test ==="

$existingOverlay = Get-Process -Name "llm-limits-overlay", "LlmLimitsOverlay" -ErrorAction SilentlyContinue
$restorableOverlayPath = $null
if ($existingOverlay) {
    Write-Output "Temporarily suspending existing overlay (PID $($existingOverlay[0].Id)) to test global hotkey Ctrl+Alt+L..."
    $restorableOverlayPath = $existingOverlay[0].Path
    Stop-Process -Id $existingOverlay.Id -Force
    Start-Sleep -Seconds 1
}

Write-Output "Launching: $ExePath"
$proc = Start-Process -FilePath $ExePath -PassThru
Start-Sleep -Seconds 3

try {
    $hwnd = [IntPtr]::Zero
    for ($i = 0; $i -lt 30; $i++) {
        $p = Get-Process -Id $proc.Id -ErrorAction SilentlyContinue
        if ($p -and $p.MainWindowHandle -ne [IntPtr]::Zero) {
            $hwnd = $p.MainWindowHandle
            break
        }
        Start-Sleep -Milliseconds 200
    }

    if ($hwnd -eq [IntPtr]::Zero) {
        Write-Error "Failed to obtain MainWindowHandle!"
        exit 1
    }

    Write-Output "Process ID: $($proc.Id)"
    Write-Output "Window Handle (HWND): $hwnd"

    # 1. Check Window Position and Size
    $rect = New-Object Win32+RECT
    [Win32]::GetWindowRect($hwnd, [ref]$rect) | Out-Null
    $left = [int]$rect.Left
    $top = [int]$rect.Top
    $right = [int]$rect.Right
    $bottom = [int]$rect.Bottom
    $width = $right - $left
    $height = $bottom - $top
    Write-Output "Window Rect: Left=$left, Top=$top, Right=$right, Bottom=$bottom (Width=$width, Height=$height)"

    # 2. Check Window Styles (Frameless, AlwaysOnTop)
    $GWL_STYLE = -16
    $GWL_EXSTYLE = -20
    $style = [Win32]::GetWindowLong($hwnd, $GWL_STYLE)
    $exStyle = [Win32]::GetWindowLong($hwnd, $GWL_EXSTYLE)

    $WS_CAPTION = 0x00C00000
    $WS_THICKFRAME = 0x00040000
    $hasCaption = ($style -band $WS_CAPTION) -eq $WS_CAPTION
    $hasThickFrame = ($style -band $WS_THICKFRAME) -ne 0
    Write-Output "Style Frameless check: hasCaption=$hasCaption, hasThickFrame=$hasThickFrame -> Frameless=$((-not $hasCaption))"

    $WS_EX_TOPMOST = 0x00000008
    $isTopMost = ($exStyle -band $WS_EX_TOPMOST) -ne 0
    Write-Output "ExStyle AlwaysOnTop check: isTopMost=$isTopMost"

    # 3. Check Transparency (White vs Black backdrop screenshot)
    Write-Output "Setting up backdrop for transparency test..."
    $bgForm = New-Object System.Windows.Forms.Form
    $bgForm.FormBorderStyle = [System.Windows.Forms.FormBorderStyle]::None
    $bgForm.StartPosition = [System.Windows.Forms.FormStartPosition]::Manual
    $bgForm.Location = [System.Drawing.Point]::new(($left - 30), ($top - 30))
    $bgForm.Size = [System.Drawing.Size]::new(($width + 60), ($height + 60))
    $bgForm.TopMost = $true
    $bgForm.BackColor = [System.Drawing.Color]::White
    $bgForm.Show()
    # Place directly behind the overlay window (below $hwnd in Z-order)
    [Win32]::SetWindowPos($bgForm.Handle, $hwnd, 0, 0, 0, 0, 0x0013) | Out-Null
    $bgForm.Refresh()
    [System.Windows.Forms.Application]::DoEvents()
    Start-Sleep -Milliseconds 800

    # Screenshot on White
    $bmpWhite = New-Object System.Drawing.Bitmap($width, $height)
    $g = [System.Drawing.Graphics]::FromImage($bmpWhite)
    $g.CopyFromScreen($left, $top, 0, 0, $bmpWhite.Size)
    $whitePath = Join-Path $OutputDir "screenshot-white.png"
    $bmpWhite.Save($whitePath, [System.Drawing.Imaging.ImageFormat]::Png)
    $g.Dispose()

    # Backdrop Black
    $bgForm.BackColor = [System.Drawing.Color]::Black
    $bgForm.Refresh()
    [System.Windows.Forms.Application]::DoEvents()
    Start-Sleep -Milliseconds 800

    # Screenshot on Black
    $bmpBlack = New-Object System.Drawing.Bitmap($width, $height)
    $g = [System.Drawing.Graphics]::FromImage($bmpBlack)
    $g.CopyFromScreen($left, $top, 0, 0, $bmpBlack.Size)
    $blackPath = Join-Path $OutputDir "screenshot-black.png"
    $bmpBlack.Save($blackPath, [System.Drawing.Imaging.ImageFormat]::Png)
    $g.Dispose()
    $bgForm.Close()

    $hashWhite = (Get-FileHash -Path $whitePath -Algorithm MD5).Hash
    $hashBlack = (Get-FileHash -Path $blackPath -Algorithm MD5).Hash
    Write-Output "Screenshot White MD5: $hashWhite"
    Write-Output "Screenshot Black MD5: $hashBlack"
    $isTransparent = ($hashWhite -ne $hashBlack)
    Write-Output "Transparency verified (hashes differ): $isTransparent"

    # 4. Check Hotkey Ctrl+Alt+L and Click-Through
    $centerX = [int](($left + $right) / 2)
    $centerY = [int](($top + $bottom) / 2)
    $pt = New-Object Win32+POINT($centerX, $centerY)

    $exBefore = [Win32]::GetWindowLong($hwnd, $GWL_EXSTYLE)
    $wfpBefore = [Win32]::WindowFromPoint($pt)
    $pidBefore = 0
    [Win32]::GetWindowThreadProcessId($wfpBefore, [ref]$pidBefore) | Out-Null
    $isOwnWindowBefore = ($pidBefore -eq $proc.Id)
    $WS_EX_TRANSPARENT = 0x00000020
    $hasTransBefore = ($exBefore -band $WS_EX_TRANSPARENT) -ne 0
    Write-Output "Initial Click-Through: WS_EX_TRANSPARENT=$hasTransBefore, WindowFromPoint belongs to overlay process: $isOwnWindowBefore"

    Write-Output "Simulating Ctrl+Alt+L hotkey..."
    # VK_CONTROL = 0x11, VK_MENU = 0x12, VK_L = 0x4C
    [Win32]::keybd_event(0x11, 0, 0, 0)
    [Win32]::keybd_event(0x12, 0, 0, 0)
    [Win32]::keybd_event(0x4C, 0, 0, 0)
    Start-Sleep -Milliseconds 100
    [Win32]::keybd_event(0x4C, 0, 2, 0)
    [Win32]::keybd_event(0x12, 0, 2, 0)
    [Win32]::keybd_event(0x11, 0, 2, 0)
    Start-Sleep -Milliseconds 800

    $exAfter = [Win32]::GetWindowLong($hwnd, $GWL_EXSTYLE)
    $wfpAfter = [Win32]::WindowFromPoint($pt)
    $pidAfter = 0
    [Win32]::GetWindowThreadProcessId($wfpAfter, [ref]$pidAfter) | Out-Null
    $isOwnWindowAfter = ($pidAfter -eq $proc.Id)
    $hasTransAfter = ($exAfter -band $WS_EX_TRANSPARENT) -ne 0
    Write-Output "After Ctrl+Alt+L: WS_EX_TRANSPARENT=$hasTransAfter, WindowFromPoint belongs to overlay process: $isOwnWindowAfter"

    $hotkeyToggledStyle = ($hasTransBefore -ne $hasTransAfter)
    Write-Output "Hotkey changed WS_EX_TRANSPARENT: $hotkeyToggledStyle"

    # Toggle back to restore
    Write-Output "Toggling Ctrl+Alt+L back..."
    [Win32]::keybd_event(0x11, 0, 0, 0)
    [Win32]::keybd_event(0x12, 0, 0, 0)
    [Win32]::keybd_event(0x4C, 0, 0, 0)
    Start-Sleep -Milliseconds 100
    [Win32]::keybd_event(0x4C, 0, 2, 0)
    [Win32]::keybd_event(0x12, 0, 2, 0)
    [Win32]::keybd_event(0x11, 0, 2, 0)
    Start-Sleep -Milliseconds 800

    $exRestored = [Win32]::GetWindowLong($hwnd, $GWL_EXSTYLE)
    $hasTransRestored = ($exRestored -band $WS_EX_TRANSPARENT) -ne 0
    Write-Output "Restored Click-Through: WS_EX_TRANSPARENT=$hasTransRestored"

    # 5. Check Tray window presence
    $trayWnd = [Win32]::FindWindow("SystrayClass", $null)
    $trayFound = ($trayWnd -ne [IntPtr]::Zero)
    Write-Output "Tray Class window found (SystrayClass): $trayFound (HWND=$trayWnd)"

    Write-Output "=== Test Summary ==="
    Write-Output "Frameless: $(-not $hasCaption)"
    Write-Output "AlwaysOnTop: $isTopMost"
    Write-Output "Translucent (MD5 diff): $isTransparent"
    Write-Output "Hotkey ClickThrough Toggle: $hotkeyToggledStyle"
    Write-Output "Tray Present: $trayFound"

} finally {
    Write-Output "Terminating overlay process..."
    Stop-Process -Id $proc.Id -Force -ErrorAction SilentlyContinue
    Write-Output "Overlay process stopped."
    if ($restorableOverlayPath) {
        Write-Output "Restoring overlay from $restorableOverlayPath..."
        Start-Process -FilePath $restorableOverlayPath
        Start-Sleep -Seconds 1
    }
}
