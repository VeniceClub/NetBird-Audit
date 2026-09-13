package netbird

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/netip"
	"strings"
	"time"

	"github.com/ekstools/netbird-audit-v3/internal/db"
)

type Client struct {
	BaseURL, Token string
	HTTP           *http.Client
}

func New(baseURL, token string) *Client {
	return &Client{BaseURL: strings.TrimRight(baseURL, "/"), Token: token, HTTP: &http.Client{Timeout: 15 * time.Second}}
}
func (c *Client) get(ctx context.Context, path string, v any) error {
	req, err := http.NewRequestWithContext(ctx, "GET", c.BaseURL+path, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Token "+c.Token)
	req.Header.Set("Accept", "application/json")
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return fmt.Errorf("netbird %s: HTTP %d", path, resp.StatusCode)
	}
	return json.NewDecoder(resp.Body).Decode(v)
}

type apiUser struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Email string `json:"email"`
}
type apiPeer struct {
	ID           string    `json:"id"`
	Name         string    `json:"name"`
	Hostname     string    `json:"hostname"`
	IP           string    `json:"ip"`
	ConnectionIP string    `json:"connection_ip"`
	Connected    bool      `json:"connected"`
	LastSeen     time.Time `json:"last_seen"`
	UserID       string    `json:"user_id"`
}
type apiNetwork struct {
	ID                string `json:"id"`
	Name              string `json:"name"`
	Description       string `json:"description"`
	RoutingPeersCount int    `json:"routing_peers_count"`
}
type apiNetworkResource struct {
	ID          string `json:"id"`
	Type        string `json:"type"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Address     string `json:"address"`
	Enabled     bool   `json:"enabled"`
}

func (c *Client) Users(ctx context.Context) ([]db.User, error) {
	var a []apiUser
	if err := c.get(ctx, "/api/users", &a); err != nil {
		return nil, err
	}
	out := make([]db.User, 0, len(a))
	for _, x := range a {
		out = append(out, db.User{ID: x.ID, Name: x.Name, Email: x.Email})
	}
	return out, nil
}

func publicIP(s string) string {
	if s == "" {
		return ""
	}
	if ap, err := netip.ParseAddrPort(s); err == nil {
		return ap.Addr().String()
	}
	if ip, err := netip.ParseAddr(s); err == nil {
		return ip.String()
	}
	if i := strings.LastIndex(s, ":"); i > 0 {
		return strings.Trim(s[:i], "[]")
	}
	return s
}

func (c *Client) Peers(ctx context.Context) ([]db.Peer, error) {
	var a []apiPeer
	if err := c.get(ctx, "/api/peers", &a); err != nil {
		return nil, err
	}
	out := make([]db.Peer, 0, len(a))
	for _, x := range a {
		name := x.Name
		if name == "" {
			name = x.Hostname
		}
		out = append(out, db.Peer{ID: x.ID, UserID: x.UserID, Name: name, IP: x.IP, PublicIP: publicIP(x.ConnectionIP), Connected: x.Connected, LastSeen: x.LastSeen})
	}
	return out, nil
}

func (c *Client) NetworkResources(ctx context.Context) ([]db.NetBirdResource, error) {
	var ns []apiNetwork
	if err := c.get(ctx, "/api/networks", &ns); err != nil {
		return nil, err
	}
	var out []db.NetBirdResource
	for _, n := range ns {
		var rs []apiNetworkResource
		path := fmt.Sprintf("/api/networks/%s/resources", n.ID)
		if err := c.get(ctx, path, &rs); err != nil {
			return nil, err
		}
		for _, r := range rs {
			out = append(out, db.NetBirdResource{ID: r.ID, NetworkID: n.ID, NetworkName: n.Name, Name: r.Name, Address: r.Address, Type: r.Type, Enabled: r.Enabled})
		}
	}
	return out, nil
}
