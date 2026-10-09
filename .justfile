# This file is the project's own.
# Add recipes leveraging provided `do` ready-made recipes, or create your own.
# The import must be kept: it mounts every shared limen task under `just do ...`.
import '.limen/just/main.just'

# The FIRST recipe defined here becomes `just`'s default.
lint: do::lint::default do::lint::go::default fmt-check validate
fix: do::fix::default do::fix::go::default fmt
test: do::test::go::default validate
security: do::security::default do::security::go::default

# Every recipe below runs the tofu this repository pins through aqua, on the
# hermetic PATH main.just sets, never a host install; dnsctl is built from
# this module at the commit checked out.

# Initialise the backend. The endpoint carries the account id, so it comes
# from the environment rather than the tree; the rest is backend.hcl.
init:
    #!/usr/bin/env bash
    set -euo pipefail
    : "${CLOUDFLARE_ACCOUNT_ID:?set CLOUDFLARE_ACCOUNT_ID to the account that owns the state bucket}"
    tofu init -input=false -backend-config=backend.hcl \
        -backend-config="endpoints={s3=\"https://${CLOUDFLARE_ACCOUNT_ID}.r2.cloudflarestorage.com\"}"

# What an apply would change: the Cloudflare plan written to a file, then
# the DNS and registrar diff. dnsctl exits 2 when it would change something,
# so the recipe does too, the way tofu's -detailed-exitcode would.
plan: init dnsctl
    #!/usr/bin/env bash
    set -euo pipefail
    mkdir -p build
    tofu plan -input=false -out=build/tofu.plan
    build/dnsctl plan zones.yaml

# Apply takes only the saved Cloudflare plan, never a fresh one, so what
# was reviewed is what runs; then DNS and the registrar follow the file.
# Order matters once: the Pages custom domain validates against the CNAME,
# and Cloudflare retries that validation on its own.
apply: dnsctl
    #!/usr/bin/env bash
    set -euo pipefail
    test -f build/tofu.plan
    tofu apply -input=false build/tofu.plan
    build/dnsctl apply zones.yaml

dnsctl:
    mkdir -p build
    go build -o build/dnsctl ./cmd/dnsctl

# Validation needs the provider but not the backend or its credentials.
validate:
    tofu init -input=false -backend=false
    tofu validate

fmt:
    tofu fmt -recursive

fmt-check:
    tofu fmt -check -diff -recursive

# Record provider hashes for every platform CI and a laptop run on, so the
# lock file verifies the download wherever it happens.
lock:
    tofu providers lock -platform=linux_amd64 -platform=linux_arm64 -platform=darwin_arm64
