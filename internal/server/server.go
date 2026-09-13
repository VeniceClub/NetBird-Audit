package server

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"html/template"
	"log"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/ekstools/netbird-audit-v3/internal/auth"
	"github.com/ekstools/netbird-audit-v3/internal/db"
	"github.com/ekstools/netbird-audit-v3/internal/netbird"
)

type Config struct {
	Listen, SensorListen, CertFile, KeyFile, AdminUser, AdminHash, TOTPSecret, SensorKey string
	SecureCookie                                                                         bool
	PollInterval                                                                         time.Duration
}

type Server struct {
	cfg      Config
	db       *db.DB
	nb       *netbird.Client
	sessions *auth.SessionStore
	tpl      *template.Template
	loginMu  sync.Mutex
	failures map[string][]time.Time

	stateMu          sync.RWMutex
	lastNetBirdSync  time.Time
	lastResourceSync time.Time
	lastSensorSeen   time.Time
	sensorInterface  string
}

func New(cfg Config, d *db.DB, nb *netbird.Client) (*Server, error) {
	hk, _ := time.LoadLocation("Asia/Hong_Kong")
	tpl, err := template.New("index").Funcs(template.FuncMap{
		"timefmt": func(t time.Time) string {
			if t.IsZero() {
				return "-"
			}
			if hk != nil {
				t = t.In(hk)
			}
			return t.Format("2006-01-02 15:04:05")
		},
		"timeonly": func(t time.Time) string {
			if t.IsZero() {
				return "-"
			}
			if hk != nil {
				t = t.In(hk)
			}
			return t.Format("15:04:05")
		},
		"dur": func(a, b time.Time) string {
			if b.IsZero() {
				b = time.Now()
			}
			d := b.Sub(a)
			if d < 0 {
				return "-"
			}
			return d.Round(time.Second).String()
		},
		"bytesfmt": humanBytes,
		"add":      func(a, b uint64) uint64 { return a + b },
		"packets":  func(a, b uint64) uint64 { return a + b },
		"timeNow":  func() time.Time { return time.Now() },
		"lower":    strings.ToLower,
		"eq":       func(a, b string) bool { return a == b },
	}).Parse(pageTemplate)
	if err != nil {
		return nil, err
	}
	return &Server{cfg: cfg, db: d, nb: nb, sessions: auth.NewSessionStore(8 * time.Hour), tpl: tpl, failures: map[string][]time.Time{}}, nil
}

