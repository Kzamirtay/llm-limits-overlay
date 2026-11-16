//go:build windows

package main

import (
	_ "embed"

	"fyne.io/systray"
	"llmlimits/internal/fetchers"
)

//go:embed app.ico
var trayIconBytes []byte

var (
	mVisibility   *systray.MenuItem
	mRefresh      *systray.MenuItem
	mClickThrough *systray.MenuItem
	mResetPos     *systray.MenuItem
	mQuit         *systray.MenuItem

	// trayUpdates — очередь правок меню трея. Правки делает только горутина
	// трея (systray не потокобезопасен), а приходят они из UI-потока окна,
	// поэтому отправляем через канал и никогда не блокируемся: зависший трей
	// раньше вешал главный поток окна, и оверлей переставал реагировать.
	trayUpdates = make(chan func(), 32)
)

// sendToTray ставит правку меню в очередь трея. Если очередь переполнена
// (трей завис) — правка отбрасывается: состояние окна важнее украшения меню.
func sendToTray(fn func()) {
	select {
	case trayUpdates <- fn:
	default:
	}
}

func runTray() {
	systray.Run(onTrayReady, onTrayExit)
}

func onTrayReady() {
	systray.SetIcon(trayIconBytes)
	systray.SetTitle("LLM Limits Overlay")
	systray.SetTooltip("LLM Limits Overlay v2.2.1 (Win32 Native)")

	mRefresh = systray.AddMenuItem("Обновить квоты", "Принудительно обновить квоты")
	mVisibility = systray.AddMenuItem("Скрыть", "Показать/Скрыть оверлей")
	mClickThrough = systray.AddMenuItemCheckbox("Клики насквозь", "Пропускать клики мыши сквозь окно", clickThrough.Load())
	mResetPos = systray.AddMenuItem("Сбросить позицию", "Переместить оверлей в правый-нижний угол")
	systray.AddSeparator()
	mQuit = systray.AddMenuItem("Выход", "Закрыть оверлей")

	fetchers.LogLine(appDir, "[overlay32] tray ready")

	go handleTrayEvents()
}

func onTrayExit() {
	// Очистка при выходе
}

func handleTrayEvents() {
	for {
		select {
		case <-mRefresh.ClickedCh:
			triggerFetch(true)

		case <-mVisibility.ClickedCh:
			// Окно трогает только UI-поток: см. wndProc / setVisibility.
			requestToggleVisibility()

		case <-mClickThrough.ClickedCh:
			requestToggleClickThrough()

		case <-mResetPos.ClickedCh:
			requestResetPosition()

		case <-mQuit.ClickedCh:
			systray.Quit()
			if mainHWND != 0 {
				procPostMessageW.Call(mainHWND, WM_DESTROY, 0, 0)
			}
			return

		case fn := <-trayUpdates:
			fn()
		}
	}
}

func updateTrayClickThrough(enabled bool) {
	sendToTray(func() {
		if mClickThrough == nil {
			return
		}
		if enabled {
			mClickThrough.Check()
		} else {
			mClickThrough.Uncheck()
		}
	})
}

func updateTrayVisibility(visible bool) {
	sendToTray(func() {
		if mVisibility == nil {
			return
		}
		if visible {
			mVisibility.SetTitle("Скрыть")
		} else {
			mVisibility.SetTitle("Показать")
		}
	})
}
