# The OpenTofu this module is written for; see the root versions.tf.
language {
  compatible_with {
    opentofu = "1.13.2"
  }
}

terraform {
  required_providers {
    cloudflare = {
      source  = "cloudflare/cloudflare"
      version = "5.28.1"
    }
  }
}
