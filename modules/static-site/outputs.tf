output "hostname" {
  description = "The site's public hostname."
  value       = local.hostname
}

output "pages_hostname" {
  description = "The project's own pages.dev hostname, the CNAME target."
  value       = cloudflare_pages_project.site[local.connection].subdomain
}
