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
  - Right-click: Context menu for manual quota refresh and exiting.
  - Windows System Tray icon: Single-click show/hide, comprehensive control menu, position reset, and tooltip with live percentages.
- **Fast Native Polling (v1.2.0+, no Python / WSL required)**:
  - All quota fetches run **in-process** as concurrent async tasks — no subprocess spawning, no `wsl.exe`, no Python.
  - Per-provider timeouts: HTTP 3.5 s for OpenRouter / Kimi; 8 s for `agy.exe`.
  - Antigravity quota caching (300 s in `%TEMP%\llm_limits_agy_usage_cache.json`) keeps the `agy.exe` call rate minimal while OpenRouter and Kimi are refreshed on every 60-second polling cycle.

---

## Quota Metrics Displayed

The overlay presents information in three logical vertical cards from top to bottom:

1. **KIMI** (Top card):
   - **Weekly Limit (7d)**: Time until quota reset (e.g., `3d 8h`), percentage consumed (`100%`), and visual progress gauge.
   - **5-Hour Limit (5h)**: Time until reset (`2h 41m`), percentage consumed (`8.9%`), and progress bar.
   - **Timestamp**: Subtle relative update time (`just now`, `4m ago`, `stale`) in the bottom-right corner, refreshed every 10 seconds.
   - Source: `GET https://api.kimi.com/coding/v1/usages` (HTTP Bearer auth).

2. **OPENROUTER** (Middle card):
   - **Key / Account Limit**: Time until calculated reset (`2d 13h`), percentage consumed (`3.7%`), and progress gauge.
   - **Secondary Status Line**: Free tier daily requests used (`free: 0/1000`) and remaining credit balance (`rem. $9.63`).
   - Source: `GET https://openrouter.ai/api/v1/key` (HTTP Bearer auth).

3. **GEMINI + CLAUDE / GPT** (Bottom combined card):
   - Grouped into a single card separated by a subtle divider:
     - **GEMINI**: Weekly rolling window (7d, top) and 5-hour rolling window (5h, bottom).
     - **CLAUDE / GPT**: Weekly rolling window (7d, top) and 5-hour rolling window (5h, bottom).
   - Source: Native `agy.exe -p /usage --output-format json` (Antigravity CLI v1.4.9+).

---

## System Requirements

- **OS**: Windows 10 or Windows 11 (64-bit) with .NET Framework 4.8 (included out of the box in Windows).
- **Antigravity CLI**: `agy.exe` (v1.4.9+) installed and signed in (`agy auth login`). Resolved automatically from `PATH` or `%LOCALAPPDATA%\agy\bin\agy.exe`.
- **No Python. No WSL.** Starting from v1.2.0 the overlay is fully self-contained.

---

## Architecture (v1.2.0+)

### In-Process Fetch

Since v1.2.0, quota fetching runs **entirely inside `LlmLimitsOverlay.exe`** with no external processes or scripts:

| Provider | Transport | Timeout | Auth |
|---|---|---|---|
| OpenRouter | `HttpWebRequest GET https://openrouter.ai/api/v1/key` | 3.5 s | Bearer `OPENROUTER_API_KEY` from `.env` |
| Kimi | `HttpWebRequest GET https://api.kimi.com/coding/v1/usages` | 3.5 s | Bearer `KIMI_API_KEY` from `.env` |
| AGY (Gemini/Claude) | `Process.Start(agy.exe -p /usage --output-format json)` | 8 s | OAuth token pre-checked at `%USERPROFILE%\.gemini\antigravity-cli\antigravity-oauth-token` |

All three providers run in parallel via `Task.Run`. Each atomically writes its fragment (`limits.agy.json`, `limits.openrouter.json`, `limits.kimi.json`) as soon as it finishes, then a merge step assembles the final `limits.json`. If one provider fails or times out, its block is marked `stale` while the others remain `online`.

### OAuth Anti-Popup Guard

Before spawning `agy.exe`, the overlay checks that `%USERPROFILE%\.gemini\antigravity-cli\antigravity-oauth-token` exists and is non-empty. If the token is missing, `agy.exe` is skipped entirely (returning `stale`) — preventing an unwanted browser OAuth popup.

### AGY Cache

The raw `agy.exe` JSON response is cached for 300 seconds in `%TEMP%\llm_limits_agy_usage_cache.json`. Within the TTL, `agy.exe` is never re-spawned; the cache is shared across process restarts.

### Error Isolation

If `agy.exe` hangs or is not found → only the Gemini/Claude card shows `stale`; OpenRouter and Kimi continue updating normally. The overall status in `limits.json` stays `online` as long as at least one provider is `online`.

### `limits.json` Format

The format is unchanged from v1.1.x. The root object:

