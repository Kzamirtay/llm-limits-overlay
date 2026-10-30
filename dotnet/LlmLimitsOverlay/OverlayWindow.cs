using System;
using System.Collections.Generic;
using System.Diagnostics;
using System.Drawing;
using System.Drawing.Imaging;
using System.Globalization;
using System.IO;
using System.Windows;
using System.Windows.Controls;
using System.Windows.Input;
using System.Windows.Interop;
using System.Windows.Media;
using System.Windows.Threading;

namespace LlmLimitsOverlay {
    public class OverlayWindow : Window {
        private readonly string _appDirectory;
        private Config _config;
        private bool _clickThrough = true;
        private string _clickThroughHotkey = "Ctrl+Alt+L";
        private bool _isUserPositioned = false;
        private const double MarginRight = 14.0;
        private const double MarginBottom = 4.0;

        // UI controls
        // Динамические карточки провайдеров (kimi/openrouter) — список ниже;
        // аги-карточка остаётся статической (одна, комбинированная).

        // Динамическая карточка провайдера (kimi/openrouter-типы)
        private class ProviderCard {
            public string Id;
            public string Kind; // kimi | openrouter
            public Border Root;
            public TextBlock TxtUpdated;
            public string UpdatedStr = "";
            public bool Stale = false;
            public string Status = "online";
            // kimi
            public Grid Row7d; public TextBlock Lbl7d; public TextBlock Pct7d; public ProgressBar Bar7d;
            public Grid Row5h; public TextBlock Lbl5h; public TextBlock Pct5h; public ProgressBar Bar5h;
            // openrouter
            public Grid RowMain; public TextBlock LblMain; public TextBlock PctMain; public ProgressBar BarMain;
            public Grid RowFree; public TextBlock LblFree; public TextBlock PctFree; public ProgressBar BarFree;
            public TextBlock TxtBalance;
        }

        private readonly List<ProviderCard> _cards = new List<ProviderCard>();

        // Gemini + Claude/GPT (аги) — статическая комбинированная карточка
        private Grid _rowGemini7d;
        private TextBlock _txtGemini7dLabel;
        private TextBlock _txtGemini7dPercent;
        private ProgressBar _barGemini7d;

        private Grid _rowGemini5h;
        private TextBlock _txtGemini5hLabel;
        private TextBlock _txtGemini5hPercent;
        private ProgressBar _barGemini5h;

        // Claude
        private Grid _rowClaude7d;
        private TextBlock _txtClaude7dLabel;
        private TextBlock _txtClaude7dPercent;
        private ProgressBar _barClaude7d;

        private Grid _rowClaude5h;
        private TextBlock _txtClaude5hLabel;
        private TextBlock _txtClaude5hPercent;
        private ProgressBar _barClaude5h;

        // Window Context Menu items
        private MenuItem _menuRefresh;
        private MenuItem _menuClose;

        // Controllers & helpers
        private QuotaFetcher _fetcher;
        private TrayManager _trayManager;
        private TestHooksDispatcher _testHooksDispatcher;
        private DispatcherTimer _clockTimer;
        private DispatcherTimer _mainTimer;

        private QuotaSnapshot _lastSnapshot;

        // Возраст аги-карточки (динамические карточки хранят состояние в _cards)
        private TextBlock _txtAgyUpdated;
        private string _agyUpdatedStr;
        private bool _agyStale = false;
        private string _agyStatus = "online";

        public bool IsClickThrough {
            get { return _clickThrough; }
        }

        public bool IsFetching {
            get { return _fetcher != null && _fetcher.IsFetching; }
        }

        public QuotaSnapshot LastSnapshot {
            get { return _lastSnapshot; }
        }

        public bool IsUserPositioned {
            get { return _isUserPositioned; }
            set { _isUserPositioned = value; }
        }

