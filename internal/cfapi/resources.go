package cfapi

import (
	"context"
	"net/url"
	"strconv"
	"strings"
)

// Zone is a Cloudflare zone.
type Zone struct {
	ID     string   `json:"id"`
	Name   string   `json:"name"`
	Status string   `json:"status"`
	Paused bool     `json:"paused"`
	Type   string   `json:"type"`
	NameServers []string `json:"name_servers"`
	Account struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	} `json:"account"`
	Plan struct {
		Name string `json:"name"`
	} `json:"plan"`
}

// DNSRecord is a DNS record.
type DNSRecord struct {
	ID        string `json:"id,omitempty"`
	ZoneID    string `json:"zone_id,omitempty"`
	Type      string `json:"type"`
	Name      string `json:"name"`
	Content   string `json:"content"`
	TTL       int    `json:"ttl"`
	Proxied   bool   `json:"proxied"`
	Priority  *int   `json:"priority,omitempty"`
	Comment   string `json:"comment,omitempty"`
	CreatedOn string `json:"created_on,omitempty"`
	ModifiedOn string `json:"modified_on,omitempty"`
}

// TokenVerify is the result of verifying a token.
type TokenVerify struct {
	ID     string `json:"id"`
	Status string `json:"status"`
}

// VerifyToken checks the current API token.
func (c *Client) VerifyToken(ctx context.Context) (*TokenVerify, error) {
	var v TokenVerify
	if err := c.do(ctx, "GET", "/user/tokens/verify", nil, &v); err != nil {
		return nil, err
	}
	return &v, nil
}

// Zones lists zones, optionally filtered by exact name.
func (c *Client) Zones(ctx context.Context, name string) ([]Zone, error) {
	path := "/zones"
	if name != "" {
		path += q(url.Values{"name": {name}})
	}
	var zs []Zone
	if err := c.doList(ctx, path, &zs); err != nil {
		return nil, err
	}
	return zs, nil
}

// Zone fetches one zone.
func (c *Client) Zone(ctx context.Context, id string) (*Zone, error) {
	var z Zone
	if err := c.do(ctx, "GET", "/zones/"+id, nil, &z); err != nil {
		return nil, err
	}
	return &z, nil
}

// ZoneByName returns the zone whose name matches, or an error.
func (c *Client) ZoneByName(ctx context.Context, name string) (*Zone, error) {
	zs, err := c.Zones(ctx, name)
	if err != nil {
		return nil, err
	}
	if len(zs) == 0 {
		return nil, errNotFound(name)
	}
	return &zs[0], nil
}

// ZoneForFQDN returns the registered zone that is the longest suffix of fqdn.
func (c *Client) ZoneForFQDN(ctx context.Context, fqdn string) (*Zone, error) {
	fqdn = strings.TrimSuffix(strings.TrimPrefix(fqdn, "*."), ".")
	labels := strings.Split(fqdn, ".")
	for i := 0; i < len(labels)-1; i++ {
		cand := strings.Join(labels[i:], ".")
		z, err := c.ZoneByName(ctx, cand)
		if err == nil {
			return z, nil
		}
		if !IsNotFound(err) {
			return nil, err
		}
	}
	return nil, errNotFound(fqdn)
}

// Records lists DNS records of a zone. When typ/name are set they filter.
func (c *Client) Records(ctx context.Context, zoneID, typ, name string) ([]DNSRecord, error) {
	v := url.Values{}
	if typ != "" {
		v.Set("type", typ)
	}
	if name != "" {
		v.Set("name", name)
	}
	var rs []DNSRecord
	if err := c.doList(ctx, "/zones/"+zoneID+"/dns_records"+q(v), &rs); err != nil {
		return nil, err
	}
	return rs, nil
}

