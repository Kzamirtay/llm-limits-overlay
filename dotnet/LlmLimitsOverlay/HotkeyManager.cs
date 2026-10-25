using System;
using System.Runtime.InteropServices;
using System.Windows.Forms;
using System.Windows.Interop;

namespace LlmLimitsOverlay {
    public class ParsedHotkey {
        public uint Modifiers { get; set; }
        public uint VirtualKey { get; set; }
        public string Display { get; set; }
    }

    public static class HotkeyManager {
        private static Action _hotkeyCallback;
        private static HwndSourceHook _hotkeyHookDelegate;
        private static HwndSource _attachedSource;
        private static IntPtr _hotkeyHwnd = IntPtr.Zero;
        private static bool _isHotkeyRegistered = false;

        public static ParsedHotkey ParseHotkeyString(string hotkeyStr) {
            if (string.IsNullOrEmpty(hotkeyStr) || string.IsNullOrEmpty(hotkeyStr.Trim())) {
                return null;
            }

            uint mod = 0;
            uint vk = 0;
            string[] rawParts = hotkeyStr.Split('+');
            var parts = new System.Collections.Generic.List<string>();
            foreach (string p in rawParts) {
                string trimmed = p.Trim();
                if (!string.IsNullOrEmpty(trimmed)) {
                    parts.Add(trimmed);
                }
            }

            for (int i = 0; i < parts.Count; i++) {
                string p = parts[i];
                string lower = p.ToLowerInvariant();

                if (lower == "ctrl" || lower == "control") {
                    mod |= NativeMethods.MOD_CONTROL;
                    continue;
                }
                if (lower == "alt" || lower == "menu") {
                    mod |= NativeMethods.MOD_ALT;
                    continue;
                }
                if (lower == "shift") {
                    mod |= NativeMethods.MOD_SHIFT;
                    continue;
                }
                if (lower == "win" || lower == "windows") {
                    mod |= NativeMethods.MOD_WIN;
                    continue;
                }

                if (i == parts.Count - 1) {
                    if (p.Length == 1) {
                        char ch = char.ToUpperInvariant(p[0]);
                        if ((ch >= 'A' && ch <= 'Z') || (ch >= '0' && ch <= '9')) {
                            vk = (uint)ch;
                        }
                    } else if (p.StartsWith("F", StringComparison.OrdinalIgnoreCase)) {
                        int fNum;
                        if (int.TryParse(p.Substring(1), out fNum) && fNum >= 1 && fNum <= 24) {
                            vk = (uint)(0x70 + (fNum - 1));
                        }
                    } else {
                        try {
                            Keys keyEnum;
                            if (Enum.TryParse<Keys>(p, true, out keyEnum)) {
                                vk = (uint)keyEnum;
                            }
                        } catch { }
                    }
                }
            }

            if (vk == 0) return null;

            return new ParsedHotkey {
                Modifiers = mod,
                VirtualKey = vk,
                Display = string.Join("+", parts.ToArray())
            };
        }

        private static IntPtr HwndHook(IntPtr hwnd, int msg, IntPtr wParam, IntPtr lParam, ref bool handled) {
            if (msg == NativeMethods.WM_HOTKEY && wParam.ToInt32() == NativeMethods.HOTKEY_ID) {
                handled = true;
                if (_hotkeyCallback != null) {
                    try {
                        _hotkeyCallback();
                    } catch { }
                }
            }
            return IntPtr.Zero;
        }

        public static bool RegisterGlobalHotkey(IntPtr hWnd, uint modifiers, uint vk, Action callback) {
            UnregisterGlobalHotkey();
            _hotkeyCallback = callback;
            _hotkeyHwnd = hWnd;

            try {
                _attachedSource = HwndSource.FromHwnd(hWnd);
                if (_attachedSource != null) {
                    _hotkeyHookDelegate = new HwndSourceHook(HwndHook);
                    _attachedSource.AddHook(_hotkeyHookDelegate);
                }
            } catch { }

            bool ok = NativeMethods.RegisterHotKey(hWnd, NativeMethods.HOTKEY_ID, modifiers | NativeMethods.MOD_NOREPEAT, vk);
            if (!ok) {
                ok = NativeMethods.RegisterHotKey(hWnd, NativeMethods.HOTKEY_ID, modifiers, vk);
            }
            _isHotkeyRegistered = ok;
            return ok;
        }

        public static void UnregisterGlobalHotkey() {
            if (_isHotkeyRegistered && _hotkeyHwnd != IntPtr.Zero) {
                try {
                    NativeMethods.UnregisterHotKey(_hotkeyHwnd, NativeMethods.HOTKEY_ID);
                } catch { }
                _isHotkeyRegistered = false;
            }
            _hotkeyHwnd = IntPtr.Zero;

            if (_attachedSource != null && _hotkeyHookDelegate != null) {
                try {
                    _attachedSource.RemoveHook(_hotkeyHookDelegate);
                } catch { }
                _attachedSource = null;
                _hotkeyHookDelegate = null;
            }
            _hotkeyCallback = null;
        }
    }
}
