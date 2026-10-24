<#
.SYNOPSIS
    LLM Limits Always-on-Top Overlay for Windows 10/11.
.DESCRIPTION
    Vertical HUD with transparent background (AllowsTransparency=True).
    Displays consumed / used quota for Gemini, Claude/GPT, Kimi, and OpenRouter.
    Progress bars fill from left to right as quota is consumed (0% empty, 100% full).
    Color scheme: Monochrome neutral dark slate progress fill that smoothly darkens with consumption.
    Asynchronous 60s auto-refresh with 6s watchdog to prevent UI freezes.
#>

Add-Type -AssemblyName PresentationFramework, PresentationCore, WindowsBase, System.Drawing, System.Windows.Forms

# Win32 Helper for WS_EX_TOOLWINDOW (excludes window from Alt-Tab switcher without breaking AllowsTransparency)
Add-Type -TypeDefinition @"
using System;
using System.Runtime.InteropServices;

public static class WindowStyles {
    public const int GWL_EXSTYLE = -20;
    public const int WS_EX_TOOLWINDOW = 0x00000080;

    [DllImport("user32.dll", SetLastError = true)]
    public static extern int GetWindowLong(IntPtr hWnd, int nIndex);

    [DllImport("user32.dll", SetLastError = true)]
    public static extern int SetWindowLong(IntPtr hWnd, int nIndex, int dwNewLong);

    public static void SetToolWindow(IntPtr hWnd) {
        int exStyle = GetWindowLong(hWnd, GWL_EXSTYLE);
        SetWindowLong(hWnd, GWL_EXSTYLE, exStyle | WS_EX_TOOLWINDOW);
    }
}
"@

[System.Net.ServicePointManager]::SecurityProtocol = [System.Net.SecurityProtocolType]::Tls12

$ScriptDir = $PSScriptRoot
if ([string]::IsNullOrEmpty($ScriptDir)) {
    $ScriptDir = Split-Path -Parent $MyInvocation.MyCommand.Path
}
if ([string]::IsNullOrEmpty($ScriptDir)) {
    $ScriptDir = (Get-Location).Path
}

# Dynamic limits.json, log file, and config locations relative to script directory
$LimitsJsonPath = Join-Path $ScriptDir "limits.json"
$LogFile = Join-Path $ScriptDir "overlay.log"
$ConfigJsonPath = Join-Path $ScriptDir "overlay.config.json"

# Fallback path (only used if file exists)
$FallbackJsonPath = if ($env:USERPROFILE) {
    Join-Path $env:USERPROFILE ".gemini\antigravity-cli\limits.json"
} else { $null }

function Write-OverlayLog($msg) {
    try {
        $ts = (Get-Date).ToString("yyyy-MM-dd HH:mm:ss")
        Add-Content -Path $LogFile -Value "[$ts] $msg" -ErrorAction SilentlyContinue
    } catch {}
}

# ------------------------------------------------------------------------------
# Backend & Environment Resolution Configuration
# ------------------------------------------------------------------------------
$script:resolvedNativePython = $null
$script:resolvedWslDistro = $null

function Get-OverlayConfig {
    if (Test-Path $ConfigJsonPath) {
        try {
            $raw = Get-Content -Raw $ConfigJsonPath -ErrorAction SilentlyContinue
            if (-not [string]::IsNullOrWhiteSpace($raw)) {
                return ($raw | ConvertFrom-Json)
            }
        } catch {
            Write-OverlayLog "Error reading ${ConfigJsonPath}: $_"
        }
    }
    return $null
}

function Save-OverlayConfigField($field, $value) {
    try {
        $cfg = Get-OverlayConfig
        if ($null -eq $cfg) {
            $cfg = [pscustomobject]@{
                backend = "windows"
                pythonPath = ""
                wslDistro = ""
                timeoutSeconds = 0
            }
        }
        $cfg | Add-Member -MemberType NoteProperty -Name $field -Value $value -Force
        $jsonStr = $cfg | ConvertTo-Json -Depth 5
        [System.IO.File]::WriteAllText($ConfigJsonPath, $jsonStr, [System.Text.Encoding]::UTF8)
    } catch {
        Write-OverlayLog "Error saving config ($field=$value): $_"
    }
}

function Resolve-OverlayBackend {
    # Precedence: env LLM_LIMITS_BACKEND -> overlay.config.json -> auto
    if ($env:LLM_LIMITS_BACKEND) {
        $b = $env:LLM_LIMITS_BACKEND.Trim().ToLower()
        if ($b -in @("auto", "windows", "wsl")) {
            return @{ Backend = $b; Source = "env LLM_LIMITS_BACKEND" }
        }
    }
    $cfg = Get-OverlayConfig
    if ($cfg -and -not [string]::IsNullOrWhiteSpace($cfg.backend)) {
        $b = $cfg.backend.Trim().ToLower()
        if ($b -in @("auto", "windows", "wsl")) {
            return @{ Backend = $b; Source = "overlay.config.json" }
        }
    }
    return @{ Backend = "auto"; Source = "default" }
}

function Test-PythonCandidate($cmd, $argsPrefix = @()) {
    try {
        $psi = New-Object System.Diagnostics.ProcessStartInfo
        $psi.FileName = $cmd
        $prefix = if ($argsPrefix) { ($argsPrefix -join " ") + " " } else { "" }
        $psi.Arguments = $prefix + "-c " + [char]34 + "import sys;print(sys.executable)" + [char]34
        $psi.UseShellExecute = $false
        $psi.CreateNoWindow = $true
        $psi.RedirectStandardOutput = $true
        $psi.RedirectStandardError = $true
        $proc = [System.Diagnostics.Process]::Start($psi)
        $out = $proc.StandardOutput.ReadToEnd().Trim()
        $null = $proc.WaitForExit(3000)
        if ($proc.ExitCode -eq 0 -and [System.IO.File]::Exists($out)) {
            return @{ Command = $cmd; PrefixArgs = $argsPrefix; Executable = $out }
        }
    } catch {}
    return $null
}

function Cache-NativePython($res) {
    $script:resolvedNativePython = $res
    return $res
}

function Resolve-NativePython {
    if ($null -ne $script:resolvedNativePython) {
        return $script:resolvedNativePython
    }

    # Native interpreter resolution:
    # 0. Config pythonPath
    $cfg = Get-OverlayConfig
    if ($cfg -and -not [string]::IsNullOrWhiteSpace($cfg.pythonPath)) {
        $t = Test-PythonCandidate $cfg.pythonPath.Trim()
        if ($t) { return (Cache-NativePython $t) }
    }

    # 1. LLM_LIMITS_PYTHON env
    if ($env:LLM_LIMITS_PYTHON) {
        $t = Test-PythonCandidate $env:LLM_LIMITS_PYTHON.Trim()
        if ($t) { return (Cache-NativePython $t) }
    }

    # 2. py -3
    $tPy = Test-PythonCandidate "py" @("-3")
    if ($tPy) { return (Cache-NativePython $tPy) }

    # 3. python.exe on PATH
    $tPyExe = Test-PythonCandidate "python.exe"
    if ($tPyExe) { return (Cache-NativePython $tPyExe) }

    # 4. python3.exe on PATH
    $tPy3Exe = Test-PythonCandidate "python3.exe"
    if ($tPy3Exe) { return (Cache-NativePython $tPy3Exe) }

    # 5. %LOCALAPPDATA%\Programs\Python\* / %LOCALAPPDATA%\Python\* / C:\Python3*
    $candidates = @()
    if ($env:LOCALAPPDATA) {
        $candidates += (Get-ChildItem -Path (Join-Path $env:LOCALAPPDATA "Programs\Python") -Filter "python.exe" -Recurse -Depth 2 -ErrorAction SilentlyContinue | Select-Object -ExpandProperty FullName)
        $candidates += (Get-ChildItem -Path (Join-Path $env:LOCALAPPDATA "Python") -Filter "python.exe" -Recurse -Depth 2 -ErrorAction SilentlyContinue | Select-Object -ExpandProperty FullName)
    }
    $candidates += (Get-ChildItem -Path "C:\Python3*" -Filter "python.exe" -Recurse -Depth 2 -ErrorAction SilentlyContinue | Select-Object -ExpandProperty FullName)

    foreach ($cand in $candidates) {
        if ([System.IO.File]::Exists($cand)) {
            $t = Test-PythonCandidate $cand
            if ($t) { return (Cache-NativePython $t) }
        }
    }

    return $null
}

