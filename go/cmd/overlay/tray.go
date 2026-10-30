package main

import (
	"fmt"
	"os"

	"fyne.io/systray"
)

var (
	mToggle  *systray.MenuItem
	mRefresh *systray.MenuItem
	mClick   *systray.MenuItem
	mReset   *systray.MenuItem
	mQuit    *systray.MenuItem
)

// runTray — иконка в трее с меню.
func runTray() {
	systray.Run(onTrayReady, onTrayExit)
}

func onTrayReady() {
	systray.SetTitle("LLM Limits Overlay")
	systray.SetTooltip("LLM Limits Overlay (Go PoC)")
	if len(appIcon) > 0 {
		systray.SetIcon(appIcon)
	}

	mToggle = systray.AddMenuItem("Скрыть оверлей", "Переключить видимость")
	mRefresh = systray.AddMenuItem("Обновить квоты", "Принудительное обновление (PoC: no-op)")
	mClick = systray.AddMenuItemCheckbox("Клики насквозь", "Переключить click-through", clickThrough.Load())
	mReset = systray.AddMenuItem("Сбросить позицию", "Вернуть окно в правый-нижний угол")
	systray.AddSeparator()
	mQuit = systray.AddMenuItem("Выход", "Закрыть оверлей")

	for {
		select {
		case <-mToggle.ClickedCh:
			toggleVisibility()
		case <-mRefresh.ClickedCh:
			fmt.Println("tray: refresh requested (PoC no-op)")
		case <-mClick.ClickedCh:
			toggleClickThroughFromTray()
		case <-mReset.ClickedCh:
			resetPosition()
		case <-mQuit.ClickedCh:
			systray.Quit()
			exitProcess(0)
			return
		}
	}
}

func onTrayExit() {}

var exitProcess = func(code int) {
	os.Exit(code)
}
