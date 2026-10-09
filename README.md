# infra

The infrastructure behind forkcloser's websites, as code. One zone per domain the
organisation owns, one block per site; a pull request adds a site, and the apply on `main`
creates it. Nothing is configured by hand except the two things no API offers: the
Cloudflare Pages GitHub App installed on the organisation, once, and the accounts and
tokens themselves.

Two tools do the work, and both are run by the same recipes. OpenTofu holds what lives at
Cloudflare: the Pages project pulling a site's repository, and its custom domain. `dnsctl`,
a Go program in this repository, holds DNS and the registrar: it makes deSEC and Gandi
match `zones.yaml`, the zone, its record sets, the delegation, and the DNSSEC key published
at the registrar. No third-party provider stands between us and either API; the Cloudflare
provider is Cloudflare's own.

This is the pilot. The `static-site` module lives here as a local directory while godolint
goes through it; once it stops moving it is extracted to a module repository referenced by
tag, and the recipes below to limen, leaving this repository its declarations.

## Layout

| Path | What |
|------|------|
| `zones.yaml` | Every domain and every record set. The file is the whole zone: a record set in DNS and not here is deleted on apply. |
| `cmd/dnsctl`, `internal/` | The DNS and registrar tool: `desec` and `gandi` are the two API clients, `zones` reads and validates the file, `reconcile` computes and applies the diff. |
| `versions.tf` | The OpenTofu and provider pins, the S3 backend on R2, and state encryption. |
| `backend.hcl` | The backend's bucket, key and R2 flags. The endpoint, which carries the account id, comes from the environment at `just init`. |
| `sites.tf` | One `static-site` module block per site. |
| `modules/static-site` | A Cloudflare Pages project pulling a GitHub repository on push, and its custom domain. |
| `.github/workflows/infra.yaml` | Plan on every pull request; plan and apply on `main`. |

## Working on it

```bash
just lint      # the shared limen baseline and its Go lanes, plus tofu fmt and validate
just test      # the Go tests, and validate with the provider and without the backend
just plan      # the Cloudflare plan to build/tofu.plan, then the DNS and registrar diff
just apply     # apply build/tofu.plan, and only that; then DNS and the registrar
just lock      # refresh provider hashes for every platform in the lock file
```

Plan and apply read every credential from the environment and nothing from the tree:

| Variable | What |
|----------|------|
| `CLOUDFLARE_ACCOUNT_ID`, `TF_VAR_cloudflare_account_id` | The account that owns the Pages projects and the state bucket. |
| `CLOUDFLARE_API_TOKEN` | Pages read and write on that account, nothing else. |
| `DESEC_API_TOKEN` | A deSEC token that may write into this organisation's zones and may not create any. |
| `DESEC_CREATE_TOKEN` | A deSEC token whose only right is to create domains. Read only when a zone in the file does not exist yet; the plan refuses up front if it is needed and absent. |
| `GANDI_PERSONAL_ACCESS_TOKEN` | A Gandi personal access token with domain management on this organisation's domains, nothing else. |
| `AWS_ACCESS_KEY_ID`, `AWS_SECRET_ACCESS_KEY` | An R2 token scoped to the state bucket. |
| `TF_VAR_state_passphrase` | The passphrase the state encryption key derives from. |

The state is ciphertext in the bucket and the plan file is ciphertext on disk; OpenTofu is
set to refuse either unencrypted. Locking is the bucket's own conditional write.

`dnsctl` keeps no state: the file and the two services are compared on every run. Two deSEC
tokens keep creation apart from routine writes: the creation token is used for one call,
creating a zone, and nothing else ever carries that right. `plan`
exits 2 when something would change, 0 when nothing would. Apply runs in the order the
DNSSEC chain needs: the zone, its record sets, the delegation, and only then the key at the
registrar, so a resolver never sees a signed delegation to a zone still served unsigned. A
zone created in a run has its key published in the same run; deSEC returns the keys on
creation.

## Adding a site

One block in `sites.tf`:

```hcl
module "example" {
  source = "./modules/static-site"

  account_id = var.cloudflare_account_id
  name       = "website-example"   # the Pages project, and its pages.dev hostname
  repository = "website-example"   # pulled from github.com/forkcloser on push
  zone       = "forkcloser.org"
  subname    = "example"           # example.forkcloser.org
}
```

And one record set in `zones.yaml`, the CNAME to the project's pages.dev name, absolute:

```yaml
      - subname: example
        type: CNAME
        ttl: 3600
        records: ["website-example.pages.dev."]
```

The content repository needs nothing from this one: Cloudflare pulls it on every push,
production from `main`, a preview per branch, serving its `site/` directory as committed.

## Adding a domain

One zone in `zones.yaml`, registrar `gandi`. The first apply creates the zone at deSEC,
writes its record sets, points the delegation at deSEC's nameservers, and publishes the
zone's signing key at Gandi.
