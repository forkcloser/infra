package reconcile_test

// The reconciler's contract, over generated worlds: "the file is the whole
// zone", so a plan names every declared set that differs from what deSEC
// holds, a deletion for every held set the file does not name (the apex NS
// set excepted, which is deSEC's own), the delegation when it is not
// deSEC's, and the keys the registrar lacks or has in excess; and one apply
// converges, so the plan after it is empty. The fake services are the
// owner's; the worlds and the model are written from the package comment.

import (
	"fmt"
	"math/rand/v2"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/forkcloser/infra/internal/desec"
	"github.com/forkcloser/infra/internal/gandi"
	"github.com/forkcloser/infra/internal/zones"
)

// vocabulary is the generated world's: four subnames, four types, two
// TTLs and two values per type.
type vocabulary struct {
	subnames, types []string
	ttls            []int
	values          map[string][]string
}

func words() vocabulary {
	return vocabulary{
		subnames: []string{"", "www", "api", "_dmarc"},
		types:    []string{"A", "AAAA", "TXT", "CNAME"},
		ttls:     []int{3600, 7200},
		values: map[string][]string{
			"A":     {"192.0.2.1", "192.0.2.2"},
			"AAAA":  {"2001:db8::1", "2001:db8::2"},
			"TXT":   {"\"v=spf1 -all\"", "\"x\""},
			"CNAME": {"a.pages.dev.", "b.pages.dev."},
		},
	}
}

// world is one generated state: the declared zone, what deSEC holds, the
// delegation and the keys on both sides.
type world struct {
	zone        zones.Zone
	held        []desec.RRset
	nameservers []string
	published   []gandi.Key
	keys        []desec.Key
}

// generate is a declared zone and a held state, each a random subset of the
// vocabulary, the held state with the apex NS set deSEC always holds and,
// sometimes, a set the file does not name.
func generate(rng *rand.Rand) world {
	v := words()
	subnames, types, ttls, values := v.subnames, v.types, v.ttls, v.values
	zone := zones.Zone{Name: "example.org", Registrar: zones.RegistrarGandi}

	var held []desec.RRset

	for _, subname := range subnames {
		for _, typ := range types {
			if typ == "CNAME" && subname == "" {
				continue
			}

			pickRecords := func() []string {
				n := 1
				if typ != "CNAME" && rng.IntN(2) == 0 {
					n = 2
				}

				return slices.Clone(values[typ][:n])
			}

			switch rng.IntN(4) {
			case 0: // declared only
				zone.Records = append(
					zone.Records,
					zones.RRset{Subname: subname, Type: typ, TTL: ttls[rng.IntN(2)], Records: pickRecords()},
				)
			case 1: // held only
				held = append(
					held,
					desec.RRset{Subname: subname, Type: typ, TTL: ttls[rng.IntN(2)], Records: pickRecords()},
				)
			case 2: // both, maybe the same
				declared := zones.RRset{Subname: subname, Type: typ, TTL: ttls[rng.IntN(2)], Records: pickRecords()}
				zone.Records = append(zone.Records, declared)

				current := desec.RRset{
					Subname: subname,
					Type:    typ,
					TTL:     declared.TTL,
					Records: slices.Clone(declared.Records),
				}
				if rng.IntN(2) == 0 {
					current.TTL = ttls[1-slices.Index(ttls, declared.TTL)]
				}

				if rng.IntN(3) == 0 {
					slices.Reverse(current.Records)
				}

				held = append(held, current)
			default: // neither
			}
		}
	}

	held = append(
		held,
		desec.RRset{Subname: "", Type: "NS", TTL: 3600, Records: []string{"ns1.desec.io.", "ns2.desec.org."}},
	)

	nameservers := []string{"ns1.desec.io", "ns2.desec.org"}

	switch rng.IntN(3) {
	case 0:
		nameservers = []string{"ns-1.gandi.net", "ns-2.gandi.net"}
	case 1:
		slices.Reverse(nameservers)
	default:
	}

	desecKeys := []desec.Key{
		{DNSKey: "257 3 13 KSKONE", Flags: 257, Managed: true},
		{DNSKey: "256 3 13 ZSKONE", Flags: 256, Managed: true},
	}
	if rng.IntN(2) == 0 {
		desecKeys = append(desecKeys, desec.Key{DNSKey: "257 3 13 FOREIGN", Flags: 257, Managed: false})
	}

	var published []gandi.Key

	if rng.IntN(2) == 0 {
		published = append(published, gandi.Key{ID: 1, Algorithm: 13, Type: gandi.KeyTypeKSK, PublicKey: "KSKONE"})
	}

	if rng.IntN(3) == 0 {
		published = append(published, gandi.Key{ID: 2, Algorithm: 13, Type: gandi.KeyTypeKSK, PublicKey: "STALE"})
	}

	return world{zone: zone, held: held, nameservers: nameservers, published: published, keys: desecKeys}
}

// expected is the model's plan for the world: the keys of the sets to
// write, the keys of the sets to delete, whether the delegation changes,
// and the keys to publish and withdraw.
type expected struct {
	writes, deletes, publish, withdraw []string
	delegation                         bool
}

