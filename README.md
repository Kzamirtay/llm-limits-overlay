# LLM Limits Overlay

**English** | [Русский](README.ru.md)

**LLM Limits Overlay** is a compact Always-on-Top HUD overlay for Windows 10/11 featuring lightweight translucency (~49%), monochrome consumption gauges, and sharp ClearType typography. It provides real-time visibility into usage quotas and reset limits for popular AI models: **Gemini**, **Claude / GPT**, **Kimi**, and **OpenRouter** in a single minimalist floating window.

---

## Screenshots

| Dark Background (IDE / Terminal) | Light Background (Browser / Documents) |
| :---: | :---: |
| ![Dark Background](docs/screenshots/dark.png) | ![Light Background](docs/screenshots/light.png) |

*(Background text and underlying windows show through the matte translucent cards, while percentages and timers remain sharp and high-contrast)*

---

## Features

- **Always-on-Top HUD**: Floats seamlessly above all windows in the bottom-right corner of the screen above the taskbar.
- **Dynamic Height Auto-Sizing (`SizeToContent="Height"`)**: Automatically conforms to content height so card shadows and rounded bottom corners are never clipped.
- **Zero Desktop Clutter**:
  - `ShowInTaskbar="False"`: Does not take up space on the Windows taskbar.
  - `WS_EX_TOOLWINDOW`: Excluded from the `Alt-Tab` application switcher.
  - Native Win32 GUI subsystem (`/target:winexe`): Launches silently without flashing CMD/PowerShell console windows or helper launchers.
- **Monochrome Visual Indicators**: A unified graphite scale that gradually darkens as quota is consumed (from neutral gray to deep black), avoiding distracting traffic-light colors.
- **Free Dragging & System Tray Control**:
  - Left-click and drag: Move the HUD to any preferred position on the screen.
  - Right-click: Context menu for manual quota refresh, runtime environment switching, and exiting.
  - Windows System Tray icon: Single-click show/hide, comprehensive control menu, position reset, and tooltip with live percentages.
- **Fast Native Polling**:
  - Operates natively on Windows using local Python and the Antigravity CLI (`agy -p /usage --output-format json`).
  - Watchdog timer: 6 seconds for native Windows Python, 30 seconds for WSL (configurable).
  - Quota caching (300s) minimizes CLI overhead while keeping external APIs (OpenRouter, Kimi) fresh on each 60-second polling cycle.

---

## Quota Metrics Displayed

The overlay presents information in three logical vertical cards from top to bottom:

1. **KIMI** (Top card):
   - **Weekly Limit (7d)**: Time until quota reset (e.g., `3d 8h`), percentage consumed (`100%`), and visual progress gauge.
   - **5-Hour Limit (5h)**: Time until reset (`2h 41m`), percentage consumed (`8.9%`), and progress bar.
   - **Timestamp**: Subtle relative update time (`just now`, `4m ago`, `stale`) in the bottom-right corner, refreshed every 10 seconds.
   - Source: Official endpoint `GET https://api.kimi.com/coding/v1/usages`.

2. **OPENROUTER** (Middle card):
   - **Key / Account Limit**: Time until calculated reset (`2d 13h`), percentage consumed (`3.7%`), and progress gauge.
   - **Secondary Status Line**: Free tier daily requests used (`free: 0/1000`) and remaining credit balance (`rem. $9.63` / `ост. $9.63`).
   - Source: Official endpoint `GET https://openrouter.ai/api/v1/key`.

3. **GEMINI + CLAUDE / GPT** (Bottom combined card):
   - Grouped into a single card separated by a subtle divider:
     - **GEMINI**: Weekly rolling window (7d, top) and 5-hour rolling window (5h, bottom).
     - **CLAUDE / GPT**: Weekly rolling window (7d, top) and 5-hour rolling window (5h, bottom).
   - Source: Official Antigravity CLI (`agy -p /usage --output-format json`) or local Language Server fallback when running under WSL.

---

## System Requirements

- **OS**: Windows 10 or Windows 11 (64-bit) with .NET Framework 4.8 (included out of the box in Windows).
- **Python**: Python 3.10+ on Windows (default). WSL is not required for standard usage.
- **WSL 2** *(optional)*: Only required if explicitly choosing the `wsl` backend or when native Windows Python is unavailable in `auto` mode.

