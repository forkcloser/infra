package reconcile_test

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/mycophonic/primordium/fault"

	"github.com/forkcloser/infra/internal/desec"
	"github.com/forkcloser/infra/internal/gandi"
	"github.com/forkcloser/infra/internal/reconcile"
	"github.com/forkcloser/infra/internal/zones"
)

// fakeServices is a deSEC and a Gandi, enough of each for the reconciler:
// one zone, its record sets and keys, the delegation and the published
// keys. Every write is recorded in the order it arrived.
type fakeServices struct {
	mu          sync.Mutex
	domains     map[string]desec.Domain
	rrsets      map[string][]desec.RRset
	nameservers map[string][]string
	published   map[string][]gandi.Key
	nextKeyID   int
	writes      []string
}

func newFake() *fakeServices {
	return &fakeServices{
		domains:     map[string]desec.Domain{},
		rrsets:      map[string][]desec.RRset{},
		nameservers: map[string][]string{},
		published:   map[string][]gandi.Key{},
		nextKeyID:   100,
	}
}

func (f *fakeServices) desecHandler(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()

	path := strings.TrimPrefix(r.URL.Path, "/api/v1/")

	switch {
	case r.Method == http.MethodPost && path == "domains/":
		var body struct {
			Name string `json:"name"`
		}

		_ = json.NewDecoder(r.Body).Decode(&body)
		d := desec.Domain{Name: body.Name, Keys: []desec.Key{{DNSKey: "257 3 13 NEWKEY", Flags: 257, Managed: true}}}
		f.domains[body.Name] = d
		f.writes = append(f.writes, "desec create "+body.Name)

		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(d)
	case r.Method == http.MethodGet && strings.HasPrefix(path, "domains/") && strings.HasSuffix(path, "/rrsets/"):
		name := strings.TrimSuffix(strings.TrimPrefix(path, "domains/"), "/rrsets/")
		_ = json.NewEncoder(w).Encode(f.rrsets[name])
	case r.Method == http.MethodPut && strings.HasSuffix(path, "/rrsets/"):
		name := strings.TrimSuffix(strings.TrimPrefix(path, "domains/"), "/rrsets/")

		var sets []desec.RRset

		_ = json.NewDecoder(r.Body).Decode(&sets)

		for _, s := range sets {
			f.rrsets[name] = slices.DeleteFunc(f.rrsets[name], func(h desec.RRset) bool {
				return h.Subname == s.Subname && h.Type == s.Type
			})

			if len(s.Records) > 0 {
				f.rrsets[name] = append(f.rrsets[name], s)
			}
		}

		f.writes = append(f.writes, "desec rrsets "+name)
		_, _ = io.WriteString(w, "[]")
	case r.Method == http.MethodGet && strings.HasPrefix(path, "domains/"):
		name := strings.TrimSuffix(strings.TrimPrefix(path, "domains/"), "/")

		d, ok := f.domains[name]
		if !ok {
			http.Error(w, "no", http.StatusNotFound)

			return
		}

		_ = json.NewEncoder(w).Encode(d)
	default:
		http.Error(w, r.Method+" "+path, http.StatusNotFound)
	}
}

