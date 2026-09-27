//go:build !darwin && !linux

package anytls_test

import (
	"fmt"
	"net"
	"runtime"
)

func addressBoundInterface(net.Conn) (string, error) {
	return "", fmt.Errorf("kernel interface binding observation unsupported on %s", runtime.GOOS)
}
