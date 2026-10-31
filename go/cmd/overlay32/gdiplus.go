package main

import (
	"math"
	"syscall"
)

var (
	gdiplus  = syscall.NewLazyDLL("gdiplus.dll")
	user32   = syscall.NewLazyDLL("user32.dll")
	gdi32    = syscall.NewLazyDLL("gdi32.dll")
	kernel32 = syscall.NewLazyDLL("kernel32.dll")

	// GDI+ APIs
	procGdiplusStartup               = gdiplus.NewProc("GdiplusStartup")
	procGdiplusShutdown              = gdiplus.NewProc("GdiplusShutdown")
	procGdipCreateBitmapFromScan0    = gdiplus.NewProc("GdipCreateBitmapFromScan0")
	procGdipGetImageGraphicsContext  = gdiplus.NewProc("GdipGetImageGraphicsContext")
	procGdipDisposeImage             = gdiplus.NewProc("GdipDisposeImage")
	procGdipDeleteGraphics           = gdiplus.NewProc("GdipDeleteGraphics")
	procGdipGraphicsClear            = gdiplus.NewProc("GdipGraphicsClear")
	procGdipSetSmoothingMode         = gdiplus.NewProc("GdipSetSmoothingMode")
	procGdipSetTextRenderingHint     = gdiplus.NewProc("GdipSetTextRenderingHint")
	procGdipCreateSolidFill          = gdiplus.NewProc("GdipCreateSolidFill")
	procGdipDeleteBrush              = gdiplus.NewProc("GdipDeleteBrush")
	procGdipCreatePen1               = gdiplus.NewProc("GdipCreatePen1")
	procGdipDeletePen                = gdiplus.NewProc("GdipDeletePen")
	procGdipCreatePath               = gdiplus.NewProc("GdipCreatePath")
	procGdipDeletePath               = gdiplus.NewProc("GdipDeletePath")
	procGdipAddPathArc               = gdiplus.NewProc("GdipAddPathArc")
	procGdipAddPathLine              = gdiplus.NewProc("GdipAddPathLine")
	procGdipClosePathFigure          = gdiplus.NewProc("GdipClosePathFigure")
	procGdipFillPath                 = gdiplus.NewProc("GdipFillPath")
	procGdipDrawPath                 = gdiplus.NewProc("GdipDrawPath")
	procGdipDrawLine                 = gdiplus.NewProc("GdipDrawLine")
	procGdipCreateFontFamilyFromName = gdiplus.NewProc("GdipCreateFontFamilyFromName")
	procGdipDeleteFontFamily         = gdiplus.NewProc("GdipDeleteFontFamily")
	procGdipCreateFont               = gdiplus.NewProc("GdipCreateFont")
	procGdipDeleteFont               = gdiplus.NewProc("GdipDeleteFont")
	procGdipCreateStringFormat       = gdiplus.NewProc("GdipCreateStringFormat")
	procGdipDeleteStringFormat       = gdiplus.NewProc("GdipDeleteStringFormat")
	procGdipSetStringFormatAlign     = gdiplus.NewProc("GdipSetStringFormatAlign")
	procGdipSetStringFormatLineAlign = gdiplus.NewProc("GdipSetStringFormatLineAlign")
	procGdipDrawString               = gdiplus.NewProc("GdipDrawString")
	procGdipMeasureString            = gdiplus.NewProc("GdipMeasureString")

	// GDI32 APIs
	procCreateCompatibleDC = gdi32.NewProc("CreateCompatibleDC")
	procCreateDIBSection   = gdi32.NewProc("CreateDIBSection")
	procSelectObject       = gdi32.NewProc("SelectObject")
	procDeleteObject       = gdi32.NewProc("DeleteObject")
	procDeleteDC           = gdi32.NewProc("DeleteDC")

	// User32 APIs
	procRegisterClassExW    = user32.NewProc("RegisterClassExW")
	procCreateWindowExW     = user32.NewProc("CreateWindowExW")
	procDefWindowProcW      = user32.NewProc("DefWindowProcW")
	procShowWindow          = user32.NewProc("ShowWindow")
	procDestroyWindow       = user32.NewProc("DestroyWindow")
	procPostQuitMessage     = user32.NewProc("PostQuitMessage")
	procGetMessageW         = user32.NewProc("GetMessageW")
	procTranslateMessage    = user32.NewProc("TranslateMessage")
	procDispatchMessageW    = user32.NewProc("DispatchMessageW")
	procPostMessageW        = user32.NewProc("PostMessageW")
	procSetWindowLongPtrW   = user32.NewProc("SetWindowLongPtrW")
	procGetWindowLongPtrW   = user32.NewProc("GetWindowLongPtrW")
	procSetWindowPos        = user32.NewProc("SetWindowPos")
	procGetWindowRect       = user32.NewProc("GetWindowRect")
	procUpdateLayeredWindow = user32.NewProc("UpdateLayeredWindow")
	procRegisterHotKey      = user32.NewProc("RegisterHotKey")
	procUnregisterHotKey    = user32.NewProc("UnregisterHotKey")
	procGetSystemMetrics    = user32.NewProc("GetSystemMetrics")

	// Kernel32
	procGetModuleHandleW = kernel32.NewProc("GetModuleHandleW")
)

