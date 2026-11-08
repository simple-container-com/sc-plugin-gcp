// SPDX-License-Identifier: MIT
// Copyright (c) Simple Container

package gcp

import (
	"fmt"
	"strings"

	"github.com/pkg/errors"
	"github.com/pulumi/pulumi-gcp/sdk/v8/go/gcp/serviceaccount"
	"github.com/pulumi/pulumi-gcp/sdk/v8/go/gcp/storage"
	sdk "github.com/pulumi/pulumi/sdk/v3/go/pulumi"
	"github.com/samber/lo"
	"github.com/simple-container-com/api/pkg/api"
	pApi "github.com/simple-container-com/api/pkg/clouds/pulumi/api"
	"github.com/simple-container-com/api/pkg/util"

	gcpApi "github.com/simple-container-com/sc-plugin-gcp/api"
)

// PrivateBucket provisions a GCS bucket with S3 interoperability
// (service account + object admin binding + HMAC key). Exported state:
// bucket name, location, HMAC access key id, HMAC secret key.
//
// Lifted verbatim from api `pkg/clouds/pulumi/gcp/bucket.go`. When
// `Adopt: true` the call is handed off to AdoptPrivateBucket, which
// imports an existing bucket into Pulumi state rather than creating a
// new one.
func PrivateBucket(ctx *sdk.Context, stack api.Stack, input api.ResourceInput, params pApi.ProvisionParams) (*api.ResourceOutput, error) {
	if input.Descriptor.Type != gcpApi.ResourceTypeBucket {
		return nil, errors.Errorf("unsupported bucket type %q", input.Descriptor.Type)
	}

	bucketCfg, ok := input.Descriptor.Config.Config.(*gcpApi.GcpBucket)
	if !ok {
		return nil, errors.Errorf("failed to convert bucket config for %q", input.Descriptor.Type)
	}

	// Handle resource adoption - exit early if adopting
	if bucketCfg.Adopt {
		return AdoptPrivateBucket(ctx, stack, input, params)
	}

	bucketName := input.ToResName(lo.If(bucketCfg.GetBucketName() == "", input.Descriptor.Name).Else(bucketCfg.GetBucketName()))
	opts := []sdk.ResourceOption{sdk.Provider(params.Provider)}

	params.Log.Info(ctx.Context(), "creating GCS bucket %q with S3 interoperability", bucketName)

	bucket, err := storage.NewBucket(ctx, bucketName, &storage.BucketArgs{
		Name:     sdk.String(bucketName),
		Location: sdk.String(bucketCfg.Location),
	}, opts...)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to provision bucket %q", bucketName)
	}

	ctx.Export(toBucketNameExport(bucketName), bucket.Name)
	ctx.Export(toBucketLocationExport(bucketName), bucket.Location)

	params.Log.Info(ctx.Context(), "creating service account for bucket %q", bucketName)

	saName := fmt.Sprintf("%s-sa", bucketName)
	sanitizedName := strings.ReplaceAll(saName, "_", "-")
	saAccountId := strings.ReplaceAll(util.TrimStringMiddle(sanitizedName, 28, "-"), "--", "-")

	sa, err := serviceaccount.NewAccount(ctx, saName, &serviceaccount.AccountArgs{
		AccountId:   sdk.String(saAccountId),
		DisplayName: sdk.String(fmt.Sprintf("Service Account for %s", bucketName)),
	}, opts...)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to create service account for bucket %q", bucketName)
	}

	params.Log.Info(ctx.Context(), "granting storage permissions to service account for bucket %q", bucketName)

	_, err = storage.NewBucketIAMMember(ctx, fmt.Sprintf("%s-iam-object", bucketName), &storage.BucketIAMMemberArgs{
		Bucket: bucket.Name,
		Role:   sdk.String("roles/storage.objectAdmin"),
		Member: sa.Email.ApplyT(func(email string) string {
			return fmt.Sprintf("serviceAccount:%s", email)
		}).(sdk.StringOutput),
	}, opts...)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to grant object permissions to service account for bucket %q", bucketName)
	}

	_, err = storage.NewBucketIAMMember(ctx, fmt.Sprintf("%s-iam-bucket", bucketName), &storage.BucketIAMMemberArgs{
		Bucket: bucket.Name,
		Role:   sdk.String("roles/storage.legacyBucketWriter"),
		Member: sa.Email.ApplyT(func(email string) string {
			return fmt.Sprintf("serviceAccount:%s", email)
		}).(sdk.StringOutput),
	}, opts...)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to grant bucket permissions to service account for bucket %q", bucketName)
	}

	params.Log.Info(ctx.Context(), "generating HMAC keys for S3-compatible access to bucket %q", bucketName)

	hmacKey, err := storage.NewHmacKey(ctx, fmt.Sprintf("%s-hmac", bucketName), &storage.HmacKeyArgs{
		ServiceAccountEmail: sa.Email,
	}, opts...)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to create HMAC key for bucket %q", bucketName)
	}

	ctx.Export(toBucketHmacAccessKeyIdExport(bucketName), hmacKey.AccessId)
	ctx.Export(toBucketHmacSecretKeyExport(bucketName), sdk.ToSecret(hmacKey.Secret))

	params.Log.Info(ctx.Context(), "successfully provisioned GCS bucket %q with S3 interoperability", bucketName)

	return &api.ResourceOutput{Ref: bucket}, nil
}

