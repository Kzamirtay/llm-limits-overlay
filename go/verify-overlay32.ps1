# ==============================================================================
# LLM Limits Overlay32 (Win32 Layered + GDI+) - Verification & Benchmark
# ==============================================================================

param(
    [int]$BenchDuration = 60
)

$ErrorActionPreference = "Stop"

$code = @"
using System;
using System.Drawing;
using System.Runtime.InteropServices;

public class Win32 {
    [DllImport("user32.dll")]
    public static extern IntPtr FindWindow(string lpClassName, string lpWindowName);

    [DllImport("user32.dll", SetLastError = true)]
    public static extern int GetWindowLong(IntPtr hWnd, int nIndex);

    [DllImport("user32.dll")]
    public static extern bool GetWindowRect(IntPtr hWnd, out RECT lpRect);

    [DllImport("user32.dll")]
    public static extern bool SetWindowPos(IntPtr hWnd, IntPtr hWndInsertAfter, int X, int Y, int cx, int cy, uint uFlags);

    [DllImport("user32.dll")]
    public static extern void keybd_event(byte bVk, byte bScan, uint dwFlags, int dwExtraInfo);

    [DllImport("user32.dll")]
    public static extern bool ShowWindow(IntPtr hWnd, int nCmdShow);

    [StructLayout(LayoutKind.Sequential)]
    public struct RECT {
        public int Left, Top, Right, Bottom;
    }
}
"@
Add-Type -TypeDefinition $code -ReferencedAssemblies System.Drawing
Add-Type -AssemblyName System.Windows.Forms

$ScriptDir = Split-Path -Parent $MyInvocation.MyCommand.Path
$RepoRoot = Split-Path -Parent $ScriptDir
$DistDir = "$RepoRoot\go\dist"
$TestDir = "C:\Users\User\AppData\Local\Temp\go_overlay32_test"
$ExePath = "$TestDir\llm-limits-overlay32.exe"

Write-Output "=========================================================="
Write-Output "  LLM Limits Overlay32 (Win32 Native) - Stage 4 Test      "
Write-Output "=========================================================="

# Stop running WPF overlay to prevent hotkey/tray conflicts
$runningWpf = Get-Process -Name "LlmLimitsOverlay" -ErrorAction SilentlyContinue
$wpfPath = $null
if ($runningWpf) {
    $wpfPath = $runningWpf[0].Path
    Write-Output "[Setup] Temporarily suspending running WPF overlay (PID $($runningWpf[0].Id)) to test Ctrl+Alt+L..."
    Stop-Process -Id $runningWpf.Id -Force
    Start-Sleep -Seconds 1
}

# Stop any existing overlay32 processes
Stop-Process -Name "llm-limits-overlay32" -Force -ErrorAction SilentlyContinue
Start-Sleep -Milliseconds 500

