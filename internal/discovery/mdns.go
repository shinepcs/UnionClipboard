package discovery

import (
	"context"
	"fmt"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/grandcat/zeroconf"

	"unionclip/internal/model"
)

const ServiceType = "_unionclip._tcp"

type Discovery struct {
	info      model.DeviceInfo
	isPaired  func(deviceID, fp string) bool
	onUpdate  func([]model.DiscoveredDevice)
	mu        sync.Mutex
	server    *zeroconf.Server
	latest    map[string]model.DiscoveredDevice
}

func New(info model.DeviceInfo, isPaired func(deviceID, fp string) bool, onUpdate func([]model.DiscoveredDevice)) *Discovery {
	return &Discovery{
		info:     info,
		isPaired: isPaired,
		onUpdate: onUpdate,
		latest:   make(map[string]model.DiscoveredDevice),
	}
}

func (d *Discovery) Start(ctx context.Context) error {
	txt := []string{
		"id=" + d.info.DeviceID,
		"name=" + sanitizeTXT(d.info.DeviceName),
		"os=" + d.info.OS,
		"fp=" + d.info.CertFingerprint,
		"ver=1",
	}
	instance := fmt.Sprintf("UnionClip-%s", shortID(d.info.DeviceID))
	srv, err := zeroconf.Register(instance, ServiceType, "local.", d.info.Port, txt, nil)
	if err == nil {
		d.mu.Lock()
		d.server = srv
		d.mu.Unlock()
	}

	go d.browseLoop(ctx)
	return err
}

func (d *Discovery) Stop() {
	d.mu.Lock()
	srv := d.server
	d.server = nil
	d.mu.Unlock()
	if srv != nil {
		srv.Shutdown()
	}
}

func (d *Discovery) List() []model.DiscoveredDevice {
	d.mu.Lock()
	defer d.mu.Unlock()
	out := make([]model.DiscoveredDevice, 0, len(d.latest))
	for _, dev := range d.latest {
		if d.isPaired != nil {
			dev.AlreadyPaired = d.isPaired(dev.DeviceID, dev.CertFingerprint)
		}
		out = append(out, dev)
	}
	return out
}

func (d *Discovery) browseLoop(ctx context.Context) {
	d.scanOnce(ctx)
	ticker := time.NewTicker(12 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			d.scanOnce(ctx)
		}
	}
}

func (d *Discovery) scanOnce(ctx context.Context) {
	resolver, err := zeroconf.NewResolver(nil)
	if err != nil {
		return
	}
	entries := make(chan *zeroconf.ServiceEntry, 16)
	scanCtx, cancel := context.WithTimeout(ctx, 3500*time.Millisecond)
	defer cancel()

	found := make(map[string]model.DiscoveredDevice)
	done := make(chan struct{})
	go func() {
		defer close(done)
		for entry := range entries {
			dev, ok := d.parseEntry(entry)
			if !ok {
				continue
			}
			found[dev.DeviceID] = dev
		}
	}()

	_ = resolver.Browse(scanCtx, ServiceType, "local.", entries)
	<-scanCtx.Done()
	<-done

	d.mu.Lock()
	d.latest = found
	d.mu.Unlock()

	if d.onUpdate != nil {
		d.onUpdate(d.List())
	}
}

func (d *Discovery) parseEntry(e *zeroconf.ServiceEntry) (model.DiscoveredDevice, bool) {
	if e == nil {
		return model.DiscoveredDevice{}, false
	}
	kv := make(map[string]string)
	for _, t := range e.Text {
		if idx := strings.IndexByte(t, '='); idx > 0 {
			kv[t[:idx]] = t[idx+1:]
		}
	}
	devID := kv["id"]
	if devID == "" || devID == d.info.DeviceID {
		return model.DiscoveredDevice{}, false
	}
	addr := pickBestIP(e.AddrIPv4, e.AddrIPv6)
	if addr == "" {
		return model.DiscoveredDevice{}, false
	}
	paired := false
	if d.isPaired != nil {
		paired = d.isPaired(devID, kv["fp"])
	}
	return model.DiscoveredDevice{
		DeviceID:        devID,
		DeviceName:      kv["name"],
		OS:              kv["os"],
		CertFingerprint: kv["fp"],
		Addr:            fmt.Sprintf("%s:%d", addr, e.Port),
		Port:            e.Port,
		AlreadyPaired:   paired,
	}, true
}

func pickBestIP(v4, v6 []net.IP) string {
	for _, ip := range v4 {
		if ip != nil && !ip.IsLoopback() && ip.To4() != nil {
			return ip.String()
		}
	}
	for _, ip := range v6 {
		if ip != nil && !ip.IsLoopback() && !ip.IsLinkLocalUnicast() {
			return "[" + ip.String() + "]"
		}
	}
	return ""
}

func sanitizeTXT(s string) string {
	s = strings.ReplaceAll(s, "\n", " ")
	s = strings.ReplaceAll(s, "\r", " ")
	if len(s) > 64 {
		s = s[:64]
	}
	return s
}

func shortID(id string) string {
	if len(id) > 8 {
		return id[:8]
	}
	return id
}
