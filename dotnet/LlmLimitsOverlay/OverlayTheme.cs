using System;
using System.Globalization;
using System.Text.RegularExpressions;
using System.Windows;
using System.Windows.Controls;
using System.Windows.Media;
using System.Windows.Media.Effects;

namespace LlmLimitsOverlay {
    public static class OverlayTheme {
        // ~49% translucent frosted slate
        public static readonly Color PodBackgroundColor = Color.FromArgb(0x7C, 0xEE, 0xF2, 0xF6);
        // ~12% subtle border
        public static readonly Color PodBorderColor = Color.FromArgb(0x20, 0x64, 0x74, 0x8B);
        public const double PodBorderThickness = 1.0;
        public const double PodCornerRadius = 8.0;

        public static readonly Color TitleTextColor = Colors.Black;
        public static readonly Color GeminiHeaderColor = Colors.Black;
        public static readonly Color ClaudeHeaderColor = Colors.Black;
        public static readonly Color OpenRouterHeaderColor = Colors.Black;
        public static readonly Color KimiHeaderColor = Colors.Black;

        public static readonly Color LabelTextColor = (Color)ColorConverter.ConvertFromString("#0F172A");
        public static readonly Color PercentTextColor = (Color)ColorConverter.ConvertFromString("#0F172A");
        public static readonly Color PercentText100Color = Colors.Black;

        public static readonly Color MetaTextColor = (Color)ColorConverter.ConvertFromString("#0F172A");
        public static readonly Color MetaTextMutedColor = (Color)ColorConverter.ConvertFromString("#475569");
        public static readonly Color UpdatedStaleTextColor = (Color)ColorConverter.ConvertFromString("#475569");

        public static readonly Color BarTrackBackgroundColor = Color.FromArgb(0x22, 0x00, 0x00, 0x00);
        public static readonly Color ProgressFillStartColor = (Color)ColorConverter.ConvertFromString("#4B5563");
        public static readonly Color ProgressFillEndColor = (Color)ColorConverter.ConvertFromString("#111827");

        public static readonly SolidColorBrush PodBackgroundBrush;
        public static readonly SolidColorBrush PodBorderBrush;
        public static readonly SolidColorBrush LabelTextBrush;
        public static readonly SolidColorBrush MetaTextBrush;
        public static readonly SolidColorBrush MetaTextMutedBrush;
        public static readonly SolidColorBrush UpdatedStaleTextBrush;
        public static readonly SolidColorBrush BarTrackBrush;
        public static readonly SolidColorBrush HeaderBrush;
        public static readonly SolidColorBrush DividerBrush;

        public static readonly FontFamily OverlayFontFamily;

        static OverlayTheme() {
            PodBackgroundBrush = new SolidColorBrush(PodBackgroundColor);
            PodBackgroundBrush.Freeze();
            PodBorderBrush = new SolidColorBrush(PodBorderColor);
            PodBorderBrush.Freeze();
            LabelTextBrush = new SolidColorBrush(LabelTextColor);
            LabelTextBrush.Freeze();
            MetaTextBrush = new SolidColorBrush(MetaTextColor);
            MetaTextBrush.Freeze();
            MetaTextMutedBrush = new SolidColorBrush(MetaTextMutedColor);
            MetaTextMutedBrush.Freeze();
            UpdatedStaleTextBrush = new SolidColorBrush(UpdatedStaleTextColor);
            UpdatedStaleTextBrush.Freeze();
            BarTrackBrush = new SolidColorBrush(BarTrackBackgroundColor);
            BarTrackBrush.Freeze();
            HeaderBrush = new SolidColorBrush(Colors.Black);
            HeaderBrush.Freeze();
            DividerBrush = new SolidColorBrush(Color.FromArgb(0x18, 0, 0, 0));
            DividerBrush.Freeze();

            OverlayFontFamily = new FontFamily("Segoe UI, Segoe UI Variable Display");
        }

        public static DropShadowEffect CreatePodShadow() {
            var ds = new DropShadowEffect {
                BlurRadius = 4,
                ShadowDepth = 1,
                Opacity = 0.06,
                Color = Colors.Black
            };
            ds.Freeze();
            return ds;
        }

        public static Brush GetProgressBrush(double usedFraction) {
            if (double.IsNaN(usedFraction) || usedFraction < 0.0) usedFraction = 0.0;
            if (usedFraction > 1.0) usedFraction = 1.0;

            int r1 = ProgressFillStartColor.R, g1 = ProgressFillStartColor.G, b1 = ProgressFillStartColor.B;
            int r2 = ProgressFillEndColor.R, g2 = ProgressFillEndColor.G, b2 = ProgressFillEndColor.B;

            byte r = (byte)Math.Round(r1 + (r2 - r1) * usedFraction);
            byte g = (byte)Math.Round(g1 + (g2 - g1) * usedFraction);
            byte b = (byte)Math.Round(b1 + (b2 - b1) * usedFraction);

            var brush = new SolidColorBrush(Color.FromRgb(r, g, b));
            brush.Freeze();
            return brush;
        }

