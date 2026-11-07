// SPDX-License-Identifier: MIT
// Copyright (c) Simple Container

package gcp

import (
	"fmt"

	"github.com/pulumi/pulumi-gcp/sdk/v8/go/gcp/projects"
	"github.com/pulumi/pulumi-gcp/sdk/v8/go/gcp/serviceaccount"
	sdk "github.com/pulumi/pulumi/sdk/v3/go/pulumi"
	"github.com/simple-container-com/api/pkg/util"
)

// ServiceAccountResult carries what a plugin caller needs to mount the
// account into a consuming compute workload: the Pulumi resource, the
// generated key resource, and the ready-to-attach credentials secret.
//
// In-tree, this type was named `CloudSQLAccount` (defined in
// cloudsql_proxy.go) — a misnomer that bled from the original CloudSQL
// consumer into the generic ServiceAccount helper. Renamed on lift
// because sc-plugin-gcp's cloudsql_proxy.go isn't lifted yet and the
// pubsub lift doesn't need it; keeping the name tied to its callers
// rather than its original consumer.
type ServiceAccountResult struct {
	ServiceAccount     *serviceaccount.Account
	ServiceAccountKey  *serviceaccount.Key
	CredentialsSecrets sdk.StringMap
}

type ServiceAccountArgs struct {
	Project     string
	Description string
	Roles       []string
}

// NewServiceAccount provisions a GCP service account + key, grants it
// the named project-level roles, and packages the private key for
// mounting into a consuming pod. Used by the pubsub compute processor
// (and in future: cloudsql, gke, static-site write bindings).
//
// Lifted verbatim from api `pkg/clouds/pulumi/gcp/service_account.go`
// except for the return type rename.
func NewServiceAccount(ctx *sdk.Context, name string, args ServiceAccountArgs, opts ...sdk.ResourceOption) (*ServiceAccountResult, error) {
	accountName := util.SanitizeGCPServiceAccountName(name)

	serviceAccount, err := serviceaccount.NewAccount(ctx, accountName, &serviceaccount.AccountArgs{
		AccountId:   sdk.String(accountName),
		Project:     sdk.String(args.Project),
		Description: sdk.String(args.Description),
		DisplayName: sdk.String(fmt.Sprintf("%s-service-account", name)),
	}, opts...)
	if err != nil {
		return nil, err
	}

	opts = append(opts, sdk.Parent(serviceAccount))
	serviceAccountKey, err := serviceaccount.NewKey(ctx, fmt.Sprintf("%s-key", accountName), &serviceaccount.KeyArgs{
		ServiceAccountId: serviceAccount.AccountId,
	}, opts...)
	if err != nil {
		return nil, err
	}
	for _, role := range args.Roles {
		_, err = projects.NewIAMMember(ctx, fmt.Sprintf("%s-%s-iam", accountName, role), &projects.IAMMemberArgs{
			Project: sdk.String(args.Project),
			Member:  serviceAccount.Member,
			Role:    sdk.String(role),
		}, opts...)
		if err != nil {
			return nil, err
		}
	}

	credentialsSecrets := sdk.StringMap{
		"credentials.json": serviceAccountKey.PrivateKey,
	}

	return &ServiceAccountResult{
		ServiceAccount:     serviceAccount,
		ServiceAccountKey:  serviceAccountKey,
		CredentialsSecrets: credentialsSecrets,
	}, nil
}