func (s *Server) Run(ctx context.Context) error {
	go s.pollNetBird(ctx)
	publicMux := http.NewServeMux()
	publicMux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200); _, _ = w.Write([]byte("ok")) })
	publicMux.HandleFunc("/assets/login.js", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/javascript; charset=utf-8")
		_, _ = w.Write([]byte(loginJS))
	})
	publicMux.HandleFunc("/assets/app.js", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/javascript; charset=utf-8")
		_, _ = w.Write([]byte(appJS))
	})
	publicMux.HandleFunc("/login", s.login)
	publicMux.HandleFunc("/logout", s.logout)
	publicMux.HandleFunc("/", s.requireAuth(s.overview))
	publicMux.HandleFunc("/access-logs", s.requireAuth(s.accessLogs))
	publicMux.HandleFunc("/vpn-sessions", s.requireAuth(s.vpnSessions))
	publicMux.HandleFunc("/resources", s.requireAuth(s.resourcesPage))
	publicMux.HandleFunc("/peers", s.requireAuth(s.peersPage))
	publicMux.HandleFunc("/system-health", s.requireAuth(s.systemHealthPage))
	publicMux.HandleFunc("/resources/sync", s.requireAuth(s.syncResourcesHandler))
	publicMux.HandleFunc("/resources/enable", s.requireAuth(s.enableResource))
	publicMux.HandleFunc("/resources/delete", s.requireAuth(s.deleteResource))
	publicMux.HandleFunc("/export/flows.csv", s.requireAuth(s.exportFlowsCSV))

	sensorMux := http.NewServeMux()
	sensorMux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200); _, _ = w.Write([]byte("ok")) })
	sensorMux.HandleFunc("/api/v1/flows", s.sensorFlows)
	sensorMux.HandleFunc("/api/v1/sensor-heartbeat", s.sensorHeartbeat)

	publicSrv := &http.Server{Addr: s.cfg.Listen, Handler: securityHeaders(publicMux), ReadHeaderTimeout: 10 * time.Second, ReadTimeout: 30 * time.Second, WriteTimeout: 30 * time.Second, IdleTimeout: 60 * time.Second}
	sensorSrv := &http.Server{Addr: s.cfg.SensorListen, Handler: sensorMux, ReadHeaderTimeout: 10 * time.Second, ReadTimeout: 30 * time.Second, WriteTimeout: 30 * time.Second, IdleTimeout: 60 * time.Second}
	errCh := make(chan error, 2)
	go func() {
		log.Printf("sensor API listening on %s", s.cfg.SensorListen)
		if err := sensorSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			errCh <- err
		}
	}()
	go func() {
		log.Printf("audit HTTPS listening on %s", s.cfg.Listen)
		if err := publicSrv.ListenAndServeTLS(s.cfg.CertFile, s.cfg.KeyFile); err != nil && err != http.ErrServerClosed {
			errCh <- err
		}
	}()
	select {
	case <-ctx.Done():
		c, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = publicSrv.Shutdown(c)
		_ = sensorSrv.Shutdown(c)
		return nil
	case err := <-errCh:
		return err
	}
}

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Strict-Transport-Security", "max-age=31536000; includeSubDomains")
		w.Header().Set("Permissions-Policy", "camera=(), microphone=(), geolocation=()")
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data:; form-action 'self'; frame-ancestors 'none'; base-uri 'none'; object-src 'none'")
		next.ServeHTTP(w, r)
	})
}
func clientIP(r *http.Request) string {
	h, _, err := net.SplitHostPort(r.RemoteAddr)
	if err == nil {
		return h
	}
	return r.RemoteAddr
}
func (s *Server) rateOK(ip string) bool {
	s.loginMu.Lock()
	defer s.loginMu.Unlock()
	cut := time.Now().Add(-10 * time.Minute)
	xs := s.failures[ip][:0]
	for _, t := range s.failures[ip] {
		if t.After(cut) {
			xs = append(xs, t)
		}
	}
	s.failures[ip] = xs
	return len(xs) < 8
}
func (s *Server) fail(ip string) {
	s.loginMu.Lock()
	s.failures[ip] = append(s.failures[ip], time.Now())
	s.loginMu.Unlock()
}

func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	if r.Method == "GET" {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(loginPage))
		return
	}
	ip := clientIP(r)
	if !s.rateOK(ip) {
		http.Error(w, "too many failed attempts", http.StatusTooManyRequests)
		return
	}
	_ = r.ParseForm()
	if r.FormValue("username") != s.cfg.AdminUser || !auth.VerifyPassword(s.cfg.AdminHash, r.FormValue("password")) || !auth.ValidateTOTP(s.cfg.TOTPSecret, r.FormValue("totp"), time.Now()) {
		s.fail(ip)
		s.db.AdminAudit(r.Context(), r.FormValue("username"), "login_failed", "", ip)
		http.Error(w, "invalid credentials", http.StatusUnauthorized)
		return
	}
	tok, err := s.sessions.New()
	if err != nil {
		http.Error(w, "session error", 500)
		return
	}
	http.SetCookie(w, &http.Cookie{Name: "nba_session", Value: tok, Path: "/", HttpOnly: true, Secure: s.cfg.SecureCookie, SameSite: http.SameSiteStrictMode, MaxAge: 28800})
	csrf := randToken(24)
	http.SetCookie(w, &http.Cookie{Name: "nba_csrf", Value: csrf, Path: "/", HttpOnly: false, Secure: s.cfg.SecureCookie, SameSite: http.SameSiteStrictMode, MaxAge: 28800})
	s.db.AdminAudit(r.Context(), s.cfg.AdminUser, "login_success", "", ip)
	http.Redirect(w, r, "/", http.StatusSeeOther)
}
func (s *Server) logout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie("nba_session"); err == nil {
		s.sessions.Delete(c.Value)
	}
	http.SetCookie(w, &http.Cookie{Name: "nba_session", Value: "", Path: "/", MaxAge: -1})
	http.SetCookie(w, &http.Cookie{Name: "nba_csrf", Value: "", Path: "/", MaxAge: -1})
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}
func (s *Server) requireAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		c, err := r.Cookie("nba_session")
		if err != nil || !s.sessions.Valid(c.Value) {
			http.Redirect(w, r, "/login", http.StatusSeeOther)
			return
		}
		if r.Method == http.MethodPost || r.Method == http.MethodPut || r.Method == http.MethodDelete || r.Method == http.MethodPatch {
			csrfCookie, err := r.Cookie("nba_csrf")
			if err != nil {
				http.Error(w, "CSRF validation failed", http.StatusForbidden)
				return
			}
			_ = r.ParseForm()
			if csrfCookie.Value == "" || !hmac.Equal([]byte(csrfCookie.Value), []byte(r.FormValue("csrf"))) {
				http.Error(w, "CSRF validation failed", http.StatusForbidden)
				return
			}
		}
		next(w, r)
	}
}

