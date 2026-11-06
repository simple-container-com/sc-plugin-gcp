// SPDX-License-Identifier: MIT
// Copyright (c) Simple Container

package main

import (
	"context"
	"testing"

	. "github.com/onsi/gomega"
	"github.com/simple-container-com/api/pkg/api"
	scpluginv1 "github.com/simple-container-com/api/pkg/plugin/proto/v1"

	gcpApi "github.com/simple-container-com/sc-plugin-gcp/api"
)

// TestSchema_AdvertisesCanonicalTokens pins the Schema advertisement
// token-by-token. Why this isn't a shape assertion: an operator-facing
// regression doesn't care about the shape of the response, it cares
// about whether `type: <token>` resolves — which is driven by WHICH
// Schema field a token lands in, not by whether the field exists.
func TestSchema_AdvertisesCanonicalTokens(t *testing.T) {
	RegisterTestingT(t)
	p := &gcpPlugin{}
	resp, err := p.Schema(context.Background(), &scpluginv1.SchemaRequest{})
	Expect(err).ToNot(HaveOccurred())

	Expect(resp.GetProviderTypes()).To(ConsistOf(gcpApi.ProviderType))
	Expect(resp.GetResourceTypes()).To(ContainElement(gcpApi.ResourceTypeBucket))
	Expect(resp.GetStateStoreTypes()).To(ConsistOf(gcpApi.StateStorageTypeGcpBucket))
	Expect(resp.GetSecretsProviderTypes()).To(ConsistOf(
		gcpApi.SecretsProviderTypeGcpKms,
		gcpApi.SecretsTypeGCPSecretsManager,
	))
	Expect(resp.GetAuthTypes()).To(ConsistOf(gcpApi.AuthTypeGCPServiceAccount))
}

// TestSchema_AuthTypes_OnlyGcpServiceAccount — guards the silent-kind-
// overwrite trap sc-ru-clouds session called out 2026-11-06. The
// registerSchemaTypes loop order (api/pkg/stackread/stackread.go:170-212)
// is: Resource → Registrar → StateStore → SecretsProvider →
// ComputeProcessor → VmDrivers → SafeToReplace → Auth. GetAuthTypes is
// the LAST loop, and RegisterPluginOwnedType does an unconditional
// kind overwrite (mapping.go:166 — `pluginOwnedTypes[name] = PluginOwnedBinding{...}`).
//
// So advertising any non-auth token under AuthTypes silently rewrites
// its kind to PluginOwnedKindAuth. `DetectSecretsType`/`DetectAuthProvider`
// both read `providerConfigMapping` (seeded by the earlier loop), so
// the parse still succeeds while the kind recorded against
// `IsPluginOwnedType` is WRONG. Downstream template-vs-resource
// routing in `Prepare*ForDeploy` and `plugin_converter.go:231-248`
// reads that kind — so the misroute manifests at deploy time, not
// at parse time, in a different process.
//
// The falsifiable check is on the token set, not on the kind
// (asserting kind would require spinning up stackread with a plugin
// binary, which is a bigger harness than this test should carry).
// The upstream assertion that stackread writes the right kind lives
// at api's TestRegisterSchemaTypes_AuthTypes_SeedsProviderConfigMapping
// (api PR #559's commit `aa504f87`).
func TestSchema_AuthTypes_OnlyGcpServiceAccount(t *testing.T) {
	RegisterTestingT(t)
	p := &gcpPlugin{}
	resp, err := p.Schema(context.Background(), &scpluginv1.SchemaRequest{})
	Expect(err).ToNot(HaveOccurred())

	// The whitelist. Any token appearing here that is NOT an auth
	// discriminator is a silent-kind-overwrite hazard — it would
	// shadow its own correct registration from an earlier loop.
	Expect(resp.GetAuthTypes()).To(HaveLen(1),
		"AuthTypes must contain exactly the auth discriminator — any other token ends up with Kind=auth silently (registerSchemaTypes GetAuthTypes loop is last)")

	// These three MUST NOT appear in AuthTypes — they belong in
	// SecretsProviderTypes / StateStoreTypes / ResourceTypes
	// respectively, and advertising them here would rewrite their
	// kind to PluginOwnedKindAuth.
	Expect(resp.GetAuthTypes()).ToNot(ContainElement(gcpApi.SecretsTypeGCPSecretsManager),
		"gcp-secrets-manager is a secrets-type, belongs in SecretsProviderTypes")
	Expect(resp.GetAuthTypes()).ToNot(ContainElement(gcpApi.SecretsProviderTypeGcpKms),
		"gcp-kms is a secrets-provider, belongs in SecretsProviderTypes")
	Expect(resp.GetAuthTypes()).ToNot(ContainElement(gcpApi.StateStorageTypeGcpBucket),
		"gcp-bucket is a state-store/resource, belongs in StateStoreTypes/ResourceTypes")
}

// TestSchema_OwnedTypesList_CoversAdvertisement — pins the invariant
// that api/init.go's RegisterPluginRequirement.OwnedTypes must list
// every token any Schema field advertises. Breaking this means
// fool-protection's auto-inject (ValidatePluginRequirements) doesn't
// cover a token this plugin actually owns — an operator who uses the
// un-listed token gets a hard "unknown type" rather than the
// paste-this-block hint from unknownTypeHint.
func TestSchema_OwnedTypesList_CoversAdvertisement(t *testing.T) {
	RegisterTestingT(t)
	p := &gcpPlugin{}
	resp, err := p.Schema(context.Background(), &scpluginv1.SchemaRequest{})
	Expect(err).ToNot(HaveOccurred())

	// Collect every token that Schema advertises.
	advertised := map[string]struct{}{}
	for _, t := range resp.GetProviderTypes() {
		advertised[t] = struct{}{}
	}
	for _, t := range resp.GetResourceTypes() {
		advertised[t] = struct{}{}
	}
	for _, t := range resp.GetStateStoreTypes() {
		advertised[t] = struct{}{}
	}
	for _, t := range resp.GetSecretsProviderTypes() {
		advertised[t] = struct{}{}
	}
	for _, t := range resp.GetAuthTypes() {
		advertised[t] = struct{}{}
	}

	// OwnedTypes list lives on PluginRequirement, pulled from the
	// RegisterPluginRequirement call that api/init.go made at Go-link
	// time. api.LookupPluginRequirement looks up by TOKEN, not by
	// plugin name, so pass any one owned token to find the requirement
	// and read the full OwnedTypes list from its result.
	req, ok := api.LookupPluginRequirement(gcpApi.ProviderType)
	Expect(ok).To(BeTrue(), "api/init.go must RegisterPluginRequirement with ProviderType in OwnedTypes")
	Expect(req.PluginName).To(Equal("gcp"))

	ownedSet := map[string]struct{}{}
	for _, t := range req.OwnedTypes {
		ownedSet[t] = struct{}{}
	}

	for token := range advertised {
		Expect(ownedSet).To(HaveKey(token),
			"Schema advertises %q but api/init.go OwnedTypes doesn't list it — fool-protection auto-inject won't cover it", token)
	}
}
