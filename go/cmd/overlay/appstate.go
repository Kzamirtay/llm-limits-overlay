package main

import (
	"context"
	"fmt"

	wailsrt "github.com/wailsapp/wails/v2/pkg/runtime"
)

var wailsCtx context.Context
var isVisible = true

func setWailsContext(ctx context.Context) { wailsCtx = ctx }

func toggleVisibility() {
	if wailsCtx == nil {
		return
	}
	if isVisible {
		wailsrt.WindowHide(wailsCtx)
		isVisible = false
		if mToggle != nil {
			mToggle.SetTitle("Показать оверлей")
		}
	} else {
		wailsrt.WindowShow(wailsCtx)
		isVisible = true
		if mToggle != nil {
			mToggle.SetTitle("Скрыть оверлей")
		}
	}
	fmt.Println("tray: visible =", isVisible)
}

func resetPosition() {
	if wailsCtx == nil {
		return
	}
	positionBottomRight(wailsCtx)
}