```json
{
  "status": "online",
  "last_updated": "2026-10-28 15:30:00",
  "data": {
    "gemini_5h":     { "used_fraction": 0.12, "used_percent": 12.0, "reset_in": "3h 22m", ... },
    "gemini_weekly": { ... },
    "claude_5h":     { ... },
    "claude_weekly": { ... },
    "openrouter": { "status": "online", "limit": 20.0, "usage": 0.74, "limit_remaining": 19.26, ... },
    "kimi": {
      "status": "online",
      "limit_5h": { "used_ratio": 0.089, "used_percent": 8.9, "reset_in": "2h 41m", ... },
      "limit_7d": { ... }
    }
  }
}
```

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
  "backend": "native",
  "timeoutSeconds": 0,
  "testHooks": false,
  "clickThrough": true,
  "clickThroughHotkey": "Ctrl+Alt+L"
}
```

- `backend`: Always `"native"` since v1.2.0 (Python/WSL removed). Legacy values `auto`/`windows`/`wsl` are silently normalized.
- `timeoutSeconds`: Overall fetch budget in seconds (0 = default 10 s).
- `testHooks`: Enables programmatic control via `.tray-action` file (default `false`).
- `clickThrough`: Startup click-through state (default `true`). Set to `false` for standard interactive mode.
- `clickThroughHotkey`: Global shortcut for toggling click-through (default `"Ctrl+Alt+L"`).

### Environment Variables

- `LLM_LIMITS_AGY_BIN`: Explicit path to `agy.exe`.
- `LLM_LIMITS_QUOTA_CACHE_SECONDS`: Cache duration for Antigravity quota calls (default `300`).
- `LLM_LIMITS_JSON` / `AGY_LIMITS_JSON`: Output path for the cached quota JSON file.

---

## API Key Setup

To display OpenRouter and Kimi quota metrics, the overlay requires valid API keys.

### Key Loading Order
API keys are read from the `.env` file located **next to `LlmLimitsOverlay.exe`** (highest priority), with fallback to process environment variables.

> [!IMPORTANT]
> Keys are **never** written to `overlay.log`, `limits.json`, or any cache file.

### Example `.env` File
A template is provided in the repository as [.env.example](.env.example):

```env
# LLM Limits Overlay - API Key Configuration
OPENROUTER_API_KEY=sk-or-v1-xxxxxxxxxxxxxxxxxxxx
KIMI_API_KEY=sk-xxxxxxxxxxxxxxxxxxxxxxxxxxxx
```

The built-in C# parser supports:
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
> **The PowerShell version has been removed from master; it remains accessible in tag and release [v1.0.0](https://github.com/Kzamirtay/llm-limits-overlay/releases/tag/v1.0.0).** Starting with version v1.1.0, the project runs exclusively as a standalone native C# application (.NET Framework 4.8 / WPF). Since v1.2.0, Python and WSL are no longer required.

### Runtime Directory Requirements
The following files must be located in the same directory as `LlmLimitsOverlay.exe`:
- `assets/app.ico` — Application icon for the HUD and system tray;
- `overlay.config.json` — Configuration file (automatically generated if missing);
- `.env` — API key configuration file (`OPENROUTER_API_KEY`, `KIMI_API_KEY`).

### Launch Options

#### 1. Running the Prebuilt Binary from Releases
1. Download the latest `LlmLimitsOverlay-vX.Y.Z-win64.zip` from [Releases](https://github.com/Kzamirtay/llm-limits-overlay/releases).
2. Extract the archive into any preferred directory.
3. Create your `.env` file with your API keys (copy from `.env.example`).
4. Ensure `agy.exe` is installed and you are signed in (`agy auth login`).
5. Launch `LlmLimitsOverlay.exe` by double-clicking it in Windows Explorer or from the command line:
   ```cmd
   LlmLimitsOverlay.exe
   ```
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

Startup sequences, fetch results, cache hits, and errors are logged to `overlay.log` in the application directory:

```
[2026-10-28 15:30:00] === LLM Limits Overlay Starting (PID=12345) ===
[2026-10-28 15:30:00] Cache loaded successfully from ...\limits.json (status: online).
[2026-10-28 15:30:00] Starting async native refresh at 2026-10-28 15:30:00...
[2026-10-28 15:30:01] [cache] agy /usage cache hit (age 45.3s <= 300s), agy.exe not respawned
[2026-10-28 15:30:01] Native refresh finished (agy: online, openrouter: online, kimi: online -> root: online).
```

No `Spawned refresh process` or `exitCode` lines appear — fetching is in-process.

---

## Repository File Structure

```
llm-limits-overlay/
├── assets/
│   └── app.ico                  # Application icon (multi-resolution Win32 icon)
├── docs/
│   └── screenshots/
│       ├── dark.png             # Overlay on dark background
│       └── light.png            # Overlay on light background
├── dotnet/                      # C# source code (.NET Framework 4.8 / WPF)
│   ├── LlmLimitsOverlay/        # C# source files (.cs)
│   ├── build.cmd                # Standalone build script via csc.exe (no external SDK required)
│   └── README.md                # Architecture documentation
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
