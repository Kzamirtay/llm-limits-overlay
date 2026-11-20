//go:build windows

package main

import (
	"fmt"
	"syscall"
	"unsafe"

	"llmlimits/internal/fetchers"
	"llmlimits/internal/model"
	"llmlimits/internal/trayui"

	"golang.org/x/sys/windows"
)

// Свой слой трея вместо fyne.io/systray: библиотека держит ровно один
// NOTIFYICONDATA и своё меню, переиспользовать его для нескольких значков
// нельзя. Здесь значки создаются напрямую через Shell_NotifyIconW: по одному
// на провайдера (у каждого свой процент и своя подсказка), HICON собирается
// в памяти (CreateIconIndirect), без временных файлов в %TEMP%.
var (
	shell32 = syscall.NewLazyDLL("shell32.dll")

	procShellNotifyIconW = shell32.NewProc("Shell_NotifyIconW")
	procCreatePopupMenu  = user32.NewProc("CreatePopupMenu")
	procAppendMenuW      = user32.NewProc("AppendMenuW")
	procTrackPopupMenu   = user32.NewProc("TrackPopupMenu")
	procDestroyMenu      = user32.NewProc("DestroyMenu")
	procSetForeground    = user32.NewProc("SetForegroundWindow")
	procGetCursorPos     = user32.NewProc("GetCursorPos")
	procCreateIconInd    = user32.NewProc("CreateIconIndirect")
	procDestroyIcon      = user32.NewProc("DestroyIcon")
	procCreateBitmap     = gdi32.NewProc("CreateBitmap")
)

const (
	trayClassName = "LLMLimitsTray32Class"

	// trayCallbackMsg — сообщение, которое оболочка шлёт в наше окно-приёмник
	// при клике по значку. lParam = WM_LBUTTONUP / WM_RBUTTONUP / …
	trayCallbackMsg   = 0x0400 + 8
	WM_USER_TRAY_SYNC = 0x0400 + 9
	// WM_USER_STRIP_TICK — раз в секунду пересчитать позицию полосы на панели
	// задач (разрешение, DPI, ширина трея, автопрятание).
	WM_USER_STRIP_TICK = 0x0400 + 10
	WM_NULL_MSG        = 0x0000
	WM_LBUTTONUP_MSG   = 0x0202
	WM_LBUTTONDBLCLK   = 0x0203
	WM_RBUTTONUP_MSG   = 0x0205
	WM_CONTEXTMENU_MSG = 0x007B

	NIM_ADD    = 0x00000000
	NIM_MODIFY = 0x00000001
	NIM_DELETE = 0x00000002

	NIF_MESSAGE = 0x00000001
	NIF_ICON    = 0x00000002
	NIF_TIP     = 0x00000004
	NIF_INFO    = 0x00000010
	NIIF_NONE   = 0x00000000

	MF_STRING    = 0x00000000
	MF_SEPARATOR = 0x00000800
	MF_GRAYED    = 0x00000001
	MF_CHECKED   = 0x00000008

	TPM_RIGHTBUTTON = 0x0002
	TPM_RETURNCMD   = 0x0100

	// Команды контекстного меню.
	cmdRefresh          = 1
	cmdToggleVisibility = 2
	cmdClickThrough     = 3
	cmdResetPosition    = 4
	cmdQuit             = 5
)

// NOTIFYICONDATA — представление Vista+ (cbSize = sizeof(struct)).
type NOTIFYICONDATA struct {
	CbSize           uint32
	HWnd             uintptr
	UID              uint32
	UFlags           uint32
	UCallbackMessage uint32
	HIcon            uintptr
	SzTip            [128]uint16
	DwState          uint32
	DwStateMask      uint32
	SzInfo           [256]uint16
	UVersion         uint32
	SzInfoTitle      [64]uint16
	DwInfoFlags      uint32
	GuidItem         windows.GUID
	HBalloonIcon     uintptr
}

// ICONINFO — исходники для CreateIconIndirect.
type ICONINFO struct {
	FIcon    int32
	XHotspot uint32
	YHotspot uint32
	HbmMask  uintptr
	HbmColor uintptr
}