function Resolve-WslDistro {
    if ($null -ne $script:resolvedWslDistro) {
        return $script:resolvedWslDistro
    }

    # WSL mode: distro from LLM_LIMITS_WSL_DISTRO / AGY_WSL_DISTRO / config, else auto-detect from wsl.exe -l -q
    if ($env:LLM_LIMITS_WSL_DISTRO) {
        $script:resolvedWslDistro = $env:LLM_LIMITS_WSL_DISTRO.Trim()
        return $script:resolvedWslDistro
    }
    if ($env:AGY_WSL_DISTRO) {
        $script:resolvedWslDistro = $env:AGY_WSL_DISTRO.Trim()
        return $script:resolvedWslDistro
    }

    $cfg = Get-OverlayConfig
    if ($cfg -and -not [string]::IsNullOrWhiteSpace($cfg.wslDistro)) {
        $script:resolvedWslDistro = $cfg.wslDistro.Trim()
        return $script:resolvedWslDistro
    }

    try {
        $rawList = (wsl.exe -l -q 2>$null)
        if ($LASTEXITCODE -eq 0 -and $rawList) {
            $distros = @()
            foreach ($item in $rawList) {
                $clean = ($item -replace [char]0, "").Trim()
                if (-not [string]::IsNullOrWhiteSpace($clean)) {
                    $distros += $clean
                }
            }

            foreach ($distro in $distros) {
                if ($distro -like "docker-desktop*") { continue }
                # Test if distro actually has python3 (allowing 25s for cold WSL boot)
                try {
                    $psi = New-Object System.Diagnostics.ProcessStartInfo
                    $psi.FileName = "wsl.exe"
                    $psi.Arguments = "-d " + $distro + " which python3"
                    $psi.UseShellExecute = $false
                    $psi.CreateNoWindow = $true
                    $psi.RedirectStandardOutput = $true
                    $psi.RedirectStandardError = $true
                    $p = [System.Diagnostics.Process]::Start($psi)
                    $out = $p.StandardOutput.ReadToEnd().Trim()
                    $null = $p.WaitForExit(25000)
                    if ($p.ExitCode -eq 0) {
                        if ($out -and -not $out.Contains("no python3")) {
                            Write-OverlayLog "Auto-detected WSL distro with python3: $distro ($out)"
                            $script:resolvedWslDistro = $distro
                            return $distro
                        }
                    } else {
                        try { $p.Kill() } catch {}
                    }
                } catch {}
            }
        }
    } catch {
        Write-OverlayLog "Error querying wsl.exe distros: $_"
    }

    return $null
}

function Resolve-WslFetchScript($distro) {
    if ($env:LLM_LIMITS_WSL_SCRIPT) { return $env:LLM_LIMITS_WSL_SCRIPT }
    if ($env:AGY_WSL_SCRIPT) { return $env:AGY_WSL_SCRIPT }

    if ([string]::IsNullOrWhiteSpace($distro)) {
        return "fetch_quota.py"
    }

    # Check local fetch_quota.py in same directory and translate to WSL path
    $localPy = Join-Path $ScriptDir "fetch_quota.py"
    if (Test-Path $localPy) {
        try {
            $p = ($localPy -replace '\\', '/')
            $wslPath = (wsl.exe -d $distro wslpath -u $p 2>$null)
            if ($LASTEXITCODE -eq 0 -and -not [string]::IsNullOrWhiteSpace($wslPath)) {
                return $wslPath.Trim()
            }
        } catch {}
    }

    # Query WSL user's home directory
    try {
        $wslHome = (wsl.exe -d $distro -e bash -lc 'echo $HOME' 2>$null)
        if ($LASTEXITCODE -eq 0 -and -not [string]::IsNullOrWhiteSpace($wslHome)) {
            $h = $wslHome.Trim()
            $candidates = @(
                "$h/llm-limits-overlay/fetch_quota.py",
                "$h/agy-limits-overlay/fetch_quota.py"
            )
            foreach ($c in $candidates) {
                $chk = (wsl.exe -d $distro test -f $c 2>$null)
                if ($LASTEXITCODE -eq 0) {
                    return $c
                }
            }
            return "$h/llm-limits-overlay/fetch_quota.py"
        }
    } catch {}

    return "fetch_quota.py"
}

# Trap global unhandled exceptions to log file
[System.AppDomain]::CurrentDomain.add_UnhandledException({
    param($sender, $e)
    Write-OverlayLog "CRITICAL AppDomain Exception: $($e.ExceptionObject)"
})

Write-OverlayLog "=== LLM Limits Overlay Starting (PID=$PID) ==="

# Single instance enforcement: terminate older overlay instances if running
try {
    $currentPid = $PID
    Get-CimInstance Win32_Process -Filter "Name='powershell.exe'" | Where-Object {
        $_.ProcessId -ne $currentPid -and (
            $_.CommandLine -like "*limits_overlay.ps1*" -or
            $_.CommandLine -like "*agy_limits_overlay.ps1*"
        )
    } | ForEach-Object {
        Write-OverlayLog "Terminating prior overlay instance (PID=$($_.ProcessId))."
        Stop-Process -Id $_.ProcessId -Force -ErrorAction SilentlyContinue
    }
} catch {}

# ==============================================================================
# НАСТРАЙВАЕМЫЕ ПАРАМЕТРЫ ТЕМЫ И ПРОЗРАЧНОСТИ (THEME CONFIGURATION)
# Изменяйте значения здесь для быстрой настройки внешнего вида без правки XAML.
# ==============================================================================
$Config = @{
    # Прозрачность подложек модулей (~49% непрозрачности)
    PodBackground      = "#7CEEF2F6"  
    PodBorderBrush     = "#2064748B"  # Тонкая малозаметная граница (~12% непрозрачности)
    PodBorderThickness = 1.0          # Толщина рамки (px)
    PodCornerRadius    = 8            # Радиус скругления углов

    # Цвета текста
    TitleText          = "#000000"    # Заголовок 'AGY' (черный)
    GeminiHeader       = "#000000"    # Заголовок 'GEMINI' (черный)
    ClaudeHeader       = "#000000"    # Заголовок 'CLAUDE/GPT' (черный)
    OpenRouterHeader   = "#000000"    # Заголовок 'OPENROUTER' (черный)
    KimiHeader         = "#000000"    # Заголовок 'KIMI' (черный)
    LabelText          = "#0F172A"    # Подписи таймеров '3ч 47м', '5д 23ч' (четкий темный графит)

    # Служебные надписи (время обновления, статус, подписи без синего/акцентного цвета):
    MetaText           = "#0F172A"    # Нейтральный темный графит в тон процентам
    MetaTextMuted      = "#475569"    # Приглушенный графит для устаревших/вторичных данных
    UpdatedText        = "#0F172A"    # Метка времени внизу (четкий нейтральный графит, без синего)
    UpdatedStaleText   = "#475569"    # Метка времени при устаревании (stale)

    # Минималистичный нейтральный бейдж [РАСХОД]
    BadgeBackground    = "#18000000"  # Полупрозрачная нейтральная заливка
    BadgeBorder        = "#25000000"  # Тонкая нейтральная обводка
    BadgeText          = "#0F172A"    # Нейтральный цвет текста бейджа (темный графит)

    # Трек прогресс-бара (светлый нейтральный)
    BarTrackBackground = "#22000000"  # Деликатный полупрозрачный трек шкалы (~13%)

    # Заполнение шкалы прогресс-бара (единый нейтральный темный графит, плавно темнеет с ростом расхода):
    # При 0% расход отображается мягким нейтральным графитом Gray 600 (#4B5563), плавно темнея к 100% до глубокого Gray 900 (#111827).
    # Для быстрой смены базового оттенка одной строкой достаточно изменить ProgressFill.
    ProgressFill       = "#1F2937"    # Базовый оттенок шкалы (темный графит Gray 800)
    ProgressFillStart  = "#4B5563"    # Начальный цвет заполнения при 0% расхода (Gray 600)
    ProgressFillEnd    = "#111827"    # Финальный цвет заполнения при 100% расхода (глубокий Gray 900)

    # Текст процентов (нейтральный темный графит, без цветовых акцентов):
    PercentText        = "#0F172A"    # Основной цвет процентов (четкий графит / Slate 900)
    PercentText100     = "#000000"    # Процент при 100% расхода (чистый черный)
}

