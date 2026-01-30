package discovery

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/grandcat/zeroconf"
)

const (
	mdnsService  = "_alices-mirror._tcp"
	mdnsDomain   = "local."
	udpPort      = 3003
	udpInterval  = 2 * time.Second
	defaultProto = "http"
)

type Info struct {
	ID           string
	Alias        string
	DisplayName  string
	UniqueName   string
	Hosts        []string
	Port         int
	AuthRequired bool
	AuthMode     string
	Yolo         bool
	Version      string
	Shell        string
	OS           string
	WorkDir      string
	Hostname     string
}

type Service struct {
	mdns      *zeroconf.Server
	udp       *udpBroadcaster
	closeOnce sync.Once
}

type payload struct {
	Type         string   `json:"type"`
	ID           string   `json:"id"`
	Alias        string   `json:"alias,omitempty"`
	DisplayName  string   `json:"display_name"`
	UniqueName   string   `json:"unique_name"`
	Hosts        []string `json:"hosts,omitempty"`
	Port         int      `json:"port"`
	Endpoints    []string `json:"endpoints,omitempty"`
	AuthRequired bool     `json:"auth_required"`
	AuthMode     string   `json:"auth_mode"`
	Yolo         bool     `json:"yolo"`
	Version      string   `json:"version,omitempty"`
	Shell        string   `json:"shell,omitempty"`
	OS           string   `json:"os,omitempty"`
	WorkDir      string   `json:"cwd,omitempty"`
	Hostname     string   `json:"hostname,omitempty"`
	Protocol     string   `json:"protocol"`
}

func Start(ctx context.Context, info Info) (*Service, error) {
	if info.Port <= 0 {
		return nil, errors.New("port is required")
	}

	normalized, err := normalizeInfo(info)
	if err != nil {
		return nil, err
	}

	svc := &Service{}
	mdnsServer, mdnsErr := startMDNS(normalized)
	svc.mdns = mdnsServer
	udpBroadcaster, udpErr := startUDP(ctx, normalized)
	svc.udp = udpBroadcaster

	if mdnsErr != nil && udpErr != nil {
		return nil, fmt.Errorf("discovery failed: mdns: %v; udp: %v", mdnsErr, udpErr)
	}

	go func() {
		<-ctx.Done()
		svc.Close()
	}()

	return svc, nil
}

func (s *Service) Close() {
	s.closeOnce.Do(func() {
		if s.mdns != nil {
			s.mdns.Shutdown()
		}
		if s.udp != nil {
			s.udp.Close()
		}
	})
}

func normalizeInfo(info Info) (Info, error) {
	info.Alias = strings.TrimSpace(info.Alias)
	info.DisplayName = strings.TrimSpace(info.DisplayName)
	info.UniqueName = strings.TrimSpace(info.UniqueName)
	info.Hosts = uniqueStrings(trimStrings(info.Hosts))

	if info.OS == "" {
		info.OS = runtime.GOOS
	}
	if info.Hostname == "" {
		hostname, err := os.Hostname()
		if err == nil {
			info.Hostname = hostname
		}
	}
	if strings.TrimSpace(info.ID) == "" {
		id, err := newID()
		if err != nil {
			return Info{}, err
		}
		info.ID = id
	}
	if info.AuthMode == "" {
		if info.AuthRequired {
			info.AuthMode = "basic"
		} else {
			info.AuthMode = "none"
		}
	}
	if info.Version == "" {
		info.Version = "unknown"
	}
	if info.DisplayName == "" {
		if info.Alias != "" {
			info.DisplayName = info.Alias
		} else {
			host := primaryHost(info.Hosts)
			if host == "" {
				host = "localhost"
			}
			info.DisplayName = fmt.Sprintf("%s:%d", host, info.Port)
		}
	}
	if info.UniqueName == "" {
		suffix := shortID(info.ID)
		if suffix != "" {
			info.UniqueName = fmt.Sprintf("%s (%s)", info.DisplayName, suffix)
		} else {
			info.UniqueName = info.DisplayName
		}
	}

	return info, nil
}