type dashData struct {
	Page, Title, Subtitle, CSRF, AdminUser                                    string
	Online, Peers, Sessions, Flows, Alerts, PeerPct, UniqueUsers, ActiveFlows int
	SessionsList                                                              []db.Session
	FlowsList                                                                 []db.Flow
	Resources                                                                 []db.Resource
	ResourceViews                                                             []db.ResourceView
	PeersList                                                                 []db.PeerRow
	TopResources                                                              []db.ResourceStat
	Hourly                                                                    []db.HourStat
	FilterUsers, FilterResources                                              []string
	FilterPeriod, FilterUser, FilterResource, FilterProtocol, FilterQuery     string
	SensorOK, MySQLOK, NetBirdOK                                              bool
	LastSensorSeen, LastNetBirdSync, LastResourceSync                         time.Time
	SensorInterface                                                           string
}

func (s *Server) baseData(r *http.Request, page, title, subtitle string) dashData {
	o, p, se, f := s.db.Counts(r.Context())
	alerts := s.db.FailedLogins24h(r.Context())
	csrf := ""
	if c, err := r.Cookie("nba_csrf"); err == nil {
		csrf = c.Value
	}
	pct := 0
	if p > 0 {
		pct = o * 100 / p
	}
	s.stateMu.RLock()
	sensorSeen, nbSeen, resSeen, sensorIf := s.lastSensorSeen, s.lastNetBirdSync, s.lastResourceSync, s.sensorInterface
	s.stateMu.RUnlock()
	return dashData{Page: page, Title: title, Subtitle: subtitle, CSRF: csrf, AdminUser: s.cfg.AdminUser, Online: o, Peers: p, Sessions: se, Flows: f, Alerts: alerts, PeerPct: pct, UniqueUsers: s.db.UniqueUsers24h(r.Context()), ActiveFlows: s.db.ActiveFlows24h(r.Context()), SensorOK: time.Since(sensorSeen) < 45*time.Second, MySQLOK: s.db.PingContext(r.Context()) == nil, NetBirdOK: time.Since(nbSeen) < maxDuration(3*s.cfg.PollInterval, 45*time.Second), LastSensorSeen: sensorSeen, LastNetBirdSync: nbSeen, LastResourceSync: resSeen, SensorInterface: sensorIf}
}
func maxDuration(a, b time.Duration) time.Duration {
	if a > b {
		return a
	}
	return b
}
func (s *Server) render(w http.ResponseWriter, d dashData) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := s.tpl.Execute(w, d); err != nil {
		log.Printf("render: %v", err)
	}
}