[xml]$xaml = @"
<Window xmlns="http://schemas.microsoft.com/winfx/2006/xaml/presentation"
        xmlns:x="http://schemas.microsoft.com/winfx/2006/xaml"
        Title="LLM Limits Overlay"
        Width="290"
        SizeToContent="Height"
        MaxHeight="650"
        WindowStyle="None"
        AllowsTransparency="True"
        Background="Transparent"
        Topmost="True"
        ShowInTaskbar="False"
        WindowStartupLocation="Manual"
        TextOptions.TextFormattingMode="Display"
        TextOptions.TextRenderingMode="ClearType"
        RenderOptions.ClearTypeHint="Enabled"
        UseLayoutRounding="True"
        SnapsToDevicePixels="True">
    <Window.ContextMenu>
        <ContextMenu Background="#F8FAFC" BorderBrush="#CBD5E1" BorderThickness="1">
            <MenuItem Name="MenuRefresh" Header="Обновить" Foreground="$($Config.MetaText)"/>
            <Separator Background="#E2E8F0"/>
            <MenuItem Name="MenuEnv" Header="Окружение" Foreground="$($Config.MetaText)">
                <MenuItem Name="MenuBackendAuto" Header="Авто" IsCheckable="True" Foreground="$($Config.MetaText)"/>
                <MenuItem Name="MenuBackendWindows" Header="Windows (нативный Python)" IsCheckable="True" Foreground="$($Config.MetaText)"/>
                <MenuItem Name="MenuBackendWsl" Header="WSL" IsCheckable="True" Foreground="$($Config.MetaText)"/>
            </MenuItem>
            <Separator Background="#E2E8F0"/>
            <MenuItem Name="MenuClose" Header="Закрыть" Foreground="$($Config.MetaText)"/>
        </ContextMenu>
    </Window.ContextMenu>
    <Window.Resources>
        <Style TargetType="ProgressBar">
            <Setter Property="Height" Value="5.5"/>
            <Setter Property="BorderThickness" Value="0"/>
            <Setter Property="Background" Value="$($Config.BarTrackBackground)"/>
            <Setter Property="ToolTipService.InitialShowDelay" Value="100"/>
        </Style>
        <Style TargetType="TextBlock">
            <Setter Property="FontFamily" Value="Segoe UI, Segoe UI Variable Display"/>
        </Style>
        <Style TargetType="Grid">
            <Setter Property="ToolTipService.InitialShowDelay" Value="100"/>
        </Style>
    </Window.Resources>

    <!-- Transparent root: allows dragging anywhere while empty spaces reveal windows underneath -->
    <Grid Background="#01000000" Margin="6,6,6,12">
        <Grid.RowDefinitions>
            <RowDefinition Height="Auto"/> <!-- 1. Kimi Pod (with update status inside) -->
            <RowDefinition Height="Auto"/> <!-- 2. OpenRouter Pod -->
            <RowDefinition Height="Auto"/> <!-- 3. Merged Gemini & Claude/GPT Pod -->
        </Grid.RowDefinitions>

        <!-- 1. KIMI Pod (Weekly first, 5h second, with update status inside) -->
        <Border Grid.Row="0"
                Background="$($Config.PodBackground)"
                BorderBrush="$($Config.PodBorderBrush)"
                BorderThickness="$($Config.PodBorderThickness)"
                CornerRadius="$($Config.PodCornerRadius)"
                Padding="10,8,10,8"
                Margin="0,0,0,6">
            <Border.Effect>
                <DropShadowEffect BlurRadius="4" ShadowDepth="1" Opacity="0.06" Color="#000000"/>
            </Border.Effect>
            <StackPanel>
                <TextBlock Text="KIMI" FontSize="11" FontWeight="Bold" Foreground="$($Config.KimiHeader)" Margin="0,0,0,4"/>

                <!-- Kimi 7d Row (Weekly first) -->
                <Grid Name="RowKimiWk" Margin="0,1,0,2" Background="#01000000" ToolTip="Лимит 7d">
                    <TextBlock Name="TxtKimiWkLabel" Text="--д --ч" FontSize="11.5" FontWeight="SemiBold" Foreground="$($Config.LabelText)"/>
                    <TextBlock Name="TxtKimiWkPercent" Text="--%" FontSize="11.5" FontWeight="SemiBold" Foreground="$($Config.PercentText)" HorizontalAlignment="Right"/>
                </Grid>
                <ProgressBar Name="BarKimiWk" Value="0" Foreground="$($Config.ProgressFillStart)" Margin="0,1,0,7" ToolTip="Лимит 7d"/>

                <!-- Kimi 5h Row -->
                <Grid Name="RowKimi5h" Margin="0,1,0,2" Background="#01000000" ToolTip="Лимит 5h">
                    <TextBlock Name="TxtKimi5hLabel" Text="--ч --м" FontSize="11.5" FontWeight="SemiBold" Foreground="$($Config.LabelText)"/>
                    <TextBlock Name="TxtKimi5hPercent" Text="--%" FontSize="11.5" FontWeight="SemiBold" Foreground="$($Config.PercentText)" HorizontalAlignment="Right"/>
                </Grid>
                <ProgressBar Name="BarKimi5h" Value="0" Foreground="$($Config.ProgressFillStart)" Margin="0,1,0,3" ToolTip="Лимит 5h"/>

                <!-- Subtle Last Updated Status inside KIMI -->
                <TextBlock Name="TxtUpdated"
                           Text="только что"
                           FontSize="8.5"
                           FontWeight="Normal"
                           Foreground="$($Config.MetaText)"
                           HorizontalAlignment="Right"
                           Margin="0,1,0,1"/>
            </StackPanel>
        </Border>

        <!-- 2. OPENROUTER Pod (Vertical) -->
        <Border Grid.Row="1"
                Background="$($Config.PodBackground)"
                BorderBrush="$($Config.PodBorderBrush)"
                BorderThickness="$($Config.PodBorderThickness)"
                CornerRadius="$($Config.PodCornerRadius)"
                Padding="10,8,10,8"
                Margin="0,0,0,6">
            <Border.Effect>
                <DropShadowEffect BlurRadius="4" ShadowDepth="1" Opacity="0.06" Color="#000000"/>
            </Border.Effect>
            <StackPanel>
                <TextBlock Text="OPENROUTER" FontSize="11" FontWeight="Bold" Foreground="$($Config.OpenRouterHeader)" Margin="0,0,0,4"/>

                <!-- Monthly Quota Row -->
                <Grid Name="RowOrMonthly" Margin="0,1,0,2" Background="#01000000" ToolTip="Месячный лимит">
                    <TextBlock Name="TxtOrLabel" Text="--д --ч" FontSize="11.5" FontWeight="SemiBold" Foreground="$($Config.LabelText)"/>
                    <TextBlock Name="TxtOrPercent" Text="--%" FontSize="11.5" FontWeight="SemiBold" Foreground="$($Config.PercentText)" HorizontalAlignment="Right"/>
                </Grid>
                <ProgressBar Name="BarOr" Value="0" Foreground="$($Config.ProgressFillStart)" Margin="0,1,0,4" ToolTip="Месячный лимит"/>

                <!-- Subtle Free Daily & Balance Status -->
                <Grid Margin="0,1,0,1">
                    <TextBlock Name="TxtOrFreeDaily" Text="free: --/--" FontSize="9" Foreground="$($Config.MetaText)"/>
                    <TextBlock Name="TxtOrBalance" Text="ост. --" FontSize="9" Foreground="$($Config.MetaText)" HorizontalAlignment="Right"/>
                </Grid>
            </StackPanel>
        </Border>

        <!-- 3. Combined GEMINI & CLAUDE/GPT Pod (~49% translucent frosted slate) -->
        <Border Grid.Row="2"
                Background="$($Config.PodBackground)"
                BorderBrush="$($Config.PodBorderBrush)"
                BorderThickness="$($Config.PodBorderThickness)"
                CornerRadius="$($Config.PodCornerRadius)"
                Padding="10,8,10,11"
                Margin="0,0,0,0">
            <Border.Effect>
                <DropShadowEffect BlurRadius="4" ShadowDepth="1" Opacity="0.06" Color="#000000"/>
            </Border.Effect>
            <StackPanel>
                <!-- GEMINI Section -->
                <TextBlock Text="GEMINI" FontSize="11" FontWeight="Bold" Foreground="$($Config.GeminiHeader)" Margin="0,0,0,4"/>

                <!-- Gemini 7d Row (Weekly first) -->
                <Grid Name="RowGeminiWk" Margin="0,1,0,2" Background="#01000000" ToolTip="Лимит 7d">
                    <TextBlock Name="TxtGeminiWkLabel" Text="--д --ч" FontSize="11.5" FontWeight="SemiBold" Foreground="$($Config.LabelText)"/>
                    <TextBlock Name="TxtGeminiWkPercent" Text="--%" FontSize="11.5" FontWeight="SemiBold" Foreground="$($Config.PercentText)" HorizontalAlignment="Right"/>
                </Grid>
                <ProgressBar Name="BarGeminiWk" Value="0" Foreground="$($Config.ProgressFillStart)" Margin="0,1,0,7" ToolTip="Лимит 7d"/>

                <!-- Gemini 5h Row -->
                <Grid Name="RowGemini5h" Margin="0,1,0,2" Background="#01000000" ToolTip="Лимит 5h">
                    <TextBlock Name="TxtGemini5hLabel" Text="--ч --м" FontSize="11.5" FontWeight="SemiBold" Foreground="$($Config.LabelText)"/>
                    <TextBlock Name="TxtGemini5hPercent" Text="--%" FontSize="11.5" FontWeight="SemiBold" Foreground="$($Config.PercentText)" HorizontalAlignment="Right"/>
                </Grid>
                <ProgressBar Name="BarGemini5h" Value="0" Foreground="$($Config.ProgressFillStart)" Margin="0,1,0,8" ToolTip="Лимит 5h"/>

                <!-- Delicate Divider between Gemini and Claude -->
                <Border Background="#18000000" Height="0.8" Margin="0,1,0,7"/>

                <!-- CLAUDE/GPT Section -->
                <TextBlock Text="CLAUDE/GPT" FontSize="11" FontWeight="Bold" Foreground="$($Config.ClaudeHeader)" Margin="0,0,0,4"/>

                <!-- Claude 7d Row (Weekly first) -->
                <Grid Name="RowClaudeWk" Margin="0,1,0,2" Background="#01000000" ToolTip="Лимит 7d">
                    <TextBlock Name="TxtClaudeWkLabel" Text="--д --ч" FontSize="11.5" FontWeight="SemiBold" Foreground="$($Config.LabelText)"/>
                    <TextBlock Name="TxtClaudeWkPercent" Text="--%" FontSize="11.5" FontWeight="SemiBold" Foreground="$($Config.PercentText)" HorizontalAlignment="Right"/>
                </Grid>
                <ProgressBar Name="BarClaudeWk" Value="0" Foreground="$($Config.ProgressFillStart)" Margin="0,1,0,7" ToolTip="Лимит 7d"/>

                <!-- Claude 5h Row -->
                <Grid Name="RowClaude5h" Margin="0,1,0,2" Background="#01000000" ToolTip="Лимит 5h">
                    <TextBlock Name="TxtClaude5hLabel" Text="--ч --м" FontSize="11.5" FontWeight="SemiBold" Foreground="$($Config.LabelText)"/>
                    <TextBlock Name="TxtClaude5hPercent" Text="--%" FontSize="11.5" FontWeight="SemiBold" Foreground="$($Config.PercentText)" HorizontalAlignment="Right"/>
                </Grid>
                <ProgressBar Name="BarClaude5h" Value="0" Foreground="$($Config.ProgressFillStart)" Margin="0,1,0,2" ToolTip="Лимит 5h"/>
            </StackPanel>
        </Border>
    </Grid>
