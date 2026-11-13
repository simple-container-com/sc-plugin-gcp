// SPDX-License-Identifier: MIT
// Copyright (c) Simple Container

package gcp

import (
	"fmt"

	"github.com/pkg/errors"

	"github.com/pulumi/pulumi-gcp/sdk/v8/go/gcp/kms"
	sdk "github.com/pulumi/pulumi/sdk/v3/go/pulumi"

	"github.com/simple-container-com/api/pkg/api"
	pApi "github.com/simple-container-com/api/pkg/clouds/pulumi/api"

	gcpApi "github.com/simple-container-com/sc-plugin-gcp/api"
)

// KmsKeySecretsProvider provisions the KMS key ring and key behind a stack's
// `gcp-kms` secrets provider, and exports the gcpkms:// URL SC core reads back
// from the stack's `--sc` secrets stack. Lifted from api
// `pkg/clouds/pulumi/gcp/kms_key.go`.
//
// Names are load-bearing for stacks provisioned before the split: the key ring
// and key are both named after the descriptor, the CryptoKey's logical name is
// input.ToResName of it, and the export key is the descriptor name.
func KmsKeySecretsProvider(ctx *sdk.Context, stack api.Stack, input api.ResourceInput, params pApi.ProvisionParams) (*api.ResourceOutput, error) {
	kmsInput, ok := input.Descriptor.Config.Config.(*gcpApi.SecretsProviderConfig)
	if !ok {
		return nil, errors.Errorf("failed to convert KmsKeyInput for %q", input.Descriptor.Type)
	}

	// Validate before any side effect. Enabling service APIs mutates the
	// project, and a KeyRing can never be deleted in GCP (destroying the Pulumi
	// resource only drops it from state), so failing later would leave a
	// permanent, un-recreatable-by-name KeyRing behind for a mere typo.
	if err := kmsInput.Validate(); err != nil {
		return nil, err
	}

	if err := enableServicesAPI(ctx.Context(), input.Descriptor.Config.Config,
		fmt.Sprintf("projects/%s/services/serviceusage.googleapis.com", kmsInput.ProjectId)); err != nil {
		return nil, errors.Wrapf(err, "serviceusage API is not enabled on project %q; enable it with "+
			"`gcloud services enable serviceusage.googleapis.com --project %s`", kmsInput.ProjectId, kmsInput.ProjectId)
	}
	kmsServiceName := fmt.Sprintf("projects/%s/services/cloudkms.googleapis.com", kmsInput.ProjectId)
	if err := enableServicesAPI(ctx.Context(), input.Descriptor.Config.Config, kmsServiceName); err != nil {
		return nil, errors.Wrapf(err, "failed to enable %s", kmsServiceName)
	}

	projectId := kmsInput.ProjectIdValue()
	keyRingName := input.Descriptor.Name

	keyRing, err := kms.NewKeyRing(ctx, keyRingName, &kms.KeyRingArgs{
		Name:     sdk.String(keyRingName),
		Location: sdk.String(kmsInput.KeyLocation),
	}, sdk.Provider(params.Provider))
	if err != nil {
		return nil, err
	}

	key, err := kms.NewCryptoKey(ctx, input.ToResName(input.Descriptor.Name), &kms.CryptoKeyArgs{
		Name:           sdk.String(input.Descriptor.Name),
		KeyRing:        keyRing.ID(),
		RotationPeriod: sdk.String(kmsInput.EffectiveKeyRotationPeriod()),
		VersionTemplate: &kms.CryptoKeyVersionTemplateArgs{
			Algorithm:       sdk.String("GOOGLE_SYMMETRIC_ENCRYPTION"),
			ProtectionLevel: sdk.String("SOFTWARE"),
		},
	}, sdk.Provider(params.Provider))
	if err != nil {
		return nil, err
	}

	ctx.Export(input.Descriptor.Name, sdk.All(keyRing.Name, key.Name).ApplyT(func(args []any) (string, error) {
		keyRingName, keyName := args[0].(string), args[1].(string)
		return fmt.Sprintf("gcpkms://projects/%s/locations/%s/keyRings/%s/cryptoKeys/%s",
			projectId, kmsInput.KeyLocation, keyRingName, keyName), nil
	}))

	return &api.ResourceOutput{Ref: key}, nil
}