// trayIcon — один значок в трее и состояние, по которому он нарисован.
type trayIcon struct {
	id      uint32
	hicon   uintptr
	iconKey string
	tip     string
}

var (
	trayHWND    uintptr
	trayIcons   []*trayIcon
	traySummary trayui.Summary
	trayReady   bool
	// trayShowIcons — нужны ли значки в области уведомлений. В режиме полосы
	// на панели задач они выключены и включаются только как откат.
	trayShowIcons = true
)

// trayStart создаёт окно-приёмник сообщений трея. Значки добавляются позже —
// по числу строк сводки (см. trayApplySummary): до первого фетча рисовать
// нечего, а пустые кольца выглядели бы как выдуманный «0 %».
func trayStart() error {
	if trayReady {
		return nil
	}
	hInst, _, _ := procGetModuleHandleW.Call(0)
	className, _ := windows.UTF16PtrFromString(trayClassName)
	title, _ := windows.UTF16PtrFromString("LLM Limits Tray")

	wc := WNDCLASSEX{
		CbSize:        uint32(unsafe.Sizeof(WNDCLASSEX{})),
		LpfnWndProc:   syscall.NewCallback(trayWndProc),
		HInstance:     hInst,
		LpszClassName: className,
	}
	if r, _, err := procRegisterClassExW.Call(uintptr(unsafe.Pointer(&wc))); r == 0 {
		return fmt.Errorf("RegisterClassExW(tray): %v", err)
	}

	hwnd, _, err := procCreateWindowExW.Call(
		0,
		uintptr(unsafe.Pointer(className)),
		uintptr(unsafe.Pointer(title)),
		uintptr(WS_POPUP),
		0, 0, 0, 0,
		0, 0, hInst, 0,
	)
	if hwnd == 0 {
		return fmt.Errorf("CreateWindowExW(tray): %v", err)
	}
	trayHWND = hwnd
	trayReady = true
	fetchers.LogLine(appDir, "[overlay32] tray window ready (own Shell_NotifyIcon)")
	return nil
}

// trayWndProc — единственный поток, который трогает значки и меню.
func trayWndProc(hwnd uintptr, msg uint32, wparam, lparam uintptr) uintptr {
	switch msg {
	case trayCallbackMsg:
		// Классический (не version-4) режим: lParam — сообщение мыши,
		// wParam — uID значка.
		switch uint32(lparam) & 0xFFFF {
		case WM_LBUTTONUP_MSG, WM_LBUTTONDBLCLK:
			requestToggleVisibility()
		case WM_RBUTTONUP_MSG, WM_CONTEXTMENU_MSG:
			showTrayMenu(hwnd)
		}
		return 0

	case WM_USER_TRAY_SYNC:
		snapMu.RLock()
		snap := lastSnapshot
		snapMu.RUnlock()
		trayApplySummary(trayui.BuildSummary(snap, cfg))
		return 0

	case WM_USER_STRIP_TICK:
		stripTick()
		return 0

	case WM_DESTROY:
		stripStop()
		trayShutdown()
		return 0
	}
	r, _, _ := procDefWindowProcW.Call(hwnd, uintptr(msg), wparam, lparam)
	return r
}

// updateTrayFromSnapshot — вызывается из горутины фетча: сам снапшот не
// передаём, только просим UI-поток перечитать lastSnapshot под мьютексом.
func updateTrayFromSnapshot(*model.Snapshot) {
	if trayHWND == 0 {
		return
	}
	procPostMessageW.Call(trayHWND, WM_USER_TRAY_SYNC, 0, 0)
}

// updateTrayVisibility: меню собирается заново при каждом открытии, поэтому
// синхронизировать заголовок «Показать/Скрыть» больше не нужно.
func updateTrayVisibility(bool) {}

// updateTrayClickThrough показывает balloon: подсказка значка живёт в своём
// лимите и режим в ней не помещается. Вызывается только из UI-потока.
func updateTrayClickThrough(enabled bool) {
	if len(trayIcons) == 0 {
		return
	}
	state := "выключены"
	if enabled {
		state = "включены"
	}
	trayBalloon("Клики насквозь", "Сквозные клики "+state)
}