</Window>
"@

$reader = (New-Object System.Xml.XmlNodeReader $xaml)
$window = [System.Windows.Markup.XamlReader]::Load($reader)

# Set WS_EX_TOOLWINDOW when HWND is created: excludes from Alt-Tab switcher completely
$window.Add_SourceInitialized({
    $helper = New-Object System.Windows.Interop.WindowInteropHelper($window)
    [WindowStyles]::SetToolWindow($helper.Handle)
})

# Position at bottom right corner (WorkingArea accounts for taskbar so overlay does not overlap it)
$screen = [System.Windows.Forms.Screen]::PrimaryScreen
$script:isUserPositioned = $false
$script:marginRight = 14
$script:marginBottom = 4

function Update-OverlayPosition {
    if ($script:isUserPositioned) { return }

    try {
        $workArea = [System.Windows.SystemParameters]::WorkArea
        $waRight = $workArea.Right
        $waBottom = $workArea.Bottom
    } catch {
        $screen = [System.Windows.Forms.Screen]::PrimaryScreen
        $waRight = $screen.WorkingArea.Right
        $waBottom = $screen.WorkingArea.Bottom
    }

    $actualW = if ($window.ActualWidth -gt 0) { $window.ActualWidth } else { 290 }
    $actualH = if ($window.ActualHeight -gt 0) { $window.ActualHeight } else {
        $window.UpdateLayout()
        if ($window.ActualHeight -gt 0) { $window.ActualHeight } else {
            $window.Measure([System.Windows.Size]::new(290, [double]::PositiveInfinity))
            $window.DesiredSize.Height
        }
    }

    if ($actualH -gt 0) {
        $window.Left = $waRight - $actualW - $script:marginRight
        $window.Top = $waBottom - $actualH - $script:marginBottom
    }
}

# Pre-measure and set initial coordinates
$window.Measure([System.Windows.Size]::new(290, [double]::PositiveInfinity))
$window.Arrange([System.Windows.Rect]::new(0, 0, 290, $window.DesiredSize.Height))
Update-OverlayPosition

$window.Add_ContentRendered({
    Update-OverlayPosition
})

$window.Add_SizeChanged({
    param($s, $e)
    if ($e.HeightChanged) {
        if ($script:isUserPositioned) {
            $deltaH = $e.NewSize.Height - $e.PreviousSize.Height
            if ($e.PreviousSize.Height -gt 0) {
                $window.Top = $window.Top - $deltaH
            }
        } else {
            Update-OverlayPosition
        }
    }
})

if ($args -contains "-VerifyPosition") {
    $window.UpdateLayout()
    Update-OverlayPosition
    $waRight = if ($workArea) { $workArea.Right } else { [System.Windows.Forms.Screen]::PrimaryScreen.WorkingArea.Right }
    $waBottom = if ($workArea) { $workArea.Bottom } else { [System.Windows.Forms.Screen]::PrimaryScreen.WorkingArea.Bottom }
    $actualH = if ($window.ActualHeight -gt 0) { $window.ActualHeight } else { $window.DesiredSize.Height }
    $actualW = if ($window.ActualWidth -gt 0) { $window.ActualWidth } else { 290 }
    $posInfo = [pscustomobject]@{
        WindowLeft = $window.Left
        WindowTop = $window.Top
        WindowWidth = $actualW
        WindowHeight = $actualH
        WorkAreaRight = $waRight
        WorkAreaBottom = $waBottom
        RightOffset = $waRight - ($window.Left + $actualW)
        BottomOffset = $waBottom - ($window.Top + $actualH)
    }
    Write-Output ($posInfo | ConvertTo-Json)
    [System.Environment]::Exit(0)
}

# UI References
$menuRefresh = $window.FindName("MenuRefresh")
$menuClose = $window.FindName("MenuClose")
$menuBackendAuto = $window.FindName("MenuBackendAuto")
$menuBackendWindows = $window.FindName("MenuBackendWindows")
$menuBackendWsl = $window.FindName("MenuBackendWsl")
if ($null -eq $menuRefresh -and $window.ContextMenu) {
    foreach ($item in $window.ContextMenu.Items) {
        if ($item -is [System.Windows.Controls.MenuItem]) {
            if ($item.Header -eq "Обновить") { $menuRefresh = $item }
            if ($item.Header -eq "Закрыть") { $menuClose = $item }
            if ($item.Header -eq "Окружение") {
                foreach ($sub in $item.Items) {
                    if ($sub -is [System.Windows.Controls.MenuItem]) {
                        if ($sub.Header -eq "Авто") { $menuBackendAuto = $sub }
                        if ($sub.Header -like "*Windows*") { $menuBackendWindows = $sub }
                        if ($sub.Header -eq "WSL") { $menuBackendWsl = $sub }
                    }
                }
            }
        }
    }
}
$txtUpdated = $window.FindName("TxtUpdated")

$rowGemini5h = $window.FindName("RowGemini5h")
$txtGemini5hLabel = $window.FindName("TxtGemini5hLabel")
$txtGemini5hPercent = $window.FindName("TxtGemini5hPercent")
$barGemini5h = $window.FindName("BarGemini5h")

$rowGeminiWk = $window.FindName("RowGeminiWk")
$txtGeminiWkLabel = $window.FindName("TxtGeminiWkLabel")
$txtGeminiWkPercent = $window.FindName("TxtGeminiWkPercent")
$barGeminiWk = $window.FindName("BarGeminiWk")

$rowClaude5h = $window.FindName("RowClaude5h")
$txtClaude5hLabel = $window.FindName("TxtClaude5hLabel")
$txtClaude5hPercent = $window.FindName("TxtClaude5hPercent")
$barClaude5h = $window.FindName("BarClaude5h")

$rowClaudeWk = $window.FindName("RowClaudeWk")
$txtClaudeWkLabel = $window.FindName("TxtClaudeWkLabel")
$txtClaudeWkPercent = $window.FindName("TxtClaudeWkPercent")
$barClaudeWk = $window.FindName("BarClaudeWk")

# OpenRouter Controls
$rowOrMonthly = $window.FindName("RowOrMonthly")
$txtOrLabel = $window.FindName("TxtOrLabel")
$txtOrPercent = $window.FindName("TxtOrPercent")
$barOr = $window.FindName("BarOr")
$txtOrFreeDaily = $window.FindName("TxtOrFreeDaily")
$txtOrBalance = $window.FindName("TxtOrBalance")

# Kimi Controls
$rowKimi5h = $window.FindName("RowKimi5h")
$txtKimi5hLabel = $window.FindName("TxtKimi5hLabel")
$txtKimi5hPercent = $window.FindName("TxtKimi5hPercent")
$barKimi5h = $window.FindName("BarKimi5h")

$rowKimiWk = $window.FindName("RowKimiWk")
$txtKimiWkLabel = $window.FindName("TxtKimiWkLabel")
$txtKimiWkPercent = $window.FindName("TxtKimiWkPercent")
$barKimiWk = $window.FindName("BarKimiWk")

# Drag & Window Controls
$window.Add_MouseLeftButtonDown({
    param($sender, $e)
    if ($e.ButtonState -eq [System.Windows.Input.MouseButtonState]::Pressed) {
        $window.DragMove()
        $script:isUserPositioned = $true
    }
})

function Update-MenuBackendSelection {
    $res = Resolve-OverlayBackend
    $current = $res.Backend
    if ($menuBackendAuto) { $menuBackendAuto.IsChecked = ($current -eq "auto") }
    if ($menuBackendWindows) { $menuBackendWindows.IsChecked = ($current -eq "windows") }
    if ($menuBackendWsl) { $menuBackendWsl.IsChecked = ($current -eq "wsl") }
}

function Set-BackendFromMenu($newBackend) {
    $script:resolvedNativePython = $null
    $script:resolvedWslDistro = $null
    Save-OverlayConfigField "backend" $newBackend
    Write-OverlayLog "User switched backend to '$newBackend' via menu."
    Update-MenuBackendSelection
    $script:lastIsStale = $false
    if ($script:fetchProc -and -not $script:fetchProc.HasExited) {
        try {
            $watchdogTimer.Stop()
            $script:fetchProc.Kill()
            $script:fetchProc.Dispose()
        } catch {}
        $script:fetchProc = $null
    }
    $script:isFetching = $false
    Start-AsyncRefresh
}

if ($menuRefresh) {
    $menuRefresh.Add_Click({
        Start-AsyncRefresh
    })
}

