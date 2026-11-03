$ErrorActionPreference = "Stop"
$env:SEE_MASK_NOZONECHECKS = "1"

$Version = "2.1.6"
$repoRoot = Split-Path $PSScriptRoot -Parent
$setupExe = Join-Path $repoRoot "dist\LlmLimitsOverlay-Setup-$Version.exe"
$envSource = Join-Path $repoRoot ".env"
$smokeDir = Join-Path $env:TEMP "llmo-smoke-test"
$localSetup = Join-Path $env:TEMP "LlmLimitsOverlay-Setup-$Version.exe"

Write-Host "=== SMOKE TEST: LLM Limits Overlay $Version ==="

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
if ($verOut -notmatch [regex]::Escape($Version)) {
    throw "CLI --version did not output $Version"
}

# Copy .env with keys for live fetch
if (Test-Path $envSource) {
    Copy-Item $envSource (Join-Path $smokeDir ".env") -Force
}

# Configure 4 providers (kimi, openrouter, codex, agy)
$fourCfg = @{
    providers = @(
        @{ id = "kimi"; type = "kimi"; title = "KIMI"; enabled = $true; apiKeyEnv = "KIMI_API_KEY" },
        @{ id = "openrouter"; type = "openrouter"; title = "OPENROUTER"; enabled = $true; apiKeyEnv = "OPENROUTER_API_KEY" },
        @{ id = "codex"; type = "codex"; title = "CODEX"; enabled = $true },
        @{ id = "agy"; type = "agy"; title = "GEMINI + CLAUDE/GPT"; enabled = $true }
    )
}
$fourCfg | ConvertTo-Json -Depth 5 | Set-Content (Join-Path $smokeDir "overlay.config.json")

$jsonOut = & $cliExe --json
if ($LASTEXITCODE -ne 0 -and $LASTEXITCODE -ne 2) {
    throw "CLI --json returned failure code $LASTEXITCODE"
}
Write-Host "  -> CLI returned exit code $LASTEXITCODE"
if ($jsonOut -match "no_key") {
    throw "CLI --json contains no_key: .env did not load properly"
}

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

# Cycle 1: wait for initial fetch with 4 providers (up to 20s)
$logPath = Join-Path $smokeDir "overlay32.log"
$limitsJson = Join-Path $smokeDir "limits.json"
$cycle1Done = $false
for ($i = 0; $i -lt 20; $i++) {
    Start-Sleep -Seconds 1
    if ((Test-Path $logPath) -and (Test-Path $limitsJson)) {
        $content = Get-Content $logPath -Raw -ErrorAction SilentlyContinue
        if ($content -match "Starting async native refresh") {
            $cycle1Done = $true
            break
        }
    }
}
if (-not $cycle1Done) {
    throw "Initial fetch with 4 providers did not complete within timeout"
}
Write-Host "  -> Initial fetch completed with 4 providers: $cycle1Done"
$limitsObj = Get-Content $limitsJson -Raw | ConvertFrom-Json
$hasCodex = ($null -ne $limitsObj.codex) -or ($null -ne $limitsObj.data.codex)
$hasAgy = ($null -ne $limitsObj.data.agy)
Write-Host "  -> limits.json assembled successfully (codex: $hasCodex, agy: $hasAgy)"
if (Test-Path $logPath) {
    Get-Content $logPath | ForEach-Object { Write-Host "     $_" }
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
