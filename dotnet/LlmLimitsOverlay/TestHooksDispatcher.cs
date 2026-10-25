using System;
using System.IO;
using System.Text.RegularExpressions;
using System.Windows.Threading;

namespace LlmLimitsOverlay {
    public class TestHooksDispatcher {
        private readonly OverlayWindow _overlayWindow;
        private readonly string _actionFilePath;
        private readonly DateTime _processStartTime;
        private DispatcherTimer _timer;

        public TestHooksDispatcher(OverlayWindow overlayWindow, string appDirectory, DateTime processStartTime) {
            _overlayWindow = overlayWindow;
            _actionFilePath = Path.Combine(appDirectory, ".tray-action");
            _processStartTime = processStartTime;

            // Clean up any stale file from a prior crash or run
            if (File.Exists(_actionFilePath)) {
                try { File.Delete(_actionFilePath); } catch { }
            }

            _timer = new DispatcherTimer {
                Interval = TimeSpan.FromMilliseconds(200)
            };
            _timer.Tick += OnTick;
            _timer.Start();

            Logger.Log("Test hooks enabled via overlay.config.json (testHooks=true).");
        }

        private void OnTick(object sender, EventArgs e) {
            if (!File.Exists(_actionFilePath)) return;

            try {
                var fi = new FileInfo(_actionFilePath);
                if (fi.LastWriteTimeUtc <= _processStartTime) {
                    try { File.Delete(_actionFilePath); } catch { }
                    Logger.Log(string.Format("Ignored stale .tray-action command file (mtime {0} <= process start {1}).", fi.LastWriteTimeUtc, _processStartTime));
                    return;
                }

                string cmd = "";
                try {
                    cmd = File.ReadAllText(_actionFilePath).Trim();
                    File.Delete(_actionFilePath);
                } catch {
                    return;
                }

                if (string.IsNullOrEmpty(cmd)) return;

                double procAgeSec = (DateTime.UtcNow - _processStartTime).TotalSeconds;
                if (procAgeSec < 5.0 && Regex.IsMatch(cmd, @"^(exit|toggle|toggle-visibility|hide)$", RegexOptions.IgnoreCase)) {
                    Logger.Log(string.Format("Ignored '{0}' from .tray-action during startup grace period ({1:F1}s < 5.0s).", cmd, procAgeSec));
                    return;
                }

                if (Regex.IsMatch(cmd, @"^(toggle|toggle-visibility)$", RegexOptions.IgnoreCase)) {
                    _overlayWindow.ToggleVisibility();
                } else if (Regex.IsMatch(cmd, @"^show$", RegexOptions.IgnoreCase)) {
                    if (_overlayWindow.Visibility != System.Windows.Visibility.Visible) {
                        _overlayWindow.ToggleVisibility();
                    }
                } else if (Regex.IsMatch(cmd, @"^hide$", RegexOptions.IgnoreCase)) {
                    if (_overlayWindow.Visibility == System.Windows.Visibility.Visible) {
                        _overlayWindow.ToggleVisibility();
                    }
                } else if (Regex.IsMatch(cmd, @"^(hotkey|wm-hotkey|trigger-hotkey)$", RegexOptions.IgnoreCase)) {
                    IntPtr h = new System.Windows.Interop.WindowInteropHelper(_overlayWindow).Handle;
                    NativeMethods.PostMessage(h, NativeMethods.WM_HOTKEY, (IntPtr)NativeMethods.HOTKEY_ID, IntPtr.Zero);
                } else if (Regex.IsMatch(cmd, @"^(toggle-click-through|click-through)$", RegexOptions.IgnoreCase)) {
                    _overlayWindow.ToggleClickThrough("testHook");
                } else if (Regex.IsMatch(cmd, @"^click-through:(true|false|1|0|on|off)$", RegexOptions.IgnoreCase)) {
                    var m = Regex.Match(cmd, @"^click-through:(true|false|1|0|on|off)$", RegexOptions.IgnoreCase);
                    string target = m.Groups[1].Value.ToLowerInvariant();
                    bool state = (target == "true" || target == "1" || target == "on");
                    _overlayWindow.SetClickThroughState(state, "testHook");
                } else if (Regex.IsMatch(cmd, @"^refresh$", RegexOptions.IgnoreCase)) {
                    Logger.Log("Tray menu: 'Обновить квоты' clicked.");
                    _overlayWindow.StartAsyncRefresh();
                } else if (Regex.IsMatch(cmd, @"^backend:(.+)$", RegexOptions.IgnoreCase)) {
                    var m = Regex.Match(cmd, @"^backend:(.+)$", RegexOptions.IgnoreCase);
                    string b = m.Groups[1].Value.Trim().ToLowerInvariant();
                    if (b == "auto" || b == "windows" || b == "wsl") {
                        _overlayWindow.SetBackendFromMenu(b);
                    }
                } else if (Regex.IsMatch(cmd, @"^move:(-?\d+),(-?\d+)$", RegexOptions.IgnoreCase)) {
                    var m = Regex.Match(cmd, @"^move:(-?\d+),(-?\d+)$", RegexOptions.IgnoreCase);
                    double l = double.Parse(m.Groups[1].Value);
                    double t = double.Parse(m.Groups[2].Value);
                    _overlayWindow.Left = l;
                    _overlayWindow.Top = t;
                    _overlayWindow.IsUserPositioned = true;
                    Logger.Log(string.Format("Window manually moved to Left={0}, Top={1}.", l, t));
                } else if (Regex.IsMatch(cmd, @"^reset-pos$", RegexOptions.IgnoreCase)) {
                    _overlayWindow.ResetOverlayPosition();
                } else if (Regex.IsMatch(cmd, @"^capture(?::(.+))?$", RegexOptions.IgnoreCase)) {
                    var m = Regex.Match(cmd, @"^capture(?::(.+))?$", RegexOptions.IgnoreCase);
                    string p = m.Groups[1].Success ? m.Groups[1].Value.Trim() : null;
                    _overlayWindow.CaptureOverlayWindow(p);
                } else if (Regex.IsMatch(cmd, @"^status$", RegexOptions.IgnoreCase)) {
                    _overlayWindow.LogStatusQuery();
                } else if (Regex.IsMatch(cmd, @"^exit$", RegexOptions.IgnoreCase)) {
                    _overlayWindow.ExitOverlay();
                }
            } catch { }
        }

        public void Stop() {
            if (_timer != null) {
                _timer.Stop();
                _timer = null;
            }
        }
    }
}
