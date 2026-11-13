// SPDX-License-Identifier: MIT
// Copyright (c) Simple Container

package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/mod/modfile"

	. "github.com/onsi/gomega"
	"github.com/simple-container-com/api/pkg/api"
	"github.com/simple-container-com/api/pkg/clouds/k8s"
	pApi "github.com/simple-container-com/api/pkg/clouds/pulumi/api"
	scPlugin "github.com/simple-container-com/api/pkg/plugin"
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
	Expect(resp.GetResourceTypes()).To(ConsistOf(
		gcpApi.ResourceTypeBucket, gcpApi.ResourceTypePubSub, gcpApi.ResourceTypeRedis,
		gcpApi.ResourceTypePostgresGcpCloudsql,
		gcpApi.ResourceTypeArtifactRegistry, gcpApi.ResourceTypeRemoteDockerImagePush,
		gcpApi.ResourceTypeGkeAutopilot,
		// The secrets-provider key is provisioned through Provision.
		gcpApi.SecretsProviderTypeGcpKms,
		// Template types fold in — see stackread.go:165-170.
		gcpApi.TemplateTypeGcpCloudrun,
		gcpApi.TemplateTypeStaticWebsite,
		gcpApi.TemplateTypeGkeAutopilot,
	))
	// ComputeProcessorTypes is deliberately SMALLER than ResourceTypes:
	// artifact-registry + docker-image-push have no in-tree compute
	// processor (consumers don't `uses:` them for env-var injection);
	// cloudrun + gcp-static-website + gcp-gke-autopilot are template
	// types, consumed by their own Provision paths, not injected via
	// `uses:`.
	Expect(resp.GetComputeProcessorTypes()).To(ConsistOf(
		gcpApi.ResourceTypeBucket, gcpApi.ResourceTypePubSub, gcpApi.ResourceTypeRedis,
		gcpApi.ResourceTypePostgresGcpCloudsql,
		gcpApi.ResourceTypeGkeAutopilot,
	))
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

// TestConvertStackConfig_StaticWebsiteDomainSurvivesRoundTrip pins
// that the ConvertStackConfig decode of StackConfigStatic preserves
// Site.Domain through to the Input the Provision path reads — a
// regression on the 5th exposure of the inline-tag asymmetry
// (api/main's release pipeline, dist.simple-container.com, 2026-11-09).
// Previously `decodeInputsAs` (yaml-roundtrip) lost Site because
// SC-core's sender json.Marshal emits `"Site":{...}` NESTED (named
// field, encoding/json ignores `,inline`) and yaml.v3's `,inline`
// decoder can't flatten it. `decodeInputsAsJSON` reads the wire with
// the same codec it was written in.
func TestConvertStackConfig_StaticWebsiteDomainSurvivesRoundTrip(t *testing.T) {
	RegisterTestingT(t)

	// Build the wire exactly the way SC-core's PlainToInputs would —
	// json.Marshal of a *StackConfigStatic puts Site NESTED as the
	// NAMED field "Site":{...} (encoding/json does not honour
	// `,inline`). Any test that puts Site's fields flat at the top
	// level does NOT model the real wire and would have passed against
	// the broken yaml-roundtrip.
	stackConfig := &api.StackConfigStatic{
		BundleDir: "./bundle",
		Location:  "EUROPE-CENTRAL2",
		Site: api.StaticSiteConfig{
			Domain:        "dist.simple-container.com",
			BaseDnsZone:   "simple-container.com",
			IndexDocument: "index.html",
			ErrorDocument: "404.html",
		},
	}
	stackInputs := inputsFromStruct(t, stackConfig)
	templateConfig := map[string]any{
		"projectId":   "simple-container-api",
		"credentials": `{"type":"service_account"}`,
	}
	templateInputs := inputsFromStruct(t, templateConfig)

	p := &gcpPlugin{progress: scPlugin.NewPublisher(0)}
	req := &scpluginv1.ConvertStackConfigRequest{
		Header:         &scpluginv1.RequestHeader{StackName: "dist", Environment: "prod"},
		TemplateType:   gcpApi.TemplateTypeStaticWebsite,
		StackName:      "dist",
		StackDir:       "/tmp",
		TemplateConfig: templateInputs,
		StackConfig:    stackInputs,
	}
	resp, err := p.ConvertStackConfig(context.Background(), req)
	Expect(err).ToNot(HaveOccurred(),
		"ConvertStackConfig for gcp-static-website must not fail when the client sends Site nested — this is the real wire shape")
	Expect(resp).ToNot(BeNil())
	Expect(resp.Input).ToNot(BeEmpty())
	domainIV, ok := resp.Input["domain"]
	Expect(ok).To(BeTrue(),
		"response Input must carry the flat `domain` scalar populated by ToStaticSiteConfig")
	Expect(domainIV.GetStringValue()).To(Equal("dist.simple-container.com"),
		"empty domain on the wire makes the Provision path build a Record URN `::Record::-record`, which Cloudflare rejects with 9000 DNS-name-invalid (see api/main release run 42599182582)")
}