        public OverlayWindow(string appDirectory, Config config, DateTime processStartTime) {
            _appDirectory = appDirectory;
            _config = config;
            _clickThrough = config.ClickThrough;
            _clickThroughHotkey = !string.IsNullOrEmpty(config.ClickThroughHotkey) ? config.ClickThroughHotkey.Trim() : "Ctrl+Alt+L";

            InitializeWindowProperties();
            BuildUi();
            BuildContextMenu();

            _fetcher = new QuotaFetcher(appDirectory, OnDataLoaded, OnFetchStateChanged, a => Dispatcher.BeginInvoke(a));

            SourceInitialized += OnSourceInitialized;
            ContentRendered += OnContentRendered;
            SizeChanged += OnWindowSizeChanged;
            MouseLeftButtonDown += OnMouseLeftButtonDown;
            Closed += OnWindowClosed;

            // 10s relative time clock timer
            _clockTimer = new DispatcherTimer { Interval = TimeSpan.FromSeconds(10) };
            _clockTimer.Tick += (s, e) => {
                if (!IsFetching) UpdateRelativeTimeText();
            };
            _clockTimer.Start();

            // 60s main refresh timer
            _mainTimer = new DispatcherTimer { Interval = TimeSpan.FromSeconds(60) };
            _mainTimer.Tick += (s, e) => {
                try {
                    IntPtr h = new WindowInteropHelper(this).Handle;
                    bool vis = NativeMethods.IsWindowVisible(h);
                    Logger.Log(string.Format("Periodic watchdog tick (HWND={0}, IsWindowVisible={1}).", h, vis));
                } catch { }
                StartAsyncRefresh();
            };
            _mainTimer.Start();

            // Create tray manager
            _trayManager = new TrayManager(this, _appDirectory);

            // Create test hooks dispatcher if testHooks == true
            if (_config.TestHooks) {
                _testHooksDispatcher = new TestHooksDispatcher(this, _appDirectory, processStartTime);
            } else {
                Logger.Log("Test hooks disabled (testHooks=false in config). Command file .tray-action will not be monitored.");
            }

            Loaded += (s, e) => {
                _fetcher.LoadCacheFromDisk(null);
                StartAsyncRefresh();
            };
        }

        private void InitializeWindowProperties() {
            Title = "LLM Limits Overlay";
            Width = 290;
            SizeToContent = SizeToContent.Height;
            MaxHeight = 650;
            WindowStyle = WindowStyle.None;
            AllowsTransparency = true;
            Background = System.Windows.Media.Brushes.Transparent;
            Topmost = true;
            ShowInTaskbar = false;
            WindowStartupLocation = WindowStartupLocation.Manual;

            TextOptions.SetTextFormattingMode(this, TextFormattingMode.Display);
            TextOptions.SetTextRenderingMode(this, TextRenderingMode.ClearType);
            RenderOptions.SetClearTypeHint(this, ClearTypeHint.Enabled);
            UseLayoutRounding = true;
            SnapsToDevicePixels = true;

            string iconPath = Path.Combine(_appDirectory, @"assets\app.ico");
            if (File.Exists(iconPath)) {
                try {
                    Icon = System.Windows.Media.Imaging.BitmapFrame.Create(new Uri(iconPath, UriKind.Absolute));
                } catch { }
            }

            Measure(new System.Windows.Size(290, double.PositiveInfinity));
            Arrange(new Rect(0, 0, 290, DesiredSize.Height));
            UpdateOverlayPosition();
        }

        private void BuildUi() {
            var rootGrid = new Grid {
                Background = new SolidColorBrush(System.Windows.Media.Color.FromArgb(0x01, 0, 0, 0)),
                Margin = new Thickness(6, 6, 6, 12)
            };

            // Карточки строятся из списка провайдеров конфига (дефолты — kimi/openrouter/agy)
            var providers = (_config != null ? _config.GetEffectiveProviders() : ProviderConfig.GetDefaults());
            int row = 0;
            foreach (var p in providers) {
                if (!p.Enabled) continue;
                rootGrid.RowDefinitions.Add(new RowDefinition { Height = GridLength.Auto });

                if (p.Type == "agy") {
                    var agyCard = BuildAgyCard(p);
                    Grid.SetRow(agyCard, row);
                    rootGrid.Children.Add(agyCard);
                } else {
                    var card = BuildProviderCard(p);
                    _cards.Add(card);
                    Grid.SetRow(card.Root, row);
                    rootGrid.Children.Add(card.Root);
                }
                row++;
            }

            Content = rootGrid;
        }

