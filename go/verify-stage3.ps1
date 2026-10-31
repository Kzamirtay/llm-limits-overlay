param(
    [string]$TestDir = "C:\Users\User\AppData\Local\Temp\go_overlay_test",
    [string]$DistDir = "\\wsl.localhost\Ubuntu-22.04\home\kzamirtay\agy-limits-overlay\go\dist",
    [string]$CsCliPath = ""
)

Add-Type -AssemblyName System.Windows.Forms
Add-Type -AssemblyName System.Drawing

$code = @"
using System;
using System.Runtime.InteropServices;

public static class Win32 {
    [StructLayout(LayoutKind.Sequential)]
    public struct RECT { public int Left, Top, Right, Bottom; }
    [StructLayout(LayoutKind.Sequential)]
    public struct POINT { public int X, Y; public POINT(int x, int y) { X = x; Y = y; } }

    [DllImport("user32.dll", SetLastError = true)]
    public static extern bool GetWindowRect(IntPtr hWnd, out RECT lpRect);

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

Write-Output "=========================================================="
Write-Output "  LLM Limits Overlay (Go/Wails) - Stage 3 Verification"
Write-Output "=========================================================="

# 0. Temporarily suspend existing overlay process to free up Ctrl+Alt+L hotkey
$existingOverlay = Get-Process -Name "llm-limits-overlay", "LlmLimitsOverlay" -ErrorAction SilentlyContinue
$restorableOverlayPath = $null
if ($existingOverlay) {
    Write-Output "[Setup] Temporarily suspending running overlay (PID $($existingOverlay[0].Id)) to test Ctrl+Alt+L..."
    $restorableOverlayPath = $existingOverlay[0].Path
    Stop-Process -Id $existingOverlay.Id -Force
    Start-Sleep -Seconds 1
}

try {
    # Ensure TestDir has the latest binary and config
    New-Item -ItemType Directory -Force -Path $TestDir | Out-Null
    Copy-Item "$DistDir\llm-limits-overlay.exe" -Destination "$TestDir\llm-limits-overlay.exe" -Force
    Copy-Item "\\wsl.localhost\Ubuntu-22.04\home\kzamirtay\agy-limits-overlay\.env" -Destination "$TestDir\.env" -Force
    Copy-Item "\\wsl.localhost\Ubuntu-22.04\home\kzamirtay\agy-limits-overlay\overlay.config.json" -Destination "$TestDir\overlay.config.json" -Force

    # Remove any existing pos file for initial test
    if (Test-Path "$TestDir\window.pos.json") {
        Remove-Item "$TestDir\window.pos.json" -Force
    }

    Write-Output "`n[Test 1] Starting Overlay with LIVE keys (Cycles 1-3)..."
    $exePath = "$TestDir\llm-limits-overlay.exe"
    $proc = Start-Process -FilePath $exePath -WorkingDirectory $TestDir -PassThru
    Write-Output "Launched process PID: $($proc.Id)"

    # Wait for window handle
    $hwnd = [IntPtr]::Zero
    for ($i = 0; $i -lt 30; $i++) {
        $p = Get-Process -Id $proc.Id -ErrorAction SilentlyContinue
        if ($p -and $p.MainWindowHandle -ne [IntPtr]::Zero) {
            $hwnd = $p.MainWindowHandle
            break
        }
        Start-Sleep -Milliseconds 300
    }
    Write-Output "Overlay HWND: $hwnd"

    # Wait for Cycle 1 to complete (agy + kimi + openrouter live fetch)
    Write-Output "Waiting for live fetch cycle 1 to finish (up to 10s)..."
    for ($i = 0; $i -lt 20; $i++) {
        if (Test-Path "$TestDir\limits.json") {
            try {
                $raw = Get-Content "$TestDir\limits.json" -Raw | ConvertFrom-Json
                if ($raw.status -eq "online") {
                    Write-Output "Cycle 1 complete: status = $($raw.status)"
                    break
                }
            } catch { }
        }
        Start-Sleep -Milliseconds 500
    }

    # Verify Window Rect and Styles
    $rect = New-Object Win32+RECT
    [Win32]::GetWindowRect($hwnd, [ref]$rect) | Out-Null
    $left = [int]$rect.Left; $top = [int]$rect.Top; $right = [int]$rect.Right; $bottom = [int]$rect.Bottom
    $width = $right - $left; $height = $bottom - $top
    Write-Output "Window Bounds: Left=$left, Top=$top, Right=$right, Bottom=$bottom ($width x $height)"

    $GWL_STYLE = -16
    $GWL_EXSTYLE = -20
    $style = [Win32]::GetWindowLong($hwnd, $GWL_STYLE)
    $exStyle = [Win32]::GetWindowLong($hwnd, $GWL_EXSTYLE)
    $WS_CAPTION = 0x00C00000
    $WS_EX_TOPMOST = 0x00000008
    $isFrameless = ($style -band $WS_CAPTION) -eq 0
    $isTopMost = ($exStyle -band $WS_EX_TOPMOST) -ne 0
    Write-Output "Window Style: Frameless=$isFrameless, TopMost=$isTopMost"

    # [Test 2] Screenshots on White and Dark backdrops
    Write-Output "`n[Test 2] Taking Screenshots on White vs Dark backdrops..."
    $bgForm = New-Object System.Windows.Forms.Form
    $bgForm.FormBorderStyle = [System.Windows.Forms.FormBorderStyle]::None
    $bgForm.StartPosition = [System.Windows.Forms.FormStartPosition]::Manual
    $bgForm.Location = [System.Drawing.Point]::new(($left - 30), ($top - 30))
    $bgForm.Size = [System.Drawing.Size]::new(($width + 60), ($height + 60))
    $bgForm.TopMost = $true
    $bgForm.BackColor = [System.Drawing.Color]::White
    $bgForm.Show()
    [Win32]::SetWindowPos($bgForm.Handle, $hwnd, 0, 0, 0, 0, 0x0013) | Out-Null
    $bgForm.Refresh()
    [System.Windows.Forms.Application]::DoEvents()
    Start-Sleep -Milliseconds 600

    $bmpWhite = New-Object System.Drawing.Bitmap($width, $height)
    $g = [System.Drawing.Graphics]::FromImage($bmpWhite)
    $g.CopyFromScreen($left, $top, 0, 0, $bmpWhite.Size)
    $whitePath = "$DistDir\stage3-white.png"
    $bmpWhite.Save($whitePath, [System.Drawing.Imaging.ImageFormat]::Png)
    $g.Dispose()

    $bgForm.BackColor = [System.Drawing.Color]::FromArgb(18, 18, 18) # Dark
    $bgForm.Refresh()
    [System.Windows.Forms.Application]::DoEvents()
    Start-Sleep -Milliseconds 600

    $bmpDark = New-Object System.Drawing.Bitmap($width, $height)
    $g = [System.Drawing.Graphics]::FromImage($bmpDark)
    $g.CopyFromScreen($left, $top, 0, 0, $bmpDark.Size)
    $darkPath = "$DistDir\stage3-dark.png"
    $bmpDark.Save($darkPath, [System.Drawing.Imaging.ImageFormat]::Png)
    $g.Dispose()
    $bgForm.Close()

    $hashWhite = (Get-FileHash -Path $whitePath -Algorithm MD5).Hash
    $hashDark = (Get-FileHash -Path $darkPath -Algorithm MD5).Hash
    Write-Output "Screenshot White MD5: $hashWhite"
    Write-Output "Screenshot Dark MD5:  $hashDark"
    $md5Diff = ($hashWhite -ne $hashDark)
    Write-Output "Translucency verified (hashes differ): $md5Diff"

    # [Test 3] Click-Through & Hotkey Ctrl+Alt+L
    Write-Output "`n[Test 3] Testing Hotkey Ctrl+Alt+L & Click-Through..."
    $WS_EX_TRANSPARENT = 0x00000020
    $exBefore = [Win32]::GetWindowLong($hwnd, $GWL_EXSTYLE)
    $hasTransBefore = ($exBefore -band $WS_EX_TRANSPARENT) -ne 0
    Write-Output "Initial WS_EX_TRANSPARENT: $hasTransBefore"

    Write-Output "Sending Ctrl+Alt+L..."
    [Win32]::keybd_event(0x11, 0, 0, 0)
    [Win32]::keybd_event(0x12, 0, 0, 0)
    [Win32]::keybd_event(0x4C, 0, 0, 0)
    Start-Sleep -Milliseconds 100
    [Win32]::keybd_event(0x4C, 0, 2, 0)
    [Win32]::keybd_event(0x12, 0, 2, 0)
    [Win32]::keybd_event(0x11, 0, 2, 0)
    Start-Sleep -Milliseconds 800

    $exAfter = [Win32]::GetWindowLong($hwnd, $GWL_EXSTYLE)
    $hasTransAfter = ($exAfter -band $WS_EX_TRANSPARENT) -ne 0
    Write-Output "After Ctrl+Alt+L: WS_EX_TRANSPARENT = $hasTransAfter"
    $hotkeyWorks = ($hasTransBefore -ne $hasTransAfter)
    Write-Output "Hotkey toggled style: $hotkeyWorks"

    # Restore
    [Win32]::keybd_event(0x11, 0, 0, 0)
    [Win32]::keybd_event(0x12, 0, 0, 0)
    [Win32]::keybd_event(0x4C, 0, 0, 0)
    Start-Sleep -Milliseconds 100
    [Win32]::keybd_event(0x4C, 0, 2, 0)
    [Win32]::keybd_event(0x12, 0, 2, 0)
    [Win32]::keybd_event(0x11, 0, 2, 0)
    Start-Sleep -Milliseconds 600

    # [Test 4] Systray presence
    Write-Output "`n[Test 4] Checking Systray..."
    $trayWnd = [Win32]::FindWindow("SystrayClass", $null)
    $trayFound = ($trayWnd -ne [IntPtr]::Zero)
    Write-Output "Tray Window (SystrayClass) found: $trayFound"

    # [Test 5] Single-Instance Check
    Write-Output "`n[Test 5] Checking Single-Instance Protection..."
    Write-Output "Attempting to launch second instance..."
    $proc2 = Start-Process -FilePath $exePath -WorkingDirectory $TestDir -PassThru
    Start-Sleep -Seconds 2
    $allProcs = Get-Process -Name "llm-limits-overlay" -ErrorAction SilentlyContinue
    $procCount = ($allProcs | Measure-Object).Count
    Write-Output "Total running instances after second launch: $procCount (expected: 1)"
    $singleInstanceOk = ($procCount -eq 1)

    # Stop overlay instance 1
    Stop-Process -Id $proc.Id -Force
    Start-Sleep -Seconds 1

    # [Test 6] Position Persistence Restart Check
    Write-Output "`n[Test 6] Checking Window Position Persistence..."
    $customPos = @{ x = 750; y = 350 }
    $customPos | ConvertTo-Json | Set-Content "$TestDir\window.pos.json"
    Write-Output "Wrote custom position: x=750, y=350 to window.pos.json"

    $proc3 = Start-Process -FilePath $exePath -WorkingDirectory $TestDir -PassThru
    Start-Sleep -Seconds 2
    $hwnd3 = [IntPtr]::Zero
    for ($i = 0; $i -lt 15; $i++) {
        $p = Get-Process -Id $proc3.Id -ErrorAction SilentlyContinue
        if ($p -and $p.MainWindowHandle -ne [IntPtr]::Zero) {
            $hwnd3 = $p.MainWindowHandle; break
        }
        Start-Sleep -Milliseconds 200
    }
    $rect3 = New-Object Win32+RECT
    [Win32]::GetWindowRect($hwnd3, [ref]$rect3) | Out-Null
    Write-Output "Restored Window Bounds: Left=$($rect3.Left), Top=$($rect3.Top)"
    $posPersistOk = ($rect3.Left -eq 750 -and $rect3.Top -eq 350)
    Write-Output "Position restored correctly: $posPersistOk"
    Stop-Process -Id $proc3.Id -Force
    Start-Sleep -Seconds 1

    # [Test 7] Scenario no_key (using .env.example)
    Write-Output "`n[Test 7] Checking Scenario: no_key (placeholder keys)..."
    $noKeyDir = "C:\Users\User\AppData\Local\Temp\go_overlay_nokey"
    New-Item -ItemType Directory -Force -Path $noKeyDir | Out-Null
    Copy-Item "$DistDir\llm-limits-overlay.exe" -Destination "$noKeyDir\llm-limits-overlay.exe" -Force
    Copy-Item "\\wsl.localhost\Ubuntu-22.04\home\kzamirtay\agy-limits-overlay\.env.example" -Destination "$noKeyDir\.env" -Force
    Copy-Item "\\wsl.localhost\Ubuntu-22.04\home\kzamirtay\agy-limits-overlay\overlay.config.json" -Destination "$noKeyDir\overlay.config.json" -Force

    $procNoKey = Start-Process -FilePath "$noKeyDir\llm-limits-overlay.exe" -WorkingDirectory $noKeyDir -PassThru
    Start-Sleep -Seconds 3
    Stop-Process -Id $procNoKey.Id -Force

    $noKeyOk = $false
    if (Test-Path "$noKeyDir\limits.json") {
        $nkJson = Get-Content "$noKeyDir\limits.json" -Raw | ConvertFrom-Json
        $kimiStatus = $nkJson.data.kimi.status
        $orStatus = $nkJson.data.openrouter.status
        Write-Output "no_key check: kimi status = $kimiStatus, openrouter status = $orStatus"
        $noKeyOk = ($kimiStatus -eq "no_key" -and $orStatus -eq "no_key")
    }
    Write-Output "no_key scenario passed: $noKeyOk"
    Remove-Item -Recurse -Force $noKeyDir -ErrorAction SilentlyContinue

    # [Test 8] Scenario stale (broken agy binary)
    Write-Output "`n[Test 8] Checking Scenario: stale (invalid agy-bin)..."
    $staleDir = "C:\Users\User\AppData\Local\Temp\go_overlay_stale"
    New-Item -ItemType Directory -Force -Path $staleDir | Out-Null
    Copy-Item "$DistDir\llm-limits-overlay.exe" -Destination "$staleDir\llm-limits-overlay.exe" -Force
    Copy-Item "$TestDir\.env" -Destination "$staleDir\.env" -Force
    Copy-Item "$TestDir\overlay.config.json" -Destination "$staleDir\overlay.config.json" -Force

    # Provide an existing limits.json so it can fall back to stale
    Copy-Item "$TestDir\limits.json" -Destination "$staleDir\limits.json" -Force

    # Clear agy cache in TEMP so it attempts fresh execution
    Remove-Item "$env:TEMP\llm_limits_agy_usage_cache.json" -Force -ErrorAction SilentlyContinue

    # Set broken LLM_LIMITS_AGY_BIN in environment (whoami.exe exists but fails on -p /usage)
    $psi = New-Object System.Diagnostics.ProcessStartInfo
    $psi.FileName = "$staleDir\llm-limits-overlay.exe"
    $psi.WorkingDirectory = $staleDir
    $psi.EnvironmentVariables["LLM_LIMITS_AGY_BIN"] = "C:\Windows\System32\whoami.exe"
    $psi.UseShellExecute = $false
    $procStale = [System.Diagnostics.Process]::Start($psi)
    Start-Sleep -Seconds 4
    $procStale.Kill()

    $staleOk = $false
    if (Test-Path "$staleDir\limits.json") {
        $staleJson = Get-Content "$staleDir\limits.json" -Raw | ConvertFrom-Json
        $agyStatus = $staleJson.data.agy.status
        Write-Output "stale check: agy status = $agyStatus"
        $staleOk = ($agyStatus -eq "stale")
    }
    Write-Output "stale scenario passed: $staleOk"
    Remove-Item -Recurse -Force $staleDir -ErrorAction SilentlyContinue

    Write-Output "`n=========================================================="
    Write-Output "                 VERIFICATION RESULTS SUMMARY"
    Write-Output "=========================================================="
    Write-Output "Frameless & TopMost:      $($isFrameless -and $isTopMost)"
    Write-Output "Translucent (MD5 diff):   $md5Diff"
    Write-Output "Click-Through Hotkey:     $hotkeyWorks"
    Write-Output "Systray Menu Present:     $trayFound"
    Write-Output "Single-Instance:          $singleInstanceOk"
    Write-Output "Position Persistence:     $posPersistOk"
    Write-Output "Scenario no_key:          $noKeyOk"
    Write-Output "Scenario stale:           $staleOk"
    Write-Output "=========================================================="

} finally {
    # Restore user's original background overlay process if it was running
    if ($restorableOverlayPath) {
        Write-Output "`n[Cleanup] Restoring user's overlay: $restorableOverlayPath"
        Start-Process -FilePath $restorableOverlayPath
    }
}
