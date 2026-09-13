package db

import (
	"context"
	"database/sql"
	"fmt"
	"net/netip"
	"strings"
	"time"

	_ "github.com/go-sql-driver/mysql"
)

type DB struct{ *sql.DB }

type Peer struct {
	ID, UserID, Name, IP, PublicIP string
	Connected                      bool
	LastSeen                       time.Time
}

type User struct{ ID, Name, Email string }

type Resource struct {
	ID                          int64
	Name, CIDR, Protocol, Ports string
	Enabled                     bool
}

type NetBirdResource struct {
	ID, NetworkID, NetworkName, Name, Address, Type string
	Enabled                                         bool
}

type ResourceView struct {
	NetBirdResource
	AuditID       int64
	AuditEnabled  bool
	AuditProtocol string
	AuditPorts    string
	Supported     bool
}

type Session struct {
	ID                                              int64
	UserEmail, UserName, PeerName, PeerIP, PublicIP string
	ConnectedAt                                     time.Time
	DisconnectedAt                                  sql.NullTime
}

type PeerRow struct {
	Peer
	UserName, UserEmail string
}

type ResourceStat struct {
	Name  string
	Flows int
	Bytes uint64
}

type HourStat struct {
	Hour  time.Time
	Flows int
	Bytes uint64
}

type Flow struct {
	ID                                                                  int64
	StartedAt, LastSeenAt                                               time.Time
	UserEmail, UserName, PeerName, SrcIP, ResourceName, DstIP, Protocol string
	SrcPort, DstPort                                                    uint16
	BytesTX, BytesRX, PacketsTX, PacketsRX                              uint64
	Ended                                                               bool
}

type FlowFilter struct {
	Since    time.Time
	User     string
	Resource string
	Protocol string
	Query    string
	Limit    int
}

func Open(dsn string) (*DB, error) {
	s, err := sql.Open("mysql", dsn)
	if err != nil {
		return nil, err
	}
	s.SetMaxOpenConns(20)
	s.SetMaxIdleConns(10)
	s.SetConnMaxLifetime(30 * time.Minute)
	if err := s.Ping(); err != nil {
		return nil, err
	}
	return &DB{s}, nil
}