        // Динамическая карточка kimi/openrouter-типа (title из конфига)
        private ProviderCard BuildProviderCard(ProviderConfig p) {
            var card = new ProviderCard { Id = p.Id, Kind = p.Type };
            card.Root = CreatePodBorder(new Thickness(0, 0, 0, 6), new Thickness(10, 8, 10, 8));
            var stack = new StackPanel();

            var color = p.Type == "openrouter" ? OverlayTheme.OpenRouterHeaderColor : OverlayTheme.KimiHeaderColor;
            var title = CreateHeader(p.Title, color, new Thickness(0, 0, 0, 4));
            card.TxtUpdated = CreateUpdatedTextBlock();
            stack.Children.Add(CreateHeaderRow(title, card.TxtUpdated));

            if (p.Type == "openrouter") {
                card.RowMain = CreateMetricRow("Месячный лимит", out card.LblMain, out card.PctMain);
                stack.Children.Add(card.RowMain);
                card.BarMain = OverlayTheme.CreateProgressBar(5.5, new Thickness(0, 1, 0, 4), "Месячный лимит");
                stack.Children.Add(card.BarMain);

                card.RowFree = CreateMetricRow(OverlayTheme.FreeDailyTooltip, out card.LblFree, out card.PctFree);
                stack.Children.Add(card.RowFree);
                card.BarFree = OverlayTheme.CreateProgressBar(5.5, new Thickness(0, 1, 0, 4), OverlayTheme.FreeDailyTooltip);
                stack.Children.Add(card.BarFree);

                var metaGrid = new Grid { Margin = new Thickness(0, 2, 0, 0) };
                card.TxtBalance = new TextBlock {
                    Text = "ост. --",
                    FontSize = 9,
                    Foreground = OverlayTheme.MetaTextBrush,
                    HorizontalAlignment = HorizontalAlignment.Right,
                    FontFamily = OverlayTheme.OverlayFontFamily
                };
                metaGrid.Children.Add(card.TxtBalance);
                stack.Children.Add(metaGrid);
            } else {
                // kimi: окна 7d (сверху) и 5h
                card.Row7d = CreateMetricRow("Лимит 7d", out card.Lbl7d, out card.Pct7d);
                stack.Children.Add(card.Row7d);
                card.Bar7d = OverlayTheme.CreateProgressBar(5.5, new Thickness(0, 1, 0, 7), "Лимит 7d");
                stack.Children.Add(card.Bar7d);

                card.Row5h = CreateMetricRow("Лимит 5h", out card.Lbl5h, out card.Pct5h);
                stack.Children.Add(card.Row5h);
                card.Bar5h = OverlayTheme.CreateProgressBar(5.5, new Thickness(0, 1, 0, 3), "Лимит 5h");
                stack.Children.Add(card.Bar5h);
            }

            card.Root.Child = stack;
            return card;
        }

        // Аги-карточка (Gemini + Claude/GPT) — статическая комбинированная
        private Border BuildAgyCard(ProviderConfig p) {
            var geminiClaudeBorder = CreatePodBorder(new Thickness(0, 0, 0, 0), new Thickness(10, 8, 10, 11));
            var gcStack = new StackPanel();

            // Gemini section header + возраст данных карточки справа
            var geminiTitle = CreateHeader("GEMINI", OverlayTheme.GeminiHeaderColor, new Thickness(0, 0, 0, 4));
            _txtAgyUpdated = CreateUpdatedTextBlock();
            gcStack.Children.Add(CreateHeaderRow(geminiTitle, _txtAgyUpdated));

            // Gemini 7d (Weekly first)
            _rowGemini7d = CreateMetricRow("Лимит 7d", out _txtGemini7dLabel, out _txtGemini7dPercent);
            gcStack.Children.Add(_rowGemini7d);
            _barGemini7d = OverlayTheme.CreateProgressBar(5.5, new Thickness(0, 1, 0, 7), "Лимит 7d");
            gcStack.Children.Add(_barGemini7d);

            // Gemini 5h
            _rowGemini5h = CreateMetricRow("Лимит 5h", out _txtGemini5hLabel, out _txtGemini5hPercent);
            gcStack.Children.Add(_rowGemini5h);
            _barGemini5h = OverlayTheme.CreateProgressBar(5.5, new Thickness(0, 1, 0, 8), "Лимит 5h");
            gcStack.Children.Add(_barGemini5h);

            // Divider
            var divider = new Border {
                Background = OverlayTheme.DividerBrush,
                Height = 0.8,
                Margin = new Thickness(0, 1, 0, 7)
            };
            gcStack.Children.Add(divider);

            // Claude/GPT section
            var claudeTitle = CreateHeader("CLAUDE/GPT", OverlayTheme.ClaudeHeaderColor, new Thickness(0, 0, 0, 4));
            gcStack.Children.Add(claudeTitle);

            // Claude 7d (Weekly first)
            _rowClaude7d = CreateMetricRow("Лимит 7d", out _txtClaude7dLabel, out _txtClaude7dPercent);
            gcStack.Children.Add(_rowClaude7d);
            _barClaude7d = OverlayTheme.CreateProgressBar(5.5, new Thickness(0, 1, 0, 7), "Лимит 7d");
            gcStack.Children.Add(_barClaude7d);

            // Claude 5h
            _rowClaude5h = CreateMetricRow("Лимит 5h", out _txtClaude5hLabel, out _txtClaude5hPercent);
            gcStack.Children.Add(_rowClaude5h);
            _barClaude5h = OverlayTheme.CreateProgressBar(5.5, new Thickness(0, 1, 0, 2), "Лимит 5h");
            gcStack.Children.Add(_barClaude5h);

            geminiClaudeBorder.Child = gcStack;
            return geminiClaudeBorder;
        }

        private Border CreatePodBorder(Thickness margin, Thickness padding) {
            return new Border {
                Background = OverlayTheme.PodBackgroundBrush,
                BorderBrush = OverlayTheme.PodBorderBrush,
                BorderThickness = new Thickness(OverlayTheme.PodBorderThickness),
                CornerRadius = new CornerRadius(OverlayTheme.PodCornerRadius),
                Padding = padding,
                Margin = margin,
                Effect = OverlayTheme.CreatePodShadow()
            };
        }