func (s *Server) overview(w http.ResponseWriter, r *http.Request) {
	d := s.baseData(r, "overview", "Good day, "+s.cfg.AdminUser+" 👋", "Monitor, audit and analyze NetBird access to your internal resources.")
	d.SessionsList, _ = s.db.RecentSessions(r.Context(), 5)
	d.FlowsList, _ = s.db.SearchFlows(r.Context(), db.FlowFilter{Since: time.Now().Add(-24 * time.Hour), Limit: 5})
	d.TopResources, _ = s.db.TopResources24h(r.Context(), 5)
	d.Hourly, _ = s.db.HourlyFlows24h(r.Context())
	s.render(w, d)
}
func (s *Server) accessLogs(w http.ResponseWriter, r *http.Request) {
	d := s.baseData(r, "logs", "Access Logs", "Audit and track NetBird access to internal resources.")
	period := r.URL.Query().Get("period")
	if period == "" {
		period = "24h"
	}
	since := time.Now().Add(-24 * time.Hour)
	switch period {
	case "1h":
		since = time.Now().Add(-time.Hour)
	case "7d":
		since = time.Now().Add(-7 * 24 * time.Hour)
	case "30d":
		since = time.Now().Add(-30 * 24 * time.Hour)
	}
	d.FilterPeriod = period
	d.FilterUser = r.URL.Query().Get("user")
	d.FilterResource = r.URL.Query().Get("resource")
	d.FilterProtocol = r.URL.Query().Get("protocol")
	d.FilterQuery = strings.TrimSpace(r.URL.Query().Get("q"))
	d.FlowsList, _ = s.db.SearchFlows(r.Context(), db.FlowFilter{Since: since, User: d.FilterUser, Resource: d.FilterResource, Protocol: d.FilterProtocol, Query: d.FilterQuery, Limit: 500})
	d.FilterUsers, _ = s.db.FlowUsers(r.Context())
	d.FilterResources, _ = s.db.FlowResources(r.Context())
	s.render(w, d)
}
func (s *Server) vpnSessions(w http.ResponseWriter, r *http.Request) {
	d := s.baseData(r, "sessions", "VPN Sessions", "See who is connected, from which device, and for how long.")
	d.SessionsList, _ = s.db.RecentSessions(r.Context(), 300)
	s.render(w, d)
}
func (s *Server) resourcesPage(w http.ResponseWriter, r *http.Request) {
	d := s.baseData(r, "resources", "Resources", "NetBird is the source of truth. Choose which routed resources should be audited.")
	d.ResourceViews, _ = s.db.ListResourceViews(r.Context())
	d.Resources, _ = s.db.ListResources(r.Context())
	s.render(w, d)
}
func (s *Server) peersPage(w http.ResponseWriter, r *http.Request) {
	d := s.baseData(r, "peers", "Peers", "Identity and connection status synchronized from NetBird.")
	d.PeersList, _ = s.db.ListPeers(r.Context())
	s.render(w, d)
}
func (s *Server) systemHealthPage(w http.ResponseWriter, r *http.Request) {
	d := s.baseData(r, "health", "System Health", "Audit server, eBPF sensor, MySQL and NetBird synchronization status.")
	s.render(w, d)
}

func (s *Server) syncResourcesHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		http.Error(w, "method", 405)
		return
	}
	if err := s.syncResources(r.Context()); err != nil {
		http.Error(w, err.Error(), 502)
		return
	}
	s.db.AdminAudit(r.Context(), s.cfg.AdminUser, "netbird_resources_sync", "", clientIP(r))
	http.Redirect(w, r, "/resources", 303)
}
func (s *Server) enableResource(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		http.Error(w, "method", 405)
		return
	}
	_ = r.ParseForm()
	id := r.FormValue("netbird_id")
	proto := strings.ToLower(strings.TrimSpace(r.FormValue("protocol")))
	ports := strings.TrimSpace(r.FormValue("ports"))
	if proto == "" {
		proto = "tcp"
	}
	if ports == "" {
		ports = "*"
	}
	if err := s.db.EnableAuditForNetBirdResource(r.Context(), id, proto, ports); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	s.db.AdminAudit(r.Context(), s.cfg.AdminUser, "resource_audit_enable", id+" "+proto+"/"+ports, clientIP(r))
	http.Redirect(w, r, "/resources", 303)
}
func (s *Server) deleteResource(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		http.Error(w, "method", 405)
		return
	}
	_ = r.ParseForm()
	id, _ := strconv.ParseInt(r.FormValue("id"), 10, 64)
	_ = s.db.DeleteResource(r.Context(), id)
	s.db.AdminAudit(r.Context(), s.cfg.AdminUser, "resource_audit_disable", fmt.Sprint(id), clientIP(r))
	http.Redirect(w, r, "/resources", 303)
}

