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
	var index int
	var optionErr error
	err = raw.Control(func(fd uintptr) { index, optionErr = unix.GetsockoptInt(int(fd), unix.IPPROTO_IP, unix.IP_BOUND_IF) })
	if err != nil {
		return "", err
	}
	if optionErr != nil {
		return "", optionErr
	}
	i, err := net.InterfaceByIndex(index)
	if err != nil {
		return "", err
	}
	return i.Name, nil
}