// BucketComputeProcessor contributes env vars + resource-tpl extensions
// for a consumer stack that `uses: [gcp-bucket]`. Reads parent-stack
// outputs via StackReference. Trap 14 (local-collector-drops-env) is
// avoided by writing through the shared `pApi.ComputeContextCollector`
// passed in — do not reimplement.
//
// Lifted verbatim from api `pkg/clouds/pulumi/gcp/bucket.go`.
func BucketComputeProcessor(ctx *sdk.Context, stack api.Stack, input api.ResourceInput, collector pApi.ComputeContextCollector, params pApi.ProvisionParams) (*api.ResourceOutput, error) {
	if params.ParentStack == nil {
		return nil, errors.Errorf("parent stack must not be nil for compute processor for %q", stack.Name)
	}
	parentStackName := params.ParentStack.StackName

	bucketCfg, ok := input.Descriptor.Config.Config.(*gcpApi.GcpBucket)
	if !ok {
		return nil, errors.Errorf("failed to convert bucket config for %q", input.Descriptor.Type)
	}

	bucketName := input.ToResName(lo.If(bucketCfg.Name == "", input.Descriptor.Name).Else(bucketCfg.Name))

	suffix := lo.If(params.ParentStack.DependsOnResource != nil, "--"+lo.FromPtr(params.ParentStack.DependsOnResource).Name).Else("")
	params.Log.Info(ctx.Context(), "getting parent's (%q) outputs for gcp bucket %q (%q)", params.ParentStack.FullReference, bucketName, suffix)
	parentRef, err := sdk.NewStackReference(ctx, fmt.Sprintf("%s--%s--%s%s--gcs-bucket-ref", stack.Name, params.ParentStack.StackName, input.Descriptor.Name, suffix), &sdk.StackReferenceArgs{
		Name: sdk.String(params.ParentStack.FullReference).ToStringOutput(),
	})
	if err != nil {
		return nil, err
	}

	bucketNameExport := toBucketNameExport(bucketName)
	resBucketName, err := pApi.GetParentOutput(parentRef, bucketNameExport, params.ParentStack.FullReference, false)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to get bucket name from parent stack for %q (%q)", stack.Name, bucketNameExport)
	} else if resBucketName == "" {
		return nil, errors.Errorf("bucket name is empty for %q (%q)", stack.Name, bucketNameExport)
	}

	accessKeyIdExport := toBucketHmacAccessKeyIdExport(bucketName)
	resAccessKeyId, err := pApi.GetParentOutput(parentRef, accessKeyIdExport, params.ParentStack.FullReference, false)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to get HMAC access key ID from parent stack for %q (%q)", stack.Name, accessKeyIdExport)
	} else if resAccessKeyId == "" {
		return nil, errors.Errorf("HMAC access key ID is empty for %q (%q)", stack.Name, accessKeyIdExport)
	}

	secretKeyExport := toBucketHmacSecretKeyExport(bucketName)
	resSecretKey, err := pApi.GetParentOutput(parentRef, secretKeyExport, params.ParentStack.FullReference, true)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to get HMAC secret key from parent stack for %q (%q)", stack.Name, secretKeyExport)
	} else if resSecretKey == "" {
		return nil, errors.Errorf("HMAC secret key is empty for %q (%q)", stack.Name, secretKeyExport)
	}

	locationExport := toBucketLocationExport(bucketName)
	resBucketLocation, err := pApi.GetParentOutput(parentRef, locationExport, params.ParentStack.FullReference, false)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to get bucket location from parent stack for %q (%q)", stack.Name, locationExport)
	} else if resBucketLocation == "" {
		return nil, errors.Errorf("bucket location is empty for %q (%q)", stack.Name, locationExport)
	}

	collector.AddOutput(ctx, parentRef.Name.ApplyT(func(refName any) any {
		s3Endpoint := "https://storage.googleapis.com"

		collector.AddEnvVariableIfNotExist(util.ToEnvVariableName(fmt.Sprintf("GCS_%s_BUCKET", bucketName)), resBucketName,
			input.Descriptor.Type, input.Descriptor.Name, parentStackName)
		collector.AddEnvVariableIfNotExist(util.ToEnvVariableName(fmt.Sprintf("GCS_%s_LOCATION", bucketName)), resBucketLocation,
			input.Descriptor.Type, input.Descriptor.Name, parentStackName)
		collector.AddSecretEnvVariableIfNotExist(util.ToEnvVariableName(fmt.Sprintf("GCS_%s_ACCESS_KEY", bucketName)), resAccessKeyId,
			input.Descriptor.Type, input.Descriptor.Name, parentStackName)
		collector.AddSecretEnvVariableIfNotExist(util.ToEnvVariableName(fmt.Sprintf("GCS_%s_SECRET_KEY", bucketName)), resSecretKey,
			input.Descriptor.Type, input.Descriptor.Name, parentStackName)
		collector.AddEnvVariableIfNotExist(util.ToEnvVariableName(fmt.Sprintf("GCS_%s_ENDPOINT", bucketName)), s3Endpoint,
			input.Descriptor.Type, input.Descriptor.Name, parentStackName)

		collector.AddEnvVariableIfNotExist(util.ToEnvVariableName(fmt.Sprintf("S3_%s_BUCKET", bucketName)), resBucketName,
			input.Descriptor.Type, input.Descriptor.Name, parentStackName)
		collector.AddEnvVariableIfNotExist(util.ToEnvVariableName(fmt.Sprintf("S3_%s_REGION", bucketName)), resBucketLocation,
			input.Descriptor.Type, input.Descriptor.Name, parentStackName)
		collector.AddSecretEnvVariableIfNotExist(util.ToEnvVariableName(fmt.Sprintf("S3_%s_ACCESS_KEY", bucketName)), resAccessKeyId,
			input.Descriptor.Type, input.Descriptor.Name, parentStackName)
		collector.AddSecretEnvVariableIfNotExist(util.ToEnvVariableName(fmt.Sprintf("S3_%s_SECRET_KEY", bucketName)), resSecretKey,
			input.Descriptor.Type, input.Descriptor.Name, parentStackName)
		collector.AddEnvVariableIfNotExist(util.ToEnvVariableName(fmt.Sprintf("S3_%s_ENDPOINT", bucketName)), s3Endpoint,
			input.Descriptor.Type, input.Descriptor.Name, parentStackName)

		collector.AddEnvVariableIfNotExist(util.ToEnvVariableName("GCS_BUCKET"), resBucketName,
			input.Descriptor.Type, input.Descriptor.Name, parentStackName)
		collector.AddEnvVariableIfNotExist(util.ToEnvVariableName("GCS_LOCATION"), resBucketLocation,
			input.Descriptor.Type, input.Descriptor.Name, parentStackName)
		collector.AddSecretEnvVariableIfNotExist(util.ToEnvVariableName("GCS_ACCESS_KEY"), resAccessKeyId,
			input.Descriptor.Type, input.Descriptor.Name, parentStackName)
		collector.AddSecretEnvVariableIfNotExist(util.ToEnvVariableName("GCS_SECRET_KEY"), resSecretKey,
			input.Descriptor.Type, input.Descriptor.Name, parentStackName)
		collector.AddEnvVariableIfNotExist(util.ToEnvVariableName("GCS_ENDPOINT"), s3Endpoint,
			input.Descriptor.Type, input.Descriptor.Name, parentStackName)

		collector.AddSecretEnvVariableIfNotExist(util.ToEnvVariableName("AWS_ACCESS_KEY_ID"), resAccessKeyId,
			input.Descriptor.Type, input.Descriptor.Name, parentStackName)
		collector.AddSecretEnvVariableIfNotExist(util.ToEnvVariableName("AWS_SECRET_ACCESS_KEY"), resSecretKey,
			input.Descriptor.Type, input.Descriptor.Name, parentStackName)
		collector.AddEnvVariableIfNotExist(util.ToEnvVariableName("S3_ENDPOINT"), s3Endpoint,
			input.Descriptor.Type, input.Descriptor.Name, parentStackName)
		collector.AddEnvVariableIfNotExist(util.ToEnvVariableName("S3_BUCKET"), resBucketName,
			input.Descriptor.Type, input.Descriptor.Name, parentStackName)
		collector.AddEnvVariableIfNotExist(util.ToEnvVariableName("S3_REGION"), resBucketLocation,
			input.Descriptor.Type, input.Descriptor.Name, parentStackName)

		collector.AddEnvVariableIfNotExist(util.ToEnvVariableName("AWS_DEFAULT_REGION"), "auto",
			input.Descriptor.Type, input.Descriptor.Name, parentStackName)
		collector.AddEnvVariableIfNotExist(util.ToEnvVariableName("AWS_S3_SIGNATURE_VERSION"), "s3v4",
			input.Descriptor.Type, input.Descriptor.Name, parentStackName)
		collector.AddEnvVariableIfNotExist(util.ToEnvVariableName("AWS_S3_ADDRESSING_STYLE"), "path",
			input.Descriptor.Type, input.Descriptor.Name, parentStackName)
		collector.AddEnvVariableIfNotExist(util.ToEnvVariableName("AWS_S3_PAYLOAD_SIGNING_ENABLED"), "false",
			input.Descriptor.Type, input.Descriptor.Name, parentStackName)
		collector.AddEnvVariableIfNotExist(util.ToEnvVariableName("AWS_REQUEST_CHECKSUM_CALCULATION"), "when_required",
			input.Descriptor.Type, input.Descriptor.Name, parentStackName)
		collector.AddEnvVariableIfNotExist(util.ToEnvVariableName("AWS_RESPONSE_CHECKSUM_VALIDATION"), "when_required",
			input.Descriptor.Type, input.Descriptor.Name, parentStackName)

		collector.AddResourceTplExtension(input.Descriptor.Name, map[string]string{
			"bucket":     resBucketName,
			"location":   resBucketLocation,
			"access-key": resAccessKeyId,
			"secret-key": resSecretKey,
			"endpoint":   s3Endpoint,
		})

		return nil
	}))

	return &api.ResourceOutput{
		Ref: parentStackName,
	}, nil
}

// Export key helpers. These names are part of the wire contract between
// a provisioning stack and any `uses:` consumer stack — if they change
// mid-flight, consumers break with "output not found".
func toBucketNameExport(bucketName string) string {
	return fmt.Sprintf("%s-bucket-name", bucketName)
}

func toBucketLocationExport(bucketName string) string {
	return fmt.Sprintf("%s-bucket-location", bucketName)
}

func toBucketHmacAccessKeyIdExport(bucketName string) string {
	return fmt.Sprintf("%s-hmac-access-key-id", bucketName)
}

func toBucketHmacSecretKeyExport(bucketName string) string {
	return fmt.Sprintf("%s-hmac-secret-key", bucketName)
}
