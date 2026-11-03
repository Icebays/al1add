// Package cfapi is a small Cloudflare API v4 client.
package cfapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// BaseURL of the Cloudflare API.
const BaseURL = "https://api.cloudflare.com/client/v4"

// Client authenticates with an API token, or a global key + email.
type Client struct {
	Token string
	Email string
	Key   string
	HTTP  *http.Client
}

// ResultInfo is pagination metadata.
type ResultInfo struct {
	Page       int `json:"page"`
	PerPage    int `json:"per_page"`
	TotalPages int `json:"total_pages"`
	Count      int `json:"count"`
	TotalCount int `json:"total_count"`
}

type envelope struct {
	Success bool `json:"success"`
	Errors  []struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"errors"`
	Result     json.RawMessage `json:"result"`
	ResultInfo *ResultInfo     `json:"result_info"`
}

// Error is a Cloudflare API error.
type Error struct {
	Status   int
	Messages []string
}

func (e *Error) Error() string {
	if len(e.Messages) == 0 {
		return fmt.Sprintf("cloudflare: HTTP %d", e.Status)
	}
	return "cloudflare: " + strings.Join(e.Messages, "; ")
}

func (c *Client) httpClient() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	return &http.Client{Timeout: 60 * time.Second}
}

// Do calls the API. body is JSON-encoded unless it is already []byte.
func (c *Client) Do(ctx context.Context, method, path string, query url.Values, body any, out any) (*ResultInfo, error) {
	if c.Token == "" && (c.Key == "" || c.Email == "") {
		return nil, errors.New("cloudflare: no credentials")
	}
	u := BaseURL + path
	if len(query) > 0 {
		u += "?" + query.Encode()
	}
	var rd io.Reader
	if body != nil {
		switch b := body.(type) {
		case []byte:
			rd = bytes.NewReader(b)
		case json.RawMessage:
			rd = bytes.NewReader(b)
		default:
			data, err := json.Marshal(body)
			if err != nil {
				return nil, err
			}
			rd = bytes.NewReader(data)
		}
	}
	req, err := http.NewRequestWithContext(ctx, method, u, rd)
	if err != nil {
		return nil, err
	}
	if c.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	} else {
		req.Header.Set("X-Auth-Email", c.Email)
		req.Header.Set("X-Auth-Key", c.Key)
	}
	if rd != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.httpClient().Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
	if err != nil {
		return nil, err
	}
	var env envelope
	if err := json.Unmarshal(data, &env); err != nil {
		return nil, &Error{Status: resp.StatusCode, Messages: []string{strings.TrimSpace(string(data[:min(len(data), 300)]))}}
	}
	if !env.Success || resp.StatusCode >= 400 {
		e := &Error{Status: resp.StatusCode}
		for _, m := range env.Errors {
			e.Messages = append(e.Messages, fmt.Sprintf("%s (%d)", m.Message, m.Code))
		}
		return nil, e
	}
	if out != nil && len(env.Result) > 0 {
		if err := json.Unmarshal(env.Result, out); err != nil {
			return nil, err
		}
	}
	return env.ResultInfo, nil
}

// GraphQL runs a query against the analytics GraphQL endpoint.
func (c *Client) GraphQL(ctx context.Context, query string, variables map[string]any) (json.RawMessage, error) {
	payload, _ := json.Marshal(map[string]any{"query": query, "variables": variables})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, BaseURL+"/graphql", bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	if c.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	} else {
		req.Header.Set("X-Auth-Email", c.Email)
		req.Header.Set("X-Auth-Key", c.Key)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.httpClient().Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var out struct {
		Data   json.RawMessage `json:"data"`
		Errors []struct {
			Message string `json:"message"`
		} `json:"errors"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 16<<20)).Decode(&out); err != nil {
		return nil, err
	}
	if len(out.Errors) > 0 {
		var msgs []string
		for _, e := range out.Errors {
			msgs = append(msgs, e.Message)
		}
		return nil, &Error{Status: resp.StatusCode, Messages: msgs}
	}
	return out.Data, nil
}

// All fetches every page of a list endpoint into a slice of T.
func All[T any](ctx context.Context, c *Client, path string, query url.Values) ([]T, error) {
	if query == nil {
		query = url.Values{}
	}
	query.Set("per_page", "100")
	var all []T
	for page := 1; page <= 200; page++ {
		query.Set("page", fmt.Sprint(page))
		var items []T
		info, err := c.Do(ctx, http.MethodGet, path, query, nil, &items)
		if err != nil {
			return nil, err
		}
		all = append(all, items...)
		if info == nil || info.TotalPages <= page || len(items) == 0 {
			break
		}
	}
	return all, nil
}

// Zone is a Cloudflare zone.
type Zone struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Status      string   `json:"status"`
	Paused      bool     `json:"paused"`
	Type        string   `json:"type"`
	NameServers []string `json:"name_servers"`
	Plan        struct {
		Name string `json:"name"`
	} `json:"plan"`
	Account struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	} `json:"account"`
	DevelopmentMode int `json:"development_mode"`
}

// DNSRecord is a DNS record.
type DNSRecord struct {
	ID       string `json:"id,omitempty"`
	Type     string `json:"type"`
	Name     string `json:"name"`
	Content  string `json:"content"`
	TTL      int    `json:"ttl"`
	Proxied  *bool  `json:"proxied,omitempty"`
	Priority *int   `json:"priority,omitempty"`
	Comment  string `json:"comment,omitempty"`
	ZoneName string `json:"zone_name,omitempty"`
	Proxiable bool  `json:"proxiable,omitempty"`
	ModifiedOn string `json:"modified_on,omitempty"`
}

// FindZone returns the zone that contains fqdn (longest suffix match).
func (c *Client) FindZone(ctx context.Context, fqdn string) (*Zone, error) {
	labels := strings.Split(strings.TrimSuffix(strings.ToLower(fqdn), "."), ".")
	for i := 0; i < len(labels)-1; i++ {
		name := strings.Join(labels[i:], ".")
		var zones []Zone
		if _, err := c.Do(ctx, http.MethodGet, "/zones", url.Values{"name": {name}}, nil, &zones); err != nil {
			return nil, err
		}
		if len(zones) > 0 {
			return &zones[0], nil
		}
	}
	return nil, fmt.Errorf("cloudflare: no zone found for %s (check token permissions: Zone:Read, DNS:Edit)", fqdn)
}

// ListRecords lists records of a zone filtered by type/name.
func (c *Client) ListRecords(ctx context.Context, zoneID, rtype, name string) ([]DNSRecord, error) {
	q := url.Values{}
	if rtype != "" {
		q.Set("type", rtype)
	}
	if name != "" {
		q.Set("name", name)
	}
	return All[DNSRecord](ctx, c, "/zones/"+zoneID+"/dns_records", q)
}
