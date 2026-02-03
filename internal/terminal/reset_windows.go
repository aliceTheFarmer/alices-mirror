//go:build windows

package terminal

import (
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	resetGracefulWait = 700 * time.Millisecond
	resetForceWait    = 700 * time.Millisecond
)

type windowsProcessInfo struct {
	ProcessId int    `json:"ProcessId"`
	Name      string `json:"Name"`
}

func terminateProcessTree(pid int) ([]ProcessInfo, error) {
	if pid <= 0 {
		return nil, errors.New("shell not ready")
	}

	_ = exec.Command("taskkill", "/T", "/PID", strconv.Itoa(pid)).Run()
	if waitForProcessTreeExit(pid, resetGracefulWait) {
		return nil, nil
	}

	_ = exec.Command("taskkill", "/T", "/F", "/PID", strconv.Itoa(pid)).Run()
	if waitForProcessTreeExit(pid, resetForceWait) {
		return nil, nil
	}

	remaining := listProcessTree(pid)
	if len(remaining) == 0 && isProcessAlive(pid) {
		remaining = []ProcessInfo{{PID: pid, Name: "unknown"}}
	}

	return remaining, errors.New("some processes could not be terminated")
}

func waitForProcessTreeExit(pid int, timeout time.Duration) bool {
	if pid <= 0 {
		return true
	}
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if len(listProcessTree(pid)) == 0 {
			return true
		}
		time.Sleep(120 * time.Millisecond)
	}
	return len(listProcessTree(pid)) == 0
}

func listProcessTree(pid int) []ProcessInfo {
	if pid <= 0 {
		return nil
	}
	script := fmt.Sprintf("$ErrorActionPreference='SilentlyContinue';$rootPid=%d;$procs=Get-CimInstance Win32_Process | Select-Object ProcessId,ParentProcessId,Name;function Get-Descendants([int]$parent){$children=$procs | Where-Object { $_.ParentProcessId -eq $parent };foreach($child in $children){$child;Get-Descendants $child.ProcessId}};$root=$procs | Where-Object { $_.ProcessId -eq $rootPid };$list=@();if ($root){$list += $root};$list += Get-Descendants $rootPid;$list | Select-Object ProcessId,Name | ConvertTo-Json -Compress", pid)
	out, err := exec.Command("powershell.exe", "-NoProfile", "-Command", script).Output()
	if err != nil {
		return listSingleProcess(pid)
	}
	raw := strings.TrimSpace(string(out))
	if raw == "" || raw == "null" {
		return nil
	}
	if strings.HasPrefix(raw, "[") {
		var items []windowsProcessInfo
		if err := json.Unmarshal([]byte(raw), &items); err != nil {
			return listSingleProcess(pid)
		}
		return toProcessInfo(items)
	}
	var item windowsProcessInfo
	if err := json.Unmarshal([]byte(raw), &item); err != nil {
		return listSingleProcess(pid)
	}
	return toProcessInfo([]windowsProcessInfo{item})
}

func toProcessInfo(items []windowsProcessInfo) []ProcessInfo {
	infos := make([]ProcessInfo, 0, len(items))
	for _, item := range items {
		if item.ProcessId <= 0 {
			continue
		}
		name := strings.TrimSpace(item.Name)
		if name == "" {
			name = "unknown"
		}
		infos = append(infos, ProcessInfo{PID: item.ProcessId, Name: name})
	}
	sort.Slice(infos, func(i, j int) bool {
		return infos[i].PID < infos[j].PID
	})
	return infos
}

func listSingleProcess(pid int) []ProcessInfo {
	if !isProcessAlive(pid) {
		return nil
	}
	return []ProcessInfo{{PID: pid, Name: "unknown"}}
}

func isProcessAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	out, err := exec.Command("tasklist", "/FI", fmt.Sprintf("PID eq %d", pid), "/FO", "CSV", "/NH").Output()
	if err != nil {
		return true
	}
	line := strings.TrimSpace(string(out))
	if line == "" || strings.HasPrefix(line, "INFO:") {
		return false
	}
	reader := csv.NewReader(strings.NewReader(line))
	record, err := reader.Read()
	if err != nil || len(record) < 2 {
		return false
	}
	return strings.TrimSpace(record[1]) == strconv.Itoa(pid)
}