// inputsFromStruct mirrors what SC-core's PlainToInputs does: json.Marshal
// the typed Go value, parse back to map[string]any, encode each leaf as
// an InputValue. Modelling the wire this way means test shapes caught
// by this regression match what a live deploy actually sends.
func inputsFromStruct(t *testing.T, v any) map[string]*scpluginv1.InputValue {
	t.Helper()
	b, err := json.Marshal(v)
	Expect(err).ToNot(HaveOccurred())
	var raw map[string]any
	Expect(json.Unmarshal(b, &raw)).To(Succeed())
	out := map[string]*scpluginv1.InputValue{}
	for k, val := range raw {
		if iv := encodeInputValue(val); iv != nil {
			out[k] = iv
		}
	}
	return out
}

func encodeInputValue(v any) *scpluginv1.InputValue {
	switch x := v.(type) {
	case string:
		return &scpluginv1.InputValue{Value: &scpluginv1.InputValue_StringValue{StringValue: x}}
	case bool:
		return &scpluginv1.InputValue{Value: &scpluginv1.InputValue_BoolValue{BoolValue: x}}
	case float64:
		return &scpluginv1.InputValue{Value: &scpluginv1.InputValue_DoubleValue{DoubleValue: x}}
	case map[string]any:
		fields := map[string]*scpluginv1.InputValue{}
		for k, item := range x {
			if iv := encodeInputValue(item); iv != nil {
				fields[k] = iv
			}
		}
		return &scpluginv1.InputValue{Value: &scpluginv1.InputValue_MapValue{MapValue: &scpluginv1.InputMap{Fields: fields}}}
	case []any:
		items := make([]*scpluginv1.InputValue, 0, len(x))
		for _, item := range x {
			iv := encodeInputValue(item)
			if iv == nil {
				iv = &scpluginv1.InputValue{}
			}
			items = append(items, iv)
		}
		return &scpluginv1.InputValue{Value: &scpluginv1.InputValue_ListValue{ListValue: &scpluginv1.InputList{Items: items}}}
	}
	return nil
}

func strInputs(kv map[string]any) map[string]*scpluginv1.InputValue {
	out := map[string]*scpluginv1.InputValue{}
	for k, v := range kv {
		switch x := v.(type) {
		case string:
			out[k] = &scpluginv1.InputValue{Value: &scpluginv1.InputValue_StringValue{StringValue: x}}
		case bool:
			out[k] = &scpluginv1.InputValue{Value: &scpluginv1.InputValue_BoolValue{BoolValue: x}}
		}
	}
	return out
}

// With `provision: true` the key URL lives in the stack's `--sc` secrets
// stack, which SC core reads itself. An empty response tells core to do that;
// an error here stops every stack before the engine starts.
func TestInitSecretsProvider_EmptyResponseLetsCoreResolveTheKey(t *testing.T) {
	RegisterTestingT(t)
	p := &gcpPlugin{progress: scPlugin.NewPublisher(0)}

	for name, req := range map[string]*scpluginv1.InitSecretsProviderRequest{
		"no config (cores that do not send it)": {SecretsProviderType: gcpApi.SecretsProviderTypeGcpKms},
		"provision: true": {SecretsProviderType: gcpApi.SecretsProviderTypeGcpKms, Config: strInputs(map[string]any{
			"provision": true, "keyName": "payspace-kms-key", "keyLocation": "global", "projectId": "p",
		})},
		"gcp-secrets-manager": {SecretsProviderType: gcpApi.SecretsTypeGCPSecretsManager},
	} {
		resp, err := p.InitSecretsProvider(context.Background(), req)
		Expect(err).ToNot(HaveOccurred(), name)
		Expect(resp.GetProviderUrl()).To(BeEmpty(), name)
		Expect(resp.GetPassphrase()).To(BeEmpty(), name)
		Expect(resp.GetEnv()).To(BeEmpty(), name)
	}
}

