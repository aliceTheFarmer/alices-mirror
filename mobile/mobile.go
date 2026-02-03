package mobile

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"

	"alices-mirror/internal/app"
	"alices-mirror/internal/discovery"
	"alices-mirror/internal/server"
	"alices-mirror/internal/terminal"
)

const ownerTokenEnv = "ALICES_MIRROR_OWNER_TOKEN"
const defaultBindList = "127.0.0.1,192.168.1.*"
const defaultAllowIPList = "127.0.0.1,192.168.1.*"

// Listener receives status and log updates from the server.
type Listener interface {
	OnLog(line string)
	OnStatus(message string)
	OnError(message string)
}

// Server exposes Alice's Mirror for mobile bindings.
type Server struct {
	mu        sync.Mutex
	running   bool
	listener  Listener
	session   *terminal.Session
	server    *server.Server
	discovery *discovery.Service
	cancel    context.CancelFunc
}

// NewServer creates a new server wrapper.
func NewServer(listener Listener) *Server {
	return &Server{listener: listener}
}

// IsRunning reports whether the server is running.
func (s *Server) IsRunning() bool {
	s.mu.Lock()
	running := s.running
	s.mu.Unlock()
	return running
}

// Start launches the server with the provided configuration.
func (s *Server) Start(
	alias string,
	workDir string,
	originCsv string,
	port int,
	user string,
	password string,
	yolo bool,
	shell string,
	visible bool,
) error {
	return s.StartWithUserLevelAndAllowIPs(alias, workDir, originCsv, defaultAllowIPList, port, "", user, password, yolo, shell, visible)
}

// StartWithUserLevel launches the server with an explicit user-level rule set.
func (s *Server) StartWithUserLevel(
	alias string,
	workDir string,
	originCsv string,
	port int,
	userLevel string,
	user string,
	password string,
	yolo bool,
	shell string,
	visible bool,
) error {
	return s.StartWithUserLevelAndAllowIPs(alias, workDir, originCsv, defaultAllowIPList, port, userLevel, user, password, yolo, shell, visible)
}

