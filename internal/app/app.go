package app

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"runtime"
	"strings"

	"alices-mirror/internal/discovery"
	"alices-mirror/internal/server"
	"alices-mirror/internal/terminal"
)

type Config struct {
	Alias    string
	Port     int
	Origins  []string
	User     string
	Password string
	Yolo     bool
	WorkDir  string
	Shell    string
	Visible  bool
}

type StartupInfo struct {
	WorkDir string
	Port    int
	Origins []string
	Auth    server.AuthConfig
	PID     int
	Daemon  bool
}

func Validate(cfg Config) error {
	if cfg.Port <= 0 || cfg.Port > 65535 {
		return errors.New("port must be between 1 and 65535")
	}
	if cfg.WorkDir == "" {
		return errors.New("work directory is required")
	}
	if len(cfg.Origins) == 0 {
		return errors.New("origin list is required")
	}
	for _, origin := range cfg.Origins {
		if strings.TrimSpace(origin) == "" {
			return errors.New("origin list is required")
		}
	}
	info, err := os.Stat(cfg.WorkDir)
	if err != nil {
		return fmt.Errorf("invalid work directory %q: %v", cfg.WorkDir, err)
	}
	if !info.IsDir() {
		return fmt.Errorf("work directory is not a directory: %s", cfg.WorkDir)
	}
	if err := terminal.CheckShell(cfg.WorkDir, cfg.Shell); err != nil {
		return fmt.Errorf("failed to start shell in %q: %v", cfg.WorkDir, err)
	}
	return nil
}

func BuildAuthConfig(cfg Config) server.AuthConfig {
	auth := server.AuthConfig{}
	if !cfg.Yolo && cfg.User != "" && cfg.Password != "" {
		auth.Enabled = true
		auth.User = cfg.User
		auth.Password = cfg.Password
	}
	return auth
}

func Run(cfg Config) error {
	if err := Validate(cfg); err != nil {
		return err
	}

	auth := BuildAuthConfig(cfg)

	session, err := terminal.NewSession(terminal.Config{
		WorkDir:    cfg.WorkDir,
		BufferSize: 256 * 1024,
		Shell:      cfg.Shell,
	})
	if err != nil {
		return err
	}

	addrs := make([]string, 0, len(cfg.Origins))
	for _, origin := range cfg.Origins {
		addrs = append(addrs, net.JoinHostPort(origin, fmt.Sprintf("%d", cfg.Port)))
	}
	alias := strings.TrimSpace(cfg.Alias)
	srv, err := server.New(server.Config{
		Addrs:   addrs,
		Session: session,
		Auth:    auth,
		Alias:   alias,
	})
	if err != nil {
		return err
	}

	lines := StartupLines(StartupInfo{
		WorkDir: cfg.WorkDir,
		Port:    cfg.Port,
		Origins: cfg.Origins,
		Auth:    auth,
	})
	for _, line := range lines {
		fmt.Println(line)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if cfg.Visible {
		hostname, _ := os.Hostname()
		_, err := discovery.Start(ctx, discovery.Info{
			Alias:        alias,
			Hosts:        filterLANHosts(buildDisplayHosts(cfg.Origins)),
			Port:         cfg.Port,
			AuthRequired: auth.Enabled,
			Yolo:         cfg.Yolo,
			Version:      readVersion(),
			Shell:        cfg.Shell,
			OS:           runtime.GOOS,
			WorkDir:      cfg.WorkDir,
			Hostname:     hostname,
		})
		if err != nil {
			return err
		}
	}

	return srv.Start(ctx)
}

func StartupLines(info StartupInfo) []string {
	lines := []string{"alices mirror is running."}
	if info.WorkDir != "" {
		lines = append(lines, fmt.Sprintf("Working directory: %s", info.WorkDir))
	}
	if info.Daemon && info.PID > 0 {
		lines = append(lines, fmt.Sprintf("PID: %d", info.PID))
	}

	hosts := buildDisplayHosts(info.Origins)
	if len(hosts) == 0 {
		lines = append(lines, "LAN address not detected. Use:")
		lines = append(lines, fmt.Sprintf("http://localhost:%d", info.Port))
		return lines
	}

	for _, host := range hosts {
		url := fmt.Sprintf("http://%s:%d", host, info.Port)
		if info.Auth.Enabled {
			url = fmt.Sprintf("http://%s:%s@%s:%d", info.Auth.User, info.Auth.Password, host, info.Port)
		}
		lines = append(lines, fmt.Sprintf("Open: %s", url))
	}

	if !info.Daemon {
		lines = append(lines, "Press Ctrl+C to stop the server.")
	}

	return lines
}

func buildDisplayHosts(origins []string) []string {
	var hosts []string
	for _, origin := range origins {
		if origin == "0.0.0.0" {
			hosts = append(hosts, server.LocalIPv4s()...)
			continue
		}
		hosts = append(hosts, origin)
	}
	return uniqueStrings(hosts)
}

func filterLANHosts(hosts []string) []string {
	var out []string
	for _, host := range hosts {
		trimmed := strings.TrimSpace(host)
		if trimmed == "" {
			continue
		}
		if strings.EqualFold(trimmed, "localhost") {
			continue
		}
		if ip := net.ParseIP(trimmed); ip != nil && ip.IsLoopback() {
			continue
		}
		out = append(out, trimmed)
	}
	return uniqueStrings(out)
}

func uniqueStrings(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	var out []string
	for _, value := range values {
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}
