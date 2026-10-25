using System;
using System.Drawing;
using System.IO;
using System.Windows.Forms;

namespace LlmLimitsOverlay {
    public class TrayManager : IDisposable {
        private NotifyIcon _trayIcon;
        private Icon _iconObj;
        private ContextMenuStrip _trayMenu;

        private ToolStripMenuItem _menuToggle;
        private ToolStripMenuItem _menuRefresh;
        private ToolStripMenuItem _menuEnv;
        private ToolStripMenuItem _menuBackendAuto;
        private ToolStripMenuItem _menuBackendWindows;
        private ToolStripMenuItem _menuBackendWsl;
        private ToolStripMenuItem _menuClickThrough;
        private ToolStripMenuItem _menuResetPos;
        private ToolStripMenuItem _menuExit;

        private readonly OverlayWindow _overlayWindow;
        private DateTime _lastToggleTime = DateTime.MinValue;

        public TrayManager(OverlayWindow overlayWindow, string appDirectory) {
            _overlayWindow = overlayWindow;

            string iconPath = Path.Combine(appDirectory, @"assets\app.ico");
            if (File.Exists(iconPath)) {
                try {
                    _iconObj = new Icon(iconPath);
                } catch (Exception ex) {
                    Logger.Log("Error loading icon from " + iconPath + ": " + ex.Message);
                }
            }

            if (_iconObj == null) {
                // Try manifest resource
                try {
                    using (var stream = typeof(TrayManager).Assembly.GetManifestResourceStream("LlmLimitsOverlay.app.ico")) {
                        if (stream != null) {
                            _iconObj = new Icon(stream);
                        }
                    }
                } catch { }
            }

            if (_iconObj == null) {
                _iconObj = SystemIcons.Application;
            }

            _trayIcon = new NotifyIcon {
                Icon = _iconObj,
                Text = _overlayWindow.IsClickThrough ? "LLM Limits Overlay [сквозь]" : "LLM Limits Overlay",
                Visible = true
            };

            BuildMenu();
            _trayIcon.ContextMenuStrip = _trayMenu;

            _trayIcon.MouseClick += (s, e) => {
                if (e.Button == MouseButtons.Left) {
                    HandleToggleClick();
                }
            };

            _trayIcon.DoubleClick += (s, e) => {
                HandleToggleClick();
            };

            Logger.Log(string.Format("Tray icon created successfully (asset: {0}, visible: {1}).", iconPath, _trayIcon.Visible));
        }

