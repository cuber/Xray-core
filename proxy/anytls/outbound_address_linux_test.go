package anytls_test

import (
	"net"

	"golang.org/x/sys/unix"
)

func addressBoundInterface(c net.Conn) (string, error) {
	raw, err := c.(*net.TCPConn).SyscallConn()
	if err != nil {
		return "", err
	}
	var name string
	var optionErr error
	err = raw.Control(func(fd uintptr) {
		name, optionErr = unix.GetsockoptString(int(fd), unix.SOL_SOCKET, unix.SO_BINDTODEVICE)
	})
	if err != nil {
		return "", err
	}
	return name, optionErr
}
