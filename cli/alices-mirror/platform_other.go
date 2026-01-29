//go:build !windows

package main

import "flag"

func platformSpecs() []flagSpec {
	return nil
}

func defaultPlatformShell() string {
	return ""
}

func registerPlatformFlags(_ *flag.FlagSet, _ *string) {}

func normalizePlatformShell(shell string) (string, error) {
	return shell, nil
}

func printPlatformHelp() {}
