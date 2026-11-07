# sc-plugin-gcp

The Simple Container plugin for Google Cloud Platform. Extracted from
in-tree `pkg/clouds/{gcloud,pulumi/gcp}` per the plugin-first policy
([`forge/docs/design/2026-10-24/sc-plugin-architecture/`](https://github.com/simple-container-com/forge/tree/main/docs/design/2026-10-24/sc-plugin-architecture)).
Still under extraction; in-tree ships in parallel until every GCP
resource + template has landed here. See
[`forge/docs/roadmap/items/sc-plugin-gcp-extraction.md`](https://github.com/simple-container-com/forge/blob/main/docs/roadmap/items/sc-plugin-gcp-extraction.md)
for phase state.

## Structure

- `/api` — CONFIG TIER. Separate Go module (SC core imports it).
  Pattern per `init.go`:
  - A `const ProviderType = "gcp"` plus per-token constants
    (`AuthTypeGCPServiceAccount`, `ResourceTypeBucket`,
    `ResourceTypePubSub`, `SecretsProviderTypeGcpKms`, etc.).
  - `init()` calls `api.RegisterProviderConfig(...)` to wire the config
    readers and `api.RegisterPluginRequirement(...)` so SC core's
    fool-protection auto-inject can find this plugin by any owned
    token.
  - **OwnedTypes must list every token this plugin owns.** Missing one
    is a silent gap — an operator who only uses the un-listed token
    gets no auto-inject and the deploy fails later with "unknown
    resource type". `TestSchema_OwnedTypesList_CoversAdvertisement` in
    `plugin/cmd/sc-plugin-gcp/main_test.go` guards this.

- `/plugin` — PROVISIONING TIER. Separate Go module.
  `cmd/sc-plugin-gcp/main.go` is the go-plugin gRPC server. Pattern:
  - `Schema()` advertises every owned token into the proto, plus
    `PulumiPlugins` (the SDKs this plugin links against) and
    `AuthTypes` (api #559 wire path). **Only `gcp-service-account`
    goes in `AuthTypes`** — any other token ends up with Kind=auth
    silently because `GetAuthTypes()` is the last loop in
    `registerSchemaTypes` and does an unconditional kind overwrite.
    `gcp-secrets-manager` + `gcp-kms` both go under
    `SecretsProviderTypes` per the AWS bootstrap pattern.
  - Every handler opens ONE Pulumi context per RPC via
    `runtime.NewContext(stdctx, header, runtime.WithLocalEngineFallback(p.progress))`
    and defers `runtime.Settle(rctx, &resp, &err)`. The handler's named
    return values must be `(resp *T, err error)` for Settle to fold
    them. Reusing a context across RPCs produces resources that cannot
    be addressed from the right monitor; skipping Settle lets Pulumi's
    teardown drop pending RegisterResource goroutines (trap 1).
  - **`params.Provider` must be built inline in every Provision
    handler.** Passing nil hits
    `Providers(nil).applyResourceOption → nil.getPackage()` SIGSEGV
    and the plugin process exits status 2, SC core sees `rpc error:
    EOF`. The `buildProvider` closure in `main.go`'s `Provision`
    handles this once and both bucket + pubsub paths call it; new
    resource types in future phases reuse the closure.
  - Provider resource URN follows SC core's own formula
    `<stack>--<type>--<name>--<env>--provider` — byte-identical to
    pre-split in-tree state. Get it wrong and a live stack reads as a
    provider replacement, which on a local.Command means every
    resource is replaced with it.
  - `runtime.HarvestExports(stdctx, rctx.Pulumi)` is how Provision gets
    its `Outputs` field. Any `rctx.Pulumi.Export(k, out)` call is what
    makes a `uses:` consumer able to read the value. Lose this and
    `uses:` returns "output not found" (traps 3 + 4).

- `/plugin/internal/bucket_test.go` + `pubsub_test.go` — URN name
  contract pins. Use `pulumi.NewContext` + `RunInfo.Mocks` for the
  cases that need a full engine; most tests exercise the code path
  that returns before any `RegisterResource` call, so no mock harness
  needed.

- `.github/workflows/release.yml` — CalVer `vYYYY.MM.DD-<sha>`
  auto-release on push to `main`, or named releases on `v*` tags.
  Three-platform build (linux amd64+arm64, darwin arm64) +
  `sha256sums.txt`. **Carries the OOM-safe pins**
  (`GOMAXPROCS=1 GOMEMLIMIT=6GiB`, `go build -p 1`). Do not undo —
  pulumi-gcp's SDK is ~500 MB.

## Release flow

**No PRs** — merge direct to `main`. Each push to `main` auto-cuts a
release. Operator convention 2026-11-07. Earlier phase commits landed
via PRs (#1, #2) which is historical, not pattern.

## Running tests

```
cd api    && go test ./...
cd plugin && go test ./...
```

Both must stay green on `main`. Preflight before every push:
`~/go/bin/gofumpt -w <files>` + `~/go/bin/gci write` with the repo's
6-section config (mirrors `forge/CLAUDE.md`).

## The traps this plugin inherits

See [TRAPS.md](TRAPS.md) for the fifteen-trap index from the shared
`sc-plugin-echo` template. Three worth calling out for GCP:

- **Trap 1 (defer-done cascade).** Every RPC uses
  `runtime.NewContext` + `defer runtime.Settle(...)`. Do not revert.
- **Trap 14 (compute collector loses env vars).** The compute
  processors (`BucketComputeProcessor`, `PubSubTopicsProcessor`)
  write through the shared `pApi.ComputeContextCollector` passed in;
  they never reimplement a local collector.
- **Trap 20 (`string` field with structured content, deploy-time
  refusal).** `api.Credentials.Credentials` is a `string` holding the
  service-account JSON document; every opaque-path decode of a GCP
  config inherits the hazard. See
  [`forge/docs/design/2026-11-01/plugin-extraction-playbook/`](https://github.com/simple-container-com/forge/tree/main/docs/design/2026-11-01/plugin-extraction-playbook/)
  Trap 20 for the full mechanism.

## Live-verified roundtrip

2026-11-06 against a disposable `forge-migration-smoke` GCP project:
plugin binary downloaded from GitHub Release via SC's fetcher, Schema
RPC answered, Provision dispatched, pulumi-gcp built the provider,
`sc provision --preview` planned seven resources end-to-end (bucket +
service account + HMAC key + two IAM bindings + provider + stack
root). Ephemeral service account deleted after the proof.

## Phase roadmap (summary)

- ✅ 1-3: scaffold + bucket lift + light tests + first GHA release +
  live preview roundtrip (`482e2e0`)
- ✅ 4a: `AuthTypes` advertisement + `gcp-secrets-manager` under
  `SecretsProviderTypes` + four Schema-invariant tests (`482e2e0`)
- ✅ 4b: `gcp-pubsub` resource + compute processor + service_account
  helper (`6e759e5`)
- ⬜ 4c: `gcp-redis` (~400 LOC)
- ⬜ 4d: `gcp-cloudsql-postgres` + adopt (~700 LOC)
- 🔒 4e-g: templates (cloudrun, static-website, gke-autopilot) —
  blocked on `sc-plugin-template-type-has-no-wire-path` roadmap item
- ⬜ 4h: `gcp-docker-image-push` + artifact-registry
- ⬜ 5: api PR removing `pkg/clouds/{gcloud,pulumi/gcp}` + forge pin
  bump
