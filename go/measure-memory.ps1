# ==============================================================================
# LLM Limits Overlay - Comprehensive Memory & CPU Benchmark (C# vs Go/Wails)
# ==============================================================================
# Runs both versions for 60 seconds, recording:
#  - WorkingSet64 of main process
#  - WorkingSet64 of all child processes (WebView2, etc.)
#  - Total WorkingSet64
#  - Total Processor CPU Time over 60 seconds
#  - Size on disk
#  - Open window vs Hidden (Tray) mode
# ==============================================================================

param(
    [int]$DurationSeconds = 60
)

$ErrorActionPreference = "Stop"

$code = @"
using System;
using System.Runtime.InteropServices;
public class WinUser {
    [DllImport("user32.dll")]
    public static extern bool ShowWindow(IntPtr hWnd, int nCmdShow);
    [DllImport("user32.dll")]
    public static extern bool IsWindowVisible(IntPtr hWnd);
}
"@
Add-Type -TypeDefinition $code

$ScriptDir = Split-Path -Parent $MyInvocation.MyCommand.Path
$RepoRoot = Split-Path -Parent $ScriptDir
$DistDir = "$RepoRoot\go\dist"

Write-Output "=========================================================="
Write-Output " LLM Limits Overlay - Performance Benchmark               "
Write-Output " Duration: $DurationSeconds seconds per variant           "
Write-Output "=========================================================="

# Check if a user background overlay is running; record to restore later
$overlayProc = Get-Process -Name "llm-limits-overlay", "LlmLimitsOverlay" -ErrorAction SilentlyContinue
$userOverlayPath = $null
if ($overlayProc) {
    $userOverlayPath = $overlayProc[0].Path
    Write-Output "[Setup] Found user background overlay (PID $($overlayProc[0].Id)). Suspending for test..."
    Stop-Process -Id $overlayProc.Id -Force
    Start-Sleep -Seconds 1
}

function Get-ProcessTreePids([int]$rootPid, [string]$cmdPattern = "") {
    $all = Get-CimInstance Win32_Process
    $found = [System.Collections.Generic.List[int]]::new()
    $found.Add($rootPid)
    $queue = [System.Collections.Generic.Queue[int]]::new()
    $queue.Enqueue($rootPid)

    while ($queue.Count -gt 0) {
        $curr = $queue.Dequeue()
        $kids = $all | Where-Object { $_.ParentProcessId -eq $curr }
        foreach ($k in $kids) {
            if (-not $found.Contains($k.ProcessId)) {
                $found.Add($k.ProcessId)
                $queue.Enqueue($k.ProcessId)
            }
        }
    }

    if ($cmdPattern) {
        $extra = $all | Where-Object { $_.CommandLine -like "*$cmdPattern*" }
        foreach ($e in $extra) {
            if (-not $found.Contains($e.ProcessId)) {
                $found.Add($e.ProcessId)
            }
        }
    }

    return $found
}