func (d *DB) Migrate(ctx context.Context) error {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS users_cache (
 id VARCHAR(128) PRIMARY KEY, name VARCHAR(255), email VARCHAR(320), updated_at DATETIME(6) NOT NULL
) ENGINE=InnoDB`,
		`CREATE TABLE IF NOT EXISTS peers_cache (
 id VARCHAR(128) PRIMARY KEY, user_id VARCHAR(128), name VARCHAR(255), ip VARCHAR(64), public_ip VARCHAR(128), connected BOOLEAN NOT NULL DEFAULT FALSE, last_seen DATETIME(6) NULL, updated_at DATETIME(6) NOT NULL,
 INDEX idx_peer_ip(ip), INDEX idx_peer_user(user_id), INDEX idx_peer_connected(connected)
) ENGINE=InnoDB`,
		`CREATE TABLE IF NOT EXISTS vpn_sessions (
 id BIGINT UNSIGNED AUTO_INCREMENT PRIMARY KEY, peer_id VARCHAR(128) NOT NULL, user_id VARCHAR(128), user_email_snapshot VARCHAR(320), user_name_snapshot VARCHAR(255), peer_name_snapshot VARCHAR(255), peer_ip_snapshot VARCHAR(64), public_ip_snapshot VARCHAR(128), connected_at DATETIME(6) NOT NULL, disconnected_at DATETIME(6) NULL,
 INDEX idx_session_peer_time(peer_id, connected_at), INDEX idx_session_user_time(user_id, connected_at), INDEX idx_session_active(disconnected_at)
) ENGINE=InnoDB`,
		`CREATE TABLE IF NOT EXISTS resources (
 id BIGINT UNSIGNED AUTO_INCREMENT PRIMARY KEY, name VARCHAR(255) NOT NULL UNIQUE, cidr VARCHAR(64) NOT NULL, protocol VARCHAR(16) NOT NULL DEFAULT 'tcp', ports VARCHAR(255) NOT NULL, enabled BOOLEAN NOT NULL DEFAULT TRUE, created_at DATETIME(6) NOT NULL, updated_at DATETIME(6) NOT NULL
) ENGINE=InnoDB`,
		`CREATE TABLE IF NOT EXISTS netbird_resources_cache (
 id VARCHAR(128) PRIMARY KEY, network_id VARCHAR(128) NOT NULL, network_name VARCHAR(255) NOT NULL, name VARCHAR(255) NOT NULL, address VARCHAR(255) NOT NULL, type VARCHAR(32), enabled BOOLEAN NOT NULL DEFAULT TRUE, updated_at DATETIME(6) NOT NULL,
 INDEX idx_nb_res_network(network_id), INDEX idx_nb_res_name(name)
) ENGINE=InnoDB`,
		`CREATE TABLE IF NOT EXISTS network_flows (
 id BIGINT UNSIGNED AUTO_INCREMENT PRIMARY KEY, flow_key VARCHAR(255) NOT NULL UNIQUE, started_at DATETIME(6) NOT NULL, last_seen_at DATETIME(6) NOT NULL, ended_at DATETIME(6) NULL, user_id VARCHAR(128), user_email_snapshot VARCHAR(320), user_name_snapshot VARCHAR(255), peer_id VARCHAR(128), peer_name_snapshot VARCHAR(255), src_ip VARCHAR(64) NOT NULL, src_port INT UNSIGNED NOT NULL, resource_id BIGINT UNSIGNED, resource_name_snapshot VARCHAR(255), dst_ip VARCHAR(64) NOT NULL, dst_port INT UNSIGNED NOT NULL, protocol VARCHAR(16) NOT NULL, bytes_tx BIGINT UNSIGNED NOT NULL DEFAULT 0, bytes_rx BIGINT UNSIGNED NOT NULL DEFAULT 0, packets_tx BIGINT UNSIGNED NOT NULL DEFAULT 0, packets_rx BIGINT UNSIGNED NOT NULL DEFAULT 0,
 INDEX idx_flow_time(started_at), INDEX idx_flow_user_time(user_id, started_at), INDEX idx_flow_resource_time(resource_id, started_at), INDEX idx_flow_dst(dst_ip, dst_port, started_at)
) ENGINE=InnoDB`,
		`CREATE TABLE IF NOT EXISTS admin_audit (
 id BIGINT UNSIGNED AUTO_INCREMENT PRIMARY KEY, occurred_at DATETIME(6) NOT NULL, admin_user VARCHAR(255) NOT NULL, action VARCHAR(128) NOT NULL, detail TEXT, remote_ip VARCHAR(128), INDEX idx_admin_time(occurred_at)
) ENGINE=InnoDB`,
	}
	for _, q := range stmts {
		if _, err := d.ExecContext(ctx, q); err != nil {
			return fmt.Errorf("migration failed: %w", err)
		}
	}
	return nil
}

func (d *DB) PingContext(ctx context.Context) error { return d.DB.PingContext(ctx) }

func (d *DB) UpsertUser(ctx context.Context, u User) error {
	_, err := d.ExecContext(ctx, `INSERT INTO users_cache(id,name,email,updated_at) VALUES(?,?,?,NOW(6)) ON DUPLICATE KEY UPDATE name=VALUES(name),email=VALUES(email),updated_at=NOW(6)`, u.ID, u.Name, u.Email)
	return err
}

func (d *DB) UpsertPeer(ctx context.Context, p Peer) error {
	var last any
	if p.LastSeen.IsZero() {
		last = nil
	} else {
		last = p.LastSeen.UTC()
	}
	_, err := d.ExecContext(ctx, `INSERT INTO peers_cache(id,user_id,name,ip,public_ip,connected,last_seen,updated_at) VALUES(?,?,?,?,?,?,?,NOW(6)) ON DUPLICATE KEY UPDATE user_id=VALUES(user_id),name=VALUES(name),ip=VALUES(ip),public_ip=VALUES(public_ip),connected=VALUES(connected),last_seen=VALUES(last_seen),updated_at=NOW(6)`, p.ID, p.UserID, p.Name, p.IP, p.PublicIP, p.Connected, last)
	return err
}

func (d *DB) ReconcileSession(ctx context.Context, p Peer, u User) error {
	var activeID uint64
	err := d.QueryRowContext(ctx, `SELECT id FROM vpn_sessions WHERE peer_id=? AND disconnected_at IS NULL ORDER BY id DESC LIMIT 1`, p.ID).Scan(&activeID)
	if err != nil && err != sql.ErrNoRows {
		return err
	}
	if p.Connected && err == sql.ErrNoRows {
		_, e := d.ExecContext(ctx, `INSERT INTO vpn_sessions(peer_id,user_id,user_email_snapshot,user_name_snapshot,peer_name_snapshot,peer_ip_snapshot,public_ip_snapshot,connected_at) VALUES(?,?,?,?,?,?,?,NOW(6))`, p.ID, p.UserID, u.Email, u.Name, p.Name, p.IP, p.PublicIP)
		return e
	}
	if !p.Connected && err == nil {
		_, e := d.ExecContext(ctx, `UPDATE vpn_sessions SET disconnected_at=COALESCE(?,NOW(6)) WHERE id=?`, nullableTime(p.LastSeen), activeID)
		return e
	}
	return nil
}

func nullableTime(t time.Time) any {
	if t.IsZero() {
		return nil
	}
	return t.UTC()
}

func (d *DB) ListResources(ctx context.Context) ([]Resource, error) {
	rows, err := d.QueryContext(ctx, `SELECT id,name,cidr,protocol,ports,enabled FROM resources ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Resource
	for rows.Next() {
		var r Resource
		if err := rows.Scan(&r.ID, &r.Name, &r.CIDR, &r.Protocol, &r.Ports, &r.Enabled); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (d *DB) AddResource(ctx context.Context, r Resource) error {
	_, err := d.ExecContext(ctx, `INSERT INTO resources(name,cidr,protocol,ports,enabled,created_at,updated_at) VALUES(?,?,?,?,?,NOW(6),NOW(6)) ON DUPLICATE KEY UPDATE cidr=VALUES(cidr),protocol=VALUES(protocol),ports=VALUES(ports),enabled=VALUES(enabled),updated_at=NOW(6)`, r.Name, r.CIDR, strings.ToLower(r.Protocol), r.Ports, r.Enabled)
	return err
}
func (d *DB) DeleteResource(ctx context.Context, id int64) error {
	_, err := d.ExecContext(ctx, `DELETE FROM resources WHERE id=?`, id)
	return err
}

func (d *DB) ReplaceNetBirdResources(ctx context.Context, rs []NetBirdResource) error {
	tx, err := d.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `DELETE FROM netbird_resources_cache`); err != nil {
		return err
	}
	for _, r := range rs {
		if _, err := tx.ExecContext(ctx, `INSERT INTO netbird_resources_cache(id,network_id,network_name,name,address,type,enabled,updated_at) VALUES(?,?,?,?,?,?,?,NOW(6))`, r.ID, r.NetworkID, r.NetworkName, r.Name, r.Address, r.Type, r.Enabled); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func normalizeAuditCIDR(addr string) (string, bool) {
	addr = strings.TrimSpace(addr)
	if p, err := netip.ParsePrefix(addr); err == nil {
		return p.String(), true
	}
	if ip, err := netip.ParseAddr(addr); err == nil {
		bits := 32
		if ip.Is6() {
			bits = 128
		}
		return netip.PrefixFrom(ip, bits).String(), true
	}
	return "", false
}

func (d *DB) ListResourceViews(ctx context.Context) ([]ResourceView, error) {
	rows, err := d.QueryContext(ctx, `SELECT n.id,n.network_id,n.network_name,n.name,n.address,n.type,n.enabled,
 COALESCE(a.id,0),COALESCE(a.enabled,0),COALESCE(a.protocol,''),COALESCE(a.ports,'')
 FROM netbird_resources_cache n LEFT JOIN resources a ON a.name=n.name AND a.cidr=CASE
 WHEN INSTR(n.address,'/')>0 THEN n.address
 ELSE CONCAT(n.address, CASE WHEN INSTR(n.address,':')>0 THEN '/128' ELSE '/32' END) END
 ORDER BY n.network_name,n.name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ResourceView
	for rows.Next() {
		var v ResourceView
		if err := rows.Scan(&v.ID, &v.NetworkID, &v.NetworkName, &v.Name, &v.Address, &v.Type, &v.Enabled, &v.AuditID, &v.AuditEnabled, &v.AuditProtocol, &v.AuditPorts); err != nil {
			return nil, err
		}
		_, v.Supported = normalizeAuditCIDR(v.Address)
		out = append(out, v)
	}
	return out, rows.Err()
}

func (d *DB) EnableAuditForNetBirdResource(ctx context.Context, nbID, protocol, ports string) error {
	var r NetBirdResource
	err := d.QueryRowContext(ctx, `SELECT id,network_id,network_name,name,address,type,enabled FROM netbird_resources_cache WHERE id=?`, nbID).Scan(&r.ID, &r.NetworkID, &r.NetworkName, &r.Name, &r.Address, &r.Type, &r.Enabled)
	if err != nil {
		return err
	}
	cidr, ok := normalizeAuditCIDR(r.Address)
	if !ok {
		return fmt.Errorf("resource address %q is not an IP/CIDR; eBPF flow auditing requires an IP resource", r.Address)
	}
	if protocol != "tcp" && protocol != "udp" {
		return fmt.Errorf("protocol must be tcp or udp")
	}
	if strings.TrimSpace(ports) == "" {
		ports = "*"
	}
	return d.AddResource(ctx, Resource{Name: r.Name, CIDR: cidr, Protocol: protocol, Ports: ports, Enabled: true})
}

func portAllowed(spec string, p uint16) bool {
	if strings.TrimSpace(spec) == "" || strings.TrimSpace(spec) == "*" {
		return true
	}
	for _, part := range strings.Split(spec, ",") {
		part = strings.TrimSpace(part)
		var a, b int
		if _, err := fmt.Sscanf(part, "%d-%d", &a, &b); err == nil && int(p) >= a && int(p) <= b {
			return true
		}
		var one int
		if _, err := fmt.Sscanf(part, "%d", &one); err == nil && int(p) == one {
			return true
		}
	}
	return false
}

func (d *DB) MatchResource(ctx context.Context, dst string, port uint16, proto string) (Resource, bool, error) {
	rs, err := d.ListResources(ctx)
	if err != nil {
		return Resource{}, false, err
	}
	ip, err := netip.ParseAddr(dst)
	if err != nil {
		return Resource{}, false, nil
	}
	for _, r := range rs {
		if !r.Enabled || strings.ToLower(r.Protocol) != strings.ToLower(proto) {
			continue
		}
		pfx, err := netip.ParsePrefix(r.CIDR)
		if err != nil {
			continue
		}
		if pfx.Contains(ip) && portAllowed(r.Ports, port) {
			return r, true, nil
		}
	}
	return Resource{}, false, nil
}

func (d *DB) PeerByIP(ctx context.Context, ip string) (Peer, User, error) {
	var p Peer
	var u User
	var last sql.NullTime
	err := d.QueryRowContext(ctx, `SELECT p.id,p.user_id,p.name,p.ip,p.public_ip,p.connected,p.last_seen,COALESCE(u.name,''),COALESCE(u.email,'') FROM peers_cache p LEFT JOIN users_cache u ON u.id=p.user_id WHERE p.ip=? LIMIT 1`, ip).Scan(&p.ID, &p.UserID, &p.Name, &p.IP, &p.PublicIP, &p.Connected, &last, &u.Name, &u.Email)
	if last.Valid {
		p.LastSeen = last.Time
	}
	u.ID = p.UserID
	return p, u, err
}

type FlowUpsert struct {
	FlowKey                                string
	StartedAt, LastSeenAt                  time.Time
	SrcIP, DstIP, Protocol                 string
	SrcPort, DstPort                       uint16
	BytesTX, BytesRX, PacketsTX, PacketsRX uint64
	Ended                                  bool
}

func (d *DB) UpsertFlow(ctx context.Context, f FlowUpsert) (bool, error) {
	r, ok, err := d.MatchResource(ctx, f.DstIP, f.DstPort, f.Protocol)
	if err != nil || !ok {
		return false, err
	}
	p, u, err := d.PeerByIP(ctx, f.SrcIP)
	if err != nil && err != sql.ErrNoRows {
		return false, err
	}
	var peerID, userID, peerName, userEmail, userName string
	if err == nil {
		peerID, userID, peerName, userEmail, userName = p.ID, p.UserID, p.Name, u.Email, u.Name
	}
	var ended any = nil
	if f.Ended {
		ended = f.LastSeenAt.UTC()
	}
	_, err = d.ExecContext(ctx, `INSERT INTO network_flows(flow_key,started_at,last_seen_at,ended_at,user_id,user_email_snapshot,user_name_snapshot,peer_id,peer_name_snapshot,src_ip,src_port,resource_id,resource_name_snapshot,dst_ip,dst_port,protocol,bytes_tx,bytes_rx,packets_tx,packets_rx) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?) ON DUPLICATE KEY UPDATE last_seen_at=VALUES(last_seen_at),ended_at=COALESCE(VALUES(ended_at),ended_at),bytes_tx=VALUES(bytes_tx),bytes_rx=VALUES(bytes_rx),packets_tx=VALUES(packets_tx),packets_rx=VALUES(packets_rx),user_id=COALESCE(NULLIF(VALUES(user_id),''),user_id),user_email_snapshot=COALESCE(NULLIF(VALUES(user_email_snapshot),''),user_email_snapshot),user_name_snapshot=COALESCE(NULLIF(VALUES(user_name_snapshot),''),user_name_snapshot),peer_id=COALESCE(NULLIF(VALUES(peer_id),''),peer_id),peer_name_snapshot=COALESCE(NULLIF(VALUES(peer_name_snapshot),''),peer_name_snapshot)`, f.FlowKey, f.StartedAt.UTC(), f.LastSeenAt.UTC(), ended, userID, userEmail, userName, peerID, peerName, f.SrcIP, f.SrcPort, r.ID, r.Name, f.DstIP, f.DstPort, strings.ToLower(f.Protocol), f.BytesTX, f.BytesRX, f.PacketsTX, f.PacketsRX)
	return true, err
}

func (d *DB) RecentSessions(ctx context.Context, limit int) ([]Session, error) {
	rows, err := d.QueryContext(ctx, `SELECT id,user_email_snapshot,user_name_snapshot,peer_name_snapshot,peer_ip_snapshot,public_ip_snapshot,connected_at,disconnected_at FROM vpn_sessions ORDER BY id DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Session
	for rows.Next() {
		var s Session
		if err := rows.Scan(&s.ID, &s.UserEmail, &s.UserName, &s.PeerName, &s.PeerIP, &s.PublicIP, &s.ConnectedAt, &s.DisconnectedAt); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

func (d *DB) SearchFlows(ctx context.Context, f FlowFilter) ([]Flow, error) {
	if f.Limit <= 0 || f.Limit > 1000 {
		f.Limit = 200
	}
	q := `SELECT id,started_at,last_seen_at,COALESCE(user_email_snapshot,''),COALESCE(user_name_snapshot,''),COALESCE(peer_name_snapshot,''),src_ip,COALESCE(resource_name_snapshot,''),dst_ip,dst_port,protocol,src_port,bytes_tx,bytes_rx,packets_tx,packets_rx,ended_at IS NOT NULL FROM network_flows WHERE 1=1`
	var args []any
	if !f.Since.IsZero() {
		q += " AND started_at>=?"
		args = append(args, f.Since.UTC())
	}
	if f.User != "" {
		q += " AND (user_email_snapshot=? OR user_name_snapshot=?)"
		args = append(args, f.User, f.User)
	}
	if f.Resource != "" {
		q += " AND resource_name_snapshot=?"
		args = append(args, f.Resource)
	}
	if f.Protocol != "" {
		q += " AND protocol=?"
		args = append(args, strings.ToLower(f.Protocol))
	}
	if f.Query != "" {
		like := "%" + f.Query + "%"
		q += " AND (user_email_snapshot LIKE ? OR user_name_snapshot LIKE ? OR peer_name_snapshot LIKE ? OR src_ip LIKE ? OR dst_ip LIKE ? OR resource_name_snapshot LIKE ?)"
		args = append(args, like, like, like, like, like, like)
	}
	q += " ORDER BY last_seen_at DESC LIMIT ?"
	args = append(args, f.Limit)
	rows, err := d.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Flow
	for rows.Next() {
		var x Flow
		if err := rows.Scan(&x.ID, &x.StartedAt, &x.LastSeenAt, &x.UserEmail, &x.UserName, &x.PeerName, &x.SrcIP, &x.ResourceName, &x.DstIP, &x.DstPort, &x.Protocol, &x.SrcPort, &x.BytesTX, &x.BytesRX, &x.PacketsTX, &x.PacketsRX, &x.Ended); err != nil {
			return nil, err
		}
		out = append(out, x)
	}
	return out, rows.Err()
}

func (d *DB) RecentFlows(ctx context.Context, limit int) ([]Flow, error) {
	return d.SearchFlows(ctx, FlowFilter{Limit: limit})
}

func (d *DB) ListPeers(ctx context.Context) ([]PeerRow, error) {
	rows, err := d.QueryContext(ctx, `SELECT p.id,p.user_id,p.name,p.ip,p.public_ip,p.connected,p.last_seen,COALESCE(u.name,''),COALESCE(u.email,'') FROM peers_cache p LEFT JOIN users_cache u ON u.id=p.user_id ORDER BY p.connected DESC,p.name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []PeerRow
	for rows.Next() {
		var x PeerRow
		var last sql.NullTime
		if err := rows.Scan(&x.ID, &x.UserID, &x.Name, &x.IP, &x.PublicIP, &x.Connected, &last, &x.UserName, &x.UserEmail); err != nil {
			return nil, err
		}
		if last.Valid {
			x.LastSeen = last.Time
		}
		out = append(out, x)
	}
	return out, rows.Err()
}

func (d *DB) FlowUsers(ctx context.Context) ([]string, error) {
	return distinctStrings(ctx, d.DB, `SELECT DISTINCT COALESCE(NULLIF(user_email_snapshot,''),NULLIF(user_name_snapshot,'')) v FROM network_flows WHERE COALESCE(user_email_snapshot,user_name_snapshot,'')<>'' ORDER BY v`)
}
func (d *DB) FlowResources(ctx context.Context) ([]string, error) {
	return distinctStrings(ctx, d.DB, `SELECT DISTINCT resource_name_snapshot v FROM network_flows WHERE COALESCE(resource_name_snapshot,'')<>'' ORDER BY v`)
}
func distinctStrings(ctx context.Context, q sqlQuerier, sqlq string) ([]string, error) {
	rows, err := q.QueryContext(ctx, sqlq)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var v sql.NullString
		if err := rows.Scan(&v); err != nil {
			return nil, err
		}
		if v.Valid && v.String != "" {
			out = append(out, v.String)
		}
	}
	return out, rows.Err()
}

type sqlQuerier interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

func (d *DB) UniqueUsers24h(ctx context.Context) int {
	var n int
	_ = d.QueryRowContext(ctx, `SELECT COUNT(DISTINCT COALESCE(NULLIF(user_id,''),src_ip)) FROM network_flows WHERE started_at>=NOW()-INTERVAL 24 HOUR`).Scan(&n)
	return n
}
func (d *DB) ActiveFlows24h(ctx context.Context) int {
	var n int
	_ = d.QueryRowContext(ctx, `SELECT COUNT(*) FROM network_flows WHERE started_at>=NOW()-INTERVAL 24 HOUR AND ended_at IS NULL`).Scan(&n)
	return n
}

func (d *DB) Counts(ctx context.Context) (online, peers, sessions, flows int) {
	_ = d.QueryRowContext(ctx, `SELECT COUNT(*) FROM peers_cache WHERE connected=1`).Scan(&online)
	_ = d.QueryRowContext(ctx, `SELECT COUNT(*) FROM peers_cache`).Scan(&peers)
	_ = d.QueryRowContext(ctx, `SELECT COUNT(*) FROM vpn_sessions WHERE connected_at>=NOW()-INTERVAL 24 HOUR`).Scan(&sessions)
	_ = d.QueryRowContext(ctx, `SELECT COUNT(*) FROM network_flows WHERE started_at>=NOW()-INTERVAL 24 HOUR`).Scan(&flows)
	return
}
func (d *DB) FailedLogins24h(ctx context.Context) int {
	var n int
	_ = d.QueryRowContext(ctx, `SELECT COUNT(*) FROM admin_audit WHERE action='login_failed' AND occurred_at>=NOW()-INTERVAL 24 HOUR`).Scan(&n)
	return n
}
func (d *DB) TopResources24h(ctx context.Context, limit int) ([]ResourceStat, error) {
	rows, err := d.QueryContext(ctx, `SELECT COALESCE(resource_name_snapshot,'Unknown'),COUNT(*),COALESCE(SUM(bytes_tx+bytes_rx),0) FROM network_flows WHERE started_at>=NOW()-INTERVAL 24 HOUR GROUP BY resource_name_snapshot ORDER BY COUNT(*) DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ResourceStat
	for rows.Next() {
		var x ResourceStat
		if err := rows.Scan(&x.Name, &x.Flows, &x.Bytes); err != nil {
			return nil, err
		}
		out = append(out, x)
	}
	return out, rows.Err()
}
func (d *DB) HourlyFlows24h(ctx context.Context) ([]HourStat, error) {
	rows, err := d.QueryContext(ctx, `SELECT DATE_FORMAT(started_at,'%Y-%m-%d %H:00:00') h,COUNT(*),COALESCE(SUM(bytes_tx+bytes_rx),0) FROM network_flows WHERE started_at>=NOW()-INTERVAL 24 HOUR GROUP BY h ORDER BY h`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []HourStat
	for rows.Next() {
		var raw string
		var x HourStat
		if err := rows.Scan(&raw, &x.Flows, &x.Bytes); err != nil {
			return nil, err
		}
		t, _ := time.ParseInLocation("2006-01-02 15:04:05", raw, time.UTC)
		x.Hour = t
		out = append(out, x)
	}
	return out, rows.Err()
}
func (d *DB) AdminAudit(ctx context.Context, user, action, detail, remote string) {
	_, _ = d.ExecContext(ctx, `INSERT INTO admin_audit(occurred_at,admin_user,action,detail,remote_ip) VALUES(NOW(6),?,?,?,?)`, user, action, detail, remote)
}
