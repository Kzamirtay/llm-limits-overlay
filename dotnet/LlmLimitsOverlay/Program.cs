using System;
using System.Diagnostics;
using System.Globalization;
using System.IO;
using System.Windows;

namespace LlmLimitsOverlay {
    public class App : Application {
        public static string ResolveAppDirectory() {
            // v1.2.0+: native C# fetcher, no fetch_quota.py.
            // The app directory is always the directory containing the exe.
            string dir = AppDomain.CurrentDomain.BaseDirectory;
            // Walk up to the directory containing overlay.config.json or .env as a sanity check
            if (File.Exists(Path.Combine(dir, "overlay.config.json")) || File.Exists(Path.Combine(dir, ".env"))) {
                return Path.GetFullPath(dir);
            }
            string parent = Path.GetFullPath(Path.Combine(dir, ".."));
            if (File.Exists(Path.Combine(parent, "overlay.config.json")) || File.Exists(Path.Combine(parent, ".env"))) {
                return parent;
            }
            return Path.GetFullPath(dir);
        }

        [STAThread]
        public static void Main(string[] args) {
            string appDir = ResolveAppDirectory();
            DateTime processStartTime = DateTime.UtcNow;

            Logger.Initialize(appDir);
            Config.Initialize(appDir);

            AppDomain.CurrentDomain.UnhandledException += (s, e) => {
                Logger.Log("CRITICAL AppDomain Exception: " + e.ExceptionObject);
            };

            int currentPid = Process.GetCurrentProcess().Id;
            string currentExePath = "";
            try {
                currentExePath = Process.GetCurrentProcess().MainModule.FileName;
            } catch { }

            // Clean up any stale command file from prior runs immediately on startup
            string staleActionFile = Path.Combine(appDir, ".tray-action");
            if (File.Exists(staleActionFile)) {
                try { File.Delete(staleActionFile); } catch { }
            }

            // Check if CLI flags were provided
            bool verifyPos = false;
            bool dumpModel = false;
            bool cliTestHooks = false;
            for (int i = 0; i < args.Length; i++) {
                if (string.Equals(args[i], "-VerifyPosition", StringComparison.OrdinalIgnoreCase)) {
                    verifyPos = true;
                } else if (string.Equals(args[i], "-DumpModel", StringComparison.OrdinalIgnoreCase)) {
                    dumpModel = true;
                } else if (string.Equals(args[i], "-TestHooks", StringComparison.OrdinalIgnoreCase)) {
                    cliTestHooks = true;
                } else if (string.Equals(args[i], "-CapturePid", StringComparison.OrdinalIgnoreCase) && i + 2 < args.Length) {
                    int targetPid;
                    if (int.TryParse(args[i + 1], out targetPid)) {
                        string dest = args[i + 2];
                        CaptureHelper.CaptureProcess(targetPid, dest);
                        return;
                    }
                }
            }

            if (verifyPos) {
                var posWindow = new OverlayWindow(appDir, Config.Load(), processStartTime);
                posWindow.UpdateLayout();
                posWindow.UpdateOverlayPosition();
                var wa = SystemParameters.WorkArea;
                string json = string.Format(CultureInfo.InvariantCulture,
                    "{{\"WindowLeft\":{0},\"WindowTop\":{1},\"WindowWidth\":{2},\"WindowHeight\":{3},\"WorkAreaRight\":{4},\"WorkAreaBottom\":{5},\"RightOffset\":{6},\"BottomOffset\":{7}}}",
                    posWindow.Left, posWindow.Top, posWindow.ActualWidth, posWindow.ActualHeight,
                    wa.Right, wa.Bottom, wa.Right - (posWindow.Left + posWindow.ActualWidth), wa.Bottom - (posWindow.Top + posWindow.ActualHeight));
                Console.WriteLine(json);
                posWindow.ExitOverlay();
                return;
            }

            if (dumpModel) {
                var fetcher = new QuotaFetcher(appDir, null, null);
                var snap = fetcher.LoadCacheFromDisk(null);
                if (snap != null) {
                    var modelDict = new System.Collections.Generic.Dictionary<string, object>();
                    modelDict["status"] = snap.Status;
                    modelDict["last_updated"] = snap.LastUpdated;
                    modelDict["kimi_7d_percent"] = snap.Kimi.Limit7d.Percent;
                    modelDict["kimi_7d_reset"] = snap.Kimi.Limit7d.ResetIn;
                    modelDict["kimi_5h_percent"] = snap.Kimi.Limit5h.Percent;
                    modelDict["kimi_5h_reset"] = snap.Kimi.Limit5h.ResetIn;
                    modelDict["openrouter_percent"] = snap.OpenRouter.UsedPercent;
                    modelDict["openrouter_usage"] = snap.OpenRouter.Usage;
                    modelDict["openrouter_remaining"] = snap.OpenRouter.LimitRemaining;
                    modelDict["gemini_weekly_percent"] = snap.GeminiWeekly.Percent;
                    modelDict["gemini_5h_percent"] = snap.Gemini5h.Percent;
                    modelDict["claude_weekly_percent"] = snap.ClaudeWeekly.Percent;
                    modelDict["claude_5h_percent"] = snap.Claude5h.Percent;
                    Console.WriteLine(SimpleJson.Serialize(modelDict, true));
                }
                return;
            }

            Logger.Log(string.Format("=== LLM Limits Overlay Starting (PID={0}) ===", currentPid));

            // Single instance enforcement: terminate older overlay instances strictly from this directory/worktree
            try {
                Process[] procs = Process.GetProcessesByName("LlmLimitsOverlay");
                foreach (Process p in procs) {
                    if (p.Id == currentPid) continue;
                    try {
                        bool isSame = false;
                        string otherPath = null;
                        try {
                            otherPath = p.MainModule.FileName;
                        } catch { }

                        if (!string.IsNullOrEmpty(otherPath) && !string.IsNullOrEmpty(currentExePath)) {
                            if (string.Equals(Path.GetFullPath(otherPath), Path.GetFullPath(currentExePath), StringComparison.OrdinalIgnoreCase)) {
                                isSame = true;
                            } else if (string.Equals(Path.GetDirectoryName(otherPath), appDir, StringComparison.OrdinalIgnoreCase)) {
                                isSame = true;
                            }
                        } else {
                            // Match by window handle PID
                            IntPtr cand = NativeMethods.FindWindow(null, "LLM Limits Overlay");
                            if (cand != IntPtr.Zero) {
                                uint candPid;
                                NativeMethods.GetWindowThreadProcessId(cand, out candPid);
                                if (candPid == p.Id) isSame = true;
                            }
                        }

                        if (isSame) {
                            int priorPid = p.Id;
                            Logger.Log(string.Format("Terminating prior overlay instance (PID={0}).", priorPid));

                            IntPtr hOld = IntPtr.Zero;
                            try {
                                if (p.MainWindowHandle != IntPtr.Zero) hOld = p.MainWindowHandle;
                            } catch { }

                            if (hOld == IntPtr.Zero) {
                                IntPtr cand = NativeMethods.FindWindow(null, "LLM Limits Overlay");
                                if (cand != IntPtr.Zero) {
                                    uint candPid;
                                    NativeMethods.GetWindowThreadProcessId(cand, out candPid);
                                    if (candPid == priorPid) hOld = cand;
                                }
                            }

                            if (hOld != IntPtr.Zero) {
                                NativeMethods.PostMessage(hOld, NativeMethods.WM_CLOSE, IntPtr.Zero, IntPtr.Zero);
                            }

                            if (!p.WaitForExit(1500)) {
                                try { p.Kill(); } catch { }
                            }
                        }
                    } catch { }
                }
            } catch (Exception ex) {
                Logger.Log("Exception during single instance enforcement: " + ex.Message);
            }

            Config config = Config.Load();
            if (cliTestHooks) {
                config.TestHooks = true;
            }
            var app = new App();
            var window = new OverlayWindow(appDir, config, processStartTime);
            window.Show();
            app.Run(window);
        }
    }