        private TextBlock CreateHeader(string text, System.Windows.Media.Color color, Thickness margin) {
            return new TextBlock {
                Text = text,
                FontSize = 11,
                FontWeight = FontWeights.Bold,
                Foreground = new SolidColorBrush(color),
                Margin = margin,
                FontFamily = OverlayTheme.OverlayFontFamily
            };
        }

        // Шапка карточки: заголовок слева, возраст данных справа — вровень
        // по нижней линии (базовой линии) заголовка. Отступ заголовка не меняется.
        private static Grid CreateHeaderRow(TextBlock title, TextBlock updated) {
            var grid = new Grid();
            grid.Margin = title.Margin;
            title.Margin = new Thickness(0);
            title.VerticalAlignment = VerticalAlignment.Bottom;
            updated.VerticalAlignment = VerticalAlignment.Bottom;
            updated.HorizontalAlignment = HorizontalAlignment.Right;
            grid.Children.Add(title);
            grid.Children.Add(updated);
            return grid;
        }

        private Grid CreateMetricRow(string tooltip, out TextBlock label, out TextBlock percent) {
            var grid = new Grid {
                Margin = new Thickness(0, 1, 0, 2),
                Background = new SolidColorBrush(System.Windows.Media.Color.FromArgb(0x01, 0, 0, 0)),
                ToolTip = tooltip
            };
            ToolTipService.SetInitialShowDelay(grid, 100);

            label = new TextBlock {
                Text = "--д --ч",
                FontSize = 11.5,
                FontWeight = FontWeights.SemiBold,
                Foreground = OverlayTheme.LabelTextBrush,
                FontFamily = OverlayTheme.OverlayFontFamily
            };

            percent = new TextBlock {
                Text = "--%",
                FontSize = 11.5,
                FontWeight = FontWeights.SemiBold,
                Foreground = OverlayTheme.LabelTextBrush,
                HorizontalAlignment = HorizontalAlignment.Right,
                FontFamily = OverlayTheme.OverlayFontFamily
            };

            grid.Children.Add(label);
            grid.Children.Add(percent);
            return grid;
        }

        private void BuildContextMenu() {
            var ctxMenu = new ContextMenu {
                Background = (System.Windows.Media.Brush)new BrushConverter().ConvertFromString("#F8FAFC"),
                BorderBrush = (System.Windows.Media.Brush)new BrushConverter().ConvertFromString("#CBD5E1"),
                BorderThickness = new Thickness(1)
            };

            _menuRefresh = new MenuItem { Header = "Обновить", Foreground = OverlayTheme.MetaTextBrush };
            _menuRefresh.Click += (s, e) => StartAsyncRefresh();
            ctxMenu.Items.Add(_menuRefresh);

            ctxMenu.Items.Add(new Separator { Background = (System.Windows.Media.Brush)new BrushConverter().ConvertFromString("#E2E8F0") });

            _menuClose = new MenuItem { Header = "Закрыть", Foreground = OverlayTheme.MetaTextBrush };
            _menuClose.Click += (s, e) => {
                Logger.Log("User clicked 'Закрыть'. Exiting.");
                ExitOverlay();
            };
            ctxMenu.Items.Add(_menuClose);

            ContextMenu = ctxMenu;
        }

        private void OnSourceInitialized(object sender, EventArgs e) {
            IntPtr handle = new WindowInteropHelper(this).Handle;
            NativeMethods.SetToolWindow(handle);
            Logger.Log(string.Format("Window HWND={0} initialized.", handle));

            if (_clickThrough) {
                NativeMethods.SetClickThrough(handle, true);
                int initEx = NativeMethods.GetWindowExStyle(handle);
                Logger.Log(string.Format("Click-through mode enabled at startup (config clickThrough={0}).", _config.ClickThrough));
                Logger.Log(string.Format("Initial click-through enabled before show (HWND={0}, GWL_EXSTYLE=0x{1:X8}).", handle, initEx));
            } else {
                NativeMethods.SetClickThrough(handle, false);
                Logger.Log("Click-through mode disabled at startup (config clickThrough=false).");
            }

            RegisterHotkey(handle);
        }

        private void OnContentRendered(object sender, EventArgs e) {
            UpdateOverlayPosition();
            try {
                IntPtr h = new WindowInteropHelper(this).Handle;
                bool vis = NativeMethods.IsWindowVisible(h);
                Logger.Log(string.Format("Window rendered and visible (HWND={0}, IsWindowVisible={1}, Left={2}, Top={3}).", h, vis, Left, Top));
            } catch { }
        }