func model(w world) expected {
	zone, held, nameservers, published, keys := w.zone, w.held, w.nameservers, w.published, w.keys

	var e expected

	byKey := map[string]desec.RRset{}
	for _, set := range held {
		byKey[set.Subname+"/"+set.Type] = set
	}

	declared := map[string]bool{}

	for _, want := range zone.Records {
		key := want.Subname + "/" + want.Type
		declared[key] = true

		current, ok := byKey[key]
		same := ok && current.TTL == want.TTL && sameStrings(current.Records, want.Records)

		if !same {
			e.writes = append(e.writes, key)
		}
	}

	for key, set := range byKey {
		if !declared[key] && !(set.Subname == "" && set.Type == "NS") {
			e.deletes = append(e.deletes, key)
		}
	}

	e.delegation = !sameStrings(nameservers, []string{"ns1.desec.io", "ns2.desec.org"})

	wanted := map[string]bool{}

	for _, key := range keys {
		if key.Managed && key.Flags%2 == 1 {
			public := strings.Fields(key.DNSKey)[3]
			wanted[public] = true

			if !slices.ContainsFunc(
				published,
				func(p gandi.Key) bool { return p.PublicKey == public && p.Algorithm == 13 },
			) {
				e.publish = append(e.publish, public)
			}
		}
	}

	for _, p := range published {
		if !wanted[p.PublicKey] {
			e.withdraw = append(e.withdraw, strconv.Itoa(p.ID))
		}
	}

	slices.Sort(e.writes)
	slices.Sort(e.deletes)
	slices.Sort(e.publish)
	slices.Sort(e.withdraw)

	return e
}

func sameStrings(a, b []string) bool {
	x, y := slices.Clone(a), slices.Clone(b)
	slices.Sort(x)
	slices.Sort(y)

	return slices.Equal(x, y)
}

func TestPlanIsTheWholeZoneAndApplyConverges(t *testing.T) {
	t.Parallel()

	for seed := range 48 {
		t.Run(fmt.Sprintf("world %d", seed), func(t *testing.T) {
			t.Parallel()

			rng := rand.New(rand.NewPCG(uint64(seed), 11))
			w := generate(rng)
			zone, held := w.zone, w.held
			want := model(w)

			f := newFake()
			f.domains[zone.Name] = desec.Domain{Name: zone.Name, Keys: w.keys}
			f.rrsets[zone.Name] = held
			f.nameservers[zone.Name] = w.nameservers
			f.published[zone.Name] = w.published
			r := reconciler(t, f)

			cfg := &zones.Config{Zones: []zones.Zone{zone}}
			if err := cfg.Validate(); err != nil {
				t.Fatalf("the generated zone is not valid: %v", err)
			}

			plan, err := r.Plan(t.Context(), cfg)
			if err != nil {
				t.Fatalf("Plan: %v", err)
			}

			zp := plan.Zones[0]

			var writes, deletes, publish, withdraw []string

			for _, set := range zp.RRsets {
				if len(set.Records) == 0 {
					deletes = append(deletes, set.Subname+"/"+set.Type)
				} else {
					writes = append(writes, set.Subname+"/"+set.Type)
				}
			}

			for _, spec := range zp.KeysToCreate {
				publish = append(publish, spec.PublicKey)
			}

			for _, key := range zp.KeysToDelete {
				withdraw = append(withdraw, strconv.Itoa(key.ID))
			}

			got := expected{
				writes:     writes,
				deletes:    deletes,
				publish:    publish,
				withdraw:   withdraw,
				delegation: zp.Nameservers != nil,
			}
			slices.Sort(got.writes)
			slices.Sort(got.deletes)
			slices.Sort(got.publish)
			slices.Sort(got.withdraw)

			if fmt.Sprintf("%+v", got) != fmt.Sprintf("%+v", want) {
				t.Fatalf("plan %+v\nmodel %+v\ndeclared %+v\nheld %+v", got, want, zone.Records, held)
			}

			if zp.CreateDomain || zp.KeysPending {
				t.Fatalf("an existing zone is planned as new: %+v", zp)
			}

			// The sets to write are sorted by key, the plan's text names
			// each of them, and the plan is empty exactly when nothing
			// differs.
			keysInOrder := make([]string, 0, len(zp.RRsets))
			for _, set := range zp.RRsets {
				keysInOrder = append(keysInOrder, set.Subname+"/"+set.Type)
			}

			if !slices.IsSorted(keysInOrder) {
				t.Errorf("sets out of order: %v", keysInOrder)
			}

			var text strings.Builder

			plan.Write(&text)

			nothing := len(want.writes)+len(want.deletes)+len(want.publish)+len(want.withdraw) == 0 && !want.delegation
			if plan.Empty() != nothing || (nothing != strings.Contains(text.String(), "No changes.")) {
				t.Errorf("Empty = %v, text %q, model says nothing=%v", plan.Empty(), text.String(), nothing)
			}

			if applyErr := r.Apply(t.Context(), plan); applyErr != nil {
				t.Fatalf("Apply: %v", applyErr)
			}

			again, err := r.Plan(t.Context(), cfg)
			if err != nil {
				t.Fatalf("Plan after Apply: %v", err)
			}

			if !again.Empty() {
				var after strings.Builder

				again.Write(&after)
				t.Fatalf("the plan after one apply is not empty:\n%s", after.String())
			}

			// What deSEC holds is the file, record for record, plus the
			// apex NS set.
			for _, set := range f.rrsets[zone.Name] {
				if set.Subname == "" && set.Type == "NS" {
					continue
				}

				i := slices.IndexFunc(
					zone.Records,
					func(r zones.RRset) bool { return r.Subname == set.Subname && r.Type == set.Type },
				)
				if i < 0 || !sameStrings(zone.Records[i].Records, set.Records) || zone.Records[i].TTL != set.TTL {
					t.Errorf("deSEC holds %+v, not in the file as such", set)
				}
			}

			if len(f.rrsets[zone.Name])-1 != len(zone.Records) {
				t.Errorf("deSEC holds %d sets, the file %d", len(f.rrsets[zone.Name])-1, len(zone.Records))
			}
		})
	}
}
