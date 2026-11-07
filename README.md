# sc-plugin-gcp

The Simple Container plugin for Google Cloud Platform, extracted from
`pkg/clouds/{gcloud,pulumi/gcp}` of
[simple-container-com/api](https://github.com/simple-container-com/api) per
the plugin-first policy in
[`forge/docs/design/2026-10-24/sc-plugin-architecture/`](https://github.com/simple-container-com/forge/tree/main/docs/design/2026-10-24/sc-plugin-architecture).

**Status: walking-skeleton, under extraction.** Not yet the authoritative
source — in-tree `pkg/clouds/{gcloud,pulumi/gcp}` still ships with SC
core until every GCP resource + template has landed here. See
[`forge/docs/roadmap/items/sc-plugin-gcp-extraction.md`](https://github.com/simple-container-com/forge/blob/main/docs/roadmap/items/sc-plugin-gcp-extraction.md)
for phase progress.

## Shipped today

| Token | Kind | Phase |
|---|---|---|
| `gcp` | provider | 1 |
| `gcp-service-account` | auth | 4a |
| `gcp-bucket` | resource + state-store + compute processor | 2 |
| `gcp-kms` | secrets-provider | 1 |
| `gcp-secrets-manager` | secrets-provider (runtime secret injection) | 4a |
| `gcp-pubsub` | resource + compute processor | 4b |

## Not shipped yet

- `gcp-redis`, `gcp-cloudsql-postgres`, `gcp-docker-image-push`,
  `gcp-gke-autopilot-cluster` — resource lifts queued (phases 4c-4h).
- `cloudrun`, `gcp-static-website`, `gcp-gke-autopilot` — template lifts.
  Blocked on
  [`forge/docs/roadmap/items/sc-plugin-template-type-has-no-wire-path.md`](https://github.com/simple-container-com/forge/blob/main/docs/roadmap/items/sc-plugin-template-type-has-no-wire-path.md)
  — `SchemaResponse` has no `template_types` field yet.
- Adoption path for existing buckets (`adopt: true`) — refused with a
  loud error until lifted.

## Release cadence

Releases cut on every merge to `main` via GitHub Actions
(`.github/workflows/release.yml`). CalVer tags of the shape
`vYYYY.MM.DD-<short-sha>` and SemVer-looking tags from manual pushes
both work; the release workflow emits four assets per cut: three
binaries (`linux-amd64`, `linux-arm64`, `darwin-arm64`) and a
`sha256sums.txt`.

Operators pin a release in their stack's `plugins:` block:

```yaml
plugins:
  gcp:
    source:
      type: github-release
      repository: simple-container-com/sc-plugin-gcp
      version: v2026.11.07-482e2e0
```

See [`docs/docs/guides/upgrading-to-plugins.md`](https://github.com/simple-container-com/api/blob/main/docs/docs/guides/upgrading-to-plugins.md)
in api for the operator-facing pin workflow.

## Repo layout

Two Go modules:

- `api/` — the config tier. Lifted types + constants + `init()`
  registrations SC core reads at parse time. Lightweight (no
  pulumi-gcp SDK). SC core depends on this module via its own `go.mod`.
- `plugin/` — the binary + provisioning tier. Imports pulumi-gcp SDK
  (~500 MB in the module cache). Not imported by SC core; invoked as a
  subprocess over the SC plugin bus.

The two-module split is why extracting a plugin is worth the work: an
SC core consumer no longer transitively pulls pulumi-gcp when it only
needs the type discriminators.

## Trap checklist baked in

See [`TRAPS.md`](TRAPS.md) for the fifteen-trap index inherited from
the shared `sc-plugin-echo` template. Three worth calling out for this
plugin specifically:

- **Trap 1 (defer-done cascade).** Every RPC uses
  `runtime.NewContext(stdctx, header, …)` + `defer runtime.Settle(...)`
  rather than `pctx.Close()`. Do not revert.
- **Trap 14 (compute collector loses env vars).** The compute
  processors write through the shared `pApi.ComputeContextCollector`
  passed in; they never reimplement a local collector.
- **Trap 20 (`string` field with structured content, deploy-time
  refusal).** `api.Credentials.Credentials` is a `string` holding the
  service-account JSON document; every opaque-path decode of a GCP
  config inherits the hazard. See
  [`forge/docs/design/2026-11-01/plugin-extraction-playbook/`](https://github.com/simple-container-com/forge/tree/main/docs/design/2026-11-01/plugin-extraction-playbook/)
  Trap 20.

## Development

```bash
cd api    && go test ./...      # config tier tests
cd plugin && go test ./...      # provisioning tier tests
cd plugin && go build ./...     # binary
```

Pulumi-gcp SDK version is pinned in `plugin/go.mod` and must match the
version advertised in `Schema.PulumiPlugins` — a test guards this
(see `plugin/cmd/sc-plugin-gcp/main_test.go:TestSchema_PulumiPluginsVersion_MatchesGoMod`).

Live `sc preview` roundtrip verified 2026-11-06 against a disposable
GCP project (`forge-migration-smoke`): plugin binary downloaded from
GitHub Release via SC's fetcher, Schema RPC answered, Provision
dispatched, pulumi-gcp built the provider, preview planned seven
resources end-to-end. See roadmap item for the full trace.