        private void OnWindowSizeChanged(object sender, SizeChangedEventArgs e) {
            if (e.HeightChanged) {
                if (_isUserPositioned) {
                    double deltaH = e.NewSize.Height - e.PreviousSize.Height;
                    if (e.PreviousSize.Height > 0) {
                        Top = Top - deltaH;
                    }
                } else {
                    UpdateOverlayPosition();
                }
            }
        }

        private void OnMouseLeftButtonDown(object sender, MouseButtonEventArgs e) {
            if (e.ButtonState == MouseButtonState.Pressed) {
                DragMove();
                _isUserPositioned = true;
            }
        }

        public void UpdateOverlayPosition() {
            if (_isUserPositioned) return;

            double waRight, waBottom;
            try {
                var wa = SystemParameters.WorkArea;
                waRight = wa.Right;
                waBottom = wa.Bottom;
            } catch {
                var screen = System.Windows.Forms.Screen.PrimaryScreen;
                waRight = screen.WorkingArea.Right;
                waBottom = screen.WorkingArea.Bottom;
            }

            double actualW = ActualWidth > 0 ? ActualWidth : 290;
            double actualH = ActualHeight > 0 ? ActualHeight : DesiredSize.Height;
            if (actualH <= 0) {
                UpdateLayout();
                actualH = ActualHeight > 0 ? ActualHeight : DesiredSize.Height;
            }

            if (actualH > 0) {
                Left = waRight - actualW - MarginRight;
                Top = waBottom - actualH - MarginBottom;
            }
        }

        public void ResetOverlayPosition() {
            _isUserPositioned = false;
            UpdateOverlayPosition();
        }

        private void RegisterHotkey(IntPtr hWnd) {
            if (string.IsNullOrEmpty(_clickThroughHotkey)) {
                Logger.Log("Click-through hotkey is disabled (clickThroughHotkey is empty).");
                return;
            }

            var parsed = HotkeyManager.ParseHotkeyString(_clickThroughHotkey);
            if (parsed == null) {
                Logger.Log(string.Format("Failed to parse clickThroughHotkey '{0}'. Hotkey not registered.", _clickThroughHotkey));
                return;
            }

            try {
                bool ok = HotkeyManager.RegisterGlobalHotkey(hWnd, parsed.Modifiers, parsed.VirtualKey, () => {
                    ToggleClickThrough("hotkey");
                });

                if (ok) {
                    Logger.Log(string.Format("Registered global hotkey '{0}' (Modifiers=0x{1:X}, VK=0x{2:X}) for click-through toggle.", parsed.Display, parsed.Modifiers, parsed.VirtualKey));
                } else {
                    int err = System.Runtime.InteropServices.Marshal.GetLastWin32Error();
                    Logger.Log(string.Format("Failed to register global hotkey '{0}' (Win32 error: {1}). Hotkey may conflict with another application.", parsed.Display, err));
                }
            } catch (Exception ex) {
                Logger.Log("Exception registering global hotkey: " + ex.Message);
            }
        }

        public void ApplyClickThroughState(bool enable) {
            try {
                IntPtr h = new WindowInteropHelper(this).Handle;
                if (h != IntPtr.Zero) {
                    NativeMethods.SetClickThrough(h, enable);
                    int curEx = NativeMethods.GetWindowExStyle(h);
                    Logger.Log(string.Format("Applied click-through={0} to HWND={1} (GWL_EXSTYLE=0x{2:X8}).", enable, h, curEx));
                }
            } catch (Exception ex) {
                Logger.Log("Error applying click-through style: " + ex.Message);
            }
        }

        public void SetClickThroughState(bool enable, string source) {
            _clickThrough = enable;
            ApplyClickThroughState(enable);
            Config.SaveField("clickThrough", enable);
            Logger.Log(string.Format("Click-through mode set to {0} via {1}.", enable, source));

            if (_trayManager != null) {
                _trayManager.ShowClickThroughNotification(enable, _clickThroughHotkey);
                UpdateTrayTooltip();
            }
        }

        public void ToggleClickThrough(string source) {
            SetClickThroughState(!_clickThrough, source);
        }

        public void ToggleVisibility() {
            IntPtr h = new WindowInteropHelper(this).Handle;
            if (Visibility == Visibility.Visible) {
                Visibility = Visibility.Hidden;
                bool vis = NativeMethods.IsWindowVisible(h);
                Logger.Log(string.Format("Tray action: Hiding overlay window (WPF Visibility=Hidden, Win32 IsWindowVisible={0}).", vis));
            } else {
                Visibility = Visibility.Visible;
                Activate();
                bool vis = NativeMethods.IsWindowVisible(h);
                Logger.Log(string.Format("Tray action: Showing overlay window (WPF Visibility=Visible, Win32 IsWindowVisible={0}).", vis));
            }
        }

