package dnsprov

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

// dnspod implements the DNSPod (Tencent) token API.
type dnspod struct{ id, token string }

func (p *dnspod) loginToken() string { return p.id + "," + p.token }

func (p *dnspod) call(ctx context.Context, path string, params url.Values, out any) error {
	params.Set("login_token", p.loginToken())
	params.Set("format", "json")
	params.Set("lang", "en")
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://dnsapi.cn/"+path, strings.NewReader(params.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("User-Agent", "al1add/1.0 (ddns)")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	var status struct {
		Status struct{ Code, Message string } `json:"status"`
	}
	_ = json.Unmarshal(data, &status)
	if status.Status.Code != "1" && status.Status.Code != "" {
		return fmt.Errorf("dnspod: %s (code %s)", status.Status.Message, status.Status.Code)
	}
	if out != nil {
		return json.Unmarshal(data, out)
	}
	return nil
}

func (p *dnspod) Upsert(ctx context.Context, fqdn, rtype, value string, ttl int, proxied bool) (bool, error) {
	var domains struct {
		Domains []struct {
			Name string `json:"name"`
		} `json:"domains"`
	}
	if err := p.call(ctx, "Domain.List", url.Values{}, &domains); err != nil {
		return false, err
	}
	zones := make([]string, 0, len(domains.Domains))
	for _, d := range domains.Domains {
		zones = append(zones, d.Name)
	}
	rr, zone, ok := splitRR(fqdn, zones)
	if !ok {
		return false, fmt.Errorf("dnspod: no domain found for %s", fqdn)
	}
	if ttl <= 0 {
		ttl = 600
	}
	var recs struct {
		Records []struct {
			ID    string `json:"id"`
			Name  string `json:"name"`
			Type  string `json:"type"`
			Value string `json:"value"`
		} `json:"records"`
	}
	if err := p.call(ctx, "Record.List", url.Values{"domain": {zone}, "sub_domain": {rr}}, &recs); err != nil {
		return false, err
	}
	base := url.Values{
		"domain":      {zone},
		"sub_domain":  {rr},
		"record_type": {rtype},
		"record_line": {"默认"},
		"value":       {value},
		"ttl":         {strconv.Itoa(ttl)},
	}
	for _, rec := range recs.Records {
		if rec.Name == rr && rec.Type == rtype {
			if rec.Value == value {
				return false, nil
			}
			base.Set("record_id", rec.ID)
			return true, p.call(ctx, "Record.Modify", base, nil)
		}
	}
	return true, p.call(ctx, "Record.Create", base, nil)
}
