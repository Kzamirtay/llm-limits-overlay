$ErrorActionPreference = "Stop"
$env:SEE_MASK_NOZONECHECKS = "1"

$RepoRoot = "\\wsl.localhost\Ubuntu-22.04\home\kzamirtay\agy-limits-overlay"
$ExeSrc = "$RepoRoot\go\dist\llm-limits-overlay.exe"
$EnvSrc = "$RepoRoot\.env"
$TempDir = "C:\Users\User\AppData\Local\Temp\llmo-hideconsole-test"

Write-Host "=========================================================="
Write-Host " LLM Limits Overlay - HideConsole & ConHost Test (>= 2m)   "
Write-Host "=========================================================="

# Stop running overlays
Get-Process -Name "llm-limits-overlay", "llm-limits-overlay32", "LlmLimitsOverlay" -ErrorAction SilentlyContinue | Stop-Process -Force
Start-Sleep -Seconds 1

# Measure baseline conhost count
$conhostBefore = (Get-Process -Name "conhost" -ErrorAction SilentlyContinue).Count
Write-Host "Baseline conhost.exe process count: $conhostBefore"

if (Test-Path $TempDir) {
    Remove-Item -Recurse -Force $TempDir -ErrorAction SilentlyContinue
}
New-Item -ItemType Directory -Force -Path $TempDir | Out-Null
New-Item -ItemType Directory -Force -Path "$TempDir\assets" | Out-Null

Copy-Item $ExeSrc -Destination "$TempDir\llm-limits-overlay.exe" -Force
if (Test-Path "$RepoRoot\assets\app.ico") {
    Copy-Item "$RepoRoot\assets\app.ico" -Destination "$TempDir\assets\app.ico" -Force
}
if (Test-Path $EnvSrc) {
    Copy-Item $EnvSrc -Destination "$TempDir\.env" -Force
}

# Config with all 4 providers (kimi, openrouter, codex, agy)
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

Write-Host "Starting GUI overlay with 4 providers (kimi, openrouter, codex, agy)..."
$p = Start-Process -FilePath "$TempDir\llm-limits-overlay.exe" -WorkingDirectory $TempDir -PassThru

$maxConhost = $conhostBefore
$monitoringSeconds = 130 # >= 2 minutes (fetch interval is 60s)
$startTime = [DateTime]::UtcNow

Write-Host "Monitoring conhost processes for $monitoringSeconds seconds (covering >= 2 fetch cycles)..."
while (([DateTime]::UtcNow - $startTime).TotalSeconds -lt $monitoringSeconds) {
    $curConhost = (Get-Process -Name "conhost" -ErrorAction SilentlyContinue).Count
    if ($curConhost -gt $maxConhost) {
        $maxConhost = $curConhost
    }
    Start-Sleep -Seconds 2
}

$conhostDuring = $maxConhost
Write-Host "Max conhost count during runtime: $conhostDuring (Difference: $($conhostDuring - $conhostBefore))"

Stop-Process -Id $p.Id -Force
Start-Sleep -Seconds 2

$conhostAfter = (Get-Process -Name "conhost" -ErrorAction SilentlyContinue).Count
Write-Host "Conhost count after terminating overlay: $conhostAfter"

# Check log file for completed cycles
$logPath = "$TempDir\overlay.log"
if (Test-Path $logPath) {
    Write-Host "`n--- Snippet from overlay.log ---"
    $lines = Get-Content $logPath
    $lines | Select-Object -Last 20 | ForEach-Object { Write-Host $_ }
    $refreshCount = ($lines | Select-String "Starting async native refresh").Count
    Write-Host "Total refresh cycles executed: $refreshCount"
}

Write-Host "`nSUMMARY:"
Write-Host "Conhost before: $conhostBefore"
Write-Host "Conhost during (peak): $conhostDuring"
Write-Host "Conhost after:  $conhostAfter"

if ($conhostDuring -gt $conhostBefore) {
    Write-Warning "Conhost process count increased during execution!"
} else {
    Write-Host "SUCCESS: Zero console windows/conhost processes created by child processes (agy, codex)!"
}