        public void StartAsyncRefresh() {
            if (_menuRefresh != null) _menuRefresh.IsEnabled = false;
            _fetcher.StartAsyncRefresh(_config);
        }

        private void OnFetchStateChanged(bool isFetching, string statusText) {
            if (_menuRefresh != null) _menuRefresh.IsEnabled = !isFetching;
            if (!string.IsNullOrEmpty(statusText)) {
                foreach (var card in _cards) {
                    if (card.TxtUpdated != null) {
                        card.TxtUpdated.Text = statusText;
                        card.TxtUpdated.Foreground = OverlayTheme.MetaTextBrush;
                    }
                }
                if (_txtAgyUpdated != null) {
                    _txtAgyUpdated.Text = statusText;
                    _txtAgyUpdated.Foreground = OverlayTheme.MetaTextBrush;
                }
            }
        }

        private static ProviderState FindProviderState(QuotaSnapshot snap, string id) {
            if (snap == null || snap.Providers == null) return null;
            foreach (var ps in snap.Providers) {
                if (string.Equals(ps.Id, id, StringComparison.OrdinalIgnoreCase)) return ps;
            }
            return null;
        }

        private void OnDataLoaded(QuotaSnapshot snapshot) {
            _lastSnapshot = snapshot;

            bool globalStale = (snapshot.Status != "online");

            _agyUpdatedStr = !string.IsNullOrEmpty(snapshot.AgyLastUpdated) ? snapshot.AgyLastUpdated : snapshot.LastUpdated;
            _agyStale = globalStale || snapshot.AgyStatus == "stale";
            _agyStatus = snapshot.AgyStatus;

            foreach (var card in _cards) {
                UpdateProviderCard(card, snapshot, globalStale);
            }

            UpdateRelativeTimeText();

            // Gemini 7d (Weekly first)
            UpdateBucketRow(_rowGemini7d, _txtGemini7dLabel, _txtGemini7dPercent, _barGemini7d, snapshot.GeminiWeekly, "7d");

            // Gemini 5h
            UpdateBucketRow(_rowGemini5h, _txtGemini5hLabel, _txtGemini5hPercent, _barGemini5h, snapshot.Gemini5h, "5h");

            // Claude 7d (Weekly first)
            UpdateBucketRow(_rowClaude7d, _txtClaude7dLabel, _txtClaude7dPercent, _barClaude7d, snapshot.ClaudeWeekly, "7d");

            // Claude 5h
            UpdateBucketRow(_rowClaude5h, _txtClaude5hLabel, _txtClaude5hPercent, _barClaude5h, snapshot.Claude5h, "5h");

            try {
                UpdateLayout();
                UpdateOverlayPosition();
            } catch { }

            UpdateTrayTooltip();
        }

        private void UpdateProviderCard(ProviderCard card, QuotaSnapshot snapshot, bool globalStale) {
            var st = FindProviderState(snapshot, card.Id);
            if (st == null) return;

            card.Status = st.Status;
            card.UpdatedStr = !string.IsNullOrEmpty(st.LastUpdated) ? st.LastUpdated : snapshot.LastUpdated;
            card.Stale = globalStale || st.Status == "stale";

            if (card.Kind == "openrouter") {
                var or = st.OpenRouter;
                if (or == null) return;
                card.LblMain.Text = OverlayTheme.FormatResetTimeLabel(or.ResetIn);
                card.PctMain.Text = or.UsedPercent + "%";
                card.BarMain.Value = Math.Min(100.0, Math.Max(0.0, or.UsedPercent));
                card.BarMain.Foreground = OverlayTheme.GetProgressBrush(or.UsedFraction);
                card.PctMain.Foreground = OverlayTheme.GetPercentBrush(or.UsedFraction);
                card.PctMain.FontWeight = (or.UsedFraction >= 1.0 || or.UsedPercent >= 100.0) ? FontWeights.Bold : FontWeights.SemiBold;

                string orTooltip = string.Format(CultureInfo.InvariantCulture, "${0:0.00} из ${1:0.00} • осталось ${2:0.00} • сброс {3}",
                    or.Usage, or.Limit, or.LimitRemaining, or.LimitReset);
                card.RowMain.ToolTip = orTooltip;
                card.BarMain.ToolTip = orTooltip;

                bool hasFree = or.FreeDailyLimit > 0;
                var freeVis = hasFree ? Visibility.Visible : Visibility.Collapsed;
                if (card.RowFree != null) card.RowFree.Visibility = freeVis;
                if (card.BarFree != null) card.BarFree.Visibility = freeVis;
                if (hasFree) {
                    double freeFrac = Math.Max(0.0, Math.Min(1.0, or.FreeDailyUsed / (double)or.FreeDailyLimit));
                    double freePct = Math.Round(freeFrac * 100.0, 1);
                    card.LblFree.Text = string.Format(OverlayTheme.FreeDailyLabelFormat, or.FreeDailyUsed, or.FreeDailyLimit);
                    card.PctFree.Text = freePct + "%";
                    card.PctFree.FontWeight = (freeFrac >= 1.0) ? FontWeights.Bold : FontWeights.SemiBold;
                    card.BarFree.Value = Math.Min(100.0, Math.Max(0.0, freePct));
                    card.BarFree.Foreground = OverlayTheme.GetProgressBrush(freeFrac);
                    card.PctFree.Foreground = OverlayTheme.GetPercentBrush(freeFrac);
                    string freeTooltip = string.Format(CultureInfo.InvariantCulture, OverlayTheme.FreeDailyTooltip + ": {0} из {1}", or.FreeDailyUsed, or.FreeDailyLimit);
                    card.RowFree.ToolTip = freeTooltip;
                    card.BarFree.ToolTip = freeTooltip;
                }
                card.TxtBalance.Text = string.Format(CultureInfo.InvariantCulture, OverlayTheme.BalanceLabelFormat, or.LimitRemaining);
            } else {
                var k = st.Kimi;
                if (k == null) return;
                UpdateKimiBucket(card.Row7d, card.Lbl7d, card.Pct7d, card.Bar7d, k.Limit7d, "Недельное окно");
                UpdateKimiBucket(card.Row5h, card.Lbl5h, card.Pct5h, card.Bar5h, k.Limit5h, "5-часовое окно");
            }
        }