        public static Brush GetPercentBrush(double usedFraction) {
            if (usedFraction >= 1.0) {
                return Brushes.Black;
            }
            return LabelTextBrush;
        }

        public static string FormatResetTimeLabel(string resetIn) {
            if (string.IsNullOrEmpty(resetIn) || resetIn == "--" || resetIn == "N/A") {
                return "--";
            }
            if (resetIn == "ready") {
                return "готов";
            }
            string ru = Regex.Replace(resetIn, @"d\s*", "д ");
            ru = Regex.Replace(ru, @"h\s*", "ч ");
            ru = Regex.Replace(ru, @"m\s*", "м");
            return ru.Trim();
        }

        public static string FormatRowTooltip(string windowType, string resetIn) {
            if (string.IsNullOrEmpty(resetIn) || resetIn == "--" || resetIn == "N/A") {
                return "Лимит " + windowType + " • Нет данных о сбросе";
            }
            if (resetIn == "ready") {
                return "Лимит " + windowType + " • Квота восстановлена";
            }
            string ru = Regex.Replace(resetIn, @"d\s*", "д ");
            ru = Regex.Replace(ru, @"h\s*", "ч ");
            ru = Regex.Replace(ru, @"m\s*", "м");
            return "Лимит " + windowType + " • Сброс через " + ru.Trim();
        }

        // Relative-time wording/format constants (single point of edit).
        // Used by every per-card updated line in the overlay.
        public const string RelativeTimeJustNow = "только что";
        public const string RelativeTimeMinutesSuffix = " мин";
        public const string RelativeTimeHoursSuffix = "ч";
        public const string RelativeTimeStaleSuffix = " (stale)";

        public static string GetRelativeTimeText(string updatedStr, bool isStale) {
            if (string.IsNullOrEmpty(updatedStr)) {
                return isStale ? "устарело" : "нет данных";
            }
            try {
                string[] formats = new string[] {
                    "yyyy-MM-dd HH:mm:ss",
                    "yyyy-MM-ddTHH:mm:ss",
                    "yyyy-MM-ddTHH:mm:ssZ",
                    "yyyy/MM/dd HH:mm:ss"
                };
                DateTime dt = DateTime.ParseExact(updatedStr, formats, CultureInfo.InvariantCulture, DateTimeStyles.None);
                TimeSpan diff = DateTime.Now - dt;
                int totalMinutes = (int)Math.Floor(diff.TotalMinutes);

                string baseText = "";
                if (totalMinutes < 1) {
                    baseText = RelativeTimeJustNow;
                } else if (totalMinutes < 60) {
                    baseText = totalMinutes + RelativeTimeMinutesSuffix;
                } else {
                    int hrs = totalMinutes / 60;
                    int mins = totalMinutes % 60;
                    baseText = mins > 0 ? (hrs + RelativeTimeHoursSuffix + " " + mins + "м") : (hrs + RelativeTimeHoursSuffix);
                }

                if (isStale) {
                    return baseText + RelativeTimeStaleSuffix;
                }
                return baseText;
            } catch {
                return isStale ? "устарело" : "обновлено";
            }
        }

        public static ControlTemplate CreateFlatProgressBarTemplate() {
            var template = new ControlTemplate(typeof(ProgressBar));
            var root = new FrameworkElementFactory(typeof(Border), "PART_Track");
            root.SetValue(Border.CornerRadiusProperty, new CornerRadius(2.5));
            root.SetValue(Border.BackgroundProperty, new TemplateBindingExtension(Control.BackgroundProperty));
            root.SetValue(Border.BorderThicknessProperty, new Thickness(0));
            root.SetValue(Border.ClipToBoundsProperty, true);

            var indicator = new FrameworkElementFactory(typeof(Border), "PART_Indicator");
            indicator.SetValue(Border.HorizontalAlignmentProperty, HorizontalAlignment.Left);
            indicator.SetValue(Border.CornerRadiusProperty, new CornerRadius(2.5));
            indicator.SetValue(Border.BackgroundProperty, new TemplateBindingExtension(Control.ForegroundProperty));

            root.AppendChild(indicator);
            template.VisualTree = root;
            return template;
        }

        public static ProgressBar CreateProgressBar(double height, Thickness margin, string tooltip) {
            var bar = new ProgressBar {
                Height = height,
                Margin = margin,
                Background = BarTrackBrush,
                Foreground = GetProgressBrush(0.0),
                BorderThickness = new Thickness(0),
                ToolTip = tooltip,
                Minimum = 0,
                Maximum = 100,
                Value = 0,
                Template = CreateFlatProgressBarTemplate()
            };
            ToolTipService.SetInitialShowDelay(bar, 100);
            return bar;
        }
    }
}