// CreateRecord creates a DNS record.
func (c *Client) CreateRecord(ctx context.Context, zoneID string, rec DNSRecord) (*DNSRecord, error) {
	var out DNSRecord
	if err := c.do(ctx, "POST", "/zones/"+zoneID+"/dns_records", rec, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// UpdateRecord replaces a DNS record.
func (c *Client) UpdateRecord(ctx context.Context, zoneID, id string, rec DNSRecord) (*DNSRecord, error) {
	var out DNSRecord
	if err := c.do(ctx, "PUT", "/zones/"+zoneID+"/dns_records/"+id, rec, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// DeleteRecord removes a DNS record.
func (c *Client) DeleteRecord(ctx context.Context, zoneID, id string) error {
	return c.do(ctx, "DELETE", "/zones/"+zoneID+"/dns_records/"+id, nil, nil)
}

// PurgeCache purges everything (all=true) or specific files.
func (c *Client) PurgeCache(ctx context.Context, zoneID string, all bool, files []string) error {
	body := map[string]any{}
	if all {
		body["purge_everything"] = true
	} else {
		body["files"] = files
	}
	return c.do(ctx, "POST", "/zones/"+zoneID+"/purge_cache", body, nil)
}

// Setting is a zone setting.
type Setting struct {
	ID         string `json:"id"`
	Value      any    `json:"value"`
	Editable   bool   `json:"editable"`
	ModifiedOn string `json:"modified_on"`
}

// Settings lists zone settings.
func (c *Client) Settings(ctx context.Context, zoneID string) ([]Setting, error) {
	var s []Setting
	if err := c.do(ctx, "GET", "/zones/"+zoneID+"/settings", nil, &s); err != nil {
		return nil, err
	}
	return s, nil
}

// SetSetting changes one zone setting (e.g. security_level, ssl, development_mode).
func (c *Client) SetSetting(ctx context.Context, zoneID, id string, value any) (*Setting, error) {
	var out Setting
	if err := c.do(ctx, "PATCH", "/zones/"+zoneID+"/settings/"+id, map[string]any{"value": value}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// FirewallRule is a legacy IP access rule.
type AccessRule struct {
	ID            string `json:"id,omitempty"`
	Mode          string `json:"mode"` // block, challenge, whitelist, js_challenge, managed_challenge
	Notes         string `json:"notes,omitempty"`
	Configuration struct {
		Target string `json:"target"` // ip, ip_range, country, asn
		Value  string `json:"value"`
	} `json:"configuration"`
	CreatedOn string `json:"created_on,omitempty"`
}

// AccessRules lists IP access rules for a zone.
func (c *Client) AccessRules(ctx context.Context, zoneID string) ([]AccessRule, error) {
	var rs []AccessRule
	if err := c.doList(ctx, "/zones/"+zoneID+"/firewall/access_rules/rules", &rs); err != nil {
		return nil, err
	}
	return rs, nil
}

// CreateAccessRule adds an IP access rule.
func (c *Client) CreateAccessRule(ctx context.Context, zoneID, mode, target, value, notes string) (*AccessRule, error) {
	body := map[string]any{
		"mode":          mode,
		"notes":         notes,
		"configuration": map[string]string{"target": target, "value": value},
	}
	var out AccessRule
	if err := c.do(ctx, "POST", "/zones/"+zoneID+"/firewall/access_rules/rules", body, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// DeleteAccessRule removes an IP access rule.
func (c *Client) DeleteAccessRule(ctx context.Context, zoneID, id string) error {
	return c.do(ctx, "DELETE", "/zones/"+zoneID+"/firewall/access_rules/rules/"+id, nil, nil)
}

// Analytics returns a 24h request/threat summary for a zone dashboard tile.
func (c *Client) ZoneDevMode(ctx context.Context, zoneID string, on bool) error {
	val := "off"
	if on {
		val = "on"
	}
	_, err := c.SetSetting(ctx, zoneID, "development_mode", val)
	return err
}

type notFoundError string

func (e notFoundError) Error() string { return "cloudflare: zone not found: " + string(e) }
func errNotFound(name string) error   { return notFoundError(name) }

// IsNotFound reports a zone-not-found error.
func IsNotFound(err error) bool { _, ok := err.(notFoundError); return ok }

func itoa(i int) string { return strconv.Itoa(i) }
