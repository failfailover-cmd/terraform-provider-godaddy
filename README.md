# terraform-provider-godaddy

Минимальный Terraform provider для переключения NS в GoDaddy.

Ресурс:

- `godaddy_domain_record`

## Provider

```hcl
provider "godaddy" {
  key    = var.godaddy_api_key
  secret = var.godaddy_api_secret
  # optional
  # base_url           = "https://api.godaddy.com/v1"
  # request_timeout_ms = 30000
  # max_retries        = 4
  # base_backoff_ms    = 300
  # max_backoff_ms     = 5000
}
```

## Resource

```hcl
resource "godaddy_domain_record" "ns" {
  domain = "example.com"
  nameservers = [
    "jake.ns.cloudflare.com",
    "lisa.ns.cloudflare.com",
  ]
}
```

## Импорт

```bash
terraform import godaddy_domain_record.ns example.com
```

