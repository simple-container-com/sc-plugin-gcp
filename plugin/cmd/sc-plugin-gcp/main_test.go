// SPDX-License-Identifier: MIT
// Copyright (c) Simple Container

package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/mod/modfile"

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

	// Guard against vacuous pass: a Schema with no advertisement at
	// all would make the loop below run zero iterations and the test
	// go green without proving anything. sc-ru-clouds session's
	// 2026-11-06 mutation-check recommendation — "a Schema test that
	// passes because the loop body never executes is easy to write."
	Expect(advertised).ToNot(BeEmpty(),
		"Schema advertises no tokens at all — this test would pass vacuously")

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

// TestSchema_PulumiPluginsVersion_MatchesGoMod — guards a live defect
// class sc-ru-clouds session surfaced 2026-11-06: a plugin that
// advertises a `PulumiPlugins` version drifting from its own `go.mod`
// has core *prefetch* one version while every resource the driver
// registers asks for the version baked into the SDK it compiled
// against — a two-version deploy. Live current instance:
// `sc-plugin-cloudru` advertises `tls 5.3.0` in Schema while its
// `plugin/go.mod` pins pulumi-tls at v5.5.0.
//
// Test parses the sibling `go.mod` with `golang.org/x/mod/modfile`,
// maps each `pulumi-<package>` require to the advertised
// `PulumiPluginRequirement{Package, Version}`, and fails naming both
// versions on a mismatch. The upstream check belongs in the plugin
// release workflow eventually; this test is the plugin-local half.
func TestSchema_PulumiPluginsVersion_MatchesGoMod(t *testing.T) {
	RegisterTestingT(t)

	// Parse plugin/go.mod relative to this test's package. The test
	// binary is run from `plugin/cmd/sc-plugin-gcp/`, so go.mod is
	// two levels up.
	goModPath := filepath.Join("..", "..", "go.mod")
	raw, err := os.ReadFile(goModPath)
	Expect(err).ToNot(HaveOccurred(), "read %s", goModPath)
	parsed, err := modfile.Parse(goModPath, raw, nil)
	Expect(err).ToNot(HaveOccurred())

	// Map pulumi-<pkg>/sdk/... → version from go.mod requires.
	modVersions := map[string]string{}
	for _, r := range parsed.Require {
		mp := r.Mod.Path
		if !strings.HasPrefix(mp, "github.com/pulumi/pulumi-") {
			continue
		}
		// pkg name is the segment between `pulumi-` and the next `/`.
		rest := strings.TrimPrefix(mp, "github.com/pulumi/pulumi-")
		pkg := rest
		if idx := strings.Index(rest, "/"); idx >= 0 {
			pkg = rest[:idx]
		}
		// Version from go.mod is `vX.Y.Z` — strip the `v` to match
		// scpluginv1.PulumiPluginRequirement.Version shape.
		modVersions[pkg] = strings.TrimPrefix(r.Mod.Version, "v")
	}
	Expect(modVersions).ToNot(BeEmpty(),
		"go.mod has no pulumi-* requires — a Schema test asserting plugin versions is vacuous")

	p := &gcpPlugin{}
	resp, err := p.Schema(context.Background(), &scpluginv1.SchemaRequest{})
	Expect(err).ToNot(HaveOccurred())

	// Every advertised PulumiPlugin must have a matching go.mod version.
	// A go.mod require NOT advertised in Schema is fine (an internal
	// dep that no provider resource surfaces); the reverse is the bug.
	pulumiPlugins := resp.GetPulumiPlugins()
	Expect(pulumiPlugins).ToNot(BeEmpty(),
		"Schema advertises no PulumiPlugins — this test passes vacuously")

	for _, pp := range pulumiPlugins {
		modVer, ok := modVersions[pp.GetPackage()]
		Expect(ok).To(BeTrue(),
			"Schema advertises pulumi-%s but go.mod has no github.com/pulumi/pulumi-%s require — core will download a Pulumi plugin the driver cannot talk to",
			pp.GetPackage(), pp.GetPackage())
		Expect(pp.GetVersion()).To(Equal(modVer),
			"pulumi-%s drift: Schema advertises %s, go.mod pins %s — core prefetches one version while the driver's SDK demands the other (cloudru-tls shape, 2026-11-06)",
			pp.GetPackage(), pp.GetVersion(), modVer)
	}
}