func (s *Server) exportFlowsCSV(w http.ResponseWriter, r *http.Request) {
	fs, _ := s.db.SearchFlows(r.Context(), db.FlowFilter{Since: time.Now().Add(-30 * 24 * time.Hour), Limit: 1000})
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", "attachment; filename=netbird-access-logs.csv")
	cw := csv.NewWriter(w)
	defer cw.Flush()
	_ = cw.Write([]string{"started_at", "last_seen_at", "user", "device", "source_ip", "resource", "destination", "protocol", "traffic_bytes", "packets", "status"})
	for _, f := range fs {
		user := f.UserEmail
		if user == "" {
			user = f.UserName
		}
		status := "active"
		if f.Ended {
			status = "ended"
		}
		_ = cw.Write([]string{f.StartedAt.UTC().Format(time.RFC3339), f.LastSeenAt.UTC().Format(time.RFC3339), user, f.PeerName, f.SrcIP, f.ResourceName, fmt.Sprintf("%s:%d", f.DstIP, f.DstPort), f.Protocol, fmt.Sprint(f.BytesTX + f.BytesRX), fmt.Sprint(f.PacketsTX + f.PacketsRX), status})
	}
}

type sensorFlow struct {
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

func (s *Server) sensorFlows(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		http.Error(w, "method", 405)
		return
	}
	if !hmac.Equal([]byte(r.Header.Get("X-Sensor-Key")), []byte(s.cfg.SensorKey)) {
		http.Error(w, "unauthorized", 401)
		return
	}
	var fs []sensorFlow
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 2<<20)).Decode(&fs); err != nil {
		http.Error(w, "bad json", 400)
		return
	}
	stored := 0
	for _, f := range fs {
		ok, err := s.db.UpsertFlow(r.Context(), db.FlowUpsert{FlowKey: f.FlowKey, StartedAt: f.StartedAt, LastSeenAt: f.LastSeenAt, SrcIP: f.SrcIP, DstIP: f.DstIP, Protocol: f.Protocol, SrcPort: f.SrcPort, DstPort: f.DstPort, BytesTX: f.BytesTX, BytesRX: f.BytesRX, PacketsTX: f.PacketsTX, PacketsRX: f.PacketsRX, Ended: f.Ended})
		if err == nil && ok {
			stored++
		}
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = fmt.Fprintf(w, "{\"stored\":%d}\n", stored)
}
func (s *Server) sensorHeartbeat(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		http.Error(w, "method", 405)
		return
	}
	if !hmac.Equal([]byte(r.Header.Get("X-Sensor-Key")), []byte(s.cfg.SensorKey)) {
		http.Error(w, "unauthorized", 401)
		return
	}
	var p struct {
		Interface string `json:"interface"`
	}
	_ = json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&p)
	s.stateMu.Lock()
	s.lastSensorSeen = time.Now()
	if p.Interface != "" {
		s.sensorInterface = p.Interface
	}
	s.stateMu.Unlock()
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) pollNetBird(ctx context.Context) {
	tick := time.NewTicker(s.cfg.PollInterval)
	defer tick.Stop()
	for {
		if err := s.sync(ctx); err != nil {
			log.Printf("netbird sync: %v", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
	}
}
func (s *Server) sync(ctx context.Context) error {
	users, err := s.nb.Users(ctx)
	if err != nil {
		return err
	}
	um := map[string]db.User{}
	for _, u := range users {
		um[u.ID] = u
		_ = s.db.UpsertUser(ctx, u)
	}
	peers, err := s.nb.Peers(ctx)
	if err != nil {
		return err
	}
	for _, p := range peers {
		_ = s.db.UpsertPeer(ctx, p)
		_ = s.db.ReconcileSession(ctx, p, um[p.UserID])
	}
	s.stateMu.Lock()
	s.lastNetBirdSync = time.Now()
	needResources := s.lastResourceSync.IsZero() || time.Since(s.lastResourceSync) > time.Minute
	s.stateMu.Unlock()
	if needResources {
		if err := s.syncResources(ctx); err != nil {
			log.Printf("netbird resource sync: %v", err)
		}
	}
	return nil
}
func (s *Server) syncResources(ctx context.Context) error {
	rs, err := s.nb.NetworkResources(ctx)
	if err != nil {
		return err
	}
	if err := s.db.ReplaceNetBirdResources(ctx, rs); err != nil {
		return err
	}
	s.stateMu.Lock()
	s.lastResourceSync = time.Now()
	s.stateMu.Unlock()
	return nil
}
func humanBytes(n uint64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := uint64(unit), 0
	for x := n / unit; x >= unit; x /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(n)/float64(div), "KMGTPE"[exp])
}
func randToken(n int) string { b := make([]byte, n); _, _ = rand.Read(b); return fmt.Sprintf("%x", b) }