// trayApplySummary приводит набор значков в соответствие со сводкой:
// по значку на строку (провайдер или блок AGY). Состав меняется только при
// смене конфига, но проверка дешёвая и делает код устойчивым к этому.
func trayApplySummary(sum trayui.Summary) {
	traySummary = sum
	// Полоса на панели задач и значки трея едят одну сводку: полоса рисуется
	// всегда, когда активна, значки — только если они включены режимом.
	if stripOn {
		stripUpdate(sum)
	}
	if !trayShowIcons {
		trayUseIcons(false)
		return
	}
	for len(trayIcons) > len(sum.Items) {
		last := trayIcons[len(trayIcons)-1]
		trayDelete(last)
		trayIcons = trayIcons[:len(trayIcons)-1]
	}
	for i, it := range sum.Items {
		if i >= len(trayIcons) {
			if !trayAdd(uint32(i+1), it) {
				return
			}
			continue
		}
		trayUpdate(trayIcons[i], it)
	}
}

// trayUseIcons включает или выключает значки в области уведомлений.
// Вызывается из UI-потока: при выключении значки снимаются сразу.
func trayUseIcons(on bool) {
	trayShowIcons = on
	if on {
		return
	}
	for _, icon := range trayIcons {
		trayDelete(icon)
	}
	trayIcons = nil
}

// trayAdd добавляет значок и возвращает false, если оболочка отказала.
func trayAdd(id uint32, it trayui.Item) bool {
	icon := &trayIcon{id: id}
	if !trayModify(icon, it, true) {
		return false
	}
	trayIcons = append(trayIcons, icon)
	return true
}

// trayUpdate обновляет подсказку и, если процент/тема/метка изменились,
// перерисовывает сам значок.
func trayUpdate(icon *trayIcon, it trayui.Item) {
	trayModify(icon, it, false)
}

// trayModify пишет NOTIFYICONDATA. add=true — NIM_ADD, иначе NIM_MODIFY.
func trayModify(icon *trayIcon, it trayui.Item, add bool) bool {
	tip := trayui.IconTip(it)
	key := trayIconKeyString(it)
	if add || key != icon.iconKey {
		hicon := buildTrayHIcon(it)
		if hicon == 0 {
			return false
		}
		if icon.hicon != 0 {
			procDestroyIcon.Call(icon.hicon)
		}
		icon.hicon = hicon
		icon.iconKey = key
	}

	nid := NOTIFYICONDATA{
		CbSize:           uint32(unsafe.Sizeof(NOTIFYICONDATA{})),
		HWnd:             trayHWND,
		UID:              icon.id,
		UFlags:           NIF_ICON | NIF_TIP | NIF_MESSAGE,
		UCallbackMessage: trayCallbackMsg,
		HIcon:            icon.hicon,
	}
	copyUTF16(nid.SzTip[:], tip)

	op := uintptr(NIM_MODIFY)
	if add {
		op = NIM_ADD
	}
	if r, _, err := procShellNotifyIconW.Call(op, uintptr(unsafe.Pointer(&nid))); r == 0 {
		fetchers.LogLine(appDir, fmt.Sprintf("[overlay32] Shell_NotifyIcon(uid=%d, add=%v) failed: %v", icon.id, add, err))
		return false
	}
	icon.tip = tip
	return true
}

// trayDelete убирает значок из трея и освобождает HICON.
func trayDelete(icon *trayIcon) {
	nid := NOTIFYICONDATA{
		CbSize: uint32(unsafe.Sizeof(NOTIFYICONDATA{})),
		HWnd:   trayHWND,
		UID:    icon.id,
	}
	procShellNotifyIconW.Call(NIM_DELETE, uintptr(unsafe.Pointer(&nid)))
	if icon.hicon != 0 {
		procDestroyIcon.Call(icon.hicon)
		icon.hicon = 0
	}
}

// trayShutdown снимает все значки при выходе.
func trayShutdown() {
	for _, icon := range trayIcons {
		trayDelete(icon)
	}
	trayIcons = nil
	trayReady = false
}

