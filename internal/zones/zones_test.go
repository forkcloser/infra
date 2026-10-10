package zones_test

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/forkcloser/infra/internal/zones"
)

func write(t *testing.T, content string) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "zones.yaml")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}

	return path
}

const valid = `
zones:
  - name: example.org
    registrar: gandi
    records:
      - subname: www
        type: CNAME
        ttl: 3600
        records: ["site.pages.dev."]
      - subname: ""
        type: MX
        ttl: 3600
        records: ["10 mail.example.org."]
`

func TestLoadValid(t *testing.T) {
	t.Parallel()

	cfg, err := zones.Load(write(t, valid))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if len(cfg.Zones) != 1 || cfg.Zones[0].Name != "example.org" || len(cfg.Zones[0].Records) != 2 {
		t.Fatalf("unexpected config: %+v", cfg)
	}

	if got := cfg.Zones[0].Records[0].Key(); got != "www/CNAME" {
		t.Fatalf("Key = %q", got)
	}
}

func TestLoadRejects(t *testing.T) {
	t.Parallel()

	cases := map[string]string{
		"unknown field": `
zones:
  - name: example.org
    registrar: gandi
    nameservers: [a]
`,
		"no zones": `zones: []`,
		"uppercase domain": `
zones:
  - name: Example.org
    registrar: gandi
`,
		"unknown registrar": `
zones:
  - name: example.org
    registrar: other
`,
		"relative CNAME": `
zones:
  - name: example.org
    registrar: gandi
    records:
      - {subname: www, type: CNAME, ttl: 3600, records: ["site.pages.dev"]}
`,
		"CNAME at apex": `
zones:
  - name: example.org
    registrar: gandi
    records:
      - {subname: "", type: CNAME, ttl: 3600, records: ["site.pages.dev."]}
`,
		"two CNAME records": `
zones:
  - name: example.org
    registrar: gandi
    records:
      - {subname: www, type: CNAME, ttl: 3600, records: ["a.", "b."]}
`,
		"ttl under minimum": `
zones:
  - name: example.org
    registrar: gandi
    records:
      - {subname: www, type: A, ttl: 60, records: ["192.0.2.1"]}
`,
		"empty records": `
zones:
  - name: example.org
    registrar: gandi
    records:
      - {subname: www, type: A, ttl: 3600, records: []}
`,
		"duplicate record set": `
zones:
  - name: example.org
    registrar: gandi
    records:
      - {subname: www, type: A, ttl: 3600, records: ["192.0.2.1"]}
      - {subname: www, type: A, ttl: 3600, records: ["192.0.2.2"]}
`,
		"unmanaged type": `
zones:
  - name: example.org
    registrar: gandi
    records:
      - {subname: www, type: SRV, ttl: 3600, records: ["0 0 443 a."]}
`,
		"MX without priority": `
zones:
  - name: example.org
    registrar: gandi
    records:
      - {subname: "", type: MX, ttl: 3600, records: ["mail.example.org."]}
`,
		"duplicate zone": `
zones:
  - name: example.org
    registrar: gandi
  - name: example.org
    registrar: gandi
`,
	}

	for name, content := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			_, err := zones.Load(write(t, content))
			if !errors.Is(err, zones.ErrInvalidZonesFile) {
				t.Fatalf("err = %v, want ErrInvalidZonesFile", err)
			}
		})
	}
}

func TestLoadMissingFile(t *testing.T) {
	t.Parallel()

	_, err := zones.Load(filepath.Join(t.TempDir(), "absent.yaml"))
	if err == nil {
		t.Fatal("expected an error for a missing file")
	}
}