type GdiplusStartupInput struct {
	GdiplusVersion           uint32
	DebugEventCallback       uintptr
	SuppressBackgroundThread bool
	SuppressExternalCodecs   bool
}

type BITMAPINFOHEADER struct {
	BiSize          uint32
	BiWidth         int32
	BiHeight        int32
	BiPlanes        uint16
	BiBitCount      uint16
	BiCompression   uint32
	BiSizeImage     uint32
	BiXPelsPerMeter int32
	BiYPelsPerMeter int32
	BiClrUsed       uint32
	BiClrImportant  uint32
}

type BLENDFUNCTION struct {
	BlendOp             byte
	BlendFlags          byte
	SourceConstantAlpha byte
	AlphaFormat         byte
}

type POINT struct {
	X, Y int32
}

type SIZE struct {
	CX, CY int32
}

type RECT struct {
	Left, Top, Right, Bottom int32
}

type RectF struct {
	X, Y, Width, Height float32
}

type WNDCLASSEX struct {
	CbSize        uint32
	Style         uint32
	LpfnWndProc   uintptr
	CbClsExtra    int32
	CbWndExtra    int32
	HInstance     uintptr
	HIcon         uintptr
	HCursor       uintptr
	HbrBackground uintptr
	LpszMenuName  *uint16
	LpszClassName *uint16
	HIconSm       uintptr
}

type MSG struct {
	Hwnd     uintptr
	Message  uint32
	WParam   uintptr
	LParam   uintptr
	Time     uint32
	Pt       POINT
	LPrivate uint32
}

const (
	// GDI+ PixelFormat
	PixelFormat32bppPARGB = 0x000E200B // Premultiplied 32-bit ARGB

	// GDI+ Smoothing and Text hints
	SmoothingModeAntiAlias       = 4
	TextRenderingHintAntiAlias   = 4
	TextRenderingHintClearType   = 5

	// GDI+ Units
	UnitPixel = 2
	UnitPoint = 3

	// UpdateLayeredWindow
	ULW_ALPHA    = 0x00000002
	AC_SRC_OVER  = 0x00
	AC_SRC_ALPHA = 0x01

	// Win32 Styles & ExStyles
	WS_POPUP          = 0x80000000
	WS_EX_LAYERED     = 0x00080000
	WS_EX_TRANSPARENT = 0x00000020
	WS_EX_TOOLWINDOW  = 0x00000080
	WS_EX_TOPMOST     = 0x00000008
	WS_EX_NOACTIVATE  = 0x08000000

	GWL_EXSTYLE = uintptr(0xFFFFFFFFFFFFFFEC) // -20

	SWP_NOSIZE       = 0x0001
	SWP_NOMOVE       = 0x0002
	SWP_NOZORDER     = 0x0004
	SWP_FRAMECHANGED = 0x0020
	SWP_NOACTIVATE   = 0x0010

	SW_HIDE = 0
	SW_SHOW = 5

	// Hit test
	WM_DESTROY     = 0x0002
	WM_MOVE        = 0x0003
	WM_EXITSIZEMOVE = 0x0232
	WM_NCHITTEST   = 0x0084
	HTCAPTION      = 2
	HTCLIENT       = 1

	// Hotkey
	WM_HOTKEY  = 0x0312
	MOD_ALT    = 0x0001
	MOD_CONTROL = 0x0002
	VK_L       = 0x4C
	HOTKEY_ID  = 101

	// Custom messages
	WM_USER_RENDER = 0x0400 + 1
)

func argb(a, r, g, b uint8) uint32 {
	return (uint32(a) << 24) | (uint32(r) << 16) | (uint32(g) << 8) | uint32(b)
}

func fbits(v float32) uintptr {
	return uintptr(math.Float32bits(v))
}
