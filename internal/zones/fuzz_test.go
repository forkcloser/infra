package zones_test

// Past the bounded shapes: any bytes as the zones file. Load never panics,
// every refusal wraps ErrInvalidZonesFile, and what Load accepts, Validate
// accepts again and the rules' model agrees with.

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/forkcloser/infra/internal/zones"
)

func FuzzLoad(f *testing.F) {
	f.Add(valid)
	f.Add("zones: []")
	f.Add("zones:\n  - name: example.org\n    registrar: gandi\n")
	f.Add(
		"zones:\n  - name: example.org\n    registrar: gandi\n    records:\n      - {subname: www, type: CNAME, ttl: 3600, records: [\"a.\"]}\n",
	)
	f.Add(
		"zones:\n  - name: example.org\n    registrar: gandi\n    records:\n      - {subname: \"\", type: MX, ttl: 3600, records: [\"10 m.\"]}\n",
	)
	f.Add("")
	f.Add("- not a mapping")
	f.Add("zones: {a: b}")
	f.Add("zones:\n  - name: &a example.org\n    registrar: gandi\n  - name: *a\n    registrar: gandi\n")

	f.Fuzz(func(t *testing.T, content string) {
		path := filepath.Join(t.TempDir(), "zones.yaml")
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}

		cfg, err := zones.Load(path)
		if err != nil {
			if !errors.Is(err, zones.ErrInvalidZonesFile) {
				t.Fatalf("a refusal that is not ErrInvalidZonesFile: %v", err)
			}

			return
		}

		if again := cfg.Validate(); again != nil {
			t.Fatalf("Load accepted what Validate refuses: %v", again)
		}

		if !(ruleModel{}).config(cfg) {
			t.Fatalf("Load accepted what the rules refuse: %+v", cfg)
		}
	})
}