// trayBalloon показывает всплывающее уведомление от первого значка.
func trayBalloon(title, text string) {
	if len(trayIcons) == 0 {
		return
	}
	nid := NOTIFYICONDATA{
		CbSize:      uint32(unsafe.Sizeof(NOTIFYICONDATA{})),
		HWnd:        trayHWND,
		UID:         trayIcons[0].id,
		UFlags:      NIF_INFO,
		DwInfoFlags: NIIF_NONE,
	}
	copyUTF16(nid.SzInfoTitle[:], title)
	copyUTF16(nid.SzInfo[:], text)
	procShellNotifyIconW.Call(NIM_MODIFY, uintptr(unsafe.Pointer(&nid)))
}

// showTrayMenu строит меню заново: состояния «показан/скрыт» и «сквозные
// клики» берутся на момент открытия, а цифры — из последней сводки.
func showTrayMenu(hwnd uintptr) {
	menu, _, _ := procCreatePopupMenu.Call()
	if menu == 0 {
		return
	}
	defer procDestroyMenu.Call(menu)

	appendMenu(menu, MF_STRING|MF_GRAYED, 0, "LLM Limits v"+appVersion)
	appendMenu(menu, MF_SEPARATOR, 0, "")
	for _, it := range traySummary.Items {
		appendMenu(menu, MF_STRING|MF_GRAYED, 0, it.MenuTitle())
	}
	appendMenu(menu, MF_SEPARATOR, 0, "")
	appendMenu(menu, MF_STRING, cmdRefresh, "Обновить квоты")
	if isVisible.Load() {
		appendMenu(menu, MF_STRING, cmdToggleVisibility, "Скрыть оверлей")
	} else {
		appendMenu(menu, MF_STRING, cmdToggleVisibility, "Показать оверлей")
	}
	ctFlags := uintptr(MF_STRING)
	if clickThrough.Load() {
		ctFlags |= MF_CHECKED
	}
	appendMenu(menu, ctFlags, cmdClickThrough, "Клики насквозь")
	appendMenu(menu, MF_SEPARATOR, 0, "")
	appendMenu(menu, MF_STRING, cmdResetPosition, "Сбросить позицию")
	appendMenu(menu, MF_STRING, cmdQuit, "Выход")

	var pt POINT
	procGetCursorPos.Call(uintptr(unsafe.Pointer(&pt)))

	// Окно обязано быть foreground, иначе меню не закроется по клику мимо;
	// WM_NULL после TrackPopupMenu — документированный обход бага фокуса.
	procSetForeground.Call(hwnd)
	cmd, _, _ := procTrackPopupMenu.Call(
		menu,
		uintptr(TPM_RIGHTBUTTON|TPM_RETURNCMD),
		uintptr(pt.X), uintptr(pt.Y),
		0, hwnd, 0,
	)
	procPostMessageW.Call(hwnd, WM_NULL_MSG, 0, 0)

	switch int(cmd) {
	case cmdRefresh:
		triggerFetch(true)
	case cmdToggleVisibility:
		requestToggleVisibility()
	case cmdClickThrough:
		requestToggleClickThrough()
	case cmdResetPosition:
		requestResetPosition()
	case cmdQuit:
		procPostMessageW.Call(mainHWND, WM_DESTROY, 0, 0)
	}
}

// appendMenu — AppendMenuW с текстом в UTF-16.
func appendMenu(menu uintptr, flags uintptr, id uintptr, text string) {
	var ptr uintptr
	if text != "" {
		p, err := windows.UTF16PtrFromString(text)
		if err == nil {
			ptr = uintptr(unsafe.Pointer(p))
		}
	}
	procAppendMenuW.Call(menu, flags, id, ptr)
}

// copyUTF16 копирует строку в буфер фиксированной длины, обрезая по границе
// символа: буферы szTip/szInfo не должны оставаться нестрокой.
func copyUTF16(dst []uint16, s string) {
	u, err := windows.UTF16FromString(s)
	if err != nil || len(u) > len(dst) || len(dst) == 0 {
		return
	}
	copy(dst, u)
}
