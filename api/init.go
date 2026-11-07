// SPDX-License-Identifier: MIT
// Copyright (c) Simple Container

// Package gcp is the CONFIG TIER of sc-plugin-gcp.
//
// This package is what SC core imports at parse time — it carries
// type-token constants, config structs for the YAML operators write, and
// the init()-time registrations for the parse-time config reader map and
// the auto-plugin-inject fool-protection.
//
// Published as its OWN Go module (api/go.mod). The plugin binary module
// (../plugin/go.mod) depends on it; SC core also depends on it. Shipping
// config + provisioning in one module would make every SC core consumer
// transitively pull every cloud SDK the plugin uses (pulumi-gcp is
// ~500 MB in the module cache).
//
// Lifted from in-tree `pkg/clouds/gcloud` of the api repo.
// Mapping doc: forge/docs/roadmap/items/sc-plugin-gcp-extraction.md.
// Full walkthrough: api/docs/docs/guides/plugin-authoring.md.
package gcp

import (
	"github.com/simple-container-com/api/pkg/api"
)

// ProviderType is the top-level discriminator every gcp config reports
// from ProviderType(). Preserved as "gcp" for byte-identical URN
// continuity with the in-tree driver (playbook §trap-9).
const ProviderType = "gcp"

// Walking-skeleton init: register the type tokens shipped in this
// commit. The rest of the gcloud type tokens (pubsub, redis, cloudsql,
// gke-autopilot, cloudrun, static-website) land in later commits as
// their lifts complete. OwnedTypes below carries the FULL set so the
// fool-protection auto-injects a `plugins.gcp:` entry the moment an
// operator references ANY GCP token — otherwise an operator using
// only an un-landed token gets "unknown resource type" on upgrade.
func init() {
	api.RegisterProviderConfig(api.ConfigRegisterMap{
		AuthTypeGCPServiceAccount:    ReadAuthServiceAccountConfig,
		SecretsTypeGCPSecretsManager: ReadSecretsProviderConfig,
		ResourceTypeBucket:           GcpBucketReadConfig,
		ResourceTypePubSub:           GcpPubSubTopicsReadConfig,
	})

	// Fool-protection: SC core's parse-time validator auto-injects
	// `plugins.gcp:` with the latest release from GitHub when it finds
	// an un-declared OwnedTypes token in server.yaml. Without this
	// registration an operator upgrading past the in-tree-drop release
	// of SC would see a hard parse error. OwnedTypes must list EVERY
	// token this plugin owns — missing a token here is a silent gap:
	// an operator who only uses the unlisted token gets no auto-inject
	// and the deploy fails later with "unknown resource type" (see
	// Addendum J of 04-no-destroy-invariant.md).
	api.RegisterPluginRequirement(api.PluginRequirement{
		PluginName: "gcp",
		OwnedTypes: []string{
			ProviderType,
			AuthTypeGCPServiceAccount,
			SecretsTypeGCPSecretsManager,
			StateStorageTypeGcpBucket,
			SecretsProviderTypeGcpKms,
			ResourceTypeBucket,
			ResourceTypePubSub,
			// Future lifts will add:
			//   "gcp-redis", "gcp-cloudsql-postgres",
			//   "gcp-gke-autopilot-cluster", "cloudrun",
			//   "gcp-static-website", "gcp-gke-autopilot",
			//   "gcp-docker-image-push"
		},
		DefaultSource: api.PluginSource{
			Type:       "github-release",
			Repository: "simple-container-com/sc-plugin-gcp",
		},
	})
}
