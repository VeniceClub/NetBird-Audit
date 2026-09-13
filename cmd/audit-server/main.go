package main

import (
	"bufio"
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/ekstools/netbird-audit-v3/internal/auth"
	"github.com/ekstools/netbird-audit-v3/internal/db"
	"github.com/ekstools/netbird-audit-v3/internal/netbird"
	"github.com/ekstools/netbird-audit-v3/internal/server"
)

func env(k, d string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return d
}
func main() {
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "hash-password":
			var password string
			if len(os.Args) >= 3 {
				password = os.Args[2]
			} else {
				sc := bufio.NewScanner(os.Stdin)
				if !sc.Scan() {
					log.Fatal("password required on stdin")
				}
				password = sc.Text()
			}
			h, err := auth.HashPassword(password)
			if err != nil {
				log.Fatal(err)
			}
			fmt.Print(h)
			return
		case "generate-totp":
			s, err := auth.GenerateTOTPSecret()
			if err != nil {
				log.Fatal(err)
			}
			fmt.Print(s)
			return
		case "otpauth":
			if len(os.Args) != 5 {
				log.Fatal("usage: audit-server otpauth <secret> <account> <issuer>")
			}
			u, _ := auth.OTPAuthURI(os.Args[2], os.Args[3], os.Args[4])
			fmt.Print(u)
			return
		}
	}
	dsn := env("MYSQL_DSN", "")
	if dsn == "" {
		log.Fatal("MYSQL_DSN required")
	}
	database, err := db.Open(dsn)
	if err != nil {
		log.Fatal(err)
	}
	defer database.Close()
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	if err := database.Migrate(ctx); err != nil {
		log.Fatal(err)
	}
	poll, _ := strconv.Atoi(env("POLL_SECONDS", "10"))
	nb := netbird.New(env("NETBIRD_URL", ""), env("NETBIRD_PAT", ""))
	srv, err := server.New(server.Config{Listen: env("LISTEN_ADDR", "0.0.0.0:9443"), SensorListen: env("SENSOR_LISTEN_ADDR", "127.0.0.1:9080"), CertFile: env("TLS_CERT", "/opt/netbird-audit/certs/audit.crt"), KeyFile: env("TLS_KEY", "/opt/netbird-audit/certs/audit.key"), AdminUser: env("ADMIN_USER", "auditadmin"), AdminHash: env("ADMIN_HASH", ""), TOTPSecret: env("TOTP_SECRET", ""), SensorKey: env("SENSOR_KEY", ""), SecureCookie: env("SECURE_COOKIE", "true") == "true", PollInterval: time.Duration(poll) * time.Second}, database, nb)
	if err != nil {
		log.Fatal(err)
	}
	if err := srv.Run(ctx); err != nil {
		log.Fatal(err)
	}
}
