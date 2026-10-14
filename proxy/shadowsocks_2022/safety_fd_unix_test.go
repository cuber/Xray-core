//go:build darwin || linux

package shadowsocks_2022

import (
	"os"
	"runtime"

	"golang.org/x/sys/unix"
)

func fdSample() int {
	if runtime.GOOS == "linux" {
		if files, err := os.ReadDir("/proc/self/fd"); err == nil {
			return len(files) - 1
		}
	}
	// Darwin's /dev/fd directory listing is not a process-wide FD inventory.
	// Use the same F_GETFD oracle as the AnyTLS resource acceptance tests.
	var limit unix.Rlimit
	if unix.Getrlimit(unix.RLIMIT_NOFILE, &limit) != nil {
		return -1
	}
	n := 0
	for fd := uint64(0); fd < limit.Cur; fd++ {
		if _, err := unix.FcntlInt(uintptr(fd), unix.F_GETFD, 0); err == nil {
			n++
		}
	}
	return n
}
