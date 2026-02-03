package terminal

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

func (s *Session) CurrentDirectory() (string, error) {
	pid := s.shellPID()
	if pid > 0 && runtime.GOOS != "windows" {
		if dir, err := readProcCwd(pid); err == nil && strings.TrimSpace(dir) != "" {
			return filepath.Clean(dir), nil
		}
	}

	s.mu.Lock()
	titleCwd := s.lastTitleCwd
	s.mu.Unlock()

	if strings.TrimSpace(titleCwd) == "" {
		return "", errors.New("current directory not available")
	}

	expanded, err := expandLeadingTilde(titleCwd)
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(expanded) == "" {
		return "", errors.New("current directory not available")
	}
	return filepath.Clean(expanded), nil
}

func (s *Session) shellPID() int {
	s.mu.Lock()
	cmd := s.cmd
	s.mu.Unlock()
	if cmd == nil {
		return 0
	}
	return cmd.PID()
}

func readProcCwd(pid int) (string, error) {
	target, err := os.Readlink(fmt.Sprintf("/proc/%d/cwd", pid))
	if err != nil {
		return "", err
	}
	return target, nil
}

func expandLeadingTilde(value string) (string, error) {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return "", errors.New("path is empty")
	}
	if trimmed == "~" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		return home, nil
	}
	if strings.HasPrefix(trimmed, "~") {
		rest := trimmed[1:]
		if strings.HasPrefix(rest, "/") || strings.HasPrefix(rest, "\\") {
			home, err := os.UserHomeDir()
			if err != nil {
				return "", err
			}
			rest = strings.TrimLeft(rest, "/\\")
			if rest == "" {
				return home, nil
			}
			return filepath.Join(home, rest), nil
		}
	}
	return trimmed, nil
}
