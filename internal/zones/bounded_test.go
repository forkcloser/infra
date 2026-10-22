package zones_test

// The validation rules as the package documents them, walked at their
// edges: every name shape a domain or a subname can take, the TTL at and
// under the floor, every record shape of every type, and the CNAME rules;
// each held to a model written from the rules in zones.go's comments, so
// a rule that drifts is seen against its statement.

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/forkcloser/infra/internal/zones"
)

// ruleModel is the documented rule set, independently written.
type ruleModel struct{}

func (ruleModel) domain(name string) bool {
	// Lowercase labels of letters, digits and inner hyphens, at least two,
	// the last letters only, no trailing dot.
	labels := strings.Split(name, ".")
	if len(labels) < 2 {
		return false
	}

	for i, label := range labels {
		if label == "" || strings.HasPrefix(label, "-") || strings.HasSuffix(label, "-") {
			return false
		}

		for _, r := range label {
			switch {
			case r >= 'a' && r <= 'z', r == '-':
			case r >= '0' && r <= '9':
				if i == len(labels)-1 {
					return false
				}
			default:
				return false
			}
		}

		if i == len(labels)-1 && len(label) < 2 {
			return false
		}
	}

	return true
}

func (ruleModel) subname(name string) bool {
	if name == "" {
		return true
	}

	labels := strings.Split(name, ".")

	for i, label := range labels {
		if label == "*" && i == 0 {
			continue
		}

		if label == "" || strings.HasPrefix(label, "-") || strings.HasSuffix(label, "-") {
			return false
		}

		for _, r := range label {
			switch {
			case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '-', r == '_':
			default:
				return false
			}
		}
	}

	return true
}

func (ruleModel) record(typ, rec string) bool {
	switch typ {
	case "CNAME":
		return strings.HasSuffix(rec, ".")
	case "MX":
		fields := strings.Fields(rec)

		return len(fields) == 2 && strings.HasSuffix(fields[1], ".")
	case "A", "AAAA", "TXT":
		return rec != ""
	default:
		return false
	}
}

func (m ruleModel) rrset(r zones.RRset) bool {
	if !m.subname(r.Subname) || r.TTL < zones.MinimumTTL || len(r.Records) == 0 {
		return false
	}

	for _, rec := range r.Records {
		if !m.record(r.Type, rec) {
			return false
		}
	}

	if r.Type == "CNAME" && (len(r.Records) != 1 || r.Subname == "") {
		return false
	}

	return true
}

func (m ruleModel) config(c *zones.Config) bool {
	if len(c.Zones) == 0 {
		return false
	}

	seenZones := map[string]bool{}

	for _, z := range c.Zones {
		if !m.domain(z.Name) || z.Registrar != "gandi" || seenZones[z.Name] {
			return false
		}

		seenZones[z.Name] = true
		seenSets := map[string]bool{}

		for _, r := range z.Records {
			if !m.rrset(r) || seenSets[r.Key()] {
				return false
			}

			seenSets[r.Key()] = true
		}
	}

	return true
}

func one(name, registrar string, sets ...zones.RRset) *zones.Config {
	return &zones.Config{Zones: []zones.Zone{{Name: name, Registrar: registrar, Records: sets}}}
}

func set(subname, typ string, ttl int, records ...string) zones.RRset {
	return zones.RRset{Subname: subname, Type: typ, TTL: ttl, Records: records}
}

func check(t *testing.T, what string, cfg *zones.Config) {
	t.Helper()

	err := cfg.Validate()
	valid := err == nil

	if want := (ruleModel{}).config(cfg); valid != want {
		t.Errorf("%s: Validate = %v, the rules say valid=%v", what, err, want)
	}

	if err != nil && !errors.Is(err, zones.ErrInvalidZonesFile) {
		t.Errorf("%s: %v does not wrap ErrInvalidZonesFile", what, err)
	}
}