func (f *fakeServices) gandiHandler(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()

	path := strings.TrimPrefix(r.URL.Path, "/v5/domain/domains/")
	name, rest, _ := strings.Cut(path, "/")

	switch {
	case r.Method == http.MethodGet && rest == "nameservers":
		_ = json.NewEncoder(w).Encode(f.nameservers[name])
	case r.Method == http.MethodPut && rest == "nameservers":
		var body struct {
			Nameservers []string `json:"nameservers"`
		}

		_ = json.NewDecoder(r.Body).Decode(&body)
		f.nameservers[name] = body.Nameservers
		f.writes = append(f.writes, "gandi nameservers "+name)

		w.WriteHeader(http.StatusAccepted)
	case r.Method == http.MethodGet && rest == "dnskeys":
		keys := f.published[name]
		if keys == nil {
			keys = []gandi.Key{}
		}

		_ = json.NewEncoder(w).Encode(keys)
	case r.Method == http.MethodPost && rest == "dnskeys":
		var spec gandi.KeySpec

		_ = json.NewDecoder(r.Body).Decode(&spec)
		f.nextKeyID++
		f.published[name] = append(
			f.published[name],
			gandi.Key{ID: f.nextKeyID, Algorithm: spec.Algorithm, Type: spec.Type, PublicKey: spec.PublicKey},
		)
		f.writes = append(f.writes, "gandi create key "+name)

		w.WriteHeader(http.StatusCreated)
	case r.Method == http.MethodDelete && strings.HasPrefix(rest, "dnskeys/"):
		id := strings.TrimPrefix(rest, "dnskeys/")
		f.published[name] = slices.DeleteFunc(f.published[name], func(k gandi.Key) bool {
			return id == strconv.Itoa(k.ID)
		})
		f.writes = append(f.writes, "gandi delete key "+name+" "+id)

		w.WriteHeader(http.StatusNoContent)
	default:
		http.Error(w, r.Method+" "+path, http.StatusNotFound)
	}
}

func reconciler(t *testing.T, f *fakeServices) *reconcile.Reconciler {
	t.Helper()

	ds := httptest.NewServer(http.HandlerFunc(f.desecHandler))
	gs := httptest.NewServer(http.HandlerFunc(f.gandiHandler))

	t.Cleanup(ds.Close)
	t.Cleanup(gs.Close)

	d, err := desec.New(ds.Client(), ds.URL+"/api/v1/", "t", "create")
	if err != nil {
		t.Fatal(err)
	}

	g, err := gandi.New(gs.Client(), gs.URL+"/v5/domain/", "t")
	if err != nil {
		t.Fatal(err)
	}

	return &reconcile.Reconciler{DeSEC: d, Gandi: g}
}

func config() *zones.Config {
	return &zones.Config{Zones: []zones.Zone{{
		Name:      "example.org",
		Registrar: zones.RegistrarGandi,
		Records: []zones.RRset{
			{Subname: "www", Type: "CNAME", TTL: 3600, Records: []string{"site.pages.dev."}},
		},
	}}}
}

func TestNewZoneIsCreatedDelegatedAndSignedInOneApply(t *testing.T) {
	t.Parallel()

	f := newFake()
	f.nameservers["example.org"] = []string{"ns-1.gandi.net"}
	r := reconciler(t, f)

	plan, err := r.Plan(t.Context(), config())
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}

	if plan.Empty() {
		t.Fatal("plan should not be empty for a new zone")
	}

	zp := plan.Zones[0]
	if !zp.CreateDomain || !zp.KeysPending || zp.Nameservers == nil || len(zp.RRsets) != 1 {
		t.Fatalf("unexpected zone plan: %+v", zp)
	}

	var out strings.Builder

	plan.Write(&out)

	for _, want := range []string{"create zone", "www CNAME", "delegation", "known once the zone exists"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("plan text lacks %q:\n%s", want, out.String())
		}
	}

	if applyErr := r.Apply(t.Context(), plan); applyErr != nil {
		t.Fatalf("Apply: %v", applyErr)
	}

	want := []string{
		"desec create example.org",
		"desec rrsets example.org",
		"gandi nameservers example.org",
		"gandi create key example.org",
	}
	if !slices.Equal(f.writes, want) {
		t.Fatalf("writes = %v, want %v", f.writes, want)
	}

	if len(f.published["example.org"]) != 1 || f.published["example.org"][0].PublicKey != "NEWKEY" ||
		f.published["example.org"][0].Algorithm != 13 {
		t.Fatalf("published keys = %+v", f.published["example.org"])
	}

	plan, err = r.Plan(t.Context(), config())
	if err != nil {
		t.Fatalf("second Plan: %v", err)
	}

	if !plan.Empty() {
		var again strings.Builder

		plan.Write(&again)
		t.Fatalf("second plan should be empty:\n%s", again.String())
	}
}

