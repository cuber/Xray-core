//go:build darwin || linux

package anytls_test

import (
	"testing"

	"golang.org/x/sys/unix"
)

func openFDCount(t *testing.T) int {
	t.Helper()
	var limit unix.Rlimit
	if err := unix.Getrlimit(unix.RLIMIT_NOFILE, &limit); err != nil {
		t.Fatal(err)
	}
	count := 0
	for fd := uint64(0); fd < limit.Cur; fd++ {
		if _, err := unix.FcntlInt(uintptr(fd), unix.F_GETFD, 0); err == nil {
			count++
		}
	}
	return count
}
