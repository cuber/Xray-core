//go:build coveragemain
// +build coveragemain

package main

import (
	"flag"
	"os"
	"testing"
)

func TestRunMainForCoverage(t *testing.T) {
	if len(flag.Args()) == 0 {
		t.Fatal("coverage Core requires command arguments after --")
	}
	os.Args = append([]string{os.Args[0]}, flag.Args()...)
	main()
}
