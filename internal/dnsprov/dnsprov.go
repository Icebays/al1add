// Package dnsprov updates DNS records at several providers. It is used by the
// DDNS module to maintain A/AAAA records.
package dnsprov

import (
	"context"
	"fmt"
	"strings"
)

// Creds identifies a provider account.
type Creds struct {
	Provider string `json:"Provider"` // cloudflare | alidns | dnspod
	ID       string `json:"ID"`       // alidns AccessKeyId; dnspod SecretId/token-id; cloudflare email (global key only)
	Secret   string `json:"Secret"`   // alidns AccessKeySecret; dnspod SecretKey/token; cloudflare API token or global key
}

// Provider manages records for one account.
type Provider interface {
	// Upsert makes fqdn/rtype resolve to value, creating or updating the
	// record. changed is false when the record already had that value.
	Upsert(ctx context.Context, fqdn, rtype, value string, ttl int, proxied bool) (changed bool, err error)
}

// Providers lists supported provider names and labels for the UI.
var Providers = [][2]string{
	{"cloudflare", "Cloudflare"},
	{"alidns", "阿里雲 DNS"},
	{"dnspod", "DNSPod (騰訊雲)"},
}

// New builds a provider from credentials.
func New(c Creds) (Provider, error) {
	switch strings.ToLower(c.Provider) {
	case "cloudflare":
		return newCloudflare(c), nil
	case "alidns":
		return &aliDNS{id: c.ID, secret: c.Secret}, nil
	case "dnspod":
		return &dnspod{id: c.ID, token: c.Secret}, nil
	}
	return nil, fmt.Errorf("unsupported DNS provider %q", c.Provider)
}

// splitRR returns (rr, zone) where zone is the longest suffix of fqdn found in
// zones. rr is "@" for the apex.
func splitRR(fqdn string, zones []string) (string, string, bool) {
	fqdn = strings.TrimSuffix(strings.ToLower(fqdn), ".")
	best := ""
	for _, z := range zones {
		z = strings.TrimSuffix(strings.ToLower(z), ".")
		if (fqdn == z || strings.HasSuffix(fqdn, "."+z)) && len(z) > len(best) {
			best = z
		}
	}
	if best == "" {
		return "", "", false
	}
	if fqdn == best {
		return "@", best, true
	}
	return strings.TrimSuffix(fqdn, "."+best), best, true
}
