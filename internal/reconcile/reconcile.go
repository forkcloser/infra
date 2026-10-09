// Package reconcile turns the zones file into what deSEC and the registrar
// hold. Plan reads both and lists every difference; Apply makes the
// changes, in the order the DNSSEC chain requires: the zone, its record
// sets, the delegation, and only then the key published at the registrar.
package reconcile

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"slices"
	"strconv"
	"strings"

	"github.com/mycophonic/primordium/fault"

	"github.com/forkcloser/infra/internal/desec"
	"github.com/forkcloser/infra/internal/gandi"
	"github.com/forkcloser/infra/internal/zones"
)

// dnskeyFields is the DNSKEY presentation format: flags, protocol,
// algorithm, and the key itself.
const dnskeyFields = 4

// Nameservers returns deSEC's set, the same for every zone it serves.
func Nameservers() []string {
	return []string{"ns1.desec.io", "ns2.desec.org"}
}

// Plan is every change one apply would make, zone by zone.
type Plan struct {
	Zones []ZonePlan
}

// ZonePlan is the changes for one zone.
type ZonePlan struct {
	Name         string
	CreateDomain bool
	// RRsets to write in one bulk request; a set with no records is a
	// deletion.
	RRsets []desec.RRset
	// Nameservers to set at the registrar, nil when already right.
	Nameservers []string
	// Keys to publish at the registrar and keys to withdraw. Unknown
	// until the domain exists: a zone being created leaves both empty and
	// KeysPending set, and the next plan shows them.
	KeysToCreate []gandi.KeySpec
	KeysToDelete []gandi.Key
	KeysPending  bool
}

// Empty reports whether the plan changes nothing.
func (p *Plan) Empty() bool {
	for i := range p.Zones {
		if !p.Zones[i].empty() {
			return false
		}
	}

	return true
}

// Write prints the plan for a reader.
func (p *Plan) Write(out io.Writer) {
	if p.Empty() {
		fmt.Fprintln(out, "No changes.")

		return
	}

	for i := range p.Zones {
		p.Zones[i].write(out)
	}
}

func (z *ZonePlan) empty() bool {
	return !z.CreateDomain && len(z.RRsets) == 0 && z.Nameservers == nil &&
		len(z.KeysToCreate) == 0 && len(z.KeysToDelete) == 0 && !z.KeysPending
}

func (z *ZonePlan) write(out io.Writer) {
	fmt.Fprintf(out, "zone %s\n", z.Name)

	if z.CreateDomain {
		fmt.Fprintln(out, "  + create zone at deSEC")
	}

	for _, set := range z.RRsets {
		writeRRset(out, set)
	}

	if z.Nameservers != nil {
		fmt.Fprintf(out, "  ~ delegation at registrar: %s\n", strings.Join(z.Nameservers, " "))
	}

	for _, key := range z.KeysToCreate {
		fmt.Fprintf(out, "  + DNSSEC key at registrar: alg %d %s\n", key.Algorithm, key.Type)
	}

	for _, key := range z.KeysToDelete {
		fmt.Fprintf(out, "  - DNSSEC key at registrar: id %d alg %d %s\n", key.ID, key.Algorithm, key.Type)
	}

	if z.KeysPending {
		fmt.Fprintln(out, "  ? DNSSEC keys: known once the zone exists; plan again after apply")
	}
}

func writeRRset(out io.Writer, set desec.RRset) {
	name := set.Subname
	if name == "" {
		name = "@"
	}

	if len(set.Records) == 0 {
		fmt.Fprintf(out, "  - %s %s\n", name, set.Type)

		return
	}

	fmt.Fprintf(out, "  ~ %s %s ttl=%d %s\n", name, set.Type, set.TTL, strings.Join(set.Records, " "))
}

// Reconciler holds the two clients.
type Reconciler struct {
	DeSEC *desec.Client
	Gandi *gandi.Client
}

// Plan computes the changes for every zone in the configuration.
func (r *Reconciler) Plan(ctx context.Context, cfg *zones.Config) (*Plan, error) {
	plan := &Plan{Zones: make([]ZonePlan, 0, len(cfg.Zones))}

	for i := range cfg.Zones {
		zonePlan, err := r.planZone(ctx, &cfg.Zones[i])
		if err != nil {
			return nil, fmt.Errorf("zone %s: %w", cfg.Zones[i].Name, err)
		}

		plan.Zones = append(plan.Zones, *zonePlan)
	}

	return plan, nil
}