    public static class CaptureHelper {
        public static bool CaptureProcess(int pid, string destPath) {
            IntPtr targetHwnd = IntPtr.Zero;
            NativeMethods.EnumWindows((hWnd, lp) => {
                uint p;
                NativeMethods.GetWindowThreadProcessId(hWnd, out p);
                if (p == pid && NativeMethods.IsWindowVisible(hWnd)) {
                    NativeMethods.RECT r;
                    if (NativeMethods.GetWindowRect(hWnd, out r)) {
                        int w = r.Right - r.Left;
                        int h = r.Bottom - r.Top;
                        if (w > 100 && h > 100) {
                            targetHwnd = hWnd;
                            return false;
                        }
                    }
                }
                return true;
            }, IntPtr.Zero);

            if (targetHwnd == IntPtr.Zero) {
                Console.WriteLine("Window not found for PID " + pid);
                return false;
            }

            NativeMethods.RECT rc;
            NativeMethods.GetWindowRect(targetHwnd, out rc);
            int width = rc.Right - rc.Left;
            int height = rc.Bottom - rc.Top;

            using (var bmp = new System.Drawing.Bitmap(width, height, System.Drawing.Imaging.PixelFormat.Format32bppArgb)) {
                using (var g = System.Drawing.Graphics.FromImage(bmp)) {
                    IntPtr hdc = g.GetHdc();
                    NativeMethods.PrintWindow(targetHwnd, hdc, NativeMethods.PW_RENDERFULLCONTENT);
                    g.ReleaseHdc(hdc);
                }
                string dir = Path.GetDirectoryName(destPath);
                if (!string.IsNullOrEmpty(dir) && !Directory.Exists(dir)) {
                    Directory.CreateDirectory(dir);
                }
                bmp.Save(destPath, System.Drawing.Imaging.ImageFormat.Png);
                Console.WriteLine(string.Format("Captured HWND={0} ({1}x{2}) to {3}", targetHwnd, width, height, destPath));
                return true;
            }
        }
    }
}
