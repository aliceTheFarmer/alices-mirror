package app

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"strings"

	"alices-mirror/internal/server"
	"alices-mirror/internal/terminal"
)

type Config struct {
	Port     int
	Origins  []string
	User     string
	Password string
	Yolo     bool
	WorkDir  string
	Shell    string
}

func Run(cfg Config) error {
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

	auth := server.AuthConfig{}
	if !cfg.Yolo && cfg.User != "" && cfg.Password != "" {
		auth.Enabled = true
		auth.User = cfg.User
		auth.Password = cfg.Password
	}

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
	srv, err := server.New(server.Config{
		Addrs:   addrs,
		Session: session,
		Auth:    auth,
	})
	if err != nil {
		return err
	}

	printStartup(cfg.Port, cfg.Origins, auth)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	return srv.Start(ctx)
}

func printStartup(port int, origins []string, auth server.AuthConfig) {
	workDir, _ := os.Getwd()
	fmt.Println("alices mirror is running.")
	fmt.Printf("Working directory: %s\n", workDir)

	hosts := buildDisplayHosts(origins)
	if len(hosts) == 0 {
		fmt.Println("LAN address not detected. Use:")
		fmt.Printf("http://localhost:%d\n", port)
		return
	}

	for _, host := range hosts {
		url := fmt.Sprintf("http://%s:%d", host, port)
		if auth.Enabled {
			url = fmt.Sprintf("http://%s:%s@%s:%d", auth.User, auth.Password, host, port)
		}
		fmt.Printf("Open: %s\n", url)
	}

	fmt.Println("Press Ctrl+C to stop the server.")
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