// StartWithUserLevelAndAllowIPs launches the server with explicit user-level and allow-ip rules.
func (s *Server) StartWithUserLevelAndAllowIPs(
	alias string,
	workDir string,
	bindCsv string,
	allowIPCsv string,
	port int,
	userLevel string,
	user string,
	password string,
	yolo bool,
	shell string,
	visible bool,
) error {
	s.mu.Lock()
	if s.running {
		s.mu.Unlock()
		return errors.New("server is already running")
	}
	s.mu.Unlock()

	resolvedWorkDir := strings.TrimSpace(workDir)
	if resolvedWorkDir == "" {
		wd, err := os.Getwd()
		if err != nil {
			return fmt.Errorf("failed to determine working directory: %w", err)
		}
		resolvedWorkDir = wd
	}

	bindPatterns := bindCsv
	if strings.TrimSpace(bindPatterns) == "" {
		bindPatterns = defaultBindList
	}
	binds, err := parseHostList(bindPatterns)
	if err != nil {
		return err
	}

	allowPatterns := allowIPCsv
	if strings.TrimSpace(allowPatterns) == "" {
		allowPatterns = defaultAllowIPList
	}
	allowIPs, err := parseHostList(allowPatterns)
	if err != nil {
		return err
	}

	cfg := app.Config{
		Alias:     alias,
		Port:      port,
		Origins:   binds,
		AllowIPs:  allowIPs,
		UserLevel: userLevel,
		User:      user,
		Password:  password,
		Yolo:      yolo,
		WorkDir:   resolvedWorkDir,
		Shell:     shell,
		Visible:   visible,
	}

	if err := app.Validate(cfg); err != nil {
		return err
	}

	resolvedBinds := server.ExpandBindPatterns(cfg.Origins)
	if len(resolvedBinds) == 0 {
		return errors.New("bind patterns did not match any local IPv4 addresses")
	}

	ownerToken := strings.TrimSpace(os.Getenv(ownerTokenEnv))
	normalizedUserLevel := strings.TrimSpace(cfg.UserLevel)
	if normalizedUserLevel == "" {
		normalizedUserLevel = "*-0"
	}
	userLevels, err := server.ParseUserLevelRules(normalizedUserLevel)
	if err != nil {
		return fmt.Errorf("invalid value %q for --user-level: %v", cfg.UserLevel, err)
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
	auth := app.BuildAuthConfig(cfg)
	trimmedAlias := strings.TrimSpace(cfg.Alias)
	srv, err := server.New(server.Config{
		Addrs:      addrs,
		AllowIPs:   cfg.AllowIPs,
		Session:    session,
		Auth:       auth,
		Alias:      trimmedAlias,
		OwnerToken: ownerToken,
		UserLevels: userLevels,
	})
	if err != nil {
		session.Close()
		return err
	}

	ctx, cancel := context.WithCancel(context.Background())

	s.mu.Lock()
	s.running = true
	s.session = session
	s.server = srv
	s.cancel = cancel
	s.mu.Unlock()

	go s.forwardStatus(session)

	if cfg.Visible {
		hostname, _ := os.Hostname()
		svc, err := discovery.Start(ctx, discovery.Info{
			Alias:        trimmedAlias,
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
			s.cleanup()
			return err
		}
		s.mu.Lock()
		s.discovery = svc
		s.mu.Unlock()
	}

	for _, line := range app.StartupLines(app.StartupInfo{
		WorkDir: cfg.WorkDir,
		Port:    cfg.Port,
		Origins: resolvedBinds,
		Auth:    auth,
		PID:     0,
		Daemon:  false,
	}) {
		s.emitLog(line)
	}

	go func() {
		err := srv.Start(ctx)
		if err != nil && ctx.Err() == nil && !errors.Is(err, http.ErrServerClosed) && !errors.Is(err, context.Canceled) {
			s.emitError(err.Error())
		}
		s.cleanup()
	}()

	return nil
}

// Stop stops the running server.
func (s *Server) Stop() error {
	s.mu.Lock()
	if !s.running {
		s.mu.Unlock()
		return errors.New("server is not running")
	}
	s.mu.Unlock()
	s.cleanup()
	return nil
}

func (s *Server) forwardStatus(session *terminal.Session) {
	for message := range session.Status() {
		s.emitStatus(message)
	}
}

func (s *Server) emitLog(line string) {
	if s.listener == nil || strings.TrimSpace(line) == "" {
		return
	}
	s.listener.OnLog(line)
}

func (s *Server) emitStatus(message string) {
	if s.listener == nil || strings.TrimSpace(message) == "" {
		return
	}
	s.listener.OnStatus(message)
}

func (s *Server) emitError(message string) {
	if s.listener == nil || strings.TrimSpace(message) == "" {
		return
	}
	s.listener.OnError(message)
}

func (s *Server) cleanup() {
	s.mu.Lock()
	cancel := s.cancel
	session := s.session
	discoverySvc := s.discovery
	s.cancel = nil
	s.session = nil
	s.server = nil
	s.discovery = nil
	s.running = false
	s.mu.Unlock()

	if cancel != nil {
		cancel()
	}
	if discoverySvc != nil {
		discoverySvc.Close()
	}
	if session != nil {
		session.Close()
	}
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

func parseHostList(raw string) ([]string, error) {
	items := strings.Split(raw, ",")
	if len(items) == 0 {
		return nil, errors.New("invalid host list")
	}

	seen := make(map[string]struct{}, len(items))
	values := make([]string, 0, len(items))
	for _, item := range items {
		cleaned := strings.TrimSpace(item)
		if cleaned == "" {
			return nil, errors.New("host list contains an empty entry")
		}
		if strings.Contains(cleaned, ":") {
			if net.ParseIP(cleaned) == nil {
				return nil, errors.New("invalid host: hostnames must not include a port")
			}
		}
		if _, ok := seen[cleaned]; ok {
			continue
		}
		seen[cleaned] = struct{}{}
		values = append(values, cleaned)
	}

	if len(values) == 0 {
		return nil, errors.New("host list is empty")
	}

	return values, nil
}