        private void UpdateBucketRow(Grid row, TextBlock lbl, TextBlock pct, ProgressBar bar, BucketInfo b, string windowType) {
            if (b == null) return;
            lbl.Text = OverlayTheme.FormatResetTimeLabel(b.ResetIn);
            pct.Text = b.Percent + "%";
            bar.Value = Math.Min(100.0, Math.Max(0.0, b.Percent));
            bar.Foreground = OverlayTheme.GetProgressBrush(b.Fraction);
            pct.Foreground = OverlayTheme.GetPercentBrush(b.Fraction);
            pct.FontWeight = (b.Fraction >= 1.0 || b.Percent >= 100.0) ? FontWeights.Bold : FontWeights.SemiBold;

            string tt = OverlayTheme.FormatRowTooltip(windowType, b.ResetIn);
            row.ToolTip = tt;
            bar.ToolTip = tt;
        }

        private void UpdateKimiBucket(Grid row, TextBlock lbl, TextBlock pct, ProgressBar bar, BucketInfo b, string windowDesc) {
            if (b == null) return;
            lbl.Text = OverlayTheme.FormatResetTimeLabel(b.ResetIn);
            pct.Text = b.Percent + "%";
            bar.Value = Math.Min(100.0, Math.Max(0.0, b.Percent));
            bar.Foreground = OverlayTheme.GetProgressBrush(b.Fraction);
            pct.Foreground = OverlayTheme.GetPercentBrush(b.Fraction);
            pct.FontWeight = (b.Fraction >= 1.0 || b.Percent >= 100.0) ? FontWeights.Bold : FontWeights.SemiBold;

            string ruReset = OverlayTheme.FormatResetTimeLabel(b.ResetIn);
            string exh = (b.Fraction >= 1.0 || b.Percent >= 100.0) ? " (исчерпан)" : "";
            string tt = windowDesc + " • сброс через " + ruReset + exh;
            row.ToolTip = tt;
            bar.ToolTip = tt;
        }

        private void UpdateRelativeTimeText() {
            foreach (var card in _cards) {
                UpdateCardUpdatedLine(card.TxtUpdated, card.UpdatedStr, card.Stale, card.Status);
            }
            UpdateCardUpdatedLine(_txtAgyUpdated, _agyUpdatedStr, _agyStale, _agyStatus);
        }

        private static void UpdateCardUpdatedLine(TextBlock tb, string updatedStr, bool isStale, string providerStatus) {
            if (tb == null || string.IsNullOrEmpty(updatedStr)) return;
            if (providerStatus == "no_key") {
                tb.Text = OverlayTheme.RelativeTimeNoKey;
                tb.Foreground = OverlayTheme.MetaTextBrush;
                return;
            }
            tb.Text = OverlayTheme.GetRelativeTimeText(updatedStr, isStale);
            tb.Foreground = isStale ? OverlayTheme.UpdatedStaleTextBrush : OverlayTheme.MetaTextBrush;
        }

        private static TextBlock CreateUpdatedTextBlock() {
            return new TextBlock {
                Text = "только что",
                FontSize = OverlayTheme.CardUpdatedFontSize,
                FontWeight = FontWeights.Normal,
                Foreground = OverlayTheme.MetaTextBrush,
                HorizontalAlignment = HorizontalAlignment.Right,
                Margin = new Thickness(0),
                FontFamily = OverlayTheme.OverlayFontFamily
            };
        }

