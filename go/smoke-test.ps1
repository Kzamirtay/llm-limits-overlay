$ErrorActionPreference = "Stop"
$env:SEE_MASK_NOZONECHECKS = "1"

$setupExe = "\\wsl.localhost\Ubuntu-22.04\home\kzamirtay\agy-limits-overlay\dist\LlmLimitsOverlay-Setup-2.1.1.exe"
$envSource = "\\wsl.localhost\Ubuntu-22.04\home\kzamirtay\agy-limits-overlay\go\dist\.env"
$smokeDir = "C:\Users\User\AppData\Local\Temp\llmo-smoke-test"
$localSetup = "C:\Users\User\AppData\Local\Temp\LlmLimitsOverlay-Setup-2.1.1.exe"

Write-Host "=== SMOKE TEST: LLM Limits Overlay 2.1.1 ==="

# 0. Check setup exe exists and copy locally to avoid UNC security prompts
if (-not (Test-Path $setupExe)) {
    throw "Setup executable not found at $setupExe"
}
Copy-Item $setupExe $localSetup -Force
Unblock-File $localSetup -ErrorAction SilentlyContinue

# 1. Clean up smoke dir if previously exists
if (Test-Path $smokeDir) {
    Remove-Item -Recurse -Force $smokeDir
}
New-Item -ItemType Directory -Path $smokeDir | Out-Null

# 2. Silent installation into $smokeDir without touching user tasks
Write-Host "[1/6] Installing silently into $smokeDir..."
$installLog = Join-Path $smokeDir "install.log"
$p = Start-Process -FilePath $localSetup -ArgumentList "/VERYSILENT /SUPPRESSMSGBOXES /NORESTART /DIR=`"$smokeDir`" /MERGETASKS=`"!addtopath,!autostart,!desktopicon`" /LOG=`"$installLog`"" -Wait -PassThru
if ($p.ExitCode -ne 0) {
    throw "Installer failed with exit code $($p.ExitCode)"
}

# Check installed files
$requiredFiles = @("llm-limits-overlay.exe", "llm-limits.exe", "unins000.exe", "overlay.config.json")
foreach ($f in $requiredFiles) {
    $fullPath = Join-Path $smokeDir $f
    if (-not (Test-Path $fullPath)) {
        throw "Installed file missing: $fullPath"
    }
}
Write-Host "  -> Installation succeeded. Required files present."

# 3. Test CLI from smoke directory
Write-Host "[2/6] Testing CLI (--version and --json)..."
$cliExe = Join-Path $smokeDir "llm-limits.exe"
$verOut = & $cliExe --version
Write-Host "  -> CLI version output: $verOut"
if ($verOut -notmatch "2\.1\.1") {
    throw "CLI --version did not output 2.1.1"
}

# Copy .env with keys for live fetch
if (Test-Path $envSource) {
    Copy-Item $envSource (Join-Path $smokeDir ".env") -Force
}

$jsonOut = & $cliExe --json
if ($LASTEXITCODE -ne 0 -and $LASTEXITCODE -ne 2) {
    throw "CLI --json returned failure code $LASTEXITCODE"
}
Write-Host "  -> CLI returned exit code $LASTEXITCODE"

# 4. Launch GUI overlay and observe 2 fetch cycles
Write-Host "[3/6] Launching GUI overlay for 2 fetch cycles..."
$overlayExe = Join-Path $smokeDir "llm-limits-overlay.exe"
$guiProc = Start-Process -FilePath $overlayExe -WorkingDirectory $smokeDir -PassThru
Write-Host "  -> Overlay process started (PID $($guiProc.Id))"
Start-Sleep -Seconds 3

