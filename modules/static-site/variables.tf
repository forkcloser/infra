variable "account_id" {
  description = "The Cloudflare account the Pages project belongs to."
  type        = string
  nullable    = false
}

variable "name" {
  description = "The Pages project name; also its pages.dev hostname. Lowercase letters, digits and hyphens."
  type        = string
  nullable    = false

  validation {
    condition     = can(regex("^[a-z0-9][a-z0-9-]*$", var.name))
    error_message = "A Pages project name begins with a lowercase letter or digit and contains only lowercase letters, digits and hyphens."
  }
}

variable "owner" {
  description = "The GitHub organisation that owns the content repository."
  type        = string
  default     = "forkcloser"
}

variable "repository" {
  description = "The content repository under the owner. Cloudflare pulls it on push."
  type        = string
  nullable    = false
}

variable "production_branch" {
  description = "The branch a push to which deploys the site."
  type        = string
  default     = "main"
}

variable "site_dir" {
  description = "The directory in the repository served as the site. No build runs."
  type        = string
  default     = "site"
}

variable "zone" {
  description = "The deSEC zone the site's record is written to."
  type        = string
  nullable    = false
}

variable "subname" {
  description = "The site's name under the zone; the hostname is subname.zone."
  type        = string
  nullable    = false
}