        private void UpdateTrayTooltip() {
            if (_trayManager == null) return;
            if (_lastSnapshot == null) {
                _trayManager.UpdateTooltip("LLM Limits Overlay");
                return;
            }

            var parts = new System.Collections.Generic.List<string>();
            var k = _lastSnapshot.Kimi;
            if (k != null) {
                if (k.Limit7d != null && k.Limit7d.Percent > 0) {
                    parts.Add("Kimi 7д " + (int)Math.Round(k.Limit7d.Percent) + "%");
                } else if (k.Limit5h != null) {
                    parts.Add("Kimi 5ч " + (int)Math.Round(k.Limit5h.Percent) + "%");
                }
            }

            if (_lastSnapshot.Gemini5h != null && _lastSnapshot.Gemini5h.Percent > 0) {
                parts.Add("Gemini " + (int)Math.Round(_lastSnapshot.Gemini5h.Percent) + "%");
            } else if (_lastSnapshot.GeminiWeekly != null) {
                parts.Add("Gemini " + (int)Math.Round(_lastSnapshot.GeminiWeekly.Percent) + "%");
            }

            if (_lastSnapshot.Claude5h != null && _lastSnapshot.Claude5h.Percent > 0) {
                parts.Add("Claude " + (int)Math.Round(_lastSnapshot.Claude5h.Percent) + "%");
            } else if (_lastSnapshot.ClaudeWeekly != null) {
                parts.Add("Claude " + (int)Math.Round(_lastSnapshot.ClaudeWeekly.Percent) + "%");
            }

            string summary = parts.Count > 0 ? string.Join(" · ", parts.ToArray()) : "LLM Limits Overlay";
            _trayManager.UpdateTooltip(summary);
        }

        public void CaptureOverlayWindow(string destPath = null) {
            if (string.IsNullOrEmpty(destPath)) {
                destPath = Path.Combine(_appDirectory, @"docs\screenshots\overlay_window.png");
            }

            try {
                IntPtr h = new WindowInteropHelper(this).Handle;
                int w = (int)ActualWidth;
                int hgt = (int)ActualHeight;
                if (w <= 0) w = 290;
                if (hgt <= 0) hgt = 500;

                using (var bmp = new Bitmap(w, hgt, System.Drawing.Imaging.PixelFormat.Format32bppArgb)) {
                    using (var g = Graphics.FromImage(bmp)) {
                        IntPtr hdc = g.GetHdc();
                        NativeMethods.PrintWindow(h, hdc, NativeMethods.PW_RENDERFULLCONTENT);
                        g.ReleaseHdc(hdc);
                    }

                    string dir = Path.GetDirectoryName(destPath);
                    if (!string.IsNullOrEmpty(dir) && !Directory.Exists(dir)) {
                        Directory.CreateDirectory(dir);
                    }

                    bmp.Save(destPath, ImageFormat.Png);
                    Logger.Log("Captured overlay window via PrintWindow (PW_RENDERFULLCONTENT=2) to " + destPath);
                }
            } catch (Exception ex) {
                Logger.Log("Error capturing overlay window: " + ex.Message);
            }
        }

        public void LogStatusQuery() {
            IntPtr h = new WindowInteropHelper(this).Handle;
            bool v = NativeMethods.IsWindowVisible(h);
            int ex = NativeMethods.GetWindowExStyle(h);

            var pt = new NativeMethods.POINT((int)(Left + 50), (int)(Top + 50));
            IntPtr hit = NativeMethods.WindowFromPoint(pt);
            bool isUnderneath = (hit != h);

            Logger.Log(string.Format("Status query: Visibility={0}, Win32 IsWindowVisible={1}, GWL_EXSTYLE=0x{2:X8}, ClickThrough={3}, Left={4}, Top={5}, HitHwnd=0x{6:X8}, IsHitUnderneath={7}",
                Visibility, v, ex, _clickThrough, Left, Top, hit.ToInt64(), isUnderneath));
        }

        public void ExitOverlay() {
            HotkeyManager.UnregisterGlobalHotkey();
            if (_testHooksDispatcher != null) {
                _testHooksDispatcher.Stop();
                _testHooksDispatcher = null;
            }
            if (_trayManager != null) {
                _trayManager.Dispose();
                _trayManager = null;
            }
            try {
                Close();
            } catch { }
            try {
                Dispatcher.InvokeShutdown();
            } catch { }
            Environment.Exit(0);
        }

        private void OnWindowClosed(object sender, EventArgs e) {
            HotkeyManager.UnregisterGlobalHotkey();
            if (_trayManager != null) {
                _trayManager.Dispose();
                _trayManager = null;
            }
            if (_testHooksDispatcher != null) {
                _testHooksDispatcher.Stop();
                _testHooksDispatcher = null;
            }
        }
    }
}
