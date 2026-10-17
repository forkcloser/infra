locals {
  hostname   = "${var.subname}.${var.zone}"
  connection = tostring(var.connection)
}

# The project pulls the repository on push: production from the production
# branch, a preview for every other branch. No build: the directory is
# served as committed. The GitHub side of the integration is the Cloudflare
# Pages App installed on the organisation, which no API creates, and the
# App must see the repository before this is applied: the connection is
# made at creation and never again, so the resources are keyed by the
# connection generation, and a bump replaces them instead of editing one.
resource "cloudflare_pages_project" "site" {
  for_each = toset([local.connection])

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
# with .pages.dev, so neither side waits on the other. Keyed like the
# project: Cloudflare drops the domain with the project it belongs to.
resource "cloudflare_pages_domain" "site" {
  for_each = toset([local.connection])

  account_id   = var.account_id
  project_name = cloudflare_pages_project.site[each.key].name
  name         = local.hostname
}

# The first generation was unkeyed; the state follows without a replacement.
moved {
  from = cloudflare_pages_project.site
  to   = cloudflare_pages_project.site["1"]
}

moved {
  from = cloudflare_pages_domain.site
  to   = cloudflare_pages_domain.site["1"]
}
