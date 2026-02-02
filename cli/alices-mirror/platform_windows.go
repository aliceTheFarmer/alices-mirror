//go:build windows

package main

import (
	"flag"
	"fmt"
	"strings"
)

func platformSpecs() []flagSpec {
	return []flagSpec{
		{Long: "shell", Short: "S", ExpectsValue: true, IsBool: false},
	}
}

func defaultPlatformShell() string {
	return "powershell"
}

func registerPlatformFlags(fs *flag.FlagSet, shell *string) {
	fs.StringVar(shell, "shell", "powershell", "")
}

func normalizePlatformShell(shell string) (string, error) {
	cleaned := strings.ToLower(strings.TrimSpace(shell))
	if cleaned == "" {
		cleaned = "powershell"
	}
	switch cleaned {
	case "powershell", "cmd":
		return cleaned, nil
	default:
		return "", fmt.Errorf("invalid value %q for --shell (allowed: powershell, cmd)", shell)
	}
}

func printPlatformHelp() {
	fmt.Println("  -S, --shell=<shell>    Select Windows shell (powershell or cmd).")
}
