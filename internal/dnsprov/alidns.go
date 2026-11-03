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
	return strings.NewReplacer("+", "%20", "*", "%2A", "%7E", "~").Replace(url.QueryEscape(s))
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
	var parts []string
	for _, k := range keys {
		parts = append(parts, aliEscape(k)+"="+aliEscape(q[k]))
	}
	canon := strings.Join(parts, "&")
	to