function Measure-AppVariant {
    param(
        [string]$Name,
        [string]$ExeSourcePath,
        [string]$TestFolderName
    )

    Write-Output "`n----------------------------------------------------------"
    Write-Output " Testing: $Name"
    Write-Output " Source:  $ExeSourcePath"
    Write-Output "----------------------------------------------------------"

    $testDir = "C:\Users\User\AppData\Local\Temp\$TestFolderName"
    if (Test-Path $testDir) {
        Remove-Item -Recurse -Force $testDir -ErrorAction SilentlyContinue
    }
    New-Item -ItemType Directory -Force -Path $testDir | Out-Null

    $exeName = Split-Path -Leaf $ExeSourcePath
    $targetExe = Join-Path $testDir $exeName
    Copy-Item $ExeSourcePath -Destination $targetExe -Force

    # Copy config and env
    if (Test-Path "$RepoRoot\.env") {
        Copy-Item "$RepoRoot\.env" -Destination "$testDir\.env" -Force
    }
    if (Test-Path "$RepoRoot\overlay.config.json") {
        Copy-Item "$RepoRoot\overlay.config.json" -Destination "$testDir\overlay.config.json" -Force
    }

    $fileSize = (Get-Item $targetExe).Length
    $fileSizeMB = [Math]::Round($fileSize / 1MB, 2)
    Write-Output "Binary Size on Disk: $fileSizeMB MB ($fileSize bytes)"

    Write-Output "Launching process..."
    $proc = Start-Process -FilePath $targetExe -WorkingDirectory $testDir -PassThru
    $mainPid = $proc.Id
    Write-Output "Main Process PID: $mainPid"

    # Wait 5s for window to initialize
    Start-Sleep -Seconds 5

    # Identify HWND
    $hwnd = [IntPtr]::Zero
    for ($i = 0; $i -lt 15; $i++) {
        $p = Get-Process -Id $mainPid -ErrorAction SilentlyContinue
        if ($p -and $p.MainWindowHandle -ne [IntPtr]::Zero) {
            $hwnd = $p.MainWindowHandle
            break
        }
        Start-Sleep -Milliseconds 200
    }
    Write-Output "MainWindowHandle: $hwnd"

    # Track samples: 5s, 30s, 60s
    $pattern = if ($exeName -like "*overlay*") { $exeName } else { "" }
    
    function Sample-Memory {
        param([int]$elapsed)
        $pids = Get-ProcessTreePids -rootPid $mainPid -cmdPattern $pattern
        $mainWs = 0.0
        $childWs = 0.0
        $totalCpu = 0.0
        $childCount = 0

        foreach ($id in $pids) {
            $p = Get-Process -Id $id -ErrorAction SilentlyContinue
            if ($p) {
                $ws = $p.WorkingSet64 / 1MB
                $cpu = $p.TotalProcessorTime.TotalSeconds
                $totalCpu += $cpu
                if ($id -eq $mainPid) {
                    $mainWs += $ws
                } else {
                    $childWs += $ws
                    $childCount++
                }
            }
        }
        $totalWs = $mainWs + $childWs
        return [PSCustomObject]@{
            ElapsedSec = $elapsed
            MainWS_MB  = [Math]::Round($mainWs, 2)
            ChildWS_MB = [Math]::Round($childWs, 2)
            TotalWS_MB = [Math]::Round($totalWs, 2)
            ChildCount = $childCount
            CpuSec     = [Math]::Round($totalCpu, 2)
        }
    }

    $sample5 = Sample-Memory 5
    Write-Output "[+5s Open]   Main: $($sample5.MainWS_MB) MB | Children ($($sample5.ChildCount)): $($sample5.ChildWS_MB) MB | Total: $($sample5.TotalWS_MB) MB | CPU: $($sample5.CpuSec)s"

    # Sleep to 30s
    Start-Sleep -Seconds 25
    $sample30 = Sample-Memory 30
    Write-Output "[+30s Open]  Main: $($sample30.MainWS_MB) MB | Children ($($sample30.ChildCount)): $($sample30.ChildWS_MB) MB | Total: $($sample30.TotalWS_MB) MB | CPU: $($sample30.CpuSec)s"

    # Sleep to 60s
    Start-Sleep -Seconds 30
    $sample60 = Sample-Memory 60
    Write-Output "[+60s Open]  Main: $($sample60.MainWS_MB) MB | Children ($($sample60.ChildCount)): $($sample60.ChildWS_MB) MB | Total: $($sample60.TotalWS_MB) MB | CPU: $($sample60.CpuSec)s"

    # Test Hidden Mode (Tray mode)
    Write-Output "Hiding window (Tray mode)..."
    if ($hwnd -ne [IntPtr]::Zero) {
        [WinUser]::ShowWindow($hwnd, 0) | Out-Null # SW_HIDE
    }
    Start-Sleep -Seconds 5
    $sampleHidden = Sample-Memory 65
    Write-Output "[+5s Hidden] Main: $($sampleHidden.MainWS_MB) MB | Children ($($sampleHidden.ChildCount)): $($sampleHidden.ChildWS_MB) MB | Total: $($sampleHidden.TotalWS_MB) MB"

    # Restore window
    if ($hwnd -ne [IntPtr]::Zero) {
        [WinUser]::ShowWindow($hwnd, 5) | Out-Null # SW_SHOW
    }
    Start-Sleep -Seconds 2

    # Cleanup processes
    $allPids = Get-ProcessTreePids -rootPid $mainPid -cmdPattern $pattern
    foreach ($id in $allPids) {
        Stop-Process -Id $id -Force -ErrorAction SilentlyContinue
    }
    Start-Sleep -Seconds 1
    Remove-Item -Recurse -Force $testDir -ErrorAction SilentlyContinue

    return [PSCustomObject]@{
        Name         = $Name
        SizeMB       = $fileSizeMB
        Open_MainWS  = $sample60.MainWS_MB
        Open_ChildWS = $sample60.ChildWS_MB
        Open_TotalWS = $sample60.TotalWS_MB
        ChildCount   = $sample60.ChildCount
        TotalCpuSec  = $sample60.CpuSec
        Hide_MainWS  = $sampleHidden.MainWS_MB
        Hide_ChildWS = $sampleHidden.ChildWS_MB
        Hide_TotalWS = $sampleHidden.TotalWS_MB
    }
}

try {
    # 1. Measure Go Native Win32 Layered Window
    $nativeResult = $null
    if (Test-Path "$DistDir\llm-limits-overlay.exe") {
        $nativeResult = Measure-AppVariant `
            -Name "Go Native Win32 (overlay32)" `
            -ExeSourcePath "$DistDir\llm-limits-overlay.exe" `
            -TestFolderName "bench_native_overlay"
    }

    # 2. Measure Go / Wails Overlay (if present)
    $wailsResult = $null
    if (Test-Path "$DistDir\llm-limits-overlay-poc.exe") {
        $wailsResult = Measure-AppVariant `
            -Name "Go / Wails v2 (WebView2)" `
            -ExeSourcePath "$DistDir\llm-limits-overlay-poc.exe" `
            -TestFolderName "bench_wails_overlay"
    }

    Write-Output "`n=========================================================================================="
    Write-Output "                        BENCHMARK RESULTS SUMMARY (60s RUN)                               "
    Write-Output "=========================================================================================="
    
    $results = @($nativeResult, $wailsResult) | Where-Object { $_ -ne $null }
    $results | Format-Table -Property Name, SizeMB, Open_MainWS, Open_ChildWS, Open_TotalWS, TotalCpuSec, Hide_TotalWS -AutoSize | Out-String | Write-Output

} finally {
    if ($userOverlayPath) {
        Write-Output "[Cleanup] Restoring original user overlay: $userOverlayPath"
        Start-Process -FilePath $userOverlayPath
    }
}
