//go:build !windows

package fetchers

import "os/exec"

func hideConsole(cmd *exec.Cmd) {}
