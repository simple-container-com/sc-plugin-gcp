// SPDX-License-Identifier: MIT
// Copyright (c) Simple Container

package gcp

import (
	"fmt"

	"github.com/pkg/errors"
	sdk "github.com/pulumi/pulumi/sdk/v3/go/pulumi"
	"github.com/simple-container-com/api/pkg/api"
	pApi "github.com/simple-container-com/api/pkg/clouds/pulumi/api"

	gcpApi "github.com/simple-container-com/sc-plugin-gcp/api"
)

// Cloudrun is the provisioning-tier dispatcher for `type: cloudrun`
// client stacks. Lifted verbatim from in-tree
// `pkg/clouds/pulumi/gcp/cloudrun.go` — the in-tree body was itself a
// `not implemented` stub that walked the compute-context dependencies
// (so a server.yaml consuming this template would at least parse) and
// logged an error. Preserved as-is so existing fixtures and the
// phase 3c `sc preview` happy path keep the exact same observable
// behaviour; a real Cloud Run reconciler is a follow-up scoped to a
// separate slice (operators asking to deploy here today still get the
// warning, not an incompatible-type parse error).
func Cloudrun(ctx *sdk.Context, stack api.Stack, input api.ResourceInput, params pApi.ProvisionParams) (*api.ResourceOutput, error) {
	if input.Descriptor.Type != gcpApi.TemplateTypeGcpCloudrun {
		return nil, errors.Errorf("unsupported template type %q", input.Descriptor.Type)
	}

	for _, dep := range params.ComputeContext.Dependencies() {
		dep.URN().ApplyT(func(urn string) (any, error) {
			fmt.Println(urn)
			return nil, nil
		})
	}
	cloudrunInput, ok := input.Descriptor.Config.Config.(*gcpApi.CloudRunInput)
	if !ok {
		return nil, errors.Errorf("failed to convert cloudrun config for %q", input.Descriptor.Type)
	}
	params.Log.Debug(ctx.Context(), "configure cloud run for %+v", cloudrunInput)

	params.Log.Error(ctx.Context(), "not implemented for %q", input.Descriptor.Type)
	return &api.ResourceOutput{Ref: nil}, nil
}
