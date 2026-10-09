variable "cloudflare_account_id" {
  description = "The Cloudflare account that owns the Pages projects and the state bucket. TF_VAR_cloudflare_account_id."
  type        = string
  nullable    = false
}

variable "state_passphrase" {
  description = "Passphrase the state encryption key is derived from. TF_VAR_state_passphrase; at least 16 characters."
  type        = string
  sensitive   = true
  nullable    = false

  validation {
    condition     = length(var.state_passphrase) >= 16
    error_message = "The state passphrase must be at least 16 characters."
  }
}
