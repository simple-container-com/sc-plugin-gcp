// SPDX-License-Identifier: MIT
// Copyright (c) Simple Container

package gcp

import (
	"github.com/pkg/errors"
	"github.com/simple-container-com/api/pkg/api"
	"github.com/simple-container-com/api/pkg/clouds/compose"
	"github.com/simple-container-com/api/pkg/clouds/k8s"
)

// TemplateTypeGcpCloudrun is the parent-template discriminator for the
// GCP Cloud Run compose-style template.
const TemplateTypeGcpCloudrun = "cloudrun"

// AlertsConfig holds the alerting knobs a Cloud Run deployment exposes.
// Carried from in-tree verbatim — no plugin consumes or validates these
// yet, but they shape the parent YAML operators already have on disk.
type AlertsConfig struct {
	MaxErrors MaxErrorConfig
	Discord   DiscordCfg
	Telegram  TelegramCfg
}

type TelegramCfg struct {
	DefaultChatId string
}

type DiscordCfg struct {
	WebhookId string
}

type MaxErrorConfig struct {
	ErrorLogMessageRegexp string
	MaxErrorCount         int
}

// CloudRunTemplate is the template config for `type: cloudrun`. Extends
// the base `TemplateConfig` (Credentials) with:
//
//   - `ArtifactRegistryResource` — name of a parent-stack
//     `gcp-artifact-registry` resource. Images from the client stack's
//     docker-compose are built and pushed there before the Cloud Run
//     service references them. Required: Cloud Run pulls images from GAR,
//     not from arbitrary registries.
//   - `Region` — Cloud Run deployment region (e.g. `us-central1`).
//     Required: Cloud Run is a regional resource.
//
// Operators who used the stub body of this template (phase 4e lift from
// in-tree) will find the stub never declared either field; they are new
// to this slice and the Provision body now requires both. Operators who
// never went past the stub have no state to carry forward.
type CloudRunTemplate struct {
	TemplateConfig           `json:",inline" yaml:",inline"`
	ArtifactRegistryResource string `json:"artifactRegistryResource,omitempty" yaml:"artifactRegistryResource,omitempty"`
	Region                   string `json:"region,omitempty" yaml:"region,omitempty"`
}

// ReadCloudRunTemplateConfig is the parse-time reader registered for
// `TemplateTypeGcpCloudrun`. Replaces the shared `ReadTemplateConfig`
// that the stub-era lift used; this one decodes the extra fields.
func ReadCloudRunTemplateConfig(config *api.Config) (api.Config, error) {
	return api.ConvertConfig(config, &CloudRunTemplate{})
}

// CloudRunInput is the typed Input SC core's PrepareCloudComposeForDeploy
// path (now the plugin's ConvertStackConfig handler) produces for a
// `type: cloudrun` client stack.
type CloudRunInput struct {
	CloudRunTemplate `json:"templateConfig" yaml:"templateConfig"`
	Deployment       k8s.DeploymentConfig `json:"deployment" yaml:"deployment"`
}

// Uses satisfies api.ResourceAware — SC core's deploy wrapper reads the
// list to resolve cross-stack dependencies before dispatching Provision.
func (i *CloudRunInput) Uses() []string {
	return i.Deployment.StackConfig.Uses
}

// OverriddenBaseZone satisfies api.DnsConfigAware — the parent stack's
// base DNS zone is overridable at the template level.
func (i *CloudRunInput) OverriddenBaseZone() string {
	return i.Deployment.StackConfig.BaseDnsZone
}

// DependsOnResources satisfies api.WithDependsOnResources so SC core's
// deploy wrapper can resolve cross-resource dependencies ahead of
// template Provision.
func (i *CloudRunInput) DependsOnResources() []api.StackConfigDependencyResource {
	return i.Deployment.StackConfig.Dependencies
}

// ToCloudRunConfig is the converter closure the pre-A2 world registered
// under cloudComposeConverterMapping. Post-A2 the plugin's
// ConvertStackConfig handler calls it directly after decoding the
// merged compose config from stackDir. Accepts either the new
// `*CloudRunTemplate` (preferred) or the legacy `*TemplateConfig`
// (stub-era) — the legacy path lands with empty `ArtifactRegistryResource`
// + `Region` and fails loud at Provision-time with a clear sentence.
func ToCloudRunConfig(tpl any, composeCfg compose.Config, stackCfg *api.StackConfigCompose) (any, error) {
	templateCfg, ok := tpl.(*CloudRunTemplate)
	if !ok {
		// Legacy stub-era shape: `*TemplateConfig` without the new fields.
		if legacy, legacyOK := tpl.(*TemplateConfig); legacyOK {
			templateCfg = &CloudRunTemplate{TemplateConfig: *legacy}
		} else {
			return nil, errors.Errorf("template config is not of type *gcp.CloudRunTemplate or *gcp.TemplateConfig")
		}
	}
	if templateCfg == nil {
		return nil, errors.Errorf("template config is nil")
	}

	res := &CloudRunInput{
		CloudRunTemplate: *templateCfg,
		Deployment: k8s.DeploymentConfig{
			StackConfig: stackCfg,
		},
	}
	containers, err := k8s.ConvertComposeToContainers(composeCfg, stackCfg)
	if err != nil {
		return nil, err
	}
	res.Deployment.Containers = containers

	iContainer, err := k8s.FindIngressContainer(composeCfg, containers)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to detect ingress container")
	}
	res.Deployment.IngressContainer = iContainer

	return res, nil
}