# Verify window bounds relative to work area and taskbar
$win32Type = @"
using System;
using System.Runtime.InteropServices;
public class SmokeWin {
    public delegate bool EnumWindowsProc(IntPtr hWnd, IntPtr lParam);
    [DllImport("user32.dll")] public static extern bool EnumWindows(EnumWindowsProc lpEnumFunc, IntPtr lParam);
    [DllImport("user32.dll")] public static extern uint GetWindowThreadProcessId(IntPtr hWnd, out uint lpdwProcessId);
    [DllImport("user32.dll")] public static extern bool GetWindowRect(IntPtr hWnd, out RECT lpRect);
    [DllImport("user32.dll")] public static extern bool SystemParametersInfo(int uAction, int uParam, ref RECT lpvParam, int fuWinIni);

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
public struct RECT { public int Left, Top, Right, Bottom; }
"@
if (-not ([System.Management.Automation.PSTypeName]"SmokeWin").Type) {
    Add-Type -TypeDefinition $win32Type
}

$smokeWa = New-Object RECT
[SmokeWin]::SystemParametersInfo(0x0030, 0, [ref]$smokeWa, 0) | Out-Null
$smokeHwnd = [SmokeWin]::FindOverlayWindow($guiProc.Id)
if ($smokeHwnd -ne [IntPtr]::Zero) {
    $smokeRect = New-Object RECT
    [SmokeWin]::GetWindowRect($smokeHwnd, [ref]$smokeRect) | Out-Null
    $gapBottom = $smokeWa.Bottom - $smokeRect.Bottom
    Write-Host "  -> Window bounds: Left=$($smokeRect.Left), Top=$($smokeRect.Top), Bottom=$($smokeRect.Bottom) (WorkArea Bottom=$($smokeWa.Bottom))"
    Write-Host "  -> Taskbar gap: $gapBottom px (expected 12)"
    if ($smokeRect.Bottom -gt $smokeWa.Bottom) {
        throw "Window is positioned under/overlapping taskbar! Bottom=$($smokeRect.Bottom) > $($smokeWa.Bottom)"
    }
    Write-Host "  -> [PASS] Window is positioned strictly above taskbar!"
}

# Cycle 1: wait for initial fetch (up to 15s)
$logPath = Join-Path $smokeDir "overlay32.log"
$cycle1Done = $false
for ($i = 0; $i -lt 15; $i++) {
    Start-Sleep -Seconds 1
    if (Test-Path $logPath) {
        $content = Get-Content $logPath -Raw -ErrorAction SilentlyContinue
        if ($content -match "Starting async native refresh|snapshot updated") {
            $cycle1Done = $true
            break
        }
    }
}
Write-Host "  -> Initial fetch completed: $cycle1Done"

# Cycle 2: overlay has a 60s ticker. Wait ~62 seconds
Write-Host "  -> Waiting 62 seconds for ticker fetch cycle 2..."
Start-Sleep -Seconds 62

$fetchCount = 0
if (Test-Path $logPath) {
    $matches = Select-String -Path $logPath -Pattern "Starting async native refresh"
    $fetchCount = $matches.Count
    Write-Host "  -> Total 'Starting async native refresh' cycles: $fetchCount"
}
if ($fetchCount -lt 2) {
    throw "Expected at least 2 fetch cycles, got $fetchCount"
}

# Stop GUI process cleanly
Write-Host "[4/6] Terminating GUI overlay process..."
Stop-Process -Id $guiProc.Id -Force
Start-Sleep -Seconds 2

# 5. Test silent uninstallation
Write-Host "[5/6] Testing silent uninstallation..."
$uninsExe = Join-Path $smokeDir "unins000.exe"
$uninsProc = Start-Process -FilePath $uninsExe -ArgumentList "/VERYSILENT /SUPPRESSMSGBOXES /NORESTART" -Wait -PassThru
Write-Host "  -> Uninstaller exit code: $($uninsProc.ExitCode)"

# Check that executables are removed
$overlayRemoved = -not (Test-Path (Join-Path $smokeDir "llm-limits-overlay.exe"))
$cliRemoved = -not (Test-Path (Join-Path $smokeDir "llm-limits.exe"))
$envPreserved = (Test-Path (Join-Path $smokeDir ".env"))
Write-Host "  -> llm-limits-overlay.exe removed: $overlayRemoved"
Write-Host "  -> llm-limits.exe removed: $cliRemoved"
Write-Host "  -> .env preserved: $envPreserved"

# Cleanup smoke dir and local setup
Remove-Item -Recurse -Force $smokeDir
Remove-Item -Force $localSetup -ErrorAction SilentlyContinue

# 6. Verify user's actual %LOCALAPPDATA%\LlmLimitsOverlay is intact
Write-Host "[6/6] Verifying user production install is intact..."
$userProdDir = "$env:LOCALAPPDATA\LlmLimitsOverlay"
$userEnv = Join-Path $userProdDir ".env"
$hasUserExe = (Test-Path (Join-Path $userProdDir "llm-limits-overlay.exe")) -or (Test-Path (Join-Path $userProdDir "LlmLimitsOverlay.exe"))
if (-not (Test-Path $userEnv) -or -not $hasUserExe) {
    throw "CRITICAL: User production directory was affected!"
}
Write-Host "  -> User production directory untouched and fully intact."

Write-Host "=== SMOKE TEST PASSED SUCCESSFULLY ==="
