$ErrorActionPreference = "Stop"
$env:SEE_MASK_NOZONECHECKS = "1"

Add-Type -AssemblyName System.Drawing
Add-Type -AssemblyName System.Windows.Forms

$code = @"
using System;
using System.Runtime.InteropServices;

public class Win32 {
    [DllImport("user32.dll")] public static extern bool EnumWindows(EnumWindowsProc lpEnumFunc, IntPtr lParam);
    public delegate bool EnumWindowsProc(IntPtr hWnd, IntPtr lParam);
    [DllImport("user32.dll")] public static extern uint GetWindowThreadProcessId(IntPtr hWnd, out uint lpdwProcessId);
    [DllImport("user32.dll", SetLastError = true)] public static extern bool GetWindowRect(IntPtr hWnd, out RECT lpRect);
    [DllImport("user32.dll")] public static extern bool SystemParametersInfo(int uAction, int uParam, ref RECT lpvParam, int fuWinIni);
    [DllImport("user32.dll")] public static extern int GetSystemMetrics(int nIndex);

    public const int SPI_GETWORKAREA = 0x0030;
    public const int SM_CXSCREEN = 0;
    public const int SM_CYSCREEN = 1;

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
$TempDir = "C:\Users\User\AppData\Local\Temp\llmo-workarea-test"
$ScreenShotPath = "$RepoRoot\docs\screenshots\taskbar-gap.png"

Write-Host "=========================================================="
Write-Host " LLM Limits Overlay - WorkArea Positioning & Clamp Test   "
Write-Host "=========================================================="

# 0. Query system metrics and work area
$scrW = [Win32]::GetSystemMetrics([Win32]::SM_CXSCREEN)
$scrH = [Win32]::GetSystemMetrics([Win32]::SM_CYSCREEN)
$wa = New-Object RECT
[Win32]::SystemParametersInfo([Win32]::SPI_GETWORKAREA, 0, [ref]$wa, 0) | Out-Null

$taskbarH = $scrH - $wa.Bottom
Write-Host "Screen Resolution: ${scrW}x${scrH}"
Write-Host "Work Area:         Left=$($wa.Left), Top=$($wa.Top), Right=$($wa.Right), Bottom=$($wa.Bottom)"
Write-Host "Bottom Taskbar:    Height=$taskbarH px"

# 1. Suspend existing background overlay if running
$existing = Get-Process -Name "llm-limits-overlay", "llm-limits-overlay32", "LlmLimitsOverlay" -ErrorAction SilentlyContinue
$restorable = $null
if ($existing) {
    $restorable = $existing[0].Path
    Write-Host "[Setup] Suspending running overlay (PID $($existing[0].Id))..."
    Stop-Process -Id $existing.Id -Force
    Start-Sleep -Seconds 1
}

try {
    if (Test-Path $TempDir) { Remove-Item -Recurse -Force $TempDir }
    New-Item -ItemType Directory -Path $TempDir | Out-Null
    Copy-Item $ExeSrc (Join-Path $TempDir "llm-limits-overlay.exe") -Force
    Copy-Item $CfgSrc (Join-Path $TempDir "overlay.config.json") -Force
    if (Test-Path $EnvSrc) { Copy-Item $EnvSrc (Join-Path $TempDir ".env") -Force }
    Unblock-File (Join-Path $TempDir "llm-limits-overlay.exe") -ErrorAction SilentlyContinue

    $exePath = Join-Path $TempDir "llm-limits-overlay.exe"

    # -------------------------------------------------------------
    # TEST 1: Default position without window.pos.json
    # -------------------------------------------------------------
    Write-Host "`n[Test 1] Launching without window.pos.json (default work area position)..."
    $p1 = Start-Process -FilePath $exePath -WorkingDirectory $TempDir -PassThru
    Start-Sleep -Seconds 3

    $hwnd1 = [Win32]::FindOverlayWindow($p1.Id)
    if ($hwnd1 -eq [IntPtr]::Zero) { throw "Overlay window not found for PID $($p1.Id)" }

    $rect1 = New-Object RECT
    [Win32]::GetWindowRect($hwnd1, [ref]$rect1) | Out-Null
    $w1 = $rect1.Right - $rect1.Left
    $h1 = $rect1.Bottom - $rect1.Top

    Write-Host "  -> Actual window bounds: Left=$($rect1.Left), Top=$($rect1.Top), Width=$w1, Height=$h1"
    
    $expectedX = $wa.Right - $w1 - 12
    $expectedY = $wa.Bottom - $h1 - 12
    $gapRight = $wa.Right - $rect1.Right
    $gapBottom = $wa.Bottom - $rect1.Bottom

    Write-Host "  -> Expected: Left=$expectedX, Top=$expectedY"
    Write-Host "  -> Gap from WorkArea right:  $gapRight px (expected 12)"
    Write-Host "  -> Gap from WorkArea bottom: $gapBottom px (expected 12)"
    Write-Host "  -> Distance to screen bottom: $($scrH - $rect1.Bottom) px ($taskbarH px taskbar + $gapBottom px gap)"

    if ($rect1.Left -ne $expectedX -or $rect1.Top -ne $expectedY) {
        throw "Test 1 FAILED: Window position ($($rect1.Left), $($rect1.Top)) != expected ($expectedX, $expectedY)"
    }
    Write-Host "  -> [PASS] Window is positioned precisely above taskbar with 12px margin!"

    # Capture visual proof: Bottom-right screen corner showing both overlay and taskbar
    Write-Host "  -> Capturing screenshot of overlay above taskbar to $ScreenShotPath..."
    $cropW = 450
    $cropH = $h1 + $taskbarH + 40
    $cropX = $scrW - $cropW
    $cropY = $scrH - $cropH

    $bmp = New-Object System.Drawing.Bitmap($cropW, $cropH)
    $g = [System.Drawing.Graphics]::FromImage($bmp)
    $g.CopyFromScreen($cropX, $cropY, 0, 0, (New-Object System.Drawing.Size($cropW, $cropH)))
    $bmp.Save($ScreenShotPath, [System.Drawing.Imaging.ImageFormat]::Png)
    $g.Dispose()
    $bmp.Dispose()
    Write-Host "  -> Screenshot saved successfully ($cropW x $cropH px)."

    Stop-Process -Id $p1.Id -Force
    Start-Sleep -Seconds 1

    # -------------------------------------------------------------
    # TEST 2: Invalid position in window.pos.json (old bug position under taskbar)
    # -------------------------------------------------------------
    Write-Host "`n[Test 2] Testing clamping of invalid position (under taskbar: x=1616, y=674)..."
    # Old bug set y = 674 on 1080p, so y + 402 = 1076 (44px under taskbar!)
    $invalidPos = @{ x = 1616; y = 674 }
    $invalidPos | ConvertTo-Json | Set-Content (Join-Path $TempDir "window.pos.json")

    $p2 = Start-Process -FilePath $exePath -WorkingDirectory $TempDir -PassThru
    Start-Sleep -Seconds 3

    $hwnd2 = [Win32]::FindOverlayWindow($p2.Id)
    $rect2 = New-Object RECT
    [Win32]::GetWindowRect($hwnd2, [ref]$rect2) | Out-Null
    $w2 = $rect2.Right - $rect2.Left
    $h2 = $rect2.Bottom - $rect2.Top

    Write-Host "  -> Window bounds after clamp: Left=$($rect2.Left), Top=$($rect2.Top)"
    Write-Host "  -> WorkArea bottom gap: $($wa.Bottom - $rect2.Bottom) px"

    if ($rect2.Bottom -gt $wa.Bottom) {
        throw "Test 2 FAILED: Window still overlaps taskbar after clamp!"
    }
    if ($rect2.Top -ne $expectedY) {
        throw "Test 2 FAILED: Expected clamped Top=$expectedY, got $($rect2.Top)"
    }
    Write-Host "  -> [PASS] Invalid Y position successfully clamped above taskbar to $expectedY!"

    # Verify window.pos.json was updated with clamped position
    $savedJson = Get-Content (Join-Path $TempDir "window.pos.json") -Raw | ConvertFrom-Json
    Write-Host "  -> Updated window.pos.json content: x=$($savedJson.x), y=$($savedJson.y)"
    if ($savedJson.y -ne $expectedY) {
        throw "Test 2 FAILED: window.pos.json was not updated with clamped Y"
    }
    Write-Host "  -> [PASS] window.pos.json automatically updated with clamped coordinates!"

    Stop-Process -Id $p2.Id -Force
    Start-Sleep -Seconds 1

    # -------------------------------------------------------------
    # TEST 3: Out-of-bounds on both axes (e.g. resolution change: x=3000, y=2000)
    # -------------------------------------------------------------
    Write-Host "`n[Test 3] Testing out-of-bounds clamping (x=3000, y=2000)..."
    $oobPos = @{ x = 3000; y = 2000 }
    $oobPos | ConvertTo-Json | Set-Content (Join-Path $TempDir "window.pos.json")

    $p3 = Start-Process -FilePath $exePath -WorkingDirectory $TempDir -PassThru
    Start-Sleep -Seconds 3

    $hwnd3 = [Win32]::FindOverlayWindow($p3.Id)
    $rect3 = New-Object RECT
    [Win32]::GetWindowRect($hwnd3, [ref]$rect3) | Out-Null

    Write-Host "  -> Window bounds after OOB clamp: Left=$($rect3.Left), Top=$($rect3.Top)"
    if ($rect3.Left -ne $expectedX -or $rect3.Top -ne $expectedY) {
        throw "Test 3 FAILED: Expected ($expectedX, $expectedY), got ($($rect3.Left), $($rect3.Top))"
    }
    Write-Host "  -> [PASS] Out-of-bounds position clamped back to work area corner ($expectedX, $expectedY)!"

    Stop-Process -Id $p3.Id -Force
    Start-Sleep -Seconds 1

    # -------------------------------------------------------------
    # TEST 4: Valid user custom position (e.g. x=500, y=300)
    # -------------------------------------------------------------
    Write-Host "`n[Test 4] Testing valid user custom position (x=500, y=300)..."
    $validPos = @{ x = 500; y = 300 }
    $validPos | ConvertTo-Json | Set-Content (Join-Path $TempDir "window.pos.json")

    $p4 = Start-Process -FilePath $exePath -WorkingDirectory $TempDir -PassThru
    Start-Sleep -Seconds 3

    $hwnd4 = [Win32]::FindOverlayWindow($p4.Id)
    $rect4 = New-Object RECT
    [Win32]::GetWindowRect($hwnd4, [ref]$rect4) | Out-Null

    Write-Host "  -> Window bounds for user position: Left=$($rect4.Left), Top=$($rect4.Top)"
    if ($rect4.Left -ne 500 -or $rect4.Top -ne 300) {
        throw "Test 4 FAILED: Valid user position was modified! Got ($($rect4.Left), $($rect4.Top))"
    }
    Write-Host "  -> [PASS] Valid user position preserved completely untouched!"

    Stop-Process -Id $p4.Id -Force
    Start-Sleep -Seconds 1

    Write-Host "`n=== ALL 4 WORK AREA POSITIONING TESTS PASSED SUCCESSFULLY ==="

} finally {
    Stop-Process -Name "llm-limits-overlay" -Force -ErrorAction SilentlyContinue
    if (Test-Path $TempDir) {
        Remove-Item -Recurse -Force $TempDir -ErrorAction SilentlyContinue
    }
    if ($restorable) {
        Write-Host "`n[Cleanup] Restoring original background overlay: $restorable"
        Start-Process -FilePath $restorable
    }
}
