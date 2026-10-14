//go:build !darwin && !linux

package anytls_test

import "testing"

func openFDCount(t *testing.T) int {
	t.Helper()
	t.Log("FD count unavailable on this platform; connection cleanup still tested")
	return -1
}