if ($menuBackendAuto) {
    $menuBackendAuto.Add_Click({ Set-BackendFromMenu "auto" })
}
if ($menuBackendWindows) {
    $menuBackendWindows.Add_Click({ Set-BackendFromMenu "windows" })
}
if ($menuBackendWsl) {
    $menuBackendWsl.Add_Click({ Set-BackendFromMenu "wsl" })
}

if ($window.ContextMenu) {
    $window.ContextMenu.Add_Opened({
        Update-MenuBackendSelection
    })
}
Update-MenuBackendSelection

if ($menuClose) {
    $menuClose.Add_Click({
        Write-OverlayLog "User clicked 'Закрыть'. Exiting."
        $window.Close()
    })
}

# ==============================================================================
# МОНОХРОМНАЯ ШКАЛА РАСХОДА (Нейтральный темный графит, плавно темнеет с ростом %)
# ==============================================================================
function Get-ProgressBrush($usedFraction) {
    if ($null -eq $usedFraction) { $usedFraction = 0.0 }
    $f = [math]::Max(0.0, [math]::Min(1.0, [double]$usedFraction))

    $hexStart = $Config.ProgressFillStart
    $hexEnd = $Config.ProgressFillEnd

    if ([string]::IsNullOrWhiteSpace($hexStart) -or [string]::IsNullOrWhiteSpace($hexEnd)) {
        $baseHex = if ($Config.ProgressFill) { $Config.ProgressFill } else { "#1F2937" }
        $c = [System.Windows.Media.ColorConverter]::ConvertFromString($baseHex)
        $r1 = [math]::Min(255, [int]($c.R * 1.5))
        $g1 = [math]::Min(255, [int]($c.G * 1.5))
        $b1 = [math]::Min(255, [int]($c.B * 1.5))
        $r2 = [math]::Max(0, [int]($c.R * 0.7))
        $g2 = [math]::Max(0, [int]($c.G * 0.7))
        $b2 = [math]::Max(0, [int]($c.B * 0.7))
    } else {
        $c1 = [System.Windows.Media.ColorConverter]::ConvertFromString($hexStart)
        $c2 = [System.Windows.Media.ColorConverter]::ConvertFromString($hexEnd)
        $r1 = $c1.R; $g1 = $c1.G; $b1 = $c1.B
        $r2 = $c2.R; $g2 = $c2.G; $b2 = $c2.B
    }

    $r = [int][math]::Round($r1 + ($r2 - $r1) * $f)
    $g = [int][math]::Round($g1 + ($g2 - $g1) * $f)
    $b = [int][math]::Round($b1 + ($b2 - $b1) * $f)

    $hex = "#{0:X2}{1:X2}{2:X2}" -f $r, $g, $b
    $converter = New-Object System.Windows.Media.BrushConverter
    return $converter.ConvertFromString($hex)
}

function Get-PercentBrush($usedFraction) {
    if ($null -eq $usedFraction) { $usedFraction = 0.0 }
    $converter = New-Object System.Windows.Media.BrushConverter
    if ([double]$usedFraction -ge 1.0) {
        $c100 = if ($Config.PercentText100) { $Config.PercentText100 } else { "#000000" }
        return $converter.ConvertFromString($c100)
    }
    $cNorm = if ($Config.PercentText) { $Config.PercentText } else { "#0F172A" }
    return $converter.ConvertFromString($cNorm)
}

function Format-ResetTimeLabel($resetIn) {
    if ([string]::IsNullOrWhiteSpace($resetIn) -or $resetIn -eq "--" -or $resetIn -eq "N/A") {
        return "--"
    }
    if ($resetIn -eq "ready") {
        return "готов"
    }
    # Formats e.g. "3h 47m" -> "3ч 47м", "5d 23h" -> "5д 23ч", "45m" -> "45м"
    $ru = $resetIn -replace 'd\s*', 'д ' -replace 'h\s*', 'ч ' -replace 'm\s*', 'м'
    return $ru.Trim()
}

function Format-RowTooltip($windowType, $resetIn) {
    if ([string]::IsNullOrWhiteSpace($resetIn) -or $resetIn -eq "--" -or $resetIn -eq "N/A") {
        return "Лимит $windowType • Нет данных о сбросе"
    }
    if ($resetIn -eq "ready") {
        return "Лимит $windowType • Квота восстановлена"
    }
    $ru = $resetIn -replace 'd\s*', 'д ' -replace 'h\s*', 'ч ' -replace 'm\s*', 'м'
    return "Лимит $windowType • Сброс через $($ru.Trim())"
}

function Get-RelativeTimeText($updatedStr, $isStale) {
    if ([string]::IsNullOrWhiteSpace($updatedStr)) {
        if ($isStale) { return "устарело" } else { return "нет данных" }
    }
    try {
        $formats = [string[]]@(
            "yyyy-MM-dd HH:mm:ss",
            "yyyy-MM-ddTHH:mm:ss",
            "yyyy-MM-ddTHH:mm:ssZ",
            "yyyy/MM/dd HH:mm:ss"
        )
        $dt = [datetime]::ParseExact($updatedStr, $formats, [System.Globalization.CultureInfo]::InvariantCulture, [System.Globalization.DateTimeStyles]::None)
        $diff = [datetime]::Now - $dt
        $totalMinutes = [int][math]::Floor($diff.TotalMinutes)

        $baseText = ""
        if ($totalMinutes -lt 1) {
            $baseText = "только что"
        } elseif ($totalMinutes -lt 60) {
            $baseText = "$totalMinutes мин"
        } else {
            $hrs = [int][math]::Floor($totalMinutes / 60)
            $mins = $totalMinutes % 60
            if ($mins -gt 0) {
                $baseText = "$($hrs)ч $($mins)м"
            } else {
                $baseText = "$($hrs)ч"
            }
        }

        if ($isStale) {
            return "$baseText (stale)"
        }
        return $baseText
    } catch {
        if ($isStale) { return "устарело" } else { return "обновлено" }
    }
}

$script:lastUpdatedStr = $null
$script:lastIsStale = $false

function Update-HeaderRelativeTime {
    if ($null -ne $txtUpdated -and -not [string]::IsNullOrEmpty($script:lastUpdatedStr)) {
        $txt = Get-RelativeTimeText $script:lastUpdatedStr $script:lastIsStale
        $txtUpdated.Text = $txt
        $converter = New-Object System.Windows.Media.BrushConverter
        if ($script:lastIsStale) {
            $staleColor = if ($Config.UpdatedStaleText) { $Config.UpdatedStaleText } else { "#475569" }
            $txtUpdated.Foreground = $converter.ConvertFromString($staleColor)
        } else {
            $normalColor = if ($Config.MetaText) { $Config.MetaText } else { "#0F172A" }
            $txtUpdated.Foreground = $converter.ConvertFromString($normalColor)
        }
    }
}

function Get-UsedInfo($bucket) {
    if ($null -eq $bucket) {
        return @{ Percent = 0.0; Fraction = 0.0; ResetIn = "--" }
    }
    $usedPct = 0.0
    $usedFrac = 0.0
    if ($null -ne $bucket.used_fraction) {
        $usedFrac = [double]$bucket.used_fraction
        $usedPct = [double]$bucket.used_percent
    } elseif ($null -ne $bucket.fraction) {
        $remFrac = [double]$bucket.fraction
        $usedFrac = [math]::Max(0.0, [math]::Min(1.0, (1.0 - $remFrac)))
        $usedPct = [math]::Round($usedFrac * 100, 1)
    }
    $resetIn = if ($bucket.reset_in) { $bucket.reset_in } else { "--" }
    return @{ Percent = $usedPct; Fraction = $usedFrac; ResetIn = $resetIn }
}

