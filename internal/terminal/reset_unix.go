//go:build !windows

package terminal

import (
	"errors"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"
)

const (
	resetGracefulWait = 700 * time.Millisecond
	resetForceWait    = 700 * time.Millisecond
	resetSudoWait     = 700 * time.Millisecond
)

func terminateProcessTree(cmd *exec.Cmd) ([]ProcessInfo, error) {
	if cmd == nil || cmd.Process == nil {
		return nil, errors.New("shell not ready")
	}
	pid := cmd.Process.Pid
	if pid <= 0 {
		return nil, errors.New("shell not ready")
	}

	pgid, err := syscall.Getpgid(pid)
	if err != nil || pgid <= 0 {
		pgid = pid
	}

	_ = syscall.Kill(-pgid, syscall.SIGTERM)
	if waitForProcessGroupExit(pgid, resetGracefulWait) {
		return nil, nil
	}

	_ = syscall.Kill(-pgid, syscall.SIGKILL)
	if waitForProcessGroupExit(pgid, resetForceWait) {
		return nil, nil
	}

	_ = runSudoKill(pgid)
	if waitForProcessGroupExit(pgid, resetSudoWait) {
		return nil, nil
	}

	if !isProcessGroupAlive(pgid) {
		return nil, nil
	}

	remaining := listProcessGroup(pgid)
	if len(remaining) == 0 {
		remaining = []ProcessInfo{{PID: pid, Name: "unknown"}}
	}

	return remaining, errors.New("some processes could not be terminated")
}

func runSudoKill(pgid int) error {
	if pgid <= 0 {
		return errors.New("invalid process group")
	}
	cmd := exec.Command("sudo", "-n", "kill", "-9", "-"+strconv.Itoa(pgid))
	return cmd.Run()
}

func waitForProcessGroupExit(pgid int, timeout time.Duration) bool {
	if pgid <= 0 {
		return true
	}
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if !isProcessGroupAlive(pgid) {
			return true
		}
		time.Sleep(80 * time.Millisecond)
	}
	return !isProcessGroupAlive(pgid)
}

func isProcessGroupAlive(pgid int) bool {
	if pgid <= 0 {
		return false
	}
	err := syscall.Kill(-pgid, 0)
	if err == nil {
		return true
	}
	return errors.Is(err, syscall.EPERM)
}

func listProcessGroup(pgid int) []ProcessInfo {
	if pgid <= 0 {
		return nil
	}
	cmd := exec.Command("ps", "-o", "pid=", "-o", "comm=", "-g", strconv.Itoa(pgid))
	output, err := cmd.Output()
	if err != nil {
		if isProcessGroupAlive(pgid) {
			return []ProcessInfo{{PID: pgid, Name: "process group"}}
		}
		return nil
	}

	lines := strings.Split(string(output), "\n")
	infos := make([]ProcessInfo, 0, len(lines))
	for _, line := range lines {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		pid, err := strconv.Atoi(fields[0])
		if err != nil || pid <= 0 {
			continue
		}
		name := strings.Join(fields[1:], " ")
		infos = append(infos, ProcessInfo{PID: pid, Name: name})
	}

	sort.Slice(infos, func(i, j int) bool {
		return infos[i].PID < infos[j].PID
	})

	return infos
}
