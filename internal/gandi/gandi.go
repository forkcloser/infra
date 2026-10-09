// Package gandi is the slice of Gandi's domain API the tool uses: a
// domain's nameserver delegation and the DNSSEC keys published for it.
package gandi

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"

	"github.com/mycophonic/primordium/fault"
)

// DefaultBaseURL is the production API, domain section.
const DefaultBaseURL = "https://api.gandi.net/v5/domain/"

// KeyTypeKSK is the key type a zone-signing key published for delegation
// carries at Gandi; deSEC's managed key, flags 257, is one.
const KeyTypeKSK = "ksk"

// maxResponseBytes caps a response body; a key list is small.
const maxResponseBytes = 1 << 20

// Client talks to one Gandi account through a personal access token.
type Client struct {
	http  *http.Client
	base  *url.URL
	token string
}

// Key is a DNSSEC key as Gandi holds it.
type Key struct {
	ID        int    `json:"id"`
	Algorithm int    `json:"algorithm"`
	Type      string `json:"type"`
	PublicKey string `json:"public_key"` //nolint:tagliatelle // the API spells it public_key
}

// KeySpec is what creating a key takes.
type KeySpec struct {
	Algorithm int    `json:"algorithm"`
	Type      string `json:"type"`
	PublicKey string `json:"public_key"` //nolint:tagliatelle // the API spells it public_key
}

// Matches reports whether the published key is the one specified.
func (k Key) Matches(s KeySpec) bool {
	return k.Algorithm == s.Algorithm && k.Type == s.Type && k.PublicKey == s.PublicKey
}

// New returns a client.
func New(httpClient *http.Client, baseURL, token string) (*Client, error) {
	base, err := url.Parse(baseURL)
	if err != nil {
		return nil, fmt.Errorf("%w: base URL %q: %w", fault.ErrInvalidArgument, baseURL, err)
	}

	if token == "" {
		return nil, fmt.Errorf("%w: empty Gandi token", fault.ErrInvalidArgument)
	}

	return &Client{http: httpClient, base: base, token: token}, nil
}

// Nameservers returns the domain's delegation.
func (c *Client) Nameservers(ctx context.Context, domain string) ([]string, error) {
	var ns []string
	if err := c.do(ctx, http.MethodGet, "domains/"+domain+"/nameservers", nil, &ns); err != nil {
		return nil, err
	}

	return ns, nil
}

// SetNameservers replaces the domain's delegation.
func (c *Client) SetNameservers(ctx context.Context, domain string, ns []string) error {
	return c.do(ctx, http.MethodPut, "domains/"+domain+"/nameservers", map[string][]string{"nameservers": ns}, nil)
}

// Keys returns the DNSSEC keys published for the domain.
func (c *Client) Keys(ctx context.Context, domain string) ([]Key, error) {
	var keys []Key
	if err := c.do(ctx, http.MethodGet, "domains/"+domain+"/dnskeys", nil, &keys); err != nil {
		return nil, err
	}

	return keys, nil
}

// CreateKey publishes a key.
func (c *Client) CreateKey(ctx context.Context, domain string, spec KeySpec) error {
	return c.do(ctx, http.MethodPost, "domains/"+domain+"/dnskeys", spec, nil)
}

// DeleteKey withdraws a key.
func (c *Client) DeleteKey(ctx context.Context, domain string, id int) error {
	return c.do(ctx, http.MethodDelete, "domains/"+domain+"/dnskeys/"+strconv.Itoa(id), nil, nil)
}

func (c *Client) do(ctx context.Context, method, path string, body, out any) error {
	ref, err := url.Parse(path)
	if err != nil {
		return fmt.Errorf("%w: path %q: %w", fault.ErrInvalidArgument, path, err)
	}

	var payload io.Reader

	if body != nil {
		encoded, encodeErr := json.Marshal(body)
		if encodeErr != nil {
			return fmt.Errorf("%w: encoding request: %w", fault.ErrInvalidJSON, encodeErr)
		}

		payload = bytes.NewReader(encoded)
	}

	req, err := http.NewRequestWithContext(ctx, method, c.base.ResolveReference(ref).String(), payload)
	if err != nil {
		return fmt.Errorf("%w: %w", fault.ErrNetworkCommunication, err)
	}

	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Accept", "application/json")

	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("%w: %s %s: %w", fault.ErrNetworkCommunication, method, path, err)
	}

	defer func() { _ = resp.Body.Close() }()

	data, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
	if err != nil {
		return fmt.Errorf("%w: reading response: %w", fault.ErrNetworkCommunication, err)
	}

	switch {
	case resp.StatusCode == http.StatusNotFound:
		return fmt.Errorf("%w: %s %s", fault.ErrNotFound, method, path)
	case resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden:
		return fmt.Errorf("%w: %s %s: %s", fault.ErrAuthenticationFailure, method, path, resp.Status)
	case resp.StatusCode < 200 || resp.StatusCode > 299:
		return fmt.Errorf(
			"%w: %s %s: %s: %s",
			fault.ErrUnacceptableResponse,
			method,
			path,
			resp.Status,
			bytes.TrimSpace(data),
		)
	}

	if out == nil {
		return nil
	}

	if err := json.Unmarshal(data, out); err != nil {
		return fmt.Errorf("%w: decoding %s %s: %w", fault.ErrInvalidJSON, method, path, err)
	}

	return nil
}
