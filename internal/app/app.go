package app

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"runtime"
	"strings"

	"alices-mirror/internal/discovery"
	"alices-mirror/internal/server"
	"alices-mirror/internal/terminal"
)

type Config struct {
	Alias     string
	Port      int
	Origins   []string
	AllowIPs  []string
	UserLevel string
	User      string
	Password  string
	Yolo      bool
	WorkDir   string
	Shell     string
	Visible   bool
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
		return errors.New("bind list is required")
	}
	for _, origin := range cfg.Origins {
		if strings.TrimSpace(origin) == "" {
			return errors.New("bind list is required")
		}
	}

	if len(cfg.AllowIPs) == 0 {
		return errors.New("allow-ip list is required")
	}
	for _, pattern := range cfg.AllowIPs {
		if strings.TrimSpace(pattern) == "" {
			return errors.New("allow-ip list is required")
		}
	}

	resolvedBinds := server.ExpandBindPatterns(cfg.Origins)
	if len(resolvedBinds) == 0 {
		return errors.New("bind patterns did not match any local IPv4 addresses")
	}

	userLevel := strings.TrimSpace(cfg.UserLevel)
	if userLevel == "" {
		userLevel = "*-0"
	}
	if _, err := server.ParseUserLevelRules(userLevel); err != nil {
		return fmt.Errorf("invalid value %q for --user-level: %v", cfg.UserLevel, err)
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
	ownerToken := strings.TrimSpace(os.Getenv("ALICES_MIRROR_OWNER_TOKEN"))
	userLevel := strings.TrimSpace(cfg.UserLevel)
	if userLevel == "" {
		userLevel = "*-0"
	}
	userLevels, err := server.ParseUserLevelRules(userLevel)
	if err != nil {
		return fmt.Errorf("invalid value %q for --user-level: %v", cfg.UserLevel, err)
	}

	resolvedBinds := server.ExpandBindPatterns(cfg.Origins)
	if len(resolvedBinds) == 0 {
		return errors.New("bind patterns did not match any local IPv4 addresses")
	}

	session, err := terminal.NewSession(terminal.Config{
		WorkDir:         cfg.WorkDir,
		BufferSize:      256 * 1024,
		Shell:           cfg.Shell,
		ExitOnShellExit: ownerToken != "",
	})
	if err != nil {
		return err
	}

	addrs := make([]string, 0, len(resolvedBinds))
	for _, origin := range resolvedBinds {
		addrs = append(addrs, net.JoinHostPort(origin, fmt.Sprintf("%d", cfg.Port)))
	}
	alias := strings.TrimSpace(cfg.Alias)
	srv, err := server.New(server.Config{
		Addrs:      addrs,
		AllowIPs:   cfg.AllowIPs,
		Session:    session,
		Auth:       auth,
		Alias:      alias,
		OwnerToken: ownerToken,
		UserLevels: userLevels,
	})
	if err != nil {
		return err
	}

	lines := StartupLines(StartupInfo{
		WorkDir: cfg.WorkDir,
		Port:    cfg.Port,
		Origins: resolvedBinds,
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
			Hosts:        filterLANHosts(buildDisplayHosts(resolvedBinds)),
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

	err = srv.Start(ctx)
	if err == nil || errors.Is(err, http.ErrServerClosed) || errors.Is(err, context.Canceled) {
		return nil
	}
	return err
}

func StartupLines(info StartupInfo) []string {
	lines := []string{"alices mirror is running."}
	if info.WorkDir != "" {
		lines = append(lines, fmt.Sprintf("Working directory: %s", info.WorkDir))
	}
	if info.Daemon && info.PID > 0 {
		lines = append(lines, fmt.Sprintf("PID: %d", info.PID))
	}

	origins := server.ExpandBindPatterns(info.Origins)
	if len(origins) == 0 {
		origins = info.Origins
	}
	hosts := buildDisplayHosts(origins)
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