// Apply makes every change in the plan. A zone created here has its keys
// published too: deSEC returns them on creation, so the chain completes in
// one run.
func (r *Reconciler) Apply(ctx context.Context, plan *Plan) error {
	for i := range plan.Zones {
		if err := r.applyZone(ctx, &plan.Zones[i]); err != nil {
			return fmt.Errorf("zone %s: %w", plan.Zones[i].Name, err)
		}
	}

	return nil
}

func (r *Reconciler) planZone(ctx context.Context, zone *zones.Zone) (*ZonePlan, error) {
	zonePlan := &ZonePlan{Name: zone.Name}

	domain, err := r.DeSEC.GetDomain(ctx, zone.Name)

	switch {
	case desec.IsNotFound(err):
		// Refused at plan time, not after the record sets and delegation
		// of other zones have already been written.
		if !r.DeSEC.CanCreateDomains() {
			return nil, fmt.Errorf(
				"%w: zone does not exist and no deSEC creation token was given",
				fault.ErrMissingRequirements,
			)
		}

		zonePlan.CreateDomain = true
		zonePlan.KeysPending = true
		zonePlan.RRsets = wantedRRsets(zone)
	case err != nil:
		return nil, fmt.Errorf("reading zone: %w", err)
	default:
		if planErr := r.planExisting(ctx, zone, domain, zonePlan); planErr != nil {
			return nil, planErr
		}
	}

	nameservers, err := r.Gandi.Nameservers(ctx, zone.Name)
	if err != nil {
		return nil, fmt.Errorf("reading delegation: %w", err)
	}

	if !sameSet(nameservers, Nameservers()) {
		zonePlan.Nameservers = Nameservers()
	}

	return zonePlan, nil
}

// planExisting fills the record-set and key diffs for a zone deSEC holds.
func (r *Reconciler) planExisting(
	ctx context.Context,
	zone *zones.Zone,
	domain *desec.Domain,
	zonePlan *ZonePlan,
) error {
	current, err := r.DeSEC.ListRRsets(ctx, zone.Name)
	if err != nil {
		return fmt.Errorf("reading record sets: %w", err)
	}

	zonePlan.RRsets = diffRRsets(current, wantedRRsets(zone))

	published, err := r.Gandi.Keys(ctx, zone.Name)
	if err != nil {
		return fmt.Errorf("reading published keys: %w", err)
	}

	zonePlan.KeysToCreate, zonePlan.KeysToDelete = diffKeys(published, domain.Keys)

	return nil
}

func (r *Reconciler) applyZone(ctx context.Context, zonePlan *ZonePlan) error {
	toCreate, toDelete := zonePlan.KeysToCreate, zonePlan.KeysToDelete

	if zonePlan.CreateDomain {
		created, err := r.DeSEC.CreateDomain(ctx, zonePlan.Name)
		if err != nil {
			return fmt.Errorf("creating zone: %w", err)
		}

		slog.InfoContext(ctx, "zone created", "zone", zonePlan.Name)

		if len(created.Keys) == 0 {
			slog.WarnContext(ctx, "zone created without keys yet; plan again to publish DNSSEC", "zone", zonePlan.Name)
		} else {
			toCreate, toDelete = diffKeys(nil, created.Keys)
		}
	}

	if len(zonePlan.RRsets) > 0 {
		if err := r.DeSEC.PutRRsets(ctx, zonePlan.Name, zonePlan.RRsets); err != nil {
			return fmt.Errorf("writing record sets: %w", err)
		}

		slog.InfoContext(ctx, "record sets written", "zone", zonePlan.Name, "count", len(zonePlan.RRsets))
	}

	if zonePlan.Nameservers != nil {
		if err := r.Gandi.SetNameservers(ctx, zonePlan.Name, zonePlan.Nameservers); err != nil {
			return fmt.Errorf("setting delegation: %w", err)
		}

		slog.InfoContext(ctx, "delegation set", "zone", zonePlan.Name, "nameservers", zonePlan.Nameservers)
	}

	return r.applyKeys(ctx, zonePlan.Name, toCreate, toDelete)
}

