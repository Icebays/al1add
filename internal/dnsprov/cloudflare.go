package dnsprov

import (
	"context"

	"github.com/icebays/al1add/internal/cfapi"
)

type cloudflare struct{ c *cfapi.Client }

func newCloudflare(cr Creds) *cloudflare {
	if cr.ID != "" {
		return &cloudflare{c: cfapi.NewKey(cr.ID, cr.Secret)}
	}
	return &cloudflare{c: cfapi.New(cr.Secret)}
}

func (p *cloudflare) Upsert(ctx context.Context, fqdn, rtype, value string, ttl int, proxied bool) (bool, error) {
	zone, err := p.c.ZoneForFQDN(ctx, fqdn)
	if err != nil {
		return false, err
	}
	if ttl <= 0 || proxied {
		ttl = 1 // "automatic"; required when proxied
	}
	recs, err := p.c.Records(ctx, zone.ID, rtype, fqdn)
	if err != nil {
		return false, err
	}
	rec := cfapi.DNSRecord{Type: rtype, Name: fqdn, Content: value, TTL: ttl, Proxied: proxied, Comment: "al1add ddns"}
	if len(recs) == 0 {
		_, err := p.c.CreateRecord(ctx, zone.ID, rec)
		return err == nil, err
	}
	cur := recs[0]
	if cur.Content == value && cur.Proxied == proxied {
		return false, nil
	}
	_, err = p.c.UpdateRecord(ctx, zone.ID, cur.ID, rec)
	return err == nil, err
}