---

## Backend Environments

The overlay supports three execution modes for the background quota worker script (`fetch_quota.py`):

1. **`auto`** *(default)*:
   - Probes and uses the native Windows Python interpreter first.
   - If native Python is not found, automatically falls back to WSL.
2. **`windows`**:
   - Runs exclusively via native Python on Windows. Calls to `wsl.exe` are completely bypassed.
   - Displays `no python` in the status line if Python is not installed.
3. **`wsl`**:
   - Executes inside WSL. Automatically discovers an installed Linux distribution with `python3` (skipping system distros like `docker-desktop`).
   - Displays `no wsl` in the status line if no compatible distribution is found.

### Switching Backends via Interface
Right-click anywhere on the overlay window cards:
- **Refresh** — Force an immediate data refresh.
- **Environment**:
  - `[x] Auto`
  - `[ ] Windows (Native Python)`
  - `[ ] WSL`
- **Close** — Exit the application.

Changing the backend immediately updates `overlay.config.json`, clears stale flags, and triggers a fresh background fetch.

---

## System Tray Icon

The application includes an icon in the Windows Notification Area (System Tray), providing complete control over the HUD even when the window is hidden or covered by full-screen applications.

### Tray Icon Interactions
- **Single or Double Left-Click**: Instantly toggles HUD visibility (window coordinates are fully preserved across hide/show cycles).
- **Tooltip**: Hovering over the icon displays a compact one-line status summary (e.g., `Kimi 7d 100% · Gemini 47% · Claude 50% [click-through]`), updated on every polling tick.

### Tray Context Menu (Right-Click)
- **Show Overlay / Hide Overlay** — Toggles window visibility while maintaining coordinates.
- *Separator*
- **Refresh Quotas** — Forces an immediate API query (disabled while a query is in progress).
- **Environment** — Interpreter selection submenu (`Auto`, `Windows (Native Python)`, `WSL`), synchronized with the window context menu.
- **Click-Through** — Toggles mouse click-through transparency mode.
- *Separator*
- **Reset Position** — Restores the HUD to its default docking spot in the bottom-right corner above the taskbar.
- **Exit** — Safely unregisters global hotkeys, cleans up the tray icon, closes the window, and terminates the background process.

> [!NOTE]
> **Windows 11**: By default, Windows 11 places new tray icons inside the overflow menu (the `^` arrow button). To keep the overlay icon always visible:
> Navigate to **Settings → Personalization → Taskbar → Other system tray icons** and enable the toggle for the overlay, or simply drag the icon from the `^` flyout directly onto the primary taskbar.

---

## Click-Through Mode

The overlay features a dedicated **click-through mode**, which is **enabled by default**.

In this mode:
- The window is completely transparent to mouse events at the Win32 subsystem level (`WS_EX_TRANSPARENT`).
- All mouse actions — left and right clicks, double-clicks, scroll wheel movements, and hovering — pass directly through to windows and UI elements positioned underneath the overlay.
- The overlay window **cannot be dragged with the mouse**, and **right-clicking it does not open the context menu**.
- The HUD remains visually pinned on top of all windows without impeding desktop interaction. The default position sits above the taskbar in the bottom-right corner, and can be repositioned via the tray menu's **Reset Position**.

### How to Toggle or Disable Click-Through
When interactive manipulation is needed (to reposition the window or open its context menu), click-through can be temporarily or permanently disabled:
1. **Global Keyboard Shortcut**: Press **`Ctrl+Alt+L`** (works globally from any application). The first keypress disables click-through and restores mouse interactivity; pressing it again re-enables click-through.
2. **System Tray Context Menu**: Right-click the system tray icon → select **Click-Through** (uncheck).
3. **Configuration File**: Set `"clickThrough": false` in `overlay.config.json` to start in standard interactive mode.

Each state change displays an informative balloon tip in the system tray, and the tooltip reflects the active mode with a `[click-through]` tag. The preference is automatically saved to `overlay.config.json` and restored on subsequent launches.