func TestInitSecretsProvider_ByoKeyReturnsItsUrl(t *testing.T) {
	RegisterTestingT(t)
	p := &gcpPlugin{progress: scPlugin.NewPublisher(0)}
	url := "gcpkms://projects/p/locations/global/keyRings/r/cryptoKeys/k"

	resp, err := p.InitSecretsProvider(context.Background(), &scpluginv1.InitSecretsProviderRequest{
		SecretsProviderType: gcpApi.SecretsProviderTypeGcpKms,
		Config:              strInputs(map[string]any{"provision": false, "keyName": url}),
	})
	Expect(err).ToNot(HaveOccurred())
	Expect(resp.GetProviderUrl()).To(Equal(url))
}

func TestInitStateStore_RequiresBucketName(t *testing.T) {
	RegisterTestingT(t)
	p := &gcpPlugin{progress: scPlugin.NewPublisher(0)}

	_, err := p.InitStateStore(context.Background(), &scpluginv1.InitStateStoreRequest{
		StateStoreType: gcpApi.StateStorageTypeGcpBucket,
		Config:         strInputs(map[string]any{"projectId": "p"}),
	})
	Expect(err).To(MatchError(ContainSubstring("bucketName")))
}

// A plan that deleted `landing` and created `landing-staging` for the
// Namespace, Deployment, Service, VPA and PDB of a deployed stack: the
// template names them off StackParams.StackName, and the plugin fed it the
// child stack name. The Kubernetes provider shows it plainest: it must be
// "<cli stack>--<env>", never "<child stack>--<env>".
func TestProvisionStackParams_UsesTheCliStackName(t *testing.T) {
	RegisterTestingT(t)

	h := &scpluginv1.RequestHeader{StackName: "landing--staging", CliStackName: "landing", Environment: "staging"}
	sp := provisionStackParams(h)
	Expect(sp.StackName).To(Equal("landing"))
	Expect(sp.Environment).To(Equal("staging"))

	in := api.ResourceInput{StackParams: sp}
	Expect(in.ToResName(sp.StackName)).To(Equal("landing--staging"), "the Kubernetes provider name of a deployed stack")
}

func TestProvisionStackParams_OlderCoresFallBackToTheStackName(t *testing.T) {
	RegisterTestingT(t)

	sp := provisionStackParams(&scpluginv1.RequestHeader{StackName: "pay-space", Environment: "production"})
	Expect(sp.StackName).To(Equal("pay-space"))
}

// Core stopped building the provider of a plugin-owned `uses:` dependency, so
// the plugin builds it. Its name is core's old initProvider formula; a
// different name is a delete of the live provider and a create of a new one.
func TestComputeProcessProviderName(t *testing.T) {
	RegisterTestingT(t)

	req := &scpluginv1.ComputeProcessRequest{
		Header:       &scpluginv1.RequestHeader{StackName: "crypto-tools-worker--staging", Environment: "staging"},
		ResourceType: gcpApi.ResourceTypePostgresGcpCloudsql,
		ResourceName: "postgres",
	}

	name, env := computeProcessProviderName(req, nil)
	Expect(name).To(Equal("crypto-tools-worker--staging--gcp-cloudsql-postgres--postgres--staging--provider"))
	Expect(env).To(Equal("staging"))

	// `uses: [postgres]` resolved through a dependsOn resource appends it to
	// the resource name; a custom stack provisions in the parent's env.
	name, env = computeProcessProviderName(req, &pApi.ParentInfo{
		ParentEnv:         "production",
		DependsOnResource: &api.StackConfigDependencyResource{Name: "main-db"},
	})
	Expect(name).To(Equal("crypto-tools-worker--staging--gcp-cloudsql-postgres--postgres--main-db--production--provider"))
	Expect(env).To(Equal("production"))
}