function Update-UIWithData($jsonObj) {
    if ($null -eq $jsonObj) { return }

    $data = $jsonObj.data
    $status = $jsonObj.status
    $updated = $jsonObj.last_updated

    $script:lastUpdatedStr = $updated
    $script:lastIsStale = ($status -ne "online")
    Update-HeaderRelativeTime

    # Gemini 5h
    if ($data.gemini_5h) {
        $u = Get-UsedInfo $data.gemini_5h
        $txtGemini5hLabel.Text = Format-ResetTimeLabel $u.ResetIn
        $txtGemini5hPercent.Text = "$($u.Percent)%"
        $barGemini5h.Value = $u.Percent
        $barGemini5h.Foreground = Get-ProgressBrush $u.Fraction
        $txtGemini5hPercent.Foreground = Get-PercentBrush $u.Fraction
        if ($u.Fraction -ge 1.0 -or $u.Percent -ge 100.0) {
            $txtGemini5hPercent.FontWeight = [System.Windows.FontWeights]::Bold
        } else {
            $txtGemini5hPercent.FontWeight = [System.Windows.FontWeights]::SemiBold
        }
        $tt = Format-RowTooltip "5h" $u.ResetIn
        $rowGemini5h.ToolTip = $tt
        $barGemini5h.ToolTip = $tt
    }

    # Gemini 7d (Weekly)
    if ($data.gemini_weekly) {
        $u = Get-UsedInfo $data.gemini_weekly
        $txtGeminiWkLabel.Text = Format-ResetTimeLabel $u.ResetIn
        $txtGeminiWkPercent.Text = "$($u.Percent)%"
        $barGeminiWk.Value = $u.Percent
        $barGeminiWk.Foreground = Get-ProgressBrush $u.Fraction
        $txtGeminiWkPercent.Foreground = Get-PercentBrush $u.Fraction
        if ($u.Fraction -ge 1.0 -or $u.Percent -ge 100.0) {
            $txtGeminiWkPercent.FontWeight = [System.Windows.FontWeights]::Bold
        } else {
            $txtGeminiWkPercent.FontWeight = [System.Windows.FontWeights]::SemiBold
        }
        $tt = Format-RowTooltip "7d" $u.ResetIn
        $rowGeminiWk.ToolTip = $tt
        $barGeminiWk.ToolTip = $tt
    }

    # Claude 5h
    if ($data.claude_5h) {
        $u = Get-UsedInfo $data.claude_5h
        $txtClaude5hLabel.Text = Format-ResetTimeLabel $u.ResetIn
        $txtClaude5hPercent.Text = "$($u.Percent)%"
        $barClaude5h.Value = $u.Percent
        $barClaude5h.Foreground = Get-ProgressBrush $u.Fraction
        $txtClaude5hPercent.Foreground = Get-PercentBrush $u.Fraction
        if ($u.Fraction -ge 1.0 -or $u.Percent -ge 100.0) {
            $txtClaude5hPercent.FontWeight = [System.Windows.FontWeights]::Bold
        } else {
            $txtClaude5hPercent.FontWeight = [System.Windows.FontWeights]::SemiBold
        }
        $tt = Format-RowTooltip "5h" $u.ResetIn
        $rowClaude5h.ToolTip = $tt
        $barClaude5h.ToolTip = $tt
    }

    # Claude 7d (Weekly)
    if ($data.claude_weekly) {
        $u = Get-UsedInfo $data.claude_weekly
        $txtClaudeWkLabel.Text = Format-ResetTimeLabel $u.ResetIn
        $txtClaudeWkPercent.Text = "$($u.Percent)%"
        $barClaudeWk.Value = $u.Percent
        $barClaudeWk.Foreground = Get-ProgressBrush $u.Fraction
        $txtClaudeWkPercent.Foreground = Get-PercentBrush $u.Fraction
        if ($u.Fraction -ge 1.0 -or $u.Percent -ge 100.0) {
            $txtClaudeWkPercent.FontWeight = [System.Windows.FontWeights]::Bold
        } else {
            $txtClaudeWkPercent.FontWeight = [System.Windows.FontWeights]::SemiBold
        }
        $tt = Format-RowTooltip "7d" $u.ResetIn
        $rowClaudeWk.ToolTip = $tt
        $barClaudeWk.ToolTip = $tt
    }

    # OpenRouter
    $orData = if ($data.openrouter) { $data.openrouter } else { $jsonObj.openrouter }
    if ($null -ne $orData -and $null -ne $txtOrPercent) {
        $limit = if ($null -ne $orData.limit) { [double]$orData.limit } else { 0.0 }
        $usage = if ($null -ne $orData.usage) { [double]$orData.usage } else { 0.0 }
        $rem = if ($null -ne $orData.limit_remaining) { [double]$orData.limit_remaining } else { [math]::Max(0.0, $limit - $usage) }
        $frac = if ($null -ne $orData.used_fraction) { [double]$orData.used_fraction } elseif ($limit -gt 0) { $usage / $limit } else { 0.0 }
        $pct = if ($null -ne $orData.used_percent) { [double]$orData.used_percent } else { [math]::Round($frac * 100, 1) }

        $resetIn = if ($orData.reset_in) { $orData.reset_in } elseif ($orData.limit_reset) { $orData.limit_reset } else { "--" }
        $txtOrLabel.Text = Format-ResetTimeLabel $resetIn
        $txtOrPercent.Text = "$pct%"
        $barOr.Value = [math]::Min(100.0, [math]::Max(0.0, $pct))
        $barOr.Foreground = Get-ProgressBrush $frac
        $txtOrPercent.Foreground = Get-PercentBrush $frac
        if ($frac -ge 1.0 -or $pct -ge 100.0) {
            $txtOrPercent.FontWeight = [System.Windows.FontWeights]::Bold
        } else {
            $txtOrPercent.FontWeight = [System.Windows.FontWeights]::SemiBold
        }

        $ci = [System.Globalization.CultureInfo]::InvariantCulture
        $usageFmt = $usage.ToString("0.00", $ci)
        $limitFmt = $limit.ToString("0.00", $ci)
        $remFmt = $rem.ToString("0.00", $ci)
        $resetFmt = if ($orData.limit_reset) { $orData.limit_reset } else { "monthly" }
        $orTooltip = "`$$usageFmt из `$$limitFmt • осталось `$$remFmt • сброс $resetFmt"
        $rowOrMonthly.ToolTip = $orTooltip
        $barOr.ToolTip = $orTooltip

        if ($null -ne $txtOrFreeDaily) {
            if ($null -ne $orData.free_daily_limit -and $orData.free_daily_limit -gt 0) {
                $fUsed = if ($null -ne $orData.free_daily_used) { $orData.free_daily_used } else { 0 }
                $fLim = $orData.free_daily_limit
                $txtOrFreeDaily.Text = "free: $fUsed/$fLim"
            } else {
                $txtOrFreeDaily.Text = ""
            }
        }
        if ($null -ne $txtOrBalance) {
            $txtOrBalance.Text = "ост. `$$remFmt"
        }
    }

    # Kimi
    $kimiData = if ($data.kimi) { $data.kimi } else { $jsonObj.kimi }
    if ($null -ne $kimiData) {
        # Kimi 5h
        if ($null -ne $kimiData.limit_5h -and $null -ne $txtKimi5hPercent) {
            $k5 = $kimiData.limit_5h
            $ratio = if ($null -ne $k5.used_ratio) { [double]$k5.used_ratio } else { 0.0 }
            $pct = if ($null -ne $k5.used_percent) { [double]$k5.used_percent } else { [math]::Round($ratio * 100, 1) }
            $resetIn = if ($k5.reset_in) { $k5.reset_in } else { "--" }
            $txtKimi5hLabel.Text = Format-ResetTimeLabel $resetIn
            $txtKimi5hPercent.Text = "$pct%"
            $barKimi5h.Value = [math]::Min(100.0, [math]::Max(0.0, $pct))
            $barKimi5h.Foreground = Get-ProgressBrush $ratio
            $txtKimi5hPercent.Foreground = Get-PercentBrush $ratio
            if ($ratio -ge 1.0 -or $pct -ge 100.0) {
                $txtKimi5hPercent.FontWeight = [System.Windows.FontWeights]::Bold
            } else {
                $txtKimi5hPercent.FontWeight = [System.Windows.FontWeights]::SemiBold
            }

            $ruReset = Format-ResetTimeLabel $resetIn
            $exhausted = if ($ratio -ge 1.0 -or $pct -ge 100.0) { " (исчерпан)" } else { "" }
            $ttK5 = "5-часовое окно • сброс через $ruReset$exhausted"
            $rowKimi5h.ToolTip = $ttK5
            $barKimi5h.ToolTip = $ttK5
        }

        # Kimi 7d (Weekly)
        if ($null -ne $kimiData.limit_7d -and $null -ne $txtKimiWkPercent) {
            $kWk = $kimiData.limit_7d
            $ratio = if ($null -ne $kWk.used_ratio) { [double]$kWk.used_ratio } else { 0.0 }
            $pct = if ($null -ne $kWk.used_percent) { [double]$kWk.used_percent } else { [math]::Round($ratio * 100, 1) }
            $resetIn = if ($kWk.reset_in) { $kWk.reset_in } else { "--" }
            $txtKimiWkLabel.Text = Format-ResetTimeLabel $resetIn
            $txtKimiWkPercent.Text = "$pct%"
            $barKimiWk.Value = [math]::Min(100.0, [math]::Max(0.0, $pct))
            $barKimiWk.Foreground = Get-ProgressBrush $ratio
            $txtKimiWkPercent.Foreground = Get-PercentBrush $ratio
            if ($ratio -ge 1.0 -or $pct -ge 100.0) {
                $txtKimiWkPercent.FontWeight = [System.Windows.FontWeights]::Bold
            } else {
                $txtKimiWkPercent.FontWeight = [System.Windows.FontWeights]::SemiBold
            }

            $ruReset = Format-ResetTimeLabel $resetIn
            $exhausted = if ($ratio -ge 1.0 -or $pct -ge 100.0) { " (исчерпан)" } else { "" }
            $ttKWk = "Недельное окно • сброс через $ruReset$exhausted"
            $rowKimiWk.ToolTip = $ttKWk
            $barKimiWk.ToolTip = $ttKWk
        }
    }

    # Re-anchor overlay position if dynamic content height changed
    try {
        $window.UpdateLayout()
        Update-OverlayPosition
    } catch {}
}

