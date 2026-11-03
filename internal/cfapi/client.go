// Package cfapi is a small Cloudflare API v4 client shared by the DDNS, SSL
// (DNS-01) and Cloudflare management modules.
package cfapi

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const base = "https://api.cloudflare.com/client/v4"

// Client talks to the Cloudflare API with an API token (preferred) or the
// legacy Global API Key + email pair.
type Client struct {
	Token  string
	Email  string
	APIKey string
	HTTP   *http.Client
}

// New returns a client authenticated with an API token.
func New(token string) *Client {
	return &Client{Token: token, HTTP: &http.Client{Timeout: 30 * time.Second}}
}

// NewKey returns a client authenticated with email + global API key.
func NewKey(email, key string) *Client {
	return &Client{Email: email, APIKey: key, HTTP: &http.Client{Timeout: 30 * time.Second}}
}

// apiError is one entry of the Cloudflare errors array.
type apiError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type envelope struct {
	Success    bool            `json:"success"`
	Errors     []apiError      `json:"errors"`
	Messages   []apiError      `json:"messages"`
	Result     json.RawMessage `json:"result"`
	ResultInfo struct {
		Page       int `json:"page"`
		TotalPages int `json:"total_pages"`
		Count      int `json:"count"`
		TotalCount int `json:"total_count"`
	} `json:"result_info"`
}

func (c *Client) do(ctx context.Context, method, path string, body, out any) error {
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, base+path, rd)
	if err != nil {
		return err
	}
	if c.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	} else {
		req.Header.Set("X-Auth-Email", c.Email)
		req.Header.Set("X-Auth-Key", c.APIKey)
	}
	req.Header.Set("Content-Type", "application/json")
	hc := c.HTTP
	if hc == nil {
		hc = http.DefaultClient
	}
	resp, err := hc.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	var env envelope
	if err := json.Unmarshal(data, &env); err != nil {
		return fmt.Errorf("cloudflare: HTTP %d: %s", resp.StatusCode, truncate(data))
	}
	if !env.Success {
		return fmt.Errorf("cloudflare: %s", formatErrors(env.Errors))
	}
	if out != nil && len(env.Result) > 0 {
		return json.Unmarshal(env.Result, out)
	}
	return nil
}

// doList fetches all pages of a listing into out (a pointer to a slice).
func (c *Client) doList(ctx context.Context, path string, out any) error {
	sep := "?"
	if strings.Contains(path, "?") {
		sep = "&"
	}
	all := []json.RawMessage{}
	for page := 1; ; page++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, fmt.Sprintf("%s%s%spage=%d&per_page=50", base, path, sep, page), nil)
		if err != nil {
			return err
		}
		if c.Token != "" {
			req.Header.Set("Authorization", "Bearer "+c.Token)
		} else {
			req.Header.Set("X-Auth-Email", c.Email)
			req.Header.Set("X-Auth-Key", c.APIKey)
		}
		hc := c.HTTP
		if hc == nil {
			hc = http.DefaultClient
		}
		resp, err := hc.Do(req)
		if err != nil {
			return err
		}
		data, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
		resp.Body.Close()
		var env envelope
		if err := json.Unmarshal(data, &env); err != nil {
			return fmt.Errorf("cloudflare: HTTP %d: %s", resp.StatusCode, truncate(data))
		}
		if !env.Success {
			return fmt.Errorf("cloudflare: %s", formatErrors(env.Errors))
		}
		var items []json.RawMessage
		if err := json.Unmarshal(env.Result, &items); err != nil {
			return err
		}
		all = append(all, items...)
		if env.ResultInfo.TotalPages <= page || len(items) == 0 {
			break
		}
	}
	joined, _ := json.Marshal(all)
	return json.Unmarshal(joined, out)
}

func formatErrors(errs []apiError) string {
	if len(errs) == 0 {
		return "unknown error"
	}
	parts := make([]string, len(errs))
	for i, e := range errs {
		parts[i] = fmt.Sprintf("%s (code %d)", e.Message, e.Code)
	}
	return strings.Join(parts, "; ")
}

func truncate(b []byte) string {
	if len(b) > 300 {
		return string(b[:300]) + "..."
	}
	return string(b)
}

func q(values url.Values) string {
	if len(values) == 0 {
		return ""
	}
	return "?" + values.Encode()
}