func TestComputeProcessCredentials_EveryComputeProcessTypeHasOne(t *testing.T) {
	RegisterTestingT(t)

	for name, cfg := range map[string]any{
		"bucket":   &gcpApi.GcpBucket{},
		"pubsub":   &gcpApi.PubSubConfig{},
		"redis":    &gcpApi.RedisConfig{},
		"postgres": &gcpApi.PostgresGcpCloudsqlConfig{},
		"gke":      &gcpApi.GkeAutopilotResource{},
	} {
		Expect(computeProcessCredentials(cfg)).ToNot(BeNil(), name)
	}
	Expect(computeProcessCredentials(&gcpApi.StateStorageConfig{})).To(BeNil())
}

// A stack with a healthcheck (`support-bot`, `promo-bot`) failed at Provision:
//
//	decode GkeAutopilotInput from Provision inputs: yaml: unmarshal errors:
//	  line 46: cannot unmarshal !!float `3e+10` into time.Duration
//
// SC core sends the template config as JSON, where a Duration is a number of
// nanoseconds; yaml.v3 does not accept a numeric Duration at all.
func TestDecodeTemplateInputs_CarriesHealthcheckDurations(t *testing.T) {
	RegisterTestingT(t)

	interval := 30 * time.Second
	want := gcpApi.GkeAutopilotInput{Deployment: k8s.DeploymentConfig{
		ReadinessProbe: &k8s.CloudRunProbe{Interval: &interval},
		StartupProbe:   &k8s.CloudRunProbe{Interval: &interval},
	}}
	raw, err := json.Marshal(want)
	Expect(err).ToNot(HaveOccurred())
	var plain map[string]any
	Expect(json.Unmarshal(raw, &plain)).To(Succeed())
	inputs := scPlugin.PlainToInputs(plain)

	var got gcpApi.GkeAutopilotInput
	Expect(decodeTemplateInputs(inputs, &got)).To(Succeed())
	Expect(got.Deployment.ReadinessProbe).ToNot(BeNil())
	Expect(*got.Deployment.ReadinessProbe.Interval).To(Equal(30 * time.Second))
	Expect(*got.Deployment.StartupProbe.Interval).To(Equal(30 * time.Second))

	// The path this replaces: it cannot read the same inputs.
	var viaYAML gcpApi.GkeAutopilotInput
	Expect(decodeInputsAs(inputs, &viaYAML)).ToNot(Succeed(), "yaml.v3 rejects a numeric time.Duration")
}

// The image build reads stack.Client.Security to decide whether to scan, sign,
// attach an SBOM and attest provenance. In a plugin it was always nil.
func TestProvisionStack_CarriesTheClientSecurityConfig(t *testing.T) {
	RegisterTestingT(t)

	raw, err := (&api.ClientDescriptor{
		Security:   &api.SecurityDescriptor{Enabled: true, Scan: &api.ScanDescriptor{Enabled: true}, Signing: &api.SigningDescriptor{Enabled: true, Keyless: true}},
		ImageBuild: &api.ImageBuildDescriptor{ReuseExistingCommitTag: true},
	}).PluginClientConfigJSON()
	Expect(err).ToNot(HaveOccurred())

	stack, err := provisionStack(&scpluginv1.ProvisionRequest{
		Header:           &scpluginv1.RequestHeader{StackName: "landing--staging"},
		ClientConfigJson: raw,
	})
	Expect(err).ToNot(HaveOccurred())
	Expect(stack.Name).To(Equal("landing--staging"))
	Expect(stack.Client.Security).ToNot(BeNil())
	Expect(stack.Client.Security.Enabled).To(BeTrue())
	Expect(stack.Client.Security.Signing.Keyless).To(BeTrue())
	Expect(stack.Client.ReuseExistingCommitTagEnabled()).To(BeTrue())
}

func TestProvisionStack_OlderCoreSendsNoClientConfig(t *testing.T) {
	RegisterTestingT(t)

	stack, err := provisionStack(&scpluginv1.ProvisionRequest{Header: &scpluginv1.RequestHeader{StackName: "pay-space"}})
	Expect(err).ToNot(HaveOccurred())
	Expect(stack.Client.Security).To(BeNil())

	_, err = provisionStack(&scpluginv1.ProvisionRequest{ClientConfigJson: []byte("{not json")})
	Expect(err).To(MatchError(ContainSubstring("client config")))
}
