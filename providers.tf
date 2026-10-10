# The provider reads its token from the environment, CLOUDFLARE_API_TOKEN.
# DNS and the registrar are not providers: dnsctl, in this repository,
# makes deSEC and Gandi match zones.yaml.
provider "cloudflare" {}
