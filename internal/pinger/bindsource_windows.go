//go:build windows

package pinger

import "syscall"

func bindSocketSource(fd uintptr, addr syscall.Sockaddr) error {
	return syscall.Bind(syscall.Handle(fd), addr)
}
