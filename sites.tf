# One block per site. The content repository deploys itself: Cloudflare pulls
# it on push. What is declared here is that the site exists, where, and by
# what name; the matching CNAME is in zones.yaml.
module "godolint" {
  source = "./modules/static-site"

  account_id = var.cloudflare_account_id
  name       = "website-godolint"
  repository = "website-godolint"
  zone       = "forkcloser.org"
  subname    = "godolint"
}

module "taciturn" {
  source = "./modules/static-site"

  account_id = var.cloudflare_account_id
  name       = "website-taciturn"
  repository = "website-taciturn"
  zone       = "forkcloser.org"
  subname    = "taciturn"
  # The first project was created before the Pages App could see the
  # repository, and a connection is made at creation only.
  connection = 2
}

# The organisation's own site, at www: the zone is served by deSEC, which has
# no ALIAS record, and a CNAME cannot sit at the apex, so forkcloser.org
# itself points nowhere until the zone moves or a redirect host exists.
module "www" {
  source = "./modules/static-site"

  account_id = var.cloudflare_account_id
  name       = "website-forkcloser"
  repository = "website"
  zone       = "forkcloser.org"
  subname    = "www"
}
