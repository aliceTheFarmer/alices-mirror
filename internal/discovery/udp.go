package discovery

import (
	"context"
	"errors"
	"net"
	"strconv"
	"sync"
	"time"
)

type udpBroadcaster struct {
	conn      *net.UDPConn
	addrs     []*net.UDPAddr
	payload   []byte
	interval  time.Duration
	closeOnce sync.Once
}

func newUDPBroadcaster(payload []byte, port int, interval time.Duration) (*udpBroadcaster, error) {
	if len(payload) == 0 {
		return nil, errors.New("payload is required")
	}
	if port <= 0 {
		return nil, errors.New("port is required")
	}
	if interval <= 0 {
		interval = time.Second
	}

	conn, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4zero, Port: 0})
	if err != nil {
		return nil, err
	}
	if err := enableBroadcast(conn); err != nil {
		_ = conn.Close()
		return nil, err
	}

	addrs := broadcastAddrs(port)
	if len(addrs) == 0 {
		_ = conn.Close()
		return nil, errors.New("no broadcast addresses available")
	}

	return &udpBroadcaster{
		conn:     conn,
		addrs:    addrs,
		payload:  payload,
		interval: interval,
	}, nil
}

func (b *udpBroadcaster) Start(ctx context.Context) {
	go b.loop(ctx)
}

func (b *udpBroadcaster) Close() {
	b.closeOnce.Do(func() {
		if b.conn != nil {
			_ = b.conn.Close()
		}
	})
}

func (b *udpBroadcaster) loop(ctx context.Context) {
	ticker := time.NewTicker(b.interval)
	defer ticker.Stop()

	b.sendOnce()
	for {
		select {
		case <-ctx.Done():
			b.Close()
			return
		case <-ticker.C:
			b.sendOnce()
		}
	}
}

func (b *udpBroadcaster) sendOnce() {
	if b.conn == nil || len(b.payload) == 0 {
		return
	}
	_ = b.conn.SetWriteDeadline(time.Now().Add(500 * time.Millisecond))
	for _, addr := range b.addrs {
		if addr == nil {
			continue
		}
		_, _ = b.conn.WriteToUDP(b.payload, addr)
	}
}

func broadcastAddrs(port int) []*net.UDPAddr {
	addrs := []net.UDPAddr{{IP: net.IPv4bcast, Port: port}}
	interfaces, err := net.Interfaces()
	if err != nil {
		return uniqueUDPAddrs(addrs)
	}

	for _, iface := range interfaces {
		if iface.Flags&net.FlagUp == 0 {
			continue
		}
		if iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		ifaceAddrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, addr := range ifaceAddrs {
			ipnet, ok := addr.(*net.IPNet)
			if !ok || ipnet.IP == nil || ipnet.Mask == nil {
				continue
			}
			ip := ipnet.IP.To4()
			if ip == nil {
				continue
			}
			broadcast := make(net.IP, len(ip))
			for i := range ip {
				broadcast[i] = ip[i] | ^ipnet.Mask[i]
			}
			addrs = append(addrs, net.UDPAddr{IP: broadcast, Port: port})
		}
	}

	return uniqueUDPAddrs(addrs)
}

func uniqueUDPAddrs(addrs []net.UDPAddr) []*net.UDPAddr {
	seen := make(map[string]struct{}, len(addrs))
	out := make([]*net.UDPAddr, 0, len(addrs))
	for _, addr := range addrs {
		if addr.IP == nil {
			continue
		}
		key := addr.IP.String() + ":" + strconv.Itoa(addr.Port)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		ip := make(net.IP, len(addr.IP))
		copy(ip, addr.IP)
		out = append(out, &net.UDPAddr{IP: ip, Port: addr.Port})
	}
	return out
}
