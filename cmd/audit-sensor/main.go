//go:build linux

package main

import (
	"context"
	"flag"
	"github.com/ekstools/netbird-audit-v3/internal/sensor"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"
)

func main() {
	var c sensor.Config
	var interval, idle int
	flag.StringVar(&c.Interface, "interface", "auto", "NetBird interface name or auto")
	flag.StringVar(&c.ObjectPath, "bpf-object", "/opt/netbird-audit/bpf/audit.bpf.o", "eBPF object path")
	flag.StringVar(&c.AuditURL, "audit-url", "http://127.0.0.1:9080/api/v1/flows", "Audit API URL")
	flag.StringVar(&c.SensorKey, "sensor-key", "", "Sensor API key")
	flag.StringVar(&c.OverlayCIDR, "overlay-cidr", "100.64.0.0/10", "NetBird overlay CIDR")
	flag.IntVar(&interval, "interval", 10, "flush interval seconds")
	flag.IntVar(&idle, "idle", 60, "flow idle timeout seconds")
	flag.Parse()
	if c.SensorKey == "" {
		log.Fatal("--sensor-key required")
	}
	c.Interval = time.Duration(interval) * time.Second
	c.IdleTimeout = time.Duration(idle) * time.Second
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	if err := sensor.Run(ctx, c); err != nil {
		log.Fatal(err)
	}
}
