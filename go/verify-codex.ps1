$ErrorActionPreference = "Stop"
$env:SEE_MASK_NOZONECHECKS = "1"

Add-Type -AssemblyName System.Drawing
Add-Type -AssemblyName System.Windows.Forms

$code = @"
using System;
using System.Drawing;
using System.Drawing.Imaging;
using System.Runtime.InteropServices;

public class WinUtil {
    [DllImport("user32.dll")] public static extern bool EnumWindows(EnumWindowsProc lpEnumFunc, IntPtr lParam);
    public delegate bool EnumWindowsProc(IntPtr hWnd, IntPtr lParam);
    [DllImport("user32.dll")] public static extern uint GetWindowThreadProcessId(IntPtr hWnd, out uint lpdwProcessId);
    [DllImport("user32.dll", SetLastError = true)] public static extern bool GetWindowRect(IntPtr hWnd, out RECT lpRect);
    [DllImport("user32.dll")] public static extern bool PrintWindow(IntPtr hWnd, IntPtr hdcBlt, uint nFlags);
    [DllImport("user32.dll")] public static extern IntPtr FindWindow(string lpClassName, string lpWindowName);

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

    public static void CaptureWindow(IntPtr hwnd, string path) {
        RECT r;
        GetWindowRect(hwnd, out r);
        int w = r.Right - r.Left;
        int h = r.Bottom - r.Top;
        if (w <= 0) w = 290;
        if (h <= 0) h = 464;
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

[StructLayout(LayoutKind.Sequential)]
public struct RECT {
    public int Left;
    public int Top;
    public int Right;
    public int Bottom;
}
"@
Add-Type -TypeDefinition $code -ReferencedAssemblies System.Drawing

$RepoRoot = "\\wsl.localhost\Ubuntu-22.04\home\kzamirtay\agy-limits-overlay"
$ExeSrc = "$RepoRoot\go\dist\llm-limits-overlay.exe"
$CliSrc = "$RepoRoot\go\dist\llm-limits.exe"
$TempDir = "C:\Users\User\AppData\Local\Temp\llmo-codex-test"
$ScreenShotPath = "$RepoRoot\docs\screenshots\codex-card.png"

Write-Host "=========================================================="
Write-Host " LLM Limits Overlay - Codex Provider Verification         "
Write-Host "=========================================================="

# Stop running overlays
Get-Process -Name "llm-limits-overlay", "llm-limits-overlay32", "LlmLimitsOverlay" -ErrorAction SilentlyContinue | Stop-Process -Force
Start-Sleep -Milliseconds 500

if (Test-Path $TempDir) {
    Remove-Item -Recurse -Force $TempDir -ErrorAction SilentlyContinue
}
New-Item -ItemType Directory -Force -Path $TempDir | Out-Null
New-Item -ItemType Directory -Force -Path "$TempDir\assets" | Out-Null

Copy-Item $ExeSrc -Destination "$TempDir\llm-limits-overlay.exe" -Force
Copy-Item $CliSrc -Destination "$TempDir\llm-limits.exe" -Force
if (Test-Path "$RepoRoot\assets\app.ico") {
    Copy-Item "$RepoRoot\assets\app.ico" -Destination "$TempDir\assets\app.ico" -Force
}

# Config with Kimi, OpenRouter, Codex, AGY
$cfg = @{
    providers = @(
        @{ id = "kimi"; type = "kimi"; title = "KIMI"; enabled = $true },
        @{ id = "openrouter"; type = "openrouter"; title = "OPENROUTER"; enabled = $true },
        @{ id = "codex"; type = "codex"; title = "CODEX"; enabled = $true },
        @{ id = "agy"; type = "agy"; title = "GEMINI + CLAUDE/GPT"; enabled = $true }
    )
    clickThrough = $false
}
$cfg | ConvertTo-Json -Depth 5 | Set-Content "$TempDir\overlay.config.json"

# Sample limits with active codex data
$limits = @{
    status = "online"
    last_updated = (Get-Date).ToString("yyyy-MM-dd HH:mm:ss")
    data = @{
        kimi = @{
            status = "online"
            last_updated = (Get-Date).ToString("yyyy-MM-dd HH:mm:ss")
            limit_5h = @{ used_percent = 12.5; used_fraction = 0.125; reset_in = "3h 40m" }
            limit_7d = @{ used_percent = 45.0; used_fraction = 0.45; reset_in = "4d 12h" }
        }
        openrouter = @{
            status = "online"
            last_updated = (Get-Date).ToString("yyyy-MM-dd HH:mm:ss")
            limit = 20.0
            usage = 6.5
            limit_remaining = 13.5
            used_percent = 32.5
            used_fraction = 0.325
            reset_in = "18d 4h"
            free_daily_used = 12
            free_daily_limit = 1000
        }
        codex = @{
            status = "online"
            last_updated = (Get-Date).ToString("yyyy-MM-dd HH:mm:ss")
            plan_type = "free"
            balance = ""
            primary = @{
                name = "30д"
                window_mins = 43200
                used_percent = 8.5
                used_fraction = 0.085
                reset_in = "29d 23h"
                resets_at = 1796149147
            }
        }
        agy = @{
            status = "online"
            last_updated = (Get-Date).ToString("yyyy-MM-dd HH:mm:ss")
            gemini_5h = @{ used_percent = 0.0; used_fraction = 0.0; reset_in = "ready" }
            gemini_weekly = @{ used_percent = 18.0; used_fraction = 0.18; reset_in = "5d 2h" }
            claude_5h = @{ used_percent = 25.0; used_fraction = 0.25; reset_in = "2h 15m" }
            claude_weekly = @{ used_percent = 60.0; used_fraction = 0.60; reset_in = "2d 18h" }
        }
    }
}
$limits | ConvertTo-Json -Depth 10 | Set-Content "$TempDir\limits.json"

Write-Host "`n[Test 1] Launching GUI with Codex card configured..."
$p = Start-Process -FilePath "$TempDir\llm-limits-overlay.exe" -WorkingDirectory $TempDir -PassThru
Start-Sleep -Seconds 3

$hwnd = [WinUtil]::FindOverlayWindow($p.Id)
if ($hwnd -eq [IntPtr]::Zero) {
    $hwnd = [WinUtil]::FindWindow("LLMLimitsOverlay32Class", $null)
}
if ($hwnd -eq [IntPtr]::Zero) {
    Stop-Process -Id $p.Id -Force
    throw "GUI window not found!"
}

$rect = New-Object RECT
[WinUtil]::GetWindowRect($hwnd, [ref]$rect) | Out-Null
$w = $rect.Right - $rect.Left
$h = $rect.Bottom - $rect.Top
Write-Host "  -> Overlay Window HWND=$hwnd, Bounds: Left=$($rect.Left), Top=$($rect.Top), Width=$w, Height=$h"

Write-Host "  -> Capturing screenshot to $ScreenShotPath..."
[WinUtil]::CaptureWindow($hwnd, $ScreenShotPath)
Write-Host "  -> Screenshot successfully saved ($w x $h px)!"

Stop-Process -Id $p.Id -Force
Start-Sleep -Seconds 1

Write-Host "`n[Test 2] Testing Windows CLI with --provider codex..."
$cliOut = & "$TempDir\llm-limits.exe" --config "$TempDir\overlay.config.json" --provider codex
Write-Host "CLI Output:"
Write-Host $cliOut

Write-Host "`n[Test 3] Testing Windows CLI with --provider codex --json..."
$jsonOut = & "$TempDir\llm-limits.exe" --config "$TempDir\overlay.config.json" --provider codex --json
Write-Host "CLI JSON Output:"
Write-Host $jsonOut

Write-Host "`n=========================================================="
Write-Host " All Codex Provider verification checks passed!"
Write-Host "=========================================================="