        private void BuildMenu() {
            _trayMenu = new ContextMenuStrip();

            _menuToggle = new ToolStripMenuItem("Скрыть оверлей");
            _menuToggle.Click += (s, e) => HandleToggleClick();
            _trayMenu.Items.Add(_menuToggle);

            _trayMenu.Items.Add(new ToolStripSeparator());

            _menuRefresh = new ToolStripMenuItem("Обновить квоты");
            _menuRefresh.Click += (s, e) => {
                Logger.Log("Tray menu: 'Обновить квоты' clicked.");
                _overlayWindow.StartAsyncRefresh();
            };
            _trayMenu.Items.Add(_menuRefresh);

            _menuEnv = new ToolStripMenuItem("Окружение");

            _menuBackendAuto = new ToolStripMenuItem("Авто") { CheckOnClick = false };
            _menuBackendAuto.Click += (s, e) => _overlayWindow.SetBackendFromMenu("auto");
            _menuEnv.DropDownItems.Add(_menuBackendAuto);

            _menuBackendWindows = new ToolStripMenuItem("Windows (нативный Python)") { CheckOnClick = false };
            _menuBackendWindows.Click += (s, e) => _overlayWindow.SetBackendFromMenu("windows");
            _menuEnv.DropDownItems.Add(_menuBackendWindows);

            _menuBackendWsl = new ToolStripMenuItem("WSL") { CheckOnClick = false };
            _menuBackendWsl.Click += (s, e) => _overlayWindow.SetBackendFromMenu("wsl");
            _menuEnv.DropDownItems.Add(_menuBackendWsl);

            _trayMenu.Items.Add(_menuEnv);

            _menuClickThrough = new ToolStripMenuItem("Клики насквозь") { CheckOnClick = false };
            _menuClickThrough.Checked = _overlayWindow.IsClickThrough;
            _menuClickThrough.Click += (s, e) => _overlayWindow.ToggleClickThrough("tray");
            _trayMenu.Items.Add(_menuClickThrough);

            _trayMenu.Items.Add(new ToolStripSeparator());

            _menuResetPos = new ToolStripMenuItem("Сбросить позицию");
            _menuResetPos.Click += (s, e) => {
                Logger.Log("Tray action: 'Сбросить позицию' clicked.");
                _overlayWindow.ResetOverlayPosition();
            };
            _trayMenu.Items.Add(_menuResetPos);

            _menuExit = new ToolStripMenuItem("Выход");
            _menuExit.Click += (s, e) => {
                Logger.Log("Tray action: 'Выход' clicked. Exiting.");
                _overlayWindow.ExitOverlay();
            };
            _trayMenu.Items.Add(_menuExit);

            _trayMenu.Opening += (s, e) => {
                _menuToggle.Text = _overlayWindow.Visibility == System.Windows.Visibility.Visible ? "Скрыть оверлей" : "Показать оверлей";
                _menuRefresh.Enabled = !_overlayWindow.IsFetching;
                _menuClickThrough.Checked = _overlayWindow.IsClickThrough;

                string currentB = _overlayWindow.CurrentBackend;
                _menuBackendAuto.Checked = (currentB == "auto");
                _menuBackendWindows.Checked = (currentB == "windows");
                _menuBackendWsl.Checked = (currentB == "wsl");
            };
        }

        private void HandleToggleClick() {
            var now = DateTime.UtcNow;
            if ((now - _lastToggleTime).TotalMilliseconds < 400) return;
            _lastToggleTime = now;

            _overlayWindow.ToggleVisibility();
            if (_menuToggle != null) {
                _menuToggle.Text = _overlayWindow.Visibility == System.Windows.Visibility.Visible ? "Скрыть оверлей" : "Показать оверлей";
            }
        }

        public void UpdateTooltip(string text) {
            if (_trayIcon == null) return;
            if (string.IsNullOrEmpty(text)) text = "LLM Limits Overlay";

            text = text.Trim();
            if (_overlayWindow.IsClickThrough && !text.Contains("[сквозь]")) {
                text = text + " [сквозь]";
            }
            if (text.Length > 63) {
                text = text.Substring(0, 60) + "...";
            }

            try {
                _trayIcon.Text = text;
            } catch {
                try {
                    _trayIcon.Text = _overlayWindow.IsClickThrough ? "LLM Limits Overlay [сквозь]" : "LLM Limits Overlay";
                } catch { }
            }
        }

        public void ShowClickThroughNotification(bool isEnabled, string hotkey) {
            if (_trayIcon == null || !_trayIcon.Visible) return;
            try {
                string hkText = !string.IsNullOrEmpty(hotkey) ? hotkey : "Ctrl+Alt+L";
                string title = "LLM Limits Overlay";
                string msg = isEnabled
                    ? "Режим «клики насквозь» включен.\nОкно прозрачно для мыши.\nВозврат: трей или " + hkText + "."
                    : "Режим «клики насквозь» выключен.\nОкно снова интерактивно.";
                _trayIcon.ShowBalloonTip(2000, title, msg, ToolTipIcon.Info);
            } catch (Exception ex) {
                Logger.Log("Error displaying tray notification: " + ex.Message);
            }
        }

        public void Dispose() {
            try {
                if (_trayIcon != null) {
                    _trayIcon.Visible = false;
                    _trayIcon.Dispose();
                    _trayIcon = null;
                    Logger.Log("Tray icon cleaned up and disposed.");
                }
                if (_iconObj != null) {
                    _iconObj.Dispose();
                    _iconObj = null;
                }
            } catch { }
        }
    }
}