func (r *Reconciler) applyKeys(ctx context.Context, zone string, toCreate []gandi.KeySpec, toDelete []gandi.Key) error {
	for _, spec := range toCreate {
		if err := r.Gandi.CreateKey(ctx, zone, spec); err != nil {
			return fmt.Errorf("publishing key: %w", err)
		}

		slog.InfoContext(ctx, "DNSSEC key published", "zone", zone, "algorithm", spec.Algorithm)
	}

	for _, key := range toDelete {
		if err := r.Gandi.DeleteKey(ctx, zone, key.ID); err != nil {
			return fmt.Errorf("withdrawing key %d: %w", key.ID, err)
		}

		slog.InfoContext(ctx, "DNSSEC key withdrawn", "zone", zone, "id", key.ID)
	}

	return nil
}

func wantedRRsets(zone *zones.Zone) []desec.RRset {
	out := make([]desec.RRset, 0, len(zone.Records))

	for _, rrset := range zone.Records {
		records := slices.Clone(rrset.Records)
		slices.Sort(records)
		out = append(out, desec.RRset{Subname: rrset.Subname, Type: rrset.Type, TTL: rrset.TTL, Records: records})
	}

	return out
}

// diffRRsets returns the sets to write: every wanted set that differs from
// what the zone holds, and a deletion for every held set the file does not
// name. The apex NS set is deSEC's own and is never touched.
func diffRRsets(current, wanted []desec.RRset) []desec.RRset {
	have := make(map[string]desec.RRset, len(current))

	for _, set := range current {
		have[set.Subname+"/"+set.Type] = set
	}

	var out []desec.RRset

	for _, want := range wanted {
		key := want.Subname + "/" + want.Type

		held, ok := have[key]
		delete(have, key)

		if ok && held.TTL == want.TTL && sameSet(held.Records, want.Records) {
			continue
		}

		out = append(out, want)
	}

	for _, held := range have {
		if held.Subname == "" && held.Type == "NS" {
			continue
		}

		out = append(out, desec.RRset{Subname: held.Subname, Type: held.Type, TTL: held.TTL, Records: []string{}})
	}

	slices.SortFunc(out, func(a, b desec.RRset) int {
		return strings.Compare(a.Subname+"/"+a.Type, b.Subname+"/"+b.Type)
	})

	return out
}

// diffKeys returns the deSEC-managed keys fit for delegation that the
// registrar does not publish, and the registrar's keys deSEC no longer
// has. A key with even flags is a zone-signing key and never delegated.
func diffKeys(published []gandi.Key, keys []desec.Key) ([]gandi.KeySpec, []gandi.Key) {
	wanted := make([]gandi.KeySpec, 0, len(keys))

	for _, key := range keys {
		if !key.Managed || key.Flags%2 == 0 {
			continue
		}

		spec, err := keySpec(key.DNSKey)
		if err != nil {
			slog.Warn("skipping unparseable DNSKEY", "dnskey", key.DNSKey, "err", err)

			continue
		}

		wanted = append(wanted, spec)
	}

	var toCreate []gandi.KeySpec

	for _, spec := range wanted {
		if !slices.ContainsFunc(published, func(key gandi.Key) bool { return key.Matches(spec) }) {
			toCreate = append(toCreate, spec)
		}
	}

	var toDelete []gandi.Key

	for _, key := range published {
		if !slices.ContainsFunc(wanted, key.Matches) {
			toDelete = append(toDelete, key)
		}
	}

	return toCreate, toDelete
}

// keySpec splits a DNSKEY presentation "flags protocol algorithm key" into
// what the registrar takes: the algorithm number and the key alone.
func keySpec(dnskey string) (gandi.KeySpec, error) {
	fields := strings.Fields(dnskey)
	if len(fields) < dnskeyFields {
		return gandi.KeySpec{}, fmt.Errorf("%w: DNSKEY %q has %d fields, want %d",
			fault.ErrUnacceptableResponse, dnskey, len(fields), dnskeyFields)
	}

	algorithm, err := strconv.Atoi(fields[2])
	if err != nil {
		return gandi.KeySpec{}, fmt.Errorf("%w: DNSKEY algorithm %q: %w", fault.ErrUnacceptableResponse, fields[2], err)
	}

	return gandi.KeySpec{Algorithm: algorithm, Type: gandi.KeyTypeKSK, PublicKey: strings.Join(fields[3:], "")}, nil
}

func sameSet(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}

	x, y := slices.Clone(a), slices.Clone(b)
	slices.Sort(x)
	slices.Sort(y)

	return slices.Equal(x, y)
}
