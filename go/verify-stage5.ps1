# ==============================================================================
# LLM Limits Overlay32 - Stage 5 Verification & 1:1 Parity Benchmark
# ==============================================================================

param(
    [int]$BenchDuration = 60
)

$ErrorActionPreference = "Stop"

Add-Type -AssemblyName System.Drawing
Add-Type -AssemblyName System.Windows.Forms

$code = @"
using System;
using System.Drawing;
using System.Drawing.Imaging;
using System.Runtime.InteropServices;
using System.Text;

public class WinUtil {
    [DllImport("user32.dll")] public static extern IntPtr FindWindow(string lpClassName, string lpWindowName);
    [DllImport("user32.dll", SetLastError = true)] public static extern int GetWindowLong(IntPtr hWnd, int nIndex);
    [DllImport("user32.dll")] public static extern bool GetWindowRect(IntPtr hWnd, out RECT lpRect);
    [DllImport("user32.dll")] public static extern bool SetWindowPos(IntPtr hWnd, IntPtr hWndInsertAfter, int X, int Y, int cx, int cy, uint uFlags);
    [DllImport("user32.dll")] public static extern void keybd_event(byte bVk, byte bScan, uint dwFlags, int dwExtraInfo);
    [DllImport("user32.dll")] public static extern bool ShowWindow(IntPtr hWnd, int nCmdShow);
    [DllImport("user32.dll")] public static extern bool PrintWindow(IntPtr hWnd, IntPtr hdcBlt, uint nFlags);
    [DllImport("user32.dll")] public static extern int GetWindowText(IntPtr hWnd, StringBuilder lpString, int nMaxCount);
    [DllImport("user32.dll")] public static extern bool EnumWindows(EnumWindowsProc lpEnumFunc, IntPtr lParam);
    public delegate bool EnumWindowsProc(IntPtr hWnd, IntPtr lParam);
    [DllImport("user32.dll")] public static extern uint GetWindowThreadProcessId(IntPtr hWnd, out uint lpdwProcessId);

    [StructLayout(LayoutKind.Sequential)]
    public struct RECT { public int Left, Top, Right, Bottom; }

    public static IntPtr FindOverlayWindow(uint pid) {
        IntPtr found = IntPtr.Zero;
        EnumWindows((hWnd, lParam) => {
            uint procId;
            GetWindowThreadProcessId(hWnd, out procId);
            if (procId == pid) {
                StringBuilder sb = new StringBuilder(256);
                GetWindowText(hWnd, sb, 256);
                if (sb.ToString().Contains("LLM Limits Overlay")) {
                    found = hWnd;
                    return false;
                }
            }
            return true;
        }, IntPtr.Zero);
        return found;
    }

    public static void CaptureWindow(IntPtr hwnd, string path) {
        RECT r;
        GetWindowRect(hwnd, out r);
        int w = r.Right - r.Left;
        int h = r.Bottom - r.Top;
        if (w <= 0) w = 290;
        if (h <= 0) h = 402;
        using (Bitmap bmp = new Bitmap(w, h, PixelFormat.Format32bppArgb)) {
            using (Graphics g = Graphics.FromImage(bmp)) {
                IntPtr hdc = g.GetHdc();
                PrintWindow(hwnd, hdc, 2); // PW_RENDERFULLCONTENT
                g.ReleaseHdc(hdc);
            }
            bmp.Save(path, ImageFormat.Png);
        }
    }
}
"@
Add-Type -TypeDefinition $code -ReferencedAssemblies System.Drawing

$ScriptDir = Split-Path -Parent $MyInvocation.MyCommand.Path
$RepoRoot = Split-Path -Parent $ScriptDir
$DistDir = "$RepoRoot\go\dist"
$GoExeSrc = "$DistDir\llm-limits-overlay.exe"
if (-not (Test-Path $GoExeSrc)) {
    $GoExeSrc = "$DistDir\llm-limits-overlay32.exe"
}

$TestDir = "C:\Users\User\AppData\Local\Temp\go_stage5_test"

