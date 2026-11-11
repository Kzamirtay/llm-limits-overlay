//go:build windows

package main

import (
	_ "embed"

	"fyne.io/systray"
)

//go:embed app.ico
var trayIconBytes []byte

var (
	mVisibility   *systray.MenuItem
	mRefresh      *systray.MenuItem
	mClickThrough *systray.MenuItem
	mResetPos     *systray.MenuItem
	mQuit         *systray.MenuItem
)

func runTray() {
	systray.Run(onTrayReady, onTrayExit)
}

func onTrayReady() {
	systray.SetIcon(trayIconBytes)
	systray.SetTitle("LLM Limits Overlay")
	systray.SetTooltip("LLM Limits Overlay v2.1.7 (Win32 Native)")

	mRefresh = systray.AddMenuItem("Обновить квоты", "Принудительно обновить квоты")
	mVisibility = systray.AddMenuItem("Скрыть", "Показать/Скрыть оверлей")
	mClickThrough = systray.AddMenuItemCheckbox("Клики насквозь", "Пропускать клики мыши сквозь окно", clickThrough.Load())
	mResetPos = systray.AddMenuItem("Сбросить позицию", "Переместить оверлей в правый-нижний угол")
	systray.AddSeparator()
	mQuit = systray.AddMenuItem("Выход", "Закрыть оверлей")

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
			toggleVisibility()

		case <-mClickThrough.ClickedCh:
			toggleClickThrough()

		case <-mResetPos.ClickedCh:
			resetPosition()

		case <-mQuit.ClickedCh:
			systray.Quit()
			if mainHWND != 0 {
				procPostMessageW.Call(mainHWND, WM_DESTROY, 0, 0)
			}
			return
		}
	}
}

func updateTrayClickThrough(enabled bool) {
	if mClickThrough == nil {
		return
	}
	if enabled {
		mClickThrough.Check()
	} else {
		mClickThrough.Uncheck()
	}
}

func updateTrayVisibility(visible bool) {
	if mVisibility == nil {
		return
	}
	if visible {
		mVisibility.SetTitle("Скрыть")
	} else {
		mVisibility.SetTitle("Показать")
	}
}
