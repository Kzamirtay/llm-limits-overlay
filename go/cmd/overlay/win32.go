//go:build windows

package main

import (
	"context"
	"fmt"
	"runtime"
	"sync/atomic"
	"time"
	"unsafe"

	"llmlimits/internal/fetchers"

	"github.com/wailsapp/go-webview2/webviewloader"
	wailsrt "github.com/wailsapp/wails/v2/pkg/runtime"
	"golang.org/x/sys/windows"
)

const (
	gwlStyle        uintptr = 0xFFFFFFFFFFFFFFF0 // -16 (GWL_STYLE)
	gwlExStyle      uintptr = 0xFFFFFFFFFFFFFFEC // -20 (GWL_EXSTYLE)
	wsCaption               = 0x00C00000
	wsThickFrame            = 0x00040000
	wsSysMenu               = 0x00080000
	wsBorder                = 0x00800000

	wsExTransparent         = 0x00000020
	wsExLayered             = 0x00080000

	swpNoSize       = 0x0001
	swpNoMove       = 0x0002
	swpNoZOrder     = 0x0004
	swpFrameChanged = 0x0020

	modControl = 0x0002
	modAlt     = 0x0001
	vkL        = 0x4C
	hotkeyID   = 1

	wmHotkey = 0x0312
)

func initWindowStyles(hwnd uintptr) {
	if hwnd == 0 {
		return
	}
	r, _, _ := procGetWindowLongPtrW.Call(hwnd, gwlStyle)
	style := uintptr(r)
	style &^= (wsCaption | wsThickFrame | wsSysMenu | wsBorder)
	procSetWindowLongPtrW.Call(hwnd, gwlStyle, style)
	procSetWindowPos.Call(hwnd, 0, 0, 0, 0, 0, uintptr(swpNoSize|swpNoMove|swpNoZOrder|swpFrameChanged))
}

var (
	user32                       = windows.NewLazySystemDLL("user32.dll")
	procFindWindowW              = user32.NewProc("FindWindowW")
	procGetWindowLongPtrW        = user32.NewProc("GetWindowLongPtrW")
	procSetWindowLongPtrW        = user32.NewProc("SetWindowLongPtrW")
	procSetWindowPos             = user32.NewProc("SetWindowPos")
	procGetSystemMetrics         = user32.NewProc("GetSystemMetrics")
	procRegisterHotKey           = user32.NewProc("RegisterHotKey")
	procUnregisterHotKey         = user32.NewProc("UnregisterHotKey")
	procGetMessageW              = user32.NewProc("GetMessageW")
	procGetWindowThreadProcessId = user32.NewProc("GetWindowThreadProcessId")
	procEnumWindows              = user32.NewProc("EnumWindows")
)

type wmsg struct {
	Hwnd    uintptr
	Message uint32
	WParam  uintptr
	LParam  uintptr
	Time    uint32
	Pt      struct{ X, Y int32 }
}

func findOverlayHWND(title string) uintptr {
	titlePtr, _ := windows.UTF16PtrFromString(title)
	r, _, _ := procFindWindowW.Call(0, uintptr(unsafe.Pointer(titlePtr)))
	if r != 0 {
		return r
	}

	var foundHWND uintptr
	pid := uint32(windows.GetCurrentProcessId())
	cb := windows.NewCallback(func(hwnd uintptr, lparam uintptr) uintptr {
		var wndPid uint32
		procGetWindowThreadProcessId.Call(hwnd, uintptr(unsafe.Pointer(&wndPid)))
		if wndPid == pid {
			foundHWND = hwnd
			return 0 // stop
		}
		return 1 // continue
	})
	procEnumWindows.Call(cb, 0)
	return foundHWND
}

func getOverlayHWNDWithRetry(title string) uintptr {
	for i := 0; i < 30; i++ {
		h := findOverlayHWND(title)
		if h != 0 {
			return h
		}
		time.Sleep(100 * time.Millisecond)
	}
	return 0
}

func setClickThrough(hwnd uintptr, enable bool) {
	if hwnd == 0 {
		return
	}
	r, _, _ := procGetWindowLongPtrW.Call(hwnd, gwlExStyle)
	style := uintptr(r)
	if enable {
		style |= wsExTransparent | wsExLayered
	} else {
		style &^= wsExTransparent
	}
	procSetWindowLongPtrW.Call(hwnd, gwlExStyle, style)
	procSetWindowPos.Call(hwnd, 0, 0, 0, 0, 0, uintptr(swpNoSize|swpNoMove|swpNoZOrder|swpFrameChanged))
}

func runHotkeyLoop(title string, state *atomic.Bool) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	r, _, err := procRegisterHotKey.Call(0, uintptr(hotkeyID), uintptr(modControl|modAlt), uintptr(vkL))
	if r == 0 {
		fmt.Println("hotkey: RegisterHotKey failed:", err)
		return
	}
	fmt.Println("hotkey: Ctrl+Alt+L registered")
	defer procUnregisterHotKey.Call(0, uintptr(hotkeyID))

	var msg wmsg
	for {
		res, _, _ := procGetMessageW.Call(uintptr(unsafe.Pointer(&msg)), 0, 0, 0)
		if int32(res) <= 0 {
			return
		}
		if msg.Message == wmHotkey && msg.WParam == hotkeyID {
			now := !state.Load()
			state.Store(now)
			hwnd := findOverlayHWND(title)
			setClickThrough(hwnd, now)
			updateTrayClickThrough(now)
			if cfg != nil {
				cfg.SaveClickThrough(now)
			}
			fmt.Println("hotkey: click-through =", now)
		}
	}
}

func initPlatformWindow(title string) {
	hwnd := getOverlayHWNDWithRetry(title)
	if hwnd != 0 {
		initWindowStyles(hwnd)
		if clickThrough.Load() {
			setClickThrough(hwnd, true)
		}
	}
}

// positionBottomRight — правый-нижний угол рабочей области (primary).
func positionBottomRight(ctx context.Context) {
	rW, _, _ := procGetSystemMetrics.Call(0) // SM_CXSCREEN
	rH, _, _ := procGetSystemMetrics.Call(1) // SM_CYSCREEN
	w := int(rW)
	h := int(rH)
	x := w - 290 - 14
	y := h - 402 - 4
	wailsrt.WindowSetPosition(ctx, x, y)
	fmt.Printf("position: screen %dx%d -> window (%d,%d)\n", w, h, x, y)

	initPlatformWindow(windowTitle)
}

func checkWebView2Runtime(dir string) {
	ver, err := webviewloader.GetAvailableCoreWebView2BrowserVersionString("")
	if err != nil {
		fetchers.LogLine(dir, fmt.Sprintf("[wails] WebView2 runtime check returned error: %v", err))
		return
	}
	if ver == "" {
		fetchers.LogLine(dir, "[wails] WebView2 runtime is NOT installed on this machine! Window cannot be displayed.")
	} else {
		fetchers.LogLine(dir, fmt.Sprintf("[wails] WebView2 runtime detected: version %s", ver))
	}
}
