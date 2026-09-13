package sensor

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/netip"
	"strings"
	"time"

	"github.com/cilium/ebpf"
	"github.com/cilium/ebpf/link"
	"github.com/cilium/ebpf/rlimit"
	"golang.org/x/sys/unix"
)

type Config struct {
	Interface, ObjectPath, AuditURL, SensorKey, OverlayCIDR string
	Interval, IdleTimeout                                   time.Duration
}
type flowKey struct {
	SrcIP   [4]byte
	DstIP   [4]byte
	SrcPort uint16
	DstPort uint16
	Proto   uint8
	Pad     [3]byte
}
type flowVal struct {
	StartNS   uint64
	LastNS    uint64
	BytesTX   uint64
	BytesRX   uint64
	PacketsTX uint64
	PacketsRX uint64
	Ended     uint8
	Pad       [7]byte
}
type flowEvent struct {
	FlowKey    string    `json:"flow_key"`
	StartedAt  time.Time `json:"started_at"`
	LastSeenAt time.Time `json:"last_seen_at"`
	SrcIP      string    `json:"src_ip"`
	DstIP      string    `json:"dst_ip"`
	Protocol   string    `json:"protocol"`
	SrcPort    uint16    `json:"src_port"`
	DstPort    uint16    `json:"dst_port"`
	BytesTX    uint64    `json:"bytes_tx"`
	BytesRX    uint64    `json:"bytes_rx"`
	PacketsTX  uint64    `json:"packets_tx"`
	PacketsRX  uint64    `json:"packets_rx"`
	Ended      bool      `json:"ended"`
}

func autoInterface(cidr string) (string, error) {
	pfx, err := netip.ParsePrefix(cidr)
	if err != nil {
		return "", err
	}
	ifs, err := net.Interfaces()
	if err != nil {
		return "", err
	}
	for _, i := range ifs {
		as, _ := i.Addrs()
		for _, a := range as {
			ipstr, _, _ := net.ParseCIDR(a.String())
			if ipstr == nil {
				continue
			}
			addr, ok := netip.AddrFromSlice(ipstr)
			if ok && pfx.Contains(addr.Unmap()) {
				return i.Name, nil
			}
		}
	}
	return "", fmt.Errorf("no interface address found in %s", cidr)
}
func Run(ctx context.Context, cfg Config) error {
	if err := rlimit.RemoveMemlock(); err != nil {
		log.Printf("remove memlock: %v", err)
	}
	if cfg.Interface == "" || cfg.Interface == "auto" {
		x, err := autoInterface(cfg.OverlayCIDR)
		if err != nil {
			return err
		}
		cfg.Interface = x
	}
	iface, err := net.InterfaceByName(cfg.Interface)
	if err != nil {
		return err
	}
	spec, err := ebpf.LoadCollectionSpec(cfg.ObjectPath)
	if err != nil {
		return fmt.Errorf("load BPF object: %w", err)
	}
	coll, err := ebpf.NewCollection(spec)
	if err != nil {
		return fmt.Errorf("load BPF collection: %w", err)
	}
	defer coll.Close()
	ing := coll.Programs["audit_ingress"]
	eg := coll.Programs["audit_egress"]
	flows := coll.Maps["flows"]
	if ing == nil || eg == nil || flows == nil {
		return fmt.Errorf("BPF object missing programs/maps")
	}
	li, err := link.AttachTCX(link.TCXOptions{Interface: iface.Index, Program: ing, Attach: ebpf.AttachTCXIngress})
	if err != nil {
		return fmt.Errorf("attach TCX ingress on %s: %w", cfg.Interface, err)
	}
	defer li.Close()
	le, err := link.AttachTCX(link.TCXOptions{Interface: iface.Index, Program: eg, Attach: ebpf.AttachTCXEgress})
	if err != nil {
		return fmt.Errorf("attach TCX egress on %s: %w", cfg.Interface, err)
	}
	defer le.Close()
	pfx, err := netip.ParsePrefix(cfg.OverlayCIDR)
	if err != nil {
		return err
	}
	client := &http.Client{Timeout: 15 * time.Second}
	ticker := time.NewTicker(cfg.Interval)
	defer ticker.Stop()
	log.Printf("eBPF TCX sensor attached to %s (ifindex=%d), overlay=%s", cfg.Interface, iface.Index, pfx)
	for {
		if err := heartbeat(ctx, cfg, client); err != nil {
			log.Printf("sensor heartbeat: %v", err)
		}
		if err := flush(ctx, flows, pfx, cfg, client); err != nil {
			log.Printf("sensor flush: %v", err)
		}
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
	}
}
func monoNS() uint64 {
	var ts unix.Timespec
	_ = unix.ClockGettime(unix.CLOCK_MONOTONIC, &ts)
	return uint64(ts.Sec)*1e9 + uint64(ts.Nsec)
}
func ip4(b [4]byte) netip.Addr { return netip.AddrFrom4(b) }
func protoName(p uint8) string {
	if p == 6 {
		return "tcp"
	}
	if p == 17 {
		return "udp"
	}
	return fmt.Sprint(p)
}
func wallFromMono(ns, nowMono uint64, now time.Time) time.Time {
	if ns == 0 || ns > nowMono {
		return now
	}
	return now.Add(-time.Duration(nowMono - ns))
}
func heartbeat(ctx context.Context, cfg Config, client *http.Client) error {
	url := strings.TrimSuffix(cfg.AuditURL, "/flows") + "/sensor-heartbeat"
	body, _ := json.Marshal(map[string]string{"interface": cfg.Interface})
	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Sensor-Key", cfg.SensorKey)
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("heartbeat HTTP %d", resp.StatusCode)
	}
	return nil
}

func flush(ctx context.Context, m *ebpf.Map, pfx netip.Prefix, cfg Config, client *http.Client) error {
	now := time.Now()
	mn := monoNS()
	var events []flowEvent
	var dels []flowKey
	it := m.Iterate()
	var k flowKey
	var v flowVal
	for it.Next(&k, &v) {
		src, dst := ip4(k.SrcIP), ip4(k.DstIP)
		if !pfx.Contains(src) {
			continue
		}
		idle := mn > v.LastNS && time.Duration(mn-v.LastNS) > cfg.IdleTimeout
		ended := v.Ended != 0 || idle
		start, last := wallFromMono(v.StartNS, mn, now), wallFromMono(v.LastNS, mn, now)
		fk := fmt.Sprintf("%s:%d>%s:%d/%s@%d", src, k.SrcPort, dst, k.DstPort, protoName(k.Proto), v.StartNS)
		events = append(events, flowEvent{FlowKey: fk, StartedAt: start.UTC(), LastSeenAt: last.UTC(), SrcIP: src.String(), DstIP: dst.String(), Protocol: protoName(k.Proto), SrcPort: k.SrcPort, DstPort: k.DstPort, BytesTX: v.BytesTX, BytesRX: v.BytesRX, PacketsTX: v.PacketsTX, PacketsRX: v.PacketsRX, Ended: ended})
		if ended {
			dels = append(dels, k)
		}
	}
	if err := it.Err(); err != nil {
		return err
	}
	if len(events) == 0 {
		return nil
	}
	body, _ := json.Marshal(events)
	req, err := http.NewRequestWithContext(ctx, "POST", cfg.AuditURL, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Sensor-Key", cfg.SensorKey)
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("audit API HTTP %d", resp.StatusCode)
	}
	for _, dk := range dels {
		_ = m.Delete(dk)
	}
	return nil
}