func TestDomainNameShapes(t *testing.T) {
	t.Parallel()

	for _, name := range []string{
		"example.org", "a.b", "a-b.org", "a--b.org", "xn--bcher-kva.example", "sub.example.org", "0.org",
		"example.o", "example.org.", ".example.org", "Example.org", "example.ORG", "-a.org", "a-.org",
		"example", "exa_mple.org", "example.123", "a..b", "", "example.org ", "ex ample.org", "ünï.org",
		"a.b.c.d.e.f.g.h", "123.456", "1a.org", "a1.org", "a.1a",
	} {
		check(t, "domain "+fmt.Sprintf("%q", name), one(name, "gandi", set("www", "A", 3600, "192.0.2.1")))
	}
}

func TestSubnameShapes(t *testing.T) {
	t.Parallel()

	for _, name := range []string{
		"", "www", "*", "*.www", "www.*", "_acme-challenge", "_dmarc.sub", "a-b", "a--b", "-a", "a-",
		"A", "www.", ".www", "a..b", "a b", "ünï", "*www", "**", "_", "0", "a.b.c.d",
	} {
		check(t, "subname "+fmt.Sprintf("%q", name), one("example.org", "gandi", set(name, "A", 3600, "192.0.2.1")))
	}
}

func TestTTLAndRecordShapes(t *testing.T) {
	t.Parallel()

	for _, ttl := range []int{-1, 0, 1, 3599, 3600, 3601, 86400, 1 << 31} {
		check(t, fmt.Sprintf("ttl %d", ttl), one("example.org", "gandi", set("www", "A", ttl, "192.0.2.1")))
	}

	for _, typ := range []string{"A", "AAAA", "CNAME", "TXT", "MX", "NS", "SRV", "a", "cname", ""} {
		for _, rec := range []string{
			"", " ", "192.0.2.1", "2001:db8::1", "host.", "host", "10 mail.", "10 mail", "10", "10 mail. extra",
			" 10 mail.", "\"text\"", "0 .",
		} {
			check(t, fmt.Sprintf("%s record %q", typ, rec), one("example.org", "gandi", set("www", typ, 3600, rec)))
			check(t, fmt.Sprintf("%s apex record %q", typ, rec), one("example.org", "gandi", set("", typ, 3600, rec)))
		}

		check(t, typ+" two records", one("example.org", "gandi", set("www", typ, 3600, "a.", "b.")))
		check(t, typ+" no records", one("example.org", "gandi", set("www", typ, 3600)))
		check(t, typ+" one good one bad", one("example.org", "gandi", set("www", typ, 3600, "host.", "")))
	}
}

func TestDuplicatesAndRegistrars(t *testing.T) {
	t.Parallel()

	for _, registrar := range []string{"gandi", "Gandi", "GANDI", "", "desec", "gandi "} {
		check(t, "registrar "+registrar, one("example.org", registrar, set("www", "A", 3600, "192.0.2.1")))
	}

	check(t, "no zones", &zones.Config{})
	check(t, "zone without records", one("example.org", "gandi"))
	check(t, "two zones", &zones.Config{Zones: []zones.Zone{
		{Name: "example.org", Registrar: "gandi"}, {Name: "example.net", Registrar: "gandi"},
	}})
	check(t, "the same zone twice", &zones.Config{Zones: []zones.Zone{
		{Name: "example.org", Registrar: "gandi"}, {Name: "example.org", Registrar: "gandi"},
	}})
	check(t, "the same set twice", one("example.org", "gandi",
		set("www", "A", 3600, "192.0.2.1"), set("www", "A", 3600, "192.0.2.2")))
	check(t, "the same name, two types", one("example.org", "gandi",
		set("www", "A", 3600, "192.0.2.1"), set("www", "AAAA", 3600, "2001:db8::1")))
	check(t, "the same set in two zones", &zones.Config{Zones: []zones.Zone{
		{Name: "example.org", Registrar: "gandi", Records: []zones.RRset{set("www", "A", 3600, "192.0.2.1")}},
		{Name: "example.net", Registrar: "gandi", Records: []zones.RRset{set("www", "A", 3600, "192.0.2.1")}},
	}})
	check(t, "the key's separator in a subname", one("example.org", "gandi",
		set("www/A", "A", 3600, "192.0.2.1")))
}