func TestExistingZoneConvergesAndDropsUndeclaredSets(t *testing.T) {
	t.Parallel()

	f := newFake()
	f.domains["example.org"] = desec.Domain{Name: "example.org", Keys: []desec.Key{
		{DNSKey: "257 3 13 KEY1", Flags: 257, Managed: true},
		{DNSKey: "256 3 13 ZSK", Flags: 256, Managed: true},
	}}
	f.rrsets["example.org"] = []desec.RRset{
		{Subname: "", Type: "NS", TTL: 3600, Records: []string{"ns1.desec.io.", "ns2.desec.org."}},
		{Subname: "www", Type: "CNAME", TTL: 3600, Records: []string{"old.pages.dev."}},
		{Subname: "stale", Type: "A", TTL: 3600, Records: []string{"192.0.2.1"}},
	}
	f.nameservers["example.org"] = []string{"ns2.desec.org", "ns1.desec.io"}
	f.published["example.org"] = []gandi.Key{{ID: 5, Algorithm: 13, Type: "ksk", PublicKey: "OLDKEY"}}
	r := reconciler(t, f)

	plan, err := r.Plan(t.Context(), config())
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}

	zp := plan.Zones[0]

	if zp.CreateDomain || zp.KeysPending {
		t.Fatalf("zone exists; plan = %+v", zp)
	}

	if zp.Nameservers != nil {
		t.Fatalf("delegation already right in a different order; plan wants %v", zp.Nameservers)
	}

	keys := func(sets []desec.RRset) []string {
		out := make([]string, 0, len(sets))
		for _, s := range sets {
			out = append(out, s.Subname+"/"+s.Type+"="+strings.Join(s.Records, ","))
		}

		return out
	}

	if got, want := keys(zp.RRsets), []string{"stale/A=", "www/CNAME=site.pages.dev."}; !slices.Equal(got, want) {
		t.Fatalf("rrsets = %v, want %v (apex NS untouched, stale deleted, www replaced)", got, want)
	}

	if len(zp.KeysToCreate) != 1 || zp.KeysToCreate[0].PublicKey != "KEY1" || len(zp.KeysToDelete) != 1 ||
		zp.KeysToDelete[0].ID != 5 {
		t.Fatalf("keys: create %+v delete %+v (the ZSK must not be delegated)", zp.KeysToCreate, zp.KeysToDelete)
	}

	if applyErr := r.Apply(t.Context(), plan); applyErr != nil {
		t.Fatalf("Apply: %v", applyErr)
	}

	want := []string{"desec rrsets example.org", "gandi create key example.org", "gandi delete key example.org 5"}
	if !slices.Equal(f.writes, want) {
		t.Fatalf("writes = %v, want %v", f.writes, want)
	}

	plan, err = r.Plan(t.Context(), config())
	if err != nil || !plan.Empty() {
		t.Fatalf("second plan: err=%v empty=%v", err, plan != nil && plan.Empty())
	}
}

func TestMissingZoneWithoutCreationTokenIsRefusedAtPlan(t *testing.T) {
	t.Parallel()

	f := newFake()
	ds := httptest.NewServer(http.HandlerFunc(f.desecHandler))
	gs := httptest.NewServer(http.HandlerFunc(f.gandiHandler))

	t.Cleanup(ds.Close)
	t.Cleanup(gs.Close)

	d, err := desec.New(ds.Client(), ds.URL+"/api/v1/", "t", "")
	if err != nil {
		t.Fatal(err)
	}

	g, err := gandi.New(gs.Client(), gs.URL+"/v5/domain/", "t")
	if err != nil {
		t.Fatal(err)
	}

	r := &reconcile.Reconciler{DeSEC: d, Gandi: g}

	_, err = r.Plan(t.Context(), config())
	if !errors.Is(err, fault.ErrMissingRequirements) {
		t.Fatalf("err = %v, want ErrMissingRequirements", err)
	}

	if len(f.writes) != 0 {
		t.Fatalf("plan must write nothing, wrote %v", f.writes)
	}
}
