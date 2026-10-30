//go:build !windows

package main

import (
	"context"
	"fmt"
	"sync/atomic"

	wailsrt "github.com/wailsapp/wails/v2/pkg/runtime"
)

func runHotkeyLoop(title string, state *atomic.Bool) {}

func positionBottomRight(ctx context.Context) {
	screens, err := wailsrt.ScreenGetAll(ctx)
	if err != nil || len(screens) == 0 {
		return
	}
	s := screens[0]
	x := s.Width - 290 - 14
	y := s.Height - 402 - 4
	wailsrt.WindowSetPosition(ctx, x, y)
	fmt.Printf("position: screen %dx%d -> window (%d,%d)\n", s.Width, s.Height, x, y)
}

func initPlatformWindow(title string) {}