function Load-CacheFromDisk($statusOverride = $null) {
    $loaded = $false
    foreach ($path in @($LimitsJsonPath, $FallbackJsonPath)) {
        if (Test-Path $path) {
            try {
                $raw = Get-Content -Raw $path -ErrorAction SilentlyContinue
                if (-not [string]::IsNullOrWhiteSpace($raw)) {
                    $json = $raw | ConvertFrom-Json
                    if ($null -ne $statusOverride) {
                        $json.status = $statusOverride
                    }
                    Update-UIWithData $json
                    $loaded = $true
                    Write-OverlayLog "Cache loaded successfully from $path (status: $($json.status))."
                    break
                }
            } catch {
                Write-OverlayLog "Error loading cache from $($path): $_"
            }
        }
    }
    if (-not $loaded -and $null -ne $statusOverride) {
        $script:lastIsStale = $true
        Update-HeaderRelativeTime
        Write-OverlayLog "No cache file found; marked stale ($statusOverride)."
    }
}

# Pre-populate UI immediately from disk cache
Load-CacheFromDisk

# --- Asynchronous Non-Blocking Refresh with Per-Backend Watchdog ---
$script:isFetching = $false
$script:fetchProc = $null
$script:fetchWatch = $null
$script:currentTimeoutMs = 6000

$watchdogTimer = New-Object System.Windows.Threading.DispatcherTimer
$watchdogTimer.Interval = [TimeSpan]::FromMilliseconds(150)
$watchdogTimer.Add_Tick({
    if ($null -eq $script:fetchProc) {
        $watchdogTimer.Stop()
        $script:isFetching = $false
        if ($menuRefresh) { $menuRefresh.IsEnabled = $true }
        return
    }

    # Case 1: Process finished normally
    if ($script:fetchProc.HasExited) {
        $watchdogTimer.Stop()
        $exitCode = $script:fetchProc.ExitCode
        $procId = try { $script:fetchProc.Id } catch { 0 }
        try { $script:fetchProc.Dispose() } catch {}
        $script:fetchProc = $null
        $script:isFetching = $false
        if ($menuRefresh) { $menuRefresh.IsEnabled = $true }

        Write-OverlayLog "Async refresh process finished (PID=$procId, exitCode=$exitCode)."
        if ($exitCode -eq 0) {
            Load-CacheFromDisk
        } else {
            Load-CacheFromDisk "stale (exit $exitCode)"
        }
        return
    }

    # Case 2: Process exceeded timeout -> Kill & Stale Fallback
    if ($script:fetchWatch.ElapsedMilliseconds -gt $script:currentTimeoutMs) {
        $watchdogTimer.Stop()
        $procId = try { $script:fetchProc.Id } catch { 0 }
        try {
            $script:fetchProc.Kill()
            $script:fetchProc.Dispose()
        } catch {}
        $script:fetchProc = $null
        $script:isFetching = $false
        if ($menuRefresh) { $menuRefresh.IsEnabled = $true }

        Write-OverlayLog "Async refresh timed out after $($script:currentTimeoutMs)ms (PID=$procId). Process killed."
        Load-CacheFromDisk "stale (timeout)"
        return
    }
})