func buildPayload(info Info) (payload, error) {
	endpoints := buildEndpoints(info.Hosts, info.Port)
	return payload{
		Type:         "alices-mirror",
		ID:           info.ID,
		Alias:        info.Alias,
		DisplayName:  info.DisplayName,
		UniqueName:   info.UniqueName,
		Hosts:        info.Hosts,
		Port:         info.Port,
		Endpoints:    endpoints,
		AuthRequired: info.AuthRequired,
		AuthMode:     info.AuthMode,
		Yolo:         info.Yolo,
		Version:      info.Version,
		Shell:        info.Shell,
		OS:           info.OS,
		WorkDir:      info.WorkDir,
		Hostname:     info.Hostname,
		Protocol:     defaultProto,
	}, nil
}

func startMDNS(info Info) (*zeroconf.Server, error) {
	records := buildTXT(info)
	return zeroconf.Register(info.UniqueName, mdnsService, mdnsDomain, info.Port, records, nil)
}

func startUDP(ctx context.Context, info Info) (*udpBroadcaster, error) {
	payloadValue, err := buildPayload(info)
	if err != nil {
		return nil, err
	}
	data, err := json.Marshal(payloadValue)
	if err != nil {
		return nil, err
	}
	broadcaster, err := newUDPBroadcaster(data, udpPort, udpInterval)
	if err != nil {
		return nil, err
	}
	broadcaster.Start(ctx)
	return broadcaster, nil
}

func buildTXT(info Info) []string {
	records := []string{
		txtRecord("id", info.ID),
		txtRecord("alias", info.Alias),
		txtRecord("display_name", info.DisplayName),
		txtRecord("unique_name", info.UniqueName),
		txtRecord("auth_required", strconv.FormatBool(info.AuthRequired)),
		txtRecord("auth_mode", info.AuthMode),
		txtRecord("yolo", strconv.FormatBool(info.Yolo)),
		txtRecord("hostname", info.Hostname),
		txtRecord("cwd", info.WorkDir),
		txtRecord("version", info.Version),
		txtRecord("shell", info.Shell),
		txtRecord("os", info.OS),
	}

	host := primaryHost(info.Hosts)
	if host != "" {
		records = append(records, txtRecord("host", host))
	}

	var out []string
	for _, record := range records {
		if record == "" {
			continue
		}
		out = append(out, record)
	}
	return out
}

func txtRecord(key, value string) string {
	value = strings.ReplaceAll(value, "\n", " ")
	value = strings.ReplaceAll(value, "\r", " ")
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	max := 255 - len(key) - 1
	if max <= 0 {
		return ""
	}
	if len(value) > max {
		value = value[:max]
	}
	return key + "=" + value
}

func buildEndpoints(hosts []string, port int) []string {
	if port <= 0 {
		return nil
	}
	if len(hosts) == 0 {
		return []string{fmt.Sprintf("%s://localhost:%d", defaultProto, port)}
	}
	seen := make(map[string]struct{}, len(hosts))
	endpoints := make([]string, 0, len(hosts))
	for _, host := range hosts {
		host = strings.TrimSpace(host)
		if host == "" {
			continue
		}
		endpoint := fmt.Sprintf("%s://%s:%d", defaultProto, host, port)
		if _, ok := seen[endpoint]; ok {
			continue
		}
		seen[endpoint] = struct{}{}
		endpoints = append(endpoints, endpoint)
	}
	if len(endpoints) == 0 {
		return []string{fmt.Sprintf("%s://localhost:%d", defaultProto, port)}
	}
	return endpoints
}

func primaryHost(hosts []string) string {
	for _, host := range hosts {
		trimmed := strings.TrimSpace(host)
		if trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func trimStrings(values []string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		trimmed := strings.TrimSpace(value)
		if trimmed == "" {
			continue
		}
		out = append(out, trimmed)
	}
	return out
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

func newID() (string, error) {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}

func shortID(value string) string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return ""
	}
	if len(trimmed) <= 8 {
		return trimmed
	}
	return trimmed[:8]
}