> [!WARNING]
> **Click-Through Mode Limitation**: While click-through is enabled, mouse clicks directly on the overlay window are ignored by design.
> **The system tray icon and the global hotkey (`Ctrl+Alt+L`) are the only ways to restore interactive mouse control.**

---

## Configuration

### File: `overlay.config.json`

The configuration file is automatically created next to the executable if not present:

```json
{
  "backend": "windows",
  "pythonPath": "",
  "wslDistro": "",
  "timeoutSeconds": 0,
  "testHooks": false,
  "clickThrough": true,
  "clickThroughHotkey": "Ctrl+Alt+L"
}
```

- `backend`: Execution mode (`auto`, `windows`, `wsl`).
- `pythonPath`: Explicit path to a Python executable (useful for dedicated virtual environments).
- `wslDistro`: Explicit WSL distribution name (if omitted, auto-discovered).
- `timeoutSeconds`: Watchdog timeout override in seconds (0 uses defaults: 6s for Windows, 30s for WSL).
- `testHooks`: Enables programmatic control via `.tray-action` (default `false`).
- `clickThrough`: Startup click-through state (default `true`). Set to `false` for standard interactive mode.
- `clickThroughHotkey`: Global shortcut for toggling click-through (default `"Ctrl+Alt+L"`).

### Environment Variables

Settings priority: **Environment Variable → `overlay.config.json` → Built-in Default**.

- `LLM_LIMITS_BACKEND`: Backend mode (`auto`, `windows`, `wsl`).
- `LLM_LIMITS_PYTHON`: Explicit path to the Windows Python binary.
- `LLM_LIMITS_AGY_BIN`: Explicit path to `agy.exe`.
- `LLM_LIMITS_QUOTA_CACHE_SECONDS`: Cache duration for Antigravity quota calls (default `300`).
- `LLM_LIMITS_WSL_DISTRO` / `AGY_WSL_DISTRO`: Target WSL distribution (e.g., `Ubuntu-24.04`).
- `LLM_LIMITS_WSL_SCRIPT` / `AGY_WSL_SCRIPT`: Target script path inside WSL.
- `LLM_LIMITS_JSON` / `AGY_LIMITS_JSON`: Output path for the cached quota JSON file.

---

## API Key Setup

To display OpenRouter and Kimi quota metrics, the overlay requires valid API keys.

### Single Source of Truth
API keys are loaded **exclusively from one location** — the local `.env` file located in the root repository folder next to `fetch_quota.py` (`<repo>\.env`).

> [!IMPORTANT]
> **Operating system environment variables (`os.environ`), `HERMES_ENV_PATH`, `~/.hermes/.env`, the Windows Registry, and WSL home directories are intentionally NOT read**, eliminating ambiguous behavior and accidental key leakage.

### Example `.env` File
A template is provided in the repository as [.env.example](.env.example):

```env
# LLM Limits Overlay - API Key Configuration
# Keys are read EXCLUSIVELY from the .env file in the root directory (<repo>\.env).
# OS environment variables (os.environ), HERMES_ENV_PATH, and ~/.hermes/.env are intentionally ignored.

OPENROUTER_API_KEY=sk-or-v1-xxxxxxxxxxxxxxxxxxxx
KIMI_API_KEY=sk-xxxxxxxxxxxxxxxxxxxxxxxxxxxx
```

The built-in parser (pure Python standard library, zero third-party dependencies) supports:
- UTF-8 with or without BOM;
- Both CRLF (Windows) and LF (Linux) line endings;
- Blank lines and comments starting with `#`;
- Optional `export ` prefix;
- Whitespace around `=` (`KEY = value`);
- Single and double quotes around values (`'...'` / `"..."`);
- Trailing inline comments after unquoted values (`KEY=value # comment`);
- Safe skipping of malformed lines without throwing exceptions.

*(Keys are never printed to terminal/stdout/stderr, never displayed in the UI, never written to `overlay.log`, and never stored in `limits.json` cache files)*.

---

## Installation & Running

The primary and only method for running the overlay is the compiled executable **`LlmLimitsOverlay.exe`**.