try {
    # Prepare clean test folder
    if (Test-Path $TestDir) {
        Remove-Item -Recurse -Force $TestDir -ErrorAction SilentlyContinue
    }
    New-Item -ItemType Directory -Force -Path $TestDir | Out-Null

    Copy-Item "$DistDir\llm-limits-overlay32.exe" -Destination $ExePath -Force
    if (Test-Path "$RepoRoot\.env") {
        Copy-Item "$RepoRoot\.env" -Destination "$TestDir\.env" -Force
    }
    if (Test-Path "$RepoRoot\overlay.config.json") {
        Copy-Item "$RepoRoot\overlay.config.json" -Destination "$TestDir\overlay.config.json" -Force
    }

    $exeSize = (Get-Item $ExePath).Length
    $exeSizeMB = [Math]::Round($exeSize / 1MB, 2)
    Write-Output "Binary Size on Disk: $exeSizeMB MB ($exeSize bytes)"

    # [Test 1] Launch Process & HWND detection
    Write-Output "`n[Test 1] Launching llm-limits-overlay32.exe..."
    $proc = Start-Process -FilePath $ExePath -WorkingDirectory $TestDir -PassThru
    Start-Sleep -Seconds 2

    $hwnd = [IntPtr]::Zero
    for ($i = 0; $i -lt 15; $i++) {
        $p = Get-Process -Id $proc.Id -ErrorAction SilentlyContinue
        if ($p -and $p.MainWindowHandle -ne [IntPtr]::Zero) {
            $hwnd = $p.MainWindowHandle
            break
        }
        Start-Sleep -Milliseconds 200
    }

    if ($hwnd -eq [IntPtr]::Zero) {
        # Check by class name
        $hwnd = [Win32]::FindWindow("LLMLimitsOverlay32Class", $null)
    }

    Write-Output "Launched process PID: $($proc.Id)"
    Write-Output "Overlay HWND: $hwnd"

    # Wait for fetch cycle 1
    Write-Output "Waiting for live fetch cycle 1..."
    for ($i = 0; $i -lt 15; $i++) {
        if (Test-Path "$TestDir\limits.json") {
            break
        }
        Start-Sleep -Milliseconds 500
    }

    $rect = New-Object Win32+RECT
    [Win32]::GetWindowRect($hwnd, [ref]$rect) | Out-Null
    $left = $rect.Left
    $top = $rect.Top
    $width = $rect.Right - $rect.Left
    $height = $rect.Bottom - $rect.Top
    Write-Output "Window Bounds: Left=$left, Top=$top, Width=$width, Height=$height"

    # Styles Check
    $GWL_STYLE = -16
    $GWL_EXSTYLE = -20
    $style = [Win32]::GetWindowLong($hwnd, $GWL_STYLE)
    $exStyle = [Win32]::GetWindowLong($hwnd, $GWL_EXSTYLE)

    $WS_POPUP = 0x80000000
    $WS_EX_LAYERED = 0x00080000
    $WS_EX_TOPMOST = 0x00000008
    $isPopup = ($style -band $WS_POPUP) -ne 0
    $isLayered = ($exStyle -band $WS_EX_LAYERED) -ne 0
    $isTopMost = ($exStyle -band $WS_EX_TOPMOST) -ne 0
    Write-Output "Window Styles: Popup=$isPopup, Layered=$isLayered, TopMost=$isTopMost"

    # [Test 2] Screenshots on White and Dark backdrops
    Write-Output "`n[Test 2] Capturing Screenshots on White vs Dark backdrops..."
    $bgForm = New-Object System.Windows.Forms.Form
    $bgForm.FormBorderStyle = [System.Windows.Forms.FormBorderStyle]::None
    $bgForm.StartPosition = [System.Windows.Forms.FormStartPosition]::Manual
    $bgForm.Location = [System.Drawing.Point]::new(($left - 20), ($top - 20))
    $bgForm.Size = [System.Drawing.Size]::new(($width + 40), ($height + 40))
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
    $whitePath = "$DistDir\overlay32-white.png"
    $bmpWhite.Save($whitePath, [System.Drawing.Imaging.ImageFormat]::Png)
    $g.Dispose()

    $bgForm.BackColor = [System.Drawing.Color]::FromArgb(18, 18, 18) # Dark
    $bgForm.Refresh()
    [System.Windows.Forms.Application]::DoEvents()
    Start-Sleep -Milliseconds 600

    $bmpDark = New-Object System.Drawing.Bitmap($width, $height)
    $g = [System.Drawing.Graphics]::FromImage($bmpDark)
    $g.CopyFromScreen($left, $top, 0, 0, $bmpDark.Size)
    $darkPath = "$DistDir\overlay32-dark.png"
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

    # Restore hotkey state
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
    $proc2 = Start-Process -FilePath $ExePath -WorkingDirectory $TestDir -PassThru
    Start-Sleep -Seconds 2
    $allProcs = Get-Process -Name "llm-limits-overlay32" -ErrorAction SilentlyContinue
    $procCount = ($allProcs | Measure-Object).Count
    Write-Output "Total running instances after second launch: $procCount (expected: 1)"
    $singleInstanceOk = ($procCount -eq 1)

    # [Test 6] 60-Second Memory & CPU Benchmark
    Write-Output "`n[Test 6] Running 60-Second Memory & CPU Benchmark..."
    $mainPid = $proc.Id
    $allKids = Get-CimInstance Win32_Process | Where-Object { $_.ParentProcessId -eq $mainPid }
    Write-Output "Children during run: $(($allKids | Measure-Object).Count) (excluding ephemeral agy.exe)"

    for ($s = 1; $s -le 6; $s++) {
        Start-Sleep -Seconds 10
        $pCurrent = Get-Process -Id $mainPid -ErrorAction SilentlyContinue
        if ($pCurrent) {
            $wsMB = [Math]::Round($pCurrent.WorkingSet64 / 1MB, 2)
            $cpu = [Math]::Round($pCurrent.TotalProcessorTime.TotalSeconds, 2)
            Write-Output "  [+$(($s * 10))s] Main WS: $wsMB MB | CPU: ${cpu}s"
        }
    }

    $finalProc = Get-Process -Id $mainPid
    $finalMainWS = [Math]::Round($finalProc.WorkingSet64 / 1MB, 2)
    $finalCPU = [Math]::Round($finalProc.TotalProcessorTime.TotalSeconds, 2)

    # Test Hidden Mode
    Write-Output "Hiding window (Tray mode)..."
    [Win32]::ShowWindow($hwnd, 0) | Out-Null
    Start-Sleep -Seconds 5
    $pAfterHide = Get-Process -Id $mainPid
    $hiddenWS = [Math]::Round($pAfterHide.WorkingSet64 / 1MB, 2)
    Write-Output "Hidden Mode WS: $hiddenWS MB"

    # Stop Process
    Stop-Process -Id $mainPid -Force
    Start-Sleep -Seconds 1

    # [Test 7] Position Persistence Restart Check
    Write-Output "`n[Test 7] Checking Window Position Persistence..."
    $customPos = @{ x = 800; y = 400 }
    $customPos | ConvertTo-Json | Set-Content "$TestDir\window.pos.json"
    Write-Output "Wrote custom position: x=800, y=400 to window.pos.json"

    $proc3 = Start-Process -FilePath $ExePath -WorkingDirectory $TestDir -PassThru
    Start-Sleep -Seconds 2
    $hwnd3 = [IntPtr]::Zero
    for ($i = 0; $i -lt 15; $i++) {
        $p = Get-Process -Id $proc3.Id -ErrorAction SilentlyContinue
        if ($p -and $p.MainWindowHandle -ne [IntPtr]::Zero) {
            $hwnd3 = $p.MainWindowHandle
            break
        }
        Start-Sleep -Milliseconds 200
    }
    $rect3 = New-Object Win32+RECT
    [Win32]::GetWindowRect($hwnd3, [ref]$rect3) | Out-Null
    Write-Output "Restored Window Bounds: Left=$($rect3.Left), Top=$($rect3.Top)"
    $posPersistOk = ($rect3.Left -eq 800 -and $rect3.Top -eq 400)
    Write-Output "Position restored correctly: $posPersistOk"
    Stop-Process -Id $proc3.Id -Force
    Start-Sleep -Seconds 1

    Write-Output "`n=========================================================="
    Write-Output "                 VERIFICATION RESULTS SUMMARY             "
    Write-Output "=========================================================="
    Write-Output "Popup & Layered & TopMost: $($isPopup -and $isLayered -and $isTopMost)"
    Write-Output "Translucent (MD5 diff):    $md5Diff"
    Write-Output "Click-Through Hotkey:      $hotkeyWorks"
    Write-Output "Systray Present:           $trayFound"
    Write-Output "Single-Instance:           $singleInstanceOk"
    Write-Output "Position Persistence:      $posPersistOk"
    Write-Output "WorkingSet (Open):         $finalMainWS MB"
    Write-Output "WorkingSet (Hidden):       $hiddenWS MB"
    Write-Output "Total CPU (60s):           ${finalCPU}s"
    Write-Output "Binary Size:               $exeSizeMB MB"
    Write-Output "=========================================================="

} finally {
    if ($wpfPath) {
        Write-Output "`n[Cleanup] Restoring user's WPF overlay: $wpfPath"
        Start-Process -FilePath $wpfPath
    }
}
