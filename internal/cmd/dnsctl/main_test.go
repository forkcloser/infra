package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/forkcloser/infra/internal/desec"
	"github.com/forkcloser/infra/internal/reconcile"
)

// The zones file the fakes are made to match, so a plan has nothing to do.
const zonesFile = `
zones:
  - name: example.org
    registrar: gandi
    records:
      - subname: www
        type: CNAME
        ttl: 3600
        records: ["site.pages.dev."]
`

// fakes serves one zone at deSEC and its delegation at Gandi, read-only:
// enough for plan, which never writes.
type fakes struct {
	rrsets []desec.RRset
}

func (f *fakes) desec(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/api/v1/")

	switch {
	case r.Method == http.MethodGet && path == "domains/example.org/rrsets/":
		_ = json.NewEncoder(w).Encode(f.rrsets)
	case r.Method == http.MethodGet && path == "domains/example.org/":
		_ = json.NewEncoder(w).Encode(desec.Domain{Name: "example.org", Keys: []desec.Key{}})
	default:
		http.Error(w, r.Method+" "+path, http.StatusNotFound)
	}
}

func (*fakes) gandi(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/v5/domain/domains/example.org/")

	switch {
	case r.Method == http.MethodGet && path == "nameservers":
		_ = json.NewEncoder(w).Encode(reconcile.Nameservers())
	case r.Method == http.MethodGet && path == "dnskeys":
		_, _ = w.Write([]byte("[]"))
	default:
		http.Error(w, r.Method+" "+path, http.StatusNotFound)
	}
}

// plan runs the command end to end, kong included, against the fakes.
func plan(t *testing.T, f *fakes) (code int, stdout, stderr string) {
	t.Helper()

	ds := httptest.NewServer(http.HandlerFunc(f.desec))
	gs := httptest.NewServer(http.HandlerFunc(f.gandi))

	t.Cleanup(ds.Close)
	t.Cleanup(gs.Close)

	path := filepath.Join(t.TempDir(), "zones.yaml")
	if err := os.WriteFile(path, []byte(zonesFile), 0o600); err != nil {
		t.Fatal(err)
	}

	var out, errOut bytes.Buffer

	// Tokens as flags, not environment: the tests then share nothing and
	// run in parallel.
	code = run(t.Context(), []string{
		"--desec-token", "routine",
		"--gandi-token", "gandi",
		"--desec-base-url", ds.URL + "/api/v1/",
		"--gandi-base-url", gs.URL + "/v5/domain/",
		"plan", path,
	}, &out, &errOut)

	return code, out.String(), errOut.String()
}

func TestPlanWithNothingToChangeExitsZero(t *testing.T) {
	t.Parallel()

	code, stdout, stderr := plan(t, &fakes{rrsets: []desec.RRset{
		{Subname: "www", Type: "CNAME", TTL: 3600, Records: []string{"site.pages.dev."}},
	}})

	if code != exitOK {
		t.Fatalf("exit %d, stderr %q", code, stderr)
	}

	if !strings.Contains(stdout, "No changes.") {
		t.Fatalf("stdout %q", stdout)
	}
}

func TestPlanWithChangesExitsTwo(t *testing.T) {
	t.Parallel()

	code, stdout, stderr := plan(t, &fakes{rrsets: []desec.RRset{}})

	if code != exitChanges {
		t.Fatalf("exit %d, stderr %q", code, stderr)
	}

	if !strings.Contains(stdout, "www") {
		t.Fatalf("stdout %q", stdout)
	}
}

func TestUsageErrorExitsOne(t *testing.T) {
	t.Parallel()

	var stdout, stderr bytes.Buffer

	code := run(t.Context(), []string{"--no-such-flag"}, &stdout, &stderr)

	if code != exitError {
		t.Fatalf("exit %d", code)
	}

	if !strings.Contains(stderr.String(), "no-such-flag") {
		t.Fatalf("stderr %q", stderr.String())
	}
}
