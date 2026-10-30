//go:build !linux

package main

import (
	"github.com/wailsapp/wails/v2/pkg/options/linux"
)

func linuxOptions() *linux.Options {
	return nil
}
