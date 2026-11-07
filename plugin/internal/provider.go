// SPDX-License-Identifier: MIT
// Copyright (c) Simple Container

package gcp

import (
	gcpOptions "google.golang.org/api/option"

	"github.com/pkg/errors"
	"github.com/pulumi/pulumi-gcp/sdk/v8/go/gcp"
	sdk "github.com/pulumi/pulumi/sdk/v3/go/pulumi"
	"github.com/simple-container-com/api/pkg/api"
	pApi "github.com/simple-container-com/api/pkg/clouds/pulumi/api"

	gcpApi "github.com/simple-container-com/sc-plugin-gcp/api"
)

// clientOptions authenticates a Google API client with the configured
// key, or with Application Default Credentials when none is configured.
// Lifted from api `pkg/clouds/pulumi/gcp/credentials.go`. Shared by
// `services_api.go` (API-enable client init).
func clientOptions(credentials string) []gcpOptions.ClientOption {
	if gcpApi.UsesAmbientCredentials(credentials) {
		return nil
	}
	return []gcpOptions.ClientOption{gcpOptions.WithCredentialsJSON([]byte(credentials))} //nolint:staticcheck // SA1019: no in-memory replacement available
}

// Provider builds the GCP Pulumi provider from a `gcp-service-account`
// auth block. The URN name is the stack-prefixed input.ToResName of the
// descriptor name — core's own formula — so a pre-split state entry
// reads as `same`, not as a provider replacement.
//
// Lifted from api `pkg/clouds/pulumi/gcp/provider.go` without state-bucket
// initialisation. State store provisioning (InitStateStore) comes in a
// later slice; walking-skeleton phase 2 covers only the Provider +
// PrivateBucket path so a `type: gcp-bucket` resource can preview.
func Provider(ctx *sdk.Context, stack api.Stack, input api.ResourceInput, params pApi.ProvisionParams) (*api.ResourceOutput, error) {
	pcfg, ok := input.Descriptor.Config.Config.(api.AuthConfig)
	if !ok {
		return nil, errors.Errorf("failed to cast config to api.AuthConfig")
	}

	creds := pcfg.CredentialsValue()
	projectId := pcfg.ProjectIdValue()

	args := &gcp.ProviderArgs{Project: sdk.String(projectId)}
	if !gcpApi.UsesAmbientCredentials(creds) {
		// Without Credentials the provider uses Application Default Credentials.
		args.Credentials = secretCredentials(creds)
	}
	provider, err := gcp.NewProvider(ctx, input.ToResName(input.Descriptor.Name), args)
	return &api.ResourceOutput{
		Ref: provider,
	}, err
}

// secretCredentials wraps the service-account document so Pulumi treats it as a
// secret.
//
// Pulumi prints the properties of every resource it creates or changes in its
// preview and update summaries, SC forwards that summary to stdout, and GitHub
// Actions archives it — so a provider credential passed as a plain property
// value is published by the first deploy of the stack. That is not theoretical:
// the same shape leaked a Yandex service-account private key into a
// world-readable log on 2026-09-30 (forge-atrium run 36687152934). See
// yandex/credentials.go and forge's
// docs/roadmap/items/yc-provider-key-in-deploy-log.md.
//
// pulumi-gcp's generated NewProvider marks only `accessToken` secret; the
// `credentials` field — which is where SC puts the whole service-account JSON,
// `private_key` included — is left to the caller. Marking it here also encrypts
// the value in the Pulumi checkpoint, where it was otherwise stored in
// plaintext.
func secretCredentials(value string) sdk.StringPtrOutput {
	return sdk.ToSecret(sdk.String(value)).(sdk.StringOutput).ToStringPtrOutput()
}
