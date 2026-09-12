//go:build !windows

package xmrswap

import "errors"

// systemDir is Windows' (winsys_windows.go); nothing here runs a Windows program.
func systemDir() (string, error) {
	return "", errors.New("there is no Windows system directory on this system")
}

// defaultWSLDistro is Windows' (winsys_windows.go).
func defaultWSLDistro() (string, error) {
	return "", errors.New("WSL is Windows'")
}