function Start-AsyncRefresh {
    if ($script:isFetching) {
        return
    }

    # 1. Resolve backend
    $backendInfo = Resolve-OverlayBackend
    $backendMode = $backendInfo.Backend
    Write-OverlayLog "Resolved backend: '$backendMode' (source: $($backendInfo.Source))"

    # 2. Determine execution target: native vs wsl
    $cfg = Get-OverlayConfig
    $cfgTimeoutSec = if ($cfg -and $cfg.timeoutSeconds -gt 0) { [double]$cfg.timeoutSeconds } else { 0.0 }

    $targetIsWsl = $false
    $pyCandidate = $null
    $wslDistro = $null

    if ($backendMode -eq "windows") {
        $pyCandidate = Resolve-NativePython
        if ($null -eq $pyCandidate) {
            Write-OverlayLog "Native Python interpreter unavailable for backend 'windows'."
            if ($txtUpdated) {
                $txtUpdated.Text = "нет python"
                $converter = New-Object System.Windows.Media.BrushConverter
                $staleColor = if ($Config.UpdatedStaleText) { $Config.UpdatedStaleText } else { "#475569" }
                $txtUpdated.Foreground = $converter.ConvertFromString($staleColor)
            }
            Load-CacheFromDisk "error (нет python)"
            return
        }
        $targetIsWsl = $false
    } elseif ($backendMode -eq "wsl") {
        $wslDistro = Resolve-WslDistro
        if ([string]::IsNullOrWhiteSpace($wslDistro)) {
            Write-OverlayLog "WSL distro with python3 unavailable for backend 'wsl'."
            if ($txtUpdated) {
                $txtUpdated.Text = "нет wsl"
                $converter = New-Object System.Windows.Media.BrushConverter
                $staleColor = if ($Config.UpdatedStaleText) { $Config.UpdatedStaleText } else { "#475569" }
                $txtUpdated.Foreground = $converter.ConvertFromString($staleColor)
            }
            Load-CacheFromDisk "error (нет wsl)"
            return
        }
        $targetIsWsl = $true
    } else {
        # auto: try native Python first, then WSL as fallback
        $pyCandidate = Resolve-NativePython
        if ($null -ne $pyCandidate) {
            $targetIsWsl = $false
        } else {
            Write-OverlayLog "Auto backend: native Python not found, probing WSL..."
            $wslDistro = Resolve-WslDistro
            if (-not [string]::IsNullOrWhiteSpace($wslDistro)) {
                $targetIsWsl = $true
            } else {
                Write-OverlayLog "Auto backend: neither native Python nor WSL available."
                if ($txtUpdated) {
                    $txtUpdated.Text = "нет python"
                    $converter = New-Object System.Windows.Media.BrushConverter
                    $staleColor = if ($Config.UpdatedStaleText) { $Config.UpdatedStaleText } else { "#475569" }
                    $txtUpdated.Foreground = $converter.ConvertFromString($staleColor)
                }
                Load-CacheFromDisk "error (нет python)"
                return
            }
        }
    }

    # Watchdog timeout: native 6s, wsl 30s, or config override
    if ($cfgTimeoutSec -gt 0) {
        $script:currentTimeoutMs = [int]($cfgTimeoutSec * 1000)
    } else {
        $script:currentTimeoutMs = if ($targetIsWsl) { 30000 } else { 6000 }
    }
    $timeoutSec = [math]::Round($script:currentTimeoutMs / 1000, 1)

    $script:isFetching = $true
    if ($menuRefresh) { $menuRefresh.IsEnabled = $false }
    if ($null -ne $txtUpdated) {
        $txtUpdated.Text = "обновление..."
        $converter = New-Object System.Windows.Media.BrushConverter
        $normalColor = if ($Config.MetaText) { $Config.MetaText } else { "#0F172A" }
        $txtUpdated.Foreground = $converter.ConvertFromString($normalColor)
    }

    try {
        $psi = New-Object System.Diagnostics.ProcessStartInfo
        $psi.UseShellExecute = $false
        $psi.CreateNoWindow = $true
        $psi.RedirectStandardOutput = $false
        $psi.RedirectStandardError = $false

        if ($targetIsWsl) {
            Write-OverlayLog "Starting async fetch via wsl.exe ($wslDistro) [timeout ${timeoutSec}s]..."
            $wslFetchScript = Resolve-WslFetchScript $wslDistro
            $wslLimitsArg = ""
            try {
                $p = ($LimitsJsonPath -replace '\\', '/')
                $w = (wsl.exe -d $wslDistro wslpath -u $p 2>$null)
                if ($LASTEXITCODE -eq 0 -and -not [string]::IsNullOrWhiteSpace($w)) {
                    $wslLimitsArg = $w.Trim()
                }
            } catch {}

            $psi.FileName = "wsl.exe"
            $psi.Arguments = if ($wslLimitsArg) {
                "-d $wslDistro python3 `"$wslFetchScript`" `"$wslLimitsArg`""
            } else {
                "-d $wslDistro python3 `"$wslFetchScript`""
            }
        } else {
            $pyScript = Join-Path $ScriptDir "fetch_quota.py"
            Write-OverlayLog "Starting async fetch via native Python ($($pyCandidate.Executable)) [timeout ${timeoutSec}s]..."
            $psi.FileName = $pyCandidate.Executable
            $psi.Arguments = "`"$pyScript`" `"$LimitsJsonPath`""
        }

        $script:fetchProc = [System.Diagnostics.Process]::Start($psi)
        $procId = $script:fetchProc.Id
        Write-OverlayLog "Spawned refresh process (PID=$procId, cmd=$($psi.FileName) $($psi.Arguments))"
        $script:fetchWatch = [System.Diagnostics.Stopwatch]::StartNew()
        $watchdogTimer.Start()
    } catch {
        $script:isFetching = $false
        if ($menuRefresh) { $menuRefresh.IsEnabled = $true }
        Write-OverlayLog "Exception starting refresh process: $_"
        $errReason = if ($targetIsWsl) { "WSL unavailable" } else { "Python unavailable" }
        if ($txtUpdated) {
            $txtUpdated.Text = if ($targetIsWsl) { "нет wsl" } else { "нет python" }
        }
        Load-CacheFromDisk "error ($errReason)"
    }
}

# 10s Clock DispatcherTimer to refresh relative time ("только что", "1 мин", etc.)
$clockTimer = New-Object System.Windows.Threading.DispatcherTimer
$clockTimer.Interval = [TimeSpan]::FromSeconds(10)
$clockTimer.Add_Tick({
    if (-not $script:isFetching) {
        Update-HeaderRelativeTime
    }
})
$clockTimer.Start()

# 60s Main DispatcherTimer
$mainTimer = New-Object System.Windows.Threading.DispatcherTimer
$mainTimer.Interval = [TimeSpan]::FromSeconds(60)
$mainTimer.Add_Tick({
    Start-AsyncRefresh
})
$mainTimer.Start()

$window.Add_Loaded({
    Load-CacheFromDisk
    Start-AsyncRefresh
})

# Check if automated test run requested
if ($args -contains "-ScreenshotAndExit") {
    $screen = [System.Windows.Forms.Screen]::PrimaryScreen
    $script:isUserPositioned = $true

    # 1. Capture 1: Overlay over realistic DARK background (VS Code / Terminal)
    $darkXaml = @"
<Window xmlns="http://schemas.microsoft.com/winfx/2006/xaml/presentation"
        Title="Dark Background"
        Width="440" Height="530"
        WindowStartupLocation="Manual"
        WindowStyle="None"
        Topmost="True"
        Background="#16161E">
    <Grid Margin="18">
        <StackPanel>
            <TextBlock Text="DARK BACKGROUND (Terminal / VS Code)" FontSize="12" FontWeight="Bold" Foreground="#7AA2F7" Margin="0,0,0,10"/>
            <TextBlock Text="const session = await agy.connect();" FontFamily="Consolas" FontSize="11" Foreground="#7DCFFF" Margin="0,0,0,3"/>
            <TextBlock Text="const quota = session.getQuotaSummary();" FontFamily="Consolas" FontSize="11" Foreground="#9ECE6A" Margin="0,0,0,3"/>
            <TextBlock Text="console.log('Gemini 5h:', quota.gemini.used);" FontFamily="Consolas" FontSize="11" Foreground="#BB9AF7" Margin="0,0,0,3"/>
            <TextBlock Text="console.log('Claude 5h:', quota.claude.used);" FontFamily="Consolas" FontSize="11" Foreground="#E0AF68" Margin="0,0,0,3"/>
            <TextBlock Text="console.log('OpenRouter:', quota.openrouter.limit_remaining);" FontFamily="Consolas" FontSize="11" Foreground="#FF9E64" Margin="0,0,0,3"/>
            <TextBlock Text="console.log('Kimi 5h/7d:', quota.kimi.status);" FontFamily="Consolas" FontSize="11" Foreground="#2AC3DE" Margin="0,0,0,10"/>
            <Border Background="#283457" Height="1" Margin="0,0,0,10"/>
            <TextBlock Text="Background code is clearly visible through ~49% translucent pods." FontSize="10" Foreground="#7982A9"/>
        </StackPanel>
    </Grid>
</Window>
"@
    $darkReader = (New-Object System.Xml.XmlNodeReader ([xml]$darkXaml))
    $darkWindow = [System.Windows.Markup.XamlReader]::Load($darkReader)
    $darkWindow.Left = $screen.WorkingArea.Right - 470
    $darkWindow.Top = [math]::Max(10, ($screen.WorkingArea.Top + 20))
    $darkWindow.Show()

    $window.Left = $darkWindow.Left + 75
    $window.Top = $darkWindow.Top + 25
    $window.Show()
    $window.Activate()

    # Wait for initial load and background fetch to complete
    $sw = [System.Diagnostics.Stopwatch]::StartNew()
    while ($sw.ElapsedMilliseconds -lt 2500) {
        [System.Windows.Forms.Application]::DoEvents()
        try { [System.Windows.Threading.Dispatcher]::CurrentDispatcher.Invoke([Action]{}, [System.Windows.Threading.DispatcherPriority]::Background) } catch {}
        Start-Sleep -Milliseconds 50
        if (-not $script:isFetching -and $sw.ElapsedMilliseconds -gt 700) { break }
    }
    [System.Windows.Forms.Application]::DoEvents()
    try { [System.Windows.Threading.Dispatcher]::CurrentDispatcher.Invoke([Action]{}, [System.Windows.Threading.DispatcherPriority]::Background) } catch {}
    Start-Sleep -Milliseconds 250

    # Capture dark background view
    $capDarkX = [int]($darkWindow.Left + 10)
    $capDarkY = [int]($darkWindow.Top + 10)
    $capDarkW = [int]($darkWindow.Width - 20)
    $capDarkH = [int]($darkWindow.Height - 20)
    $rectDark = New-Object System.Drawing.Rectangle($capDarkX, $capDarkY, $capDarkW, $capDarkH)
    $bmpDark = New-Object System.Drawing.Bitmap($rectDark.Width, $rectDark.Height)
    $gDark = [System.Drawing.Graphics]::FromImage($bmpDark)
    $gDark.CopyFromScreen($rectDark.Location, [System.Drawing.Point]::Empty, $rectDark.Size)
    $shotsDir = Join-Path $ScriptDir "docs\screenshots"
    if (-not (Test-Path $shotsDir)) { New-Item -ItemType Directory -Path $shotsDir -Force | Out-Null }
    $darkShotPath = Join-Path $shotsDir "dark.png"
    $bmpDark.Save($darkShotPath, [System.Drawing.Imaging.ImageFormat]::Png)
    Write-Output "Dark background screenshot saved to: $darkShotPath"

    # 2. Capture 2: Overlay over realistic LIGHT background (Browser / Word / Docs)
    $darkWindow.Close()

    $lightXaml = @"
<Window xmlns="http://schemas.microsoft.com/winfx/2006/xaml/presentation"
        Title="Light Background"
        Width="440" Height="530"
        WindowStartupLocation="Manual"
        WindowStyle="None"
        Topmost="True"
        Background="#FFFFFF">
    <Grid Margin="18">
        <StackPanel>
            <TextBlock Text="LIGHT BACKGROUND (Browser / Word / Docs)" FontSize="12" FontWeight="Bold" Foreground="#1F2328" Margin="0,0,0,10"/>
            <TextBlock Text="const session = await agy.connect();" FontFamily="Consolas" FontSize="11" Foreground="#0969DA" Margin="0,0,0,3"/>
            <TextBlock Text="const quota = session.getQuotaSummary();" FontFamily="Consolas" FontSize="11" Foreground="#24292F" Margin="0,0,0,3"/>
            <TextBlock Text="console.log('Gemini 5h:', quota.gemini.used);" FontFamily="Consolas" FontSize="11" Foreground="#CF222E" Margin="0,0,0,3"/>
            <TextBlock Text="console.log('Claude 5h:', quota.claude.used);" FontFamily="Consolas" FontSize="11" Foreground="#8250DF" Margin="0,0,0,3"/>
            <TextBlock Text="console.log('OpenRouter:', quota.openrouter.remaining);" FontFamily="Consolas" FontSize="11" Foreground="#BC4C00" Margin="0,0,0,3"/>
            <TextBlock Text="console.log('Kimi 5h/7d:', quota.kimi.status);" FontFamily="Consolas" FontSize="11" Foreground="#1A7F37" Margin="0,0,0,10"/>
            <Border Background="#D0D7DE" Height="1" Margin="0,0,0,10"/>
            <TextBlock Text="Underlying light documents are visible through ~49% translucent pods." FontSize="10" Foreground="#57606A"/>
        </StackPanel>
    </Grid>
</Window>
"@
    $lightReader = (New-Object System.Xml.XmlNodeReader ([xml]$lightXaml))
    $lightWindow = [System.Windows.Markup.XamlReader]::Load($lightReader)
    $lightWindow.Left = $screen.WorkingArea.Right - 470
    $lightWindow.Top = [math]::Max(10, ($screen.WorkingArea.Top + 20))
    $lightWindow.Show()

    $window.Left = $lightWindow.Left + 75
    $window.Top = $lightWindow.Top + 25
    $window.Show()
    $window.Activate()

    [System.Windows.Forms.Application]::DoEvents()
    try { [System.Windows.Threading.Dispatcher]::CurrentDispatcher.Invoke([Action]{}, [System.Windows.Threading.DispatcherPriority]::Background) } catch {}
    Start-Sleep -Milliseconds 350

    $capLightX = [int]($lightWindow.Left + 10)
    $capLightY = [int]($lightWindow.Top + 10)
    $capLightW = [int]($lightWindow.Width - 20)
    $capLightH = [int]($lightWindow.Height - 20)
    $rectLight = New-Object System.Drawing.Rectangle($capLightX, $capLightY, $capLightW, $capLightH)
    $bmpLight = New-Object System.Drawing.Bitmap($rectLight.Width, $rectLight.Height)
    $gLight = [System.Drawing.Graphics]::FromImage($bmpLight)
    $gLight.CopyFromScreen($rectLight.Location, [System.Drawing.Point]::Empty, $rectLight.Size)
    $lightShotPath = Join-Path $shotsDir "light.png"
    $bmpLight.Save($lightShotPath, [System.Drawing.Imaging.ImageFormat]::Png)
    Write-Output "Light background screenshot saved to: $lightShotPath"

    $gDark.Dispose()
    $bmpDark.Dispose()
    $gLight.Dispose()
    $bmpLight.Dispose()
    $lightWindow.Close()

    $window.Close()
    try { [System.Windows.Threading.Dispatcher]::CurrentDispatcher.InvokeShutdown() } catch {}
    Stop-Process -Id $PID -Force
}

[void]$window.ShowDialog()
