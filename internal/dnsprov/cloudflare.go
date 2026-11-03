package dnsprov

import (
	"context"
	"net/http"

	"github.com/icebays/al1add/internal/cfapi"
)

type cloudflare struct{ c *cfapi.Client }

func newCloudflare(cr Creds) *cloudflare {
	c := &cfapi.Client{}
	if cr.ID != "" {
		c.Email, c.Key = cr.ID, cr.Secret
	} else {
		c.Token = cr.Secret
	}
	return &cloudflare{c: c}
}

func (p *cloudflare) Upsert(ctx context.Context, fqdn, rtype, value string, ttl int, proxied bool) (bool, error) {
	zone, err := p.c.FindZone(ctx, fqdn)
	if err != nil {
		return false, err
	}
	recs, err := p.c.ListRecords(ctx, zone.ID, rtype, fqdn)
	if err != nil {
		return false, err
	}
	if ttl <= 0 {
		ttl = 1 // automatic
	}
	rec := cfapi.DNSRecord{Type: rtype, Name: fqdn, Content: value, TTL: ttl, Proxied: &proxied}
	if len(recs) == 0 {
		_, err := p.c.Do(ctx, http.MethodPost, "/zones/"+zone.ID+"/dns_records", nil, rec, nil)
		return err == nil, err
	}
	cur := recs[0]
	if cur.Content == value && cur.Proxied != nil && *cur.Proxied == proxied {
		return false, nil
	}
	_, err = p.c.Do(ctx, http.MethodPut, "/zones/"+zone.ID+"/dns_records/"+cur.ID, nil, rec, nil)
	return err == nil, err
}

func (p *cloudflare) CreateTXT(ctx context.Context, fqdn, value string) (func(), error) {
	zone, err := p.c.FindZone(ctx, fqdn)
	if err != nil {
		return nil, err
	}
	var created cfapi.DNSRecord
	rec := cfapi.DNSRecord{Type: "TXT", Name: fqdn, Content: value, TTL: 120}
	if _, err := p.c.Do(ctx, http.MethodPost, "/zones/"+zone.ID+"/dns_records", nil, rec, &created); err != nil {
		return nil, err
	}
	return func() {
		_, _ = p.c.Do(context.Background(), http.MethodDelete, "/zones/"+zone.ID+"/dns_records/"+created.ID, nil, nil, nil)
	}, nil
}
