terraform {
  required_version = "1.13.1"

  required_providers {
    cloudflare = {
      source  = "cloudflare/cloudflare"
      version = "5.27.0"
    }
  }

  # The state lives in an R2 bucket through the S3 backend. The bucket, key
  # and the flags R2 needs are in backend.hcl; the endpoint carries the
  # account id and is passed by `just init` from the environment, so no
  # account identifier is written into the tree.
  backend "s3" {}

  # State carries every resource attribute the provider returns, some of
  # them secrets, so the object in the bucket is ciphertext: AES-GCM under a
  # key derived from a passphrase the environment supplies. `enforced` makes
  # OpenTofu refuse an unencrypted state or plan rather than read it.
  encryption {
    key_provider "pbkdf2" "state" {
      passphrase = var.state_passphrase
    }
    method "aes_gcm" "state" {
      keys = key_provider.pbkdf2.state
    }
    state {
      method   = method.aes_gcm.state
      enforced = true
    }
    plan {
      method   = method.aes_gcm.state
      enforced = true
    }
  }
}
