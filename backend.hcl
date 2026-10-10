# Partial backend configuration for `tofu init -backend-config=backend.hcl`.
# R2 speaks S3 without the AWS account machinery, hence the skips; the
# endpoint, which carries the account id, comes from `just init`. Locking is
# the bucket's own conditional write (use_lockfile): there is no DynamoDB.
bucket                      = "forkcloser-tofu-state"
key                         = "forkcloser.tfstate"
region                      = "auto"
use_lockfile                = true
skip_credentials_validation = true
skip_region_validation      = true
skip_requesting_account_id  = true
skip_metadata_api_check     = true
skip_s3_checksum            = true
