package dnsprov

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
)

// aliDNS implements the Alibaba Cloud DNS RPC API (signature version 1.0).
type aliDNS struct{ id, secret string }

func aliEscape(s string) string {
	e := url.QueryEscape(s)
	e = strings.ReplaceAll(e, "+", "%20")
	e = strings.ReplaceAll(e, "*", "%2A")
	e = strings.ReplaceAll(e, "%7E", "~")
	return e
}

func (p *aliDNS) call(ctx context.Context, action string, params map[string]string, out any) error {
	nonce := make([]byte, 8)
	_, _ = rand.Read(nonce)
	q := map[string]string{
		"Format":           "JSON",
		"Version":          "2015-01-09",
		"AccessKeyId":      p.id,
		"SignatureMethod":  "HMAC-SHA1",
		"Timestamp":        time.Now().UTC().Format("2006-01-02T15:04:05Z"),
		"SignatureVersion": "1.0",
		"SignatureNonce":   hex.EncodeToString(nonce),
		"Action":           action,
	}
	for k, v := range params {
		q[k] = v
	}
	keys := make([]string, 0, len(q))
	for k := range q {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, aliEscape(k)+"="+aliEscape(q[k]))
	}
	canon := strings.Join(parts, "&")
	toSign := "GET&" + aliEscape("/") + "&" + aliEscape(canon)
	mac := hmac.New(sha1.New, []byte(p.secret+"&"))
	mac.Write([]byte(toSign))
	sig := base64.StdEncoding.EncodeToString(mac.Sum(nil))
	endpoint := "https://alidns.aliyuncs.com/?" + canon + "&Signature=" + aliEscape(sig)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		var e struct{ Message, Code string }
		_ = json.Unmarshal(data, &e)
		if e.Message != "" {
			return fmt.Errorf("alidns: %s (%s)", e.Message, e.Code)
		}
		return fmt.Errorf("alidns: HTTP %d", resp.StatusCode)
	}
	if out != nil {
		return json.Unmarshal(data, out)
	}
	return nil
}

func (p *aliDNS) Upsert(ctx context.Context, fqdn, rtype, value string, ttl int, proxied bool) (bool, error) {
	rr, zone, err := p.resolveZone(ctx, fqdn)
	if err != nil {
		return false, err
	}
	if ttl <= 0 {
		ttl = 600
	}
	var list struct {
		DomainRecords struct {
			Record []struct {
				RecordId, RR, Type, Value string
			}
		}
	}
	if err := p.call(ctx, "DescribeDomainRecords", map[string]string{
		"DomainName": zone, "RRKeyWord": rr, "TypeKeyWord": rtype,
	}, &list); err != nil {
		return false, err
	}
	for _, rec := range list.DomainRecords.Record {
		if rec.RR == rr && rec.Type == rtype {
			if rec.Value == value {
				return false, nil
			}
			return true, p.call(ctx, "UpdateDomainRecord", map[string]string{
				"RecordId": rec.RecordId, "RR": rr, "Type": rtype, "Value": value, "TTL": strconv.Itoa(ttl),
			}, nil)
		}
	}
	return true, p.call(ctx, "AddDomainRecord", map[string]string{
		"DomainName": zone, "RR": rr, "Type": rtype, "Value": value, "TTL": strconv.Itoa(ttl),
	}, nil)
}

func (p *aliDNS) resolveZone(ctx context.Context, fqdn string) (string, string, error) {
	var resp struct {
		Domains struct {
			Domain []struct{ DomainName string }
		}
	}
	if err := p.call(ctx, "DescribeDomains", map[string]string{"PageSize": "100"}, &resp); err != nil {
		return "", "", err
	}
	zones := make([]string, 0, len(resp.Domains.Domain))
	for _, d := range resp.Domains.Domain {
		zones = append(zones, d.DomainName)
	}
	rr, zone, ok := splitRR(fqdn, zones)
	if !ok {
		return "", "", fmt.Errorf("alidns: no domain found for %s", fqdn)
	}
	return rr, zone, nil
}
