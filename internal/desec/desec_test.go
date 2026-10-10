package desec_test

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/mycophonic/primordium/fault"

	"github.com/forkcloser/infra/internal/desec"
)

func TestNewRejectsEmptyToken(t *testing.T) {
	t.Parallel()

	_, err := desec.New(http.DefaultClient, desec.DefaultBaseURL, "", "create")
	if !errors.Is(err, fault.ErrInvalidArgument) {
		t.Fatalf("err = %v, want ErrInvalidArgument", err)
	}
}

func TestGetDomain(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Token secret" {
			http.Error(w, "no token", http.StatusUnauthorized)

			return
		}

		switch r.URL.Path {
		case "/api/v1/domains/example.org/":
			_, _ = io.WriteString(
				w,
				`{"name":"example.org","keys":[{"dnskey":"257 3 13 AAAA","ds":["1 13 2 ab"],"flags":257,"keytype":"csk","managed":true}]}`,
			)
		default:
			http.Error(w, "nope", http.StatusNotFound)
		}
	}))
	defer srv.Close()

	c, err := desec.New(srv.Client(), srv.URL+"/api/v1/", "secret", "create")
	if err != nil {
		t.Fatal(err)
	}

	d, err := c.GetDomain(t.Context(), "example.org")
	if err != nil {
		t.Fatalf("GetDomain: %v", err)
	}

	if d.Name != "example.org" || len(d.Keys) != 1 || d.Keys[0].Flags != 257 || !d.Keys[0].Managed {
		t.Fatalf("unexpected domain: %+v", d)
	}

	_, err = c.GetDomain(t.Context(), "absent.org")
	if !desec.IsNotFound(err) {
		t.Fatalf("err = %v, want not found", err)
	}

	bad, err := desec.New(srv.Client(), srv.URL+"/api/v1/", "wrong", "create")
	if err != nil {
		t.Fatal(err)
	}

	_, err = bad.GetDomain(t.Context(), "example.org")
	if !errors.Is(err, fault.ErrAuthenticationFailure) {
		t.Fatalf("err = %v, want ErrAuthenticationFailure", err)
	}
}

func TestPutRRsetsIsOneBulkRequest(t *testing.T) {
	t.Parallel()

	var (
		method string
		path   string
		body   []desec.RRset
		calls  int
	)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		method, path = r.Method, r.URL.Path

		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)

			return
		}

		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, `[]`)
	}))
	defer srv.Close()

	c, err := desec.New(srv.Client(), srv.URL+"/api/v1/", "secret", "create")
	if err != nil {
		t.Fatal(err)
	}

	sets := []desec.RRset{
		{Subname: "www", Type: "CNAME", TTL: 3600, Records: []string{"site.pages.dev."}},
		{Subname: "old", Type: "A", TTL: 3600, Records: []string{}},
	}

	if err := c.PutRRsets(t.Context(), "example.org", sets); err != nil {
		t.Fatalf("PutRRsets: %v", err)
	}

	if calls != 1 || method != http.MethodPut || path != "/api/v1/domains/example.org/rrsets/" {
		t.Fatalf("calls=%d method=%s path=%s", calls, method, path)
	}

	if len(body) != 2 || body[1].Records == nil || len(body[1].Records) != 0 {
		t.Fatalf("deletion must be sent as an empty records array, got %+v", body)
	}

	if err := c.PutRRsets(t.Context(), "example.org", nil); err != nil || calls != 1 {
		t.Fatalf("an empty write must not call the API: err=%v calls=%d", err, calls)
	}
}

func TestErrorBodyIsReported(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, `[{"records":["Record content of type CNAME needs a trailing dot."]}]`, http.StatusBadRequest)
	}))
	defer srv.Close()

	c, err := desec.New(srv.Client(), srv.URL+"/api/v1/", "secret", "create")
	if err != nil {
		t.Fatal(err)
	}

	err = c.PutRRsets(
		t.Context(),
		"example.org",
		[]desec.RRset{{Subname: "www", Type: "CNAME", TTL: 3600, Records: []string{"x"}}},
	)
	if !errors.Is(err, fault.ErrUnacceptableResponse) {
		t.Fatalf("err = %v, want ErrUnacceptableResponse", err)
	}

	if want := "trailing dot"; err == nil || !strings.Contains(err.Error(), want) {
		t.Fatalf("error %q should carry the API's message %q", err, want)
	}
}

func TestCreateDomainUsesTheCreationToken(t *testing.T) {
	t.Parallel()

	var seen string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = r.Header.Get("Authorization")

		if r.Method != http.MethodPost || r.URL.Path != "/api/v1/domains/" {
			http.Error(w, r.Method+" "+r.URL.Path, http.StatusNotFound)

			return
		}

		w.WriteHeader(http.StatusCreated)
		_, _ = io.WriteString(
			w,
			`{"name":"example.org","keys":[{"dnskey":"257 3 13 K","ds":[],"flags":257,"keytype":"csk","managed":true}]}`,
		)
	}))
	defer srv.Close()

	withCreate, err := desec.New(srv.Client(), srv.URL+"/api/v1/", "routine", "creator")
	if err != nil {
		t.Fatal(err)
	}

	if !withCreate.CanCreateDomains() {
		t.Fatal("CanCreateDomains should be true with a creation token")
	}

	d, err := withCreate.CreateDomain(t.Context(), "example.org")
	if err != nil || d.Name != "example.org" || len(d.Keys) != 1 {
		t.Fatalf("CreateDomain: %+v %v", d, err)
	}

	if seen != "Token creator" {
		t.Fatalf("creation used %q, want the creation token", seen)
	}

	withoutCreate, err := desec.New(srv.Client(), srv.URL+"/api/v1/", "routine", "")
	if err != nil {
		t.Fatal(err)
	}

	if withoutCreate.CanCreateDomains() {
		t.Fatal("CanCreateDomains should be false without a creation token")
	}

	seen = ""

	_, err = withoutCreate.CreateDomain(t.Context(), "example.org")
	if !errors.Is(err, fault.ErrMissingRequirements) || seen != "" {
		t.Fatalf("err = %v, seen = %q; want ErrMissingRequirements and no request", err, seen)
	}
}