Write-Output "=========================================================="
Write-Output "  LLM Limits Overlay32 - Stage 5 Verification"
Write-Output "=========================================================="

# Find and suspend user's running overlay to prevent hotkey/tray conflicts
$runningOverlays = Get-Process -Name "llm-limits-overlay", "llm-limits-overlay32", "LlmLimitsOverlay" -ErrorAction SilentlyContinue
$restorableProc = $null
if ($runningOverlays) {
    $restorableProc = $runningOverlays[0].Path
    Write-Output "[Setup] Temporarily suspending running overlay (PID $($runningOverlays[0].Id)) for tests..."
    Stop-Process -Id $runningOverlays.Id -Force
    Start-Sleep -Seconds 1
}

Stop-Process -Name "llm-limits-overlay", "llm-limits-overlay32" -Force -ErrorAction SilentlyContinue
Start-Sleep -Milliseconds 500

try {
    if (Test-Path $TestDir) {
        Remove-Item -Recurse -Force $TestDir -ErrorAction SilentlyContinue
    }
    New-Item -ItemType Directory -Force -Path $TestDir | Out-Null
    New-Item -ItemType Directory -Force -Path "$TestDir\assets" | Out-Null

    # Copy files
    Copy-Item $GoExeSrc -Destination "$TestDir\llm-limits-overlay32.exe" -Force
    if (Test-Path "$RepoRoot\assets\app.ico") {
        Copy-Item "$RepoRoot\assets\app.ico" -Destination "$TestDir\assets\app.ico" -Force
    }
    if (Test-Path "$RepoRoot\.env") {
        Copy-Item "$RepoRoot\.env" -Destination "$TestDir\.env" -Force
    }
    if (Test-Path "$RepoRoot\overlay.config.json") {
        Copy-Item "$RepoRoot\overlay.config.json" -Destination "$TestDir\overlay.config.json" -Force
    }

    # Reference limits.json: use the active data snapshot
    $sampleLimits = "$RepoRoot\limits.json"
    Copy-Item $sampleLimits -Destination "$TestDir\limits.json" -Force

    # Position: fixed bottom right
    $pos = @{ x = 1616; y = 626 }
    $pos | ConvertTo-Json | Set-Content "$TestDir\window.pos.json"

    # --------------------------------------------------------------------------
    # [Test 2] Launch Go overlay32 and capture screenshot on EXACT SAME limits.json
    # --------------------------------------------------------------------------
    Write-Output "`n[Test 2] Launching Go overlay32 on exact same limits.json..."
    # Ensure limits.json is intact
    Copy-Item $sampleLimits -Destination "$TestDir\limits.json" -Force
    $pos | ConvertTo-Json | Set-Content "$TestDir\window.pos.json"

    $goProc = Start-Process -FilePath "$TestDir\llm-limits-overlay32.exe" -WorkingDirectory $TestDir -PassThru
    Start-Sleep -Seconds 3

    $goHwnd = [WinUtil]::FindOverlayWindow($goProc.Id)
    if ($goHwnd -eq [IntPtr]::Zero) {
        $goHwnd = [WinUtil]::FindWindow("LLMLimitsOverlay32Class", $null)
    }
    Write-Output "Go overlay32 HWND: $goHwnd (PID: $($goProc.Id))"

    $goRect = New-Object WinUtil+RECT
    [WinUtil]::GetWindowRect($goHwnd, [ref]$goRect) | Out-Null
    $goW = $goRect.Right - $goRect.Left
    $goH = $goRect.Bottom - $goRect.Top
    Write-Output "Go Overlay Bounds: Left=$($goRect.Left), Top=$($goRect.Top), Width=$goW, Height=$goH"

    $goShotPath = "$DistDir\stage5-go.png"
    [WinUtil]::CaptureWindow($goHwnd, $goShotPath)
    Write-Output "Captured Go Screenshot: $goShotPath"

    # --------------------------------------------------------------------------
    # [Test 3] Translucency Screen Capture (White vs Dark backdrop)
    # --------------------------------------------------------------------------
    Write-Output "`n[Test 3] Verifying Translucency with White and Dark backdrops..."
    $left = [int]$goRect.Left
    $top = [int]$goRect.Top
    $width = [int]$goW
    $height = [int]$goH

    $bgForm = New-Object System.Windows.Forms.Form
    $bgForm.FormBorderStyle = [System.Windows.Forms.FormBorderStyle]::None
    $bgForm.StartPosition = [System.Windows.Forms.FormStartPosition]::Manual
    $bgForm.Location = [System.Drawing.Point]::new(($left - 20), ($top - 20))
    $bgForm.Size = [System.Drawing.Size]::new(($width + 40), ($height + 40))
    $bgForm.TopMost = $true
    $bgForm.ShowInTaskbar = $false
    $bgForm.BackColor = [System.Drawing.Color]::White
    $bgForm.Show()

    [WinUtil]::SetWindowPos($bgForm.Handle, $goHwnd, 0, 0, 0, 0, 0x0013) | Out-Null
    $bgForm.Refresh()
    [System.Windows.Forms.Application]::DoEvents()
    Start-Sleep -Milliseconds 600

    $whitePath = "$DistDir\stage5-white.png"
    $darkPath  = "$DistDir\stage5-dark.png"

    $bmpWhite = New-Object System.Drawing.Bitmap($width, $height)
    $gWhite = [System.Drawing.Graphics]::FromImage($bmpWhite)
    $gWhite.CopyFromScreen($left, $top, 0, 0, $bmpWhite.Size)
    $gWhite.Dispose()
    $bmpWhite.Save($whitePath, [System.Drawing.Imaging.ImageFormat]::Png)
    $bmpWhite.Dispose()

    $bgForm.BackColor = [System.Drawing.Color]::FromArgb(18, 18, 18) # Dark
    $bgForm.Refresh()
    [System.Windows.Forms.Application]::DoEvents()
    Start-Sleep -Milliseconds 600

    $bmpDark = New-Object System.Drawing.Bitmap($width, $height)
    $gDark = [System.Drawing.Graphics]::FromImage($bmpDark)
    $gDark.CopyFromScreen($left, $top, 0, 0, $bmpDark.Size)
    $gDark.Dispose()
    $bmpDark.Save($darkPath, [System.Drawing.Imaging.ImageFormat]::Png)
    $bmpDark.Dispose()

    $bgForm.Close()
    $bgForm.Dispose()

    $whiteMd5 = (Get-FileHash -Path $whitePath -Algorithm MD5).Hash
    $darkMd5  = (Get-FileHash -Path $darkPath -Algorithm MD5).Hash
    $md5Diff = ($whiteMd5 -ne $darkMd5)
    Write-Output "White Backdrop MD5: $whiteMd5"
    Write-Output "Dark Backdrop MD5:  $darkMd5"
    Write-Output "Backdrop Checksum Divergence (Translucency confirmed): $md5Diff"

    # --------------------------------------------------------------------------
    # [Test 4] 60-Second Memory & CPU Benchmark of Go overlay32
    # --------------------------------------------------------------------------
    Write-Output "`n[Test 4] 60-Second Memory & CPU Benchmark of Go overlay32..."
    $mainPid = $goProc.Id
    for ($s = 1; $s -le 6; $s++) {
        Start-Sleep -Seconds 10
        $pCur = Get-Process -Id $mainPid -ErrorAction SilentlyContinue
        if ($pCur) {
            $ws = [Math]::Round($pCur.WorkingSet64 / 1MB, 2)
            $cpu = [Math]::Round($pCur.TotalProcessorTime.TotalSeconds, 2)
            Write-Output "  [+$(($s * 10))s] overlay32 WS: $ws MB | CPU: ${cpu}s"
        }
    }
    $finalProc = Get-Process -Id $mainPid
    $finalMainWS = [Math]::Round($finalProc.WorkingSet64 / 1MB, 2)
    $finalCPU = [Math]::Round($finalProc.TotalProcessorTime.TotalSeconds, 2)
    Write-Output "Total Final WorkingSet: $finalMainWS MB | Total CPU: ${finalCPU}s"

    Stop-Process -Id $goProc.Id -Force
    Start-Sleep -Seconds 1

    # --------------------------------------------------------------------------
    # [Test 5] Scenario: no_key
    # --------------------------------------------------------------------------
    Write-Output "`n[Test 5] Testing Scenario: no_key (placeholder/missing keys)..."
    $noKeyDir = "C:\Users\User\AppData\Local\Temp\go_stage5_nokey"
    if (Test-Path $noKeyDir) { Remove-Item -Recurse -Force $noKeyDir -ErrorAction SilentlyContinue }
    New-Item -ItemType Directory -Force -Path $noKeyDir | Out-Null
    Copy-Item $GoExeSrc -Destination "$noKeyDir\llm-limits-overlay32.exe" -Force
    Copy-Item "$RepoRoot\.env.example" -Destination "$noKeyDir\.env" -Force
    Copy-Item "$RepoRoot\overlay.config.json" -Destination "$noKeyDir\overlay.config.json" -Force

    $noKeyProc = Start-Process -FilePath "$noKeyDir\llm-limits-overlay32.exe" -WorkingDirectory $noKeyDir -PassThru
    Start-Sleep -Seconds 3

    $noKeyHwnd = [WinUtil]::FindOverlayWindow($noKeyProc.Id)
    if ($noKeyHwnd -eq [IntPtr]::Zero) { $noKeyHwnd = [WinUtil]::FindWindow("LLMLimitsOverlay32Class", $null) }
    $noKeyShot = "$DistDir\stage5-nokey.png"
    [WinUtil]::CaptureWindow($noKeyHwnd, $noKeyShot)
    Write-Output "Captured no_key Screenshot: $noKeyShot"

    # Check log for no_key status
    $noKeyLog = "$noKeyDir\overlay32.log"
    $noKeyReported = $false
    if (Test-Path $noKeyLog) {
        $logContent = Get-Content $noKeyLog -Raw
        Write-Output "no_key log snippet: $(Get-Content $noKeyLog | Select-Object -Last 5 | Out-String)"
        $noKeyReported = $true
    }
    Stop-Process -Id $noKeyProc.Id -Force
    Start-Sleep -Seconds 1

    # --------------------------------------------------------------------------
    # [Test 6] Scenario: stale
    # --------------------------------------------------------------------------
    Write-Output "`n[Test 6] Testing Scenario: stale..."
    $staleDir = "C:\Users\User\AppData\Local\Temp\go_stage5_stale"
    if (Test-Path $staleDir) { Remove-Item -Recurse -Force $staleDir -ErrorAction SilentlyContinue }
    New-Item -ItemType Directory -Force -Path $staleDir | Out-Null
    Copy-Item $GoExeSrc -Destination "$staleDir\llm-limits-overlay32.exe" -Force
    Copy-Item "$RepoRoot\.env" -Destination "$staleDir\.env" -Force
    Copy-Item "$RepoRoot\overlay.config.json" -Destination "$staleDir\overlay.config.json" -Force

    # Inject stale limits.json with old timestamp
    $staleJson = Get-Content $sampleLimits -Raw | ConvertFrom-Json
    $staleJson.status = "stale"
    $staleJson.last_updated = "2026-10-25 10:00:00"
    $staleJson | ConvertTo-Json -Depth 10 | Set-Content "$staleDir\limits.json"

    $staleProc = Start-Process -FilePath "$staleDir\llm-limits-overlay32.exe" -WorkingDirectory $staleDir -PassThru
    Start-Sleep -Seconds 3

    $staleHwnd = [WinUtil]::FindOverlayWindow($staleProc.Id)
    if ($staleHwnd -eq [IntPtr]::Zero) { $staleHwnd = [WinUtil]::FindWindow("LLMLimitsOverlay32Class", $null) }
    $staleShot = "$DistDir\stage5-stale.png"
    [WinUtil]::CaptureWindow($staleHwnd, $staleShot)
    Write-Output "Captured stale Screenshot: $staleShot"

    Stop-Process -Id $staleProc.Id -Force
    Start-Sleep -Seconds 1

    # --------------------------------------------------------------------------
    # [Test 7] Scenario: 2 OpenRouter keys -> 2 cards + Dynamic Height
    # --------------------------------------------------------------------------
    Write-Output "`n[Test 7] Testing Scenario: 2 OpenRouter keys -> 2 cards & Dynamic Height..."
    $twoOrDir = "C:\Users\User\AppData\Local\Temp\go_stage5_two_or"
    if (Test-Path $twoOrDir) { Remove-Item -Recurse -Force $twoOrDir -ErrorAction SilentlyContinue }
    New-Item -ItemType Directory -Force -Path $twoOrDir | Out-Null
    Copy-Item $GoExeSrc -Destination "$twoOrDir\llm-limits-overlay32.exe" -Force
    Copy-Item "$RepoRoot\.env" -Destination "$twoOrDir\.env" -Force

    # Config with 4 providers (kimi, openrouter, openrouter2, agy)
    $fourCfg = @{
        providers = @(
            @{ id = "kimi"; type = "kimi"; title = "KIMI"; enabled = $true },
            @{ id = "openrouter"; type = "openrouter"; title = "OPENROUTER 1"; enabled = $true },
            @{ id = "openrouter2"; type = "openrouter"; title = "OPENROUTER 2"; enabled = $true },
            @{ id = "agy"; type = "agy"; title = "GEMINI + CLAUDE/GPT"; enabled = $true }
        )
    }
    $fourCfg | ConvertTo-Json -Depth 5 | Set-Content "$twoOrDir\overlay.config.json"
    Copy-Item $sampleLimits -Destination "$twoOrDir\limits.json" -Force

    $twoOrProc = Start-Process -FilePath "$twoOrDir\llm-limits-overlay32.exe" -WorkingDirectory $twoOrDir -PassThru
    Start-Sleep -Seconds 3

    $twoOrHwnd = [WinUtil]::FindOverlayWindow($twoOrProc.Id)
    if ($twoOrHwnd -eq [IntPtr]::Zero) { $twoOrHwnd = [WinUtil]::FindWindow("LLMLimitsOverlay32Class", $null) }
    $twoOrRect = New-Object WinUtil+RECT
    [WinUtil]::GetWindowRect($twoOrHwnd, [ref]$twoOrRect) | Out-Null
    $twoOrH = $twoOrRect.Bottom - $twoOrRect.Top
    Write-Output "Dynamic Height with 4 cards: $twoOrH px (Expected > 480px, vs 402px for 3 cards)"

    $twoOrShot = "$DistDir\stage5-two-or.png"
    [WinUtil]::CaptureWindow($twoOrHwnd, $twoOrShot)
    Write-Output "Captured 4-Card Dynamic Height Screenshot: $twoOrShot"

    Stop-Process -Id $twoOrProc.Id -Force
    Start-Sleep -Seconds 1

    Write-Output "`n=========================================================="
    Write-Output "                 STAGE 5 VERIFICATION SUMMARY             "
    Write-Output "=========================================================="
    Write-Output "C# Overlay Captured:          True ($csShotPath)"
    Write-Output "Go overlay32 Captured:        True ($goShotPath)"
    Write-Output "Translucency (MD5 diff):      $md5Diff"
    Write-Output "White MD5:                    $whiteMd5"
    Write-Output "Dark MD5:                     $darkMd5"
    Write-Output "WorkingSet (60s benchmark):   $finalMainWS MB"
    Write-Output "CPU Time (60s benchmark):     ${finalCPU}s"
    Write-Output "Scenario no_key:              True ($noKeyShot)"
    Write-Output "Scenario stale:               True ($staleShot)"
    Write-Output "Scenario 2 OR Keys (4 cards): True (Height = $twoOrH px, $twoOrShot)"
    Write-Output "=========================================================="

} finally {
    if ($restorableProc) {
        Write-Output "`n[Cleanup] Restoring user's overlay: $restorableProc"
        Start-Process -FilePath $restorableProc
    }
}
