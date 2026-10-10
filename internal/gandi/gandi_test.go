package gandi_test

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/mycophonic/primordium/fault"

	"github.com/forkcloser/infra/internal/gandi"
)

func TestNewRejectsEmptyToken(t *testing.T) {
	t.Parallel()

	_, err := gandi.New(http.DefaultClient, gandi.DefaultBaseURL, "")
	if !errors.Is(err, fault.ErrInvalidArgument) {
		t.Fatalf("err = %v, want ErrInvalidArgument", err)
	}
}

func TestNameserversAndKeys(t *testing.T) {
	t.Parallel()

	var (
		putNS   []string
		postKey gandi.KeySpec
		deleted string
	)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer pat" {
			http.Error(w, "no token", http.StatusForbidden)

			return
		}

		switch r.Method + " " + r.URL.Path {
		case "GET /v5/domain/domains/example.org/nameservers":
			_, _ = io.WriteString(w, `["ns-1.gandi.net","ns-2.gandi.net"]`)
		case "PUT /v5/domain/domains/example.org/nameservers":
			var body struct {
				Nameservers []string `json:"nameservers"`
			}

			_ = json.NewDecoder(r.Body).Decode(&body)
			putNS = body.Nameservers

			w.WriteHeader(http.StatusAccepted)
		case "GET /v5/domain/domains/example.org/dnskeys":
			_, _ = io.WriteString(
				w,
				`[{"id":7,"algorithm":13,"type":"ksk","public_key":"AAAA","digest":"x","digest_type":2,"keytag":1}]`,
			)
		case "POST /v5/domain/domains/example.org/dnskeys":
			_ = json.NewDecoder(r.Body).Decode(&postKey)

			w.WriteHeader(http.StatusCreated)
		case "DELETE /v5/domain/domains/example.org/dnskeys/7":
			deleted = r.URL.Path

			w.WriteHeader(http.StatusNoContent)
		default:
			http.Error(w, r.Method+" "+r.URL.Path, http.StatusNotFound)
		}
	}))
	defer srv.Close()

	c, err := gandi.New(srv.Client(), srv.URL+"/v5/domain/", "pat")
	if err != nil {
		t.Fatal(err)
	}

	ctx := t.Context()

	ns, err := c.Nameservers(ctx, "example.org")
	if err != nil || len(ns) != 2 {
		t.Fatalf("Nameservers: %v %v", ns, err)
	}

	if setErr := c.SetNameservers(ctx, "example.org", []string{"ns1.desec.io", "ns2.desec.org"}); setErr != nil {
		t.Fatalf("SetNameservers: %v", setErr)
	}

	if len(putNS) != 2 || putNS[0] != "ns1.desec.io" {
		t.Fatalf("PUT body = %v", putNS)
	}

	keys, err := c.Keys(ctx, "example.org")
	if err != nil || len(keys) != 1 || keys[0].ID != 7 {
		t.Fatalf("Keys: %v %v", keys, err)
	}

	spec := gandi.KeySpec{Algorithm: 13, Type: gandi.KeyTypeKSK, PublicKey: "AAAA"}
	if !keys[0].Matches(spec) {
		t.Fatalf("key %+v should match %+v", keys[0], spec)
	}

	if createErr := c.CreateKey(ctx, "example.org", spec); createErr != nil || postKey != spec {
		t.Fatalf("CreateKey: err=%v body=%+v", createErr, postKey)
	}

	if deleteErr := c.DeleteKey(ctx, "example.org", 7); deleteErr != nil || deleted == "" {
		t.Fatalf("DeleteKey: err=%v deleted=%q", deleteErr, deleted)
	}

	_, err = c.Nameservers(ctx, "absent.org")
	if !errors.Is(err, fault.ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}

	bad, err := gandi.New(srv.Client(), srv.URL+"/v5/domain/", "wrong")
	if err != nil {
		t.Fatal(err)
	}

	_, err = bad.Nameservers(ctx, "example.org")
	if !errors.Is(err, fault.ErrAuthenticationFailure) {
		t.Fatalf("err = %v, want ErrAuthenticationFailure", err)
	}
}
