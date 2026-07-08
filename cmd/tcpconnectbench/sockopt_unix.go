//go:build !windows

package main

import "syscall"

func setSockOptInt(fd uintptr, level, opt, value int) error {
	return syscall.SetsockoptInt(int(fd), level, opt, value)
}
