// Package zones reads the declarative zones file: every domain the
// organisation owns, the registrar it is held at, and every record set the
// zone carries. The file is the whole zone: a record set present in DNS and
// absent from the file is one to delete.
package zones

import (
	"fmt"
	"os"
	"regexp"
	"strings"

	"github.com/mycophonic/primordium/fault"
	"go.yaml.in/yaml/v3"
)

// RegistrarGandi is the one registrar the tool knows.
const RegistrarGandi = "gandi"

// Record set types the tool accepts. deSEC takes many more; these are the
// ones a static site needs, and each has a validation rule below.
const (
	TypeA     = "A"
	TypeAAAA  = "AAAA"
	TypeCNAME = "CNAME"
	TypeTXT   = "TXT"
	TypeMX    = "MX"
)

// MinimumTTL is deSEC's default floor for a record set's TTL, in seconds.
const MinimumTTL = 3600

// ErrInvalidZonesFile is the sentinel every validation failure wraps.
var ErrInvalidZonesFile = fault.ErrInvalidArgument

var (
	domainName = regexp.MustCompile(`^([a-z0-9](-*[a-z0-9])*\.)+[a-z]{2,}$`)
	subname    = regexp.MustCompile(`^(\*|[a-z0-9_](-*[a-z0-9_])*)(\.[a-z0-9_](-*[a-z0-9_])*)*$`)
)

// Config is the zones file.
type Config struct {
	Zones []Zone `yaml:"zones"`
}

// Zone is one domain: its registrar and the record sets it carries.
type Zone struct {
	Name      string  `yaml:"name"`
	Registrar string  `yaml:"registrar"`
	Records   []RRset `yaml:"records"`
}

// RRset is one record set: every record of one type at one name. The
// apex is the empty subname, as deSEC spells it.
type RRset struct {
	Subname string   `yaml:"subname"`
	Type    string   `yaml:"type"`
	TTL     int      `yaml:"ttl"`
	Records []string `yaml:"records"`
}

// Key identifies a record set within a zone.
func (r RRset) Key() string {
	return r.Subname + "/" + r.Type
}

// Load reads and validates the zones file at path.
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path) // #nosec G304 -- the zones file is the command's argument
	if err != nil {
		return nil, fmt.Errorf("%w: reading %s: %w", fault.ErrReadFailure, path, err)
	}

	var cfg Config

	dec := yaml.NewDecoder(strings.NewReader(string(data)))
	dec.KnownFields(true)

	if err := dec.Decode(&cfg); err != nil {
		return nil, fmt.Errorf("%w: parsing %s: %w", ErrInvalidZonesFile, path, err)
	}

	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}

	return &cfg, nil
}

// Validate checks the configuration for everything the APIs would reject
// late, or accept and serve wrong. Every failure wraps ErrInvalidZonesFile.
func (c *Config) Validate() error {
	if len(c.Zones) == 0 {
		return fmt.Errorf("%w: no zones declared", ErrInvalidZonesFile)
	}

	seen := make(map[string]struct{}, len(c.Zones))

	for i := range c.Zones {
		zone := &c.Zones[i]

		if err := zone.validate(); err != nil {
			return fmt.Errorf("zone %q: %w", zone.Name, err)
		}

		if _, dup := seen[zone.Name]; dup {
			return fmt.Errorf("%w: zone %q declared twice", ErrInvalidZonesFile, zone.Name)
		}

		seen[zone.Name] = struct{}{}
	}

	return nil
}

func (z *Zone) validate() error {
	if !domainName.MatchString(z.Name) {
		return fmt.Errorf("%w: %q is not a domain name (lowercase, no trailing dot)", ErrInvalidZonesFile, z.Name)
	}

	if z.Registrar != RegistrarGandi {
		return fmt.Errorf("%w: registrar %q is not %q", ErrInvalidZonesFile, z.Registrar, RegistrarGandi)
	}

	seen := make(map[string]struct{}, len(z.Records))

	for _, rrset := range z.Records {
		if err := rrset.validate(); err != nil {
			return fmt.Errorf("record set %s: %w", rrset.Key(), err)
		}

		if _, dup := seen[rrset.Key()]; dup {
			return fmt.Errorf("%w: record set %s declared twice", ErrInvalidZonesFile, rrset.Key())
		}

		seen[rrset.Key()] = struct{}{}
	}

	return nil
}

func (r RRset) validate() error {
	if r.Subname != "" && !subname.MatchString(r.Subname) {
		return fmt.Errorf("%w: subname %q is not a relative name (the apex is the empty string)",
			ErrInvalidZonesFile, r.Subname)
	}

	if r.TTL < MinimumTTL {
		return fmt.Errorf("%w: ttl %d is under deSEC's minimum of %d", ErrInvalidZonesFile, r.TTL, MinimumTTL)
	}

	if len(r.Records) == 0 {
		return fmt.Errorf("%w: no records (delete a record set by leaving it out of the file)", ErrInvalidZonesFile)
	}

	for _, rec := range r.Records {
		if err := validateRecord(r.Type, rec); err != nil {
			return err
		}
	}

	if r.Type == TypeCNAME {
		if len(r.Records) != 1 {
			return fmt.Errorf("%w: a CNAME record set holds exactly one record", ErrInvalidZonesFile)
		}

		if r.Subname == "" {
			return fmt.Errorf("%w: a CNAME cannot sit at the apex", ErrInvalidZonesFile)
		}
	}

	return nil
}

// validateRecord checks what deSEC would reject, and the one thing it would
// accept and serve wrong: a name without its trailing dot is relative to
// the zone, so "x.pages.dev" becomes x.pages.dev.forkcloser.org.
func validateRecord(typ, rec string) error {
	switch typ {
	case TypeCNAME:
		if !strings.HasSuffix(rec, ".") {
			return fmt.Errorf("%w: CNAME target %q must be absolute (trailing dot)", ErrInvalidZonesFile, rec)
		}
	case TypeMX:
		fields := strings.Fields(rec)
		if len(fields) != 2 || !strings.HasSuffix(fields[1], ".") {
			return fmt.Errorf("%w: MX record %q must be \"<priority> <absolute host.>\"", ErrInvalidZonesFile, rec)
		}
	case TypeA, TypeAAAA, TypeTXT:
		if rec == "" {
			return fmt.Errorf("%w: empty %s record", ErrInvalidZonesFile, typ)
		}
	default:
		return fmt.Errorf("%w: record type %q is not one this tool manages (%s)", ErrInvalidZonesFile, typ,
			strings.Join([]string{TypeA, TypeAAAA, TypeCNAME, TypeTXT, TypeMX}, ", "))
	}

	return nil
}
