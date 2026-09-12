//go:build windows

package xmrswap

import (
	"errors"
	"fmt"
	"path/filepath"
	"regexp"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

// systemDir is Windows' System32 as GetSystemDirectoryW names it (the review of 2026-09-12, finding 5). It used to be
// %SystemRoot%\System32, and %SystemRoot% is an environment variable: whoever starts the companion sets it, and with it
// would choose the wsl.exe put in front of the prover and the powershell.exe that registers the wakes.
func systemDir() (string, error) {
	d, err := windows.GetSystemDirectory()
	if err != nil {
		return "", fmt.Errorf("GetSystemDirectoryW: %w", err)
	}
	if !filepath.IsAbs(d) {
		return "", fmt.Errorf("GetSystemDirectoryW answered %q, not an absolute path", d)
	}
	return d, nil
}

// lxssKey is where WSL records its distributions for this user: the default's GUID in DefaultDistribution, and each
// GUID's name in its own subkey's DistributionName.
const lxssKey = `Software\Microsoft\Windows\CurrentVersion\Lxss`

var wslGUID = regexp.MustCompile(`^\{[0-9A-Fa-f]{8}-[0-9A-Fa-f]{4}-[0-9A-Fa-f]{4}-[0-9A-Fa-f]{4}-[0-9A-Fa-f]{12}\}$`)

// defaultWSLDistro reads the default distribution's name from this user's registry, as wsl.exe does, through the
// registry API. It starts no program: it used to run reg.exe, found through %SystemRoot%.
func defaultWSLDistro() (string, error) {
	guid, err := regString(lxssKey, "DefaultDistribution")
	if err != nil {
		return "", err
	}
	if !wslGUID.MatchString(guid) {
		return "", fmt.Errorf(`HKCU\%s DefaultDistribution is %q, not a GUID`, lxssKey, guid)
	}
	return regString(lxssKey+`\`+guid, "DistributionName")
}

// regString is one REG_SZ value under HKEY_CURRENT_USER.
func regString(key, value string) (string, error) {
	k, err := registry.OpenKey(registry.CURRENT_USER, key, registry.QUERY_VALUE)
	if err != nil {
		return "", fmt.Errorf(`HKCU\%s: %w`, key, err)
	}
	defer k.Close()
	v, typ, err := k.GetStringValue(value)
	if err != nil {
		return "", fmt.Errorf(`HKCU\%s %s: %w`, key, value, err)
	}
	if typ != registry.SZ {
		return "", errors.New(`HKCU\` + key + ` ` + value + ` is not a REG_SZ value`)
	}
	return v, nil
}
