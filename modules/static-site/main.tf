locals {
  hostname = "${var.subname}.${var.zone}"
}

# The project pulls the repository on push: production from the production
# branch, a preview for every other branch. No build: the directory is
# served as committed. The GitHub side of the integration is the Cloudflare
# Pages App installed on the organisation, which no API creates.
resource "cloudflare_pages_project" "site" {
  account_id        = var.account_id
  name              = var.name
  production_branch = var.production_branch

  source = {
    type = "github"
    config = {
      owner                          = var.owner
      repo_name                      = var.repository
      production_branch              = var.production_branch
      production_deployments_enabled = true
      preview_deployment_setting     = "all"
      pr_comments_enabled            = false
    }
  }

  build_config = {
    build_command   = ""
    destination_dir = var.site_dir
    root_dir        = ""
  }
}

# The custom domain needs the CNAME in the zone to validate; that record is
# zones.yaml's, written by dnsctl, and its target is this project's name
# with .pages.dev, so neither side waits on the other.
resource "cloudflare_pages_domain" "site" {
  account_id   = var.account_id
  project_name = cloudflare_pages_project.site.name
  name         = local.hostname
}
