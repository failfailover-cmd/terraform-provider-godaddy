# Distribution

## Release process

1. Merge changes to `main`.
2. Create and push a semver tag, e.g. `v0.1.0`.
3. GitHub Actions `release.yml` builds binaries and publishes GitHub Release assets.
4. Assets include platform zips, checksums, and signed checksums.

## Required secrets

- `GPG_PRIVATE_KEY`
- `PASSPHRASE`

## Local smoke checks

```bash
go test ./...
go build ./...
```
