// Package desec is the slice of deSEC's API the tool uses: a domain, its
// DNSSEC keys, and bulk replacement of record sets.
package desec

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"

	"github.com/mycophonic/primordium/fault"
)

// DefaultBaseURL is the production API.
const DefaultBaseURL = "https://desec.io/api/v1/"

// maxResponseBytes caps a response body; a zone's record sets are small.
const maxResponseBytes = 8 << 20

// Client talks to one deSEC account with two tokens: one that may write
// into the account's zones, and one whose only right is to create a zone,
// held apart so the routine token never carries it.
type Client struct {
	http        *http.Client
	base        *url.URL
	token       string
	createToken string
}

// Domain is a zone as deSEC returns it.
type Domain struct {
	Name string `json:"name"`
	Keys []Key  `json:"keys"`
}

// Key is one DNSSEC key of a zone. DNSKEY is "flags protocol algorithm
// key"; Managed says deSEC generated it.
type Key struct {
	DNSKey  string   `json:"dnskey"`
	DS      []string `json:"ds"`
	Flags   int      `json:"flags"`
	KeyType string   `json:"keytype"`
	Managed bool     `json:"managed"`
}

// RRset is a record set as deSEC exchanges it. An empty Records in a bulk
// write deletes the set.
type RRset struct {
	Subname string   `json:"subname"`
	Type    string   `json:"type"`
	TTL     int      `json:"ttl"`
	Records []string `json:"records"`
}

// New returns a client. The HTTP client supplies retries and rate limiting;
// deSEC allows two record-set writes per second per domain. createToken may
// be empty: then CreateDomain refuses, and every other call works.
func New(httpClient *http.Client, baseURL, token, createToken string) (*Client, error) {
	base, err := url.Parse(baseURL)
	if err != nil {
		return nil, fmt.Errorf("%w: base URL %q: %w", fault.ErrInvalidArgument, baseURL, err)
	}

	if token == "" {
		return nil, fmt.Errorf("%w: empty deSEC token", fault.ErrInvalidArgument)
	}

	return &Client{http: httpClient, base: base, token: token, createToken: createToken}, nil
}

// CanCreateDomains reports whether a creation token was given.
func (c *Client) CanCreateDomains() bool {
	return c.createToken != ""
}

// GetDomain returns the zone with its keys, or fault.ErrNotFound.
func (c *Client) GetDomain(ctx context.Context, name string) (*Domain, error) {
	var d Domain
	if err := c.do(ctx, http.MethodGet, "domains/"+name+"/", nil, &d); err != nil {
		return nil, err
	}

	return &d, nil
}

// CreateDomain creates the zone with the creation token. deSEC returns the
// zone's keys in the response, so the DNSSEC chain can be published without
// a second read.
func (c *Client) CreateDomain(ctx context.Context, name string) (*Domain, error) {
	if c.createToken == "" {
		return nil, fmt.Errorf(
			"%w: creating zone %s needs the deSEC creation token",
			fault.ErrMissingRequirements,
			name,
		)
	}

	var domain Domain
	if err := c.doWith(
		ctx,
		c.createToken,
		http.MethodPost,
		"domains/",
		map[string]string{"name": name},
		&domain,
	); err != nil {
		return nil, err
	}

	return &domain, nil
}

// ListRRsets returns every record set of the zone.
func (c *Client) ListRRsets(ctx context.Context, domain string) ([]RRset, error) {
	var sets []RRset
	if err := c.do(ctx, http.MethodGet, "domains/"+domain+"/rrsets/", nil, &sets); err != nil {
		return nil, err
	}

	return sets, nil
}

// PutRRsets creates, replaces and deletes record sets in one atomic
// request: every set given is written as given, and one with no records is
// deleted. Sets not given are untouched.
func (c *Client) PutRRsets(ctx context.Context, domain string, sets []RRset) error {
	if len(sets) == 0 {
		return nil
	}

	return c.do(ctx, http.MethodPut, "domains/"+domain+"/rrsets/", sets, nil)
}

func (c *Client) do(ctx context.Context, method, path string, body, out any) error {
	return c.doWith(ctx, c.token, method, path, body, out)
}

func (c *Client) doWith(ctx context.Context, token, method, path string, body, out any) error {
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

	req.Header.Set("Authorization", "Token "+token)
	req.Header.Set("Accept", "application/json")

	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("%w: %s %s: %w", fault.ErrNetworkCommunication, method, path, err)
	}

	defer func() { _ = resp.Body.Close() }()

	// Bodies are small (a zone's record sets); the cap keeps a wrong
	// endpoint from streaming forever.
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

// IsNotFound reports whether err is the zone-does-not-exist answer.
func IsNotFound(err error) bool {
	return errors.Is(err, fault.ErrNotFound)
}
