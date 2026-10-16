# The OpenTofu this configuration is written for, stated the OpenTofu way:
# a top-level language block, which OpenTofu 1.13 enforces ("Incompatible
# module" on a mismatch). The binary itself is the aqua pin in .aqua/aqua.yaml,
# the same version. `terraform { required_version }` is not used: OpenTofu
# keeps it only as a Terraform compatibility statement and does not hold
# itself to it, and Renovate reads it as a Terraform version to bump.
language {
  compatible_with {
    opentofu = "1.13.1"
  }
}

terraform {
  required_providers {
    cloudflare = {
      source  = "cloudflare/cloudflare"
      version = "5.28.0"
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
