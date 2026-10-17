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