> [!NOTE]
> **The PowerShell version has been removed from master; it remains accessible in tag and release [v1.0.0](https://github.com/Kzamirtay/llm-limits-overlay/releases/tag/v1.0.0).** Starting with version v1.1.0, the project runs exclusively as a standalone native C# application (.NET Framework 4.8 / WPF).

### Runtime Directory Requirements
The following files must be located in the same directory as `LlmLimitsOverlay.exe`:
- `fetch_quota.py` — Python quota fetcher script (Antigravity CLI / OpenRouter / Kimi);
- `assets/app.ico` — Application icon for the HUD and system tray;
- `overlay.config.json` — Configuration file (automatically generated if missing);
- `.env` — API key configuration file (`OPENROUTER_API_KEY`, `KIMI_API_KEY`);
- Python 3 (3.10+) installed on Windows for native quota retrieval.

### Launch Options

#### 1. Running the Prebuilt Binary from Releases
1. Download `LlmLimitsOverlay-v1.1.1-win64.zip` from [Releases](https://github.com/Kzamirtay/llm-limits-overlay/releases).
2. Extract the archive into any preferred directory.
3. Create your `.env` file with your API keys (copy from `.env.example`).
4. Launch `LlmLimitsOverlay.exe` by double-clicking it in Windows Explorer or from the command line:
   ```cmd
   LlmLimitsOverlay.exe
   ```
   (or `.\dotnet\bin\LlmLimitsOverlay.exe` during local development from the repository root).
   The application starts cleanly as a Windows GUI subsystem process (`/target:winexe`) without console flashes.

#### 2. Building from Source
If you cloned the repository and wish to compile the binary locally:
```cmd
dotnet\build.cmd
```
The build script automatically locates the system compiler `csc.exe` (`C:\Windows\Microsoft.NET\Framework64\v4.0.30319\csc.exe`), requires no Visual Studio, MSBuild, or .NET SDK installation, and produces an optimized standalone executable in:
```
dotnet\bin\LlmLimitsOverlay.exe
```
Technical architecture details are documented in [dotnet/README.md](dotnet/README.md).

---

## Diagnostics & Logs

Startup sequences, interpreter discovery, process IDs, query results, and errors are logged to `overlay.log` in the application directory:

```
[2026-10-25 00:11:18] === LLM Limits Overlay Starting (PID=21240) ===
[2026-10-25 00:11:19] Resolved backend: 'auto' (source: default)
[2026-10-25 00:11:20] Starting async fetch via native Python (C:\...\python.exe) [timeout 6s]...
[2026-10-25 00:11:20] Spawned refresh process (PID=28780, cmd=python.exe "...\fetch_quota.py" "...\limits.json")
[2026-10-25 00:11:20] Async refresh process finished (PID=28780, exitCode=0).
[2026-10-25 00:11:21] Cache loaded successfully from ...\limits.json (status: online).
```

---

## Repository File Structure

```
llm-limits-overlay/
├── assets/
│   └── app.ico                  # Application icon (multi-resolution Win32 icon)
├── docs/
│   └── screenshots/
│       ├── cs-version.png       # Native C# version screenshot
│       ├── dark.png             # Overlay on dark background
│       ├── light.png            # Overlay on light background
│       └── ps-version.png       # Legacy PowerShell version screenshot (v1.0.0)
├── dotnet/                      # C# source code (.NET Framework 4.8 / WPF)
│   ├── LlmLimitsOverlay/        # C# source files (.cs)
│   ├── build.cmd                # Standalone build script via csc.exe (no external SDK required)
│   └── README.md                # Architecture documentation
├── fetch_quota.py               # Quota fetcher script (Antigravity CLI / OpenRouter / Kimi)
├── LlmLimitsOverlay.exe         # Compiled native overlay executable
├── overlay.config.json          # Configuration file (auto-generated if missing)
├── .env.example                 # Template for API keys (OpenRouter, Kimi)
├── .gitignore                   # Git ignore patterns for caches, logs, and local state
├── LICENSE                      # MIT License
├── README.md                    # Project documentation (English)
└── README.ru.md                 # Project documentation (Russian)
```

---

## License

This project is licensed under the open-source [MIT License](LICENSE).
Copyright (c) 2026 Kzamirtay.
