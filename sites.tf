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
