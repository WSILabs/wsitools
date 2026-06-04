package main

import "os/exec"

// runBin executes the wsitools binary with the given args and returns combined
// stdout+stderr output. It is shared by DICOM integration tests.
func runBin(bin string, args ...string) ([]byte, error) {
	return exec.Command(bin, args...).CombinedOutput()
}
