package app

import (
	"os"
	"path/filepath"
	"strings"
)

func readVersion() string {
	if version := readVersionFrom("VERSION"); version != "" {
		return version
	}
	exe, err := os.Executable()
	if err != nil {
		return "unknown"
	}
	version := readVersionFrom(filepath.Join(filepath.Dir(exe), "VERSION"))
	if version == "" {
		return "unknown"
	}
	return version
}

func readVersionFrom(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	version := strings.TrimSpace(string(data))
	if version == "" {
		return ""
	}
	return version
}
