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
// yet (the Pulumi-side Cloudrun handler is a `not implemented` stub),
// but they shape the parent YAML operators already have on disk.
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

// CloudRunInput is the typed Input SC core's PrepareCloudComposeForDeploy
// path (now the plugin's ConvertStackConfig handler) produces for a
// `type: cloudrun` client stack. Lifted from in-tree pkg/clouds/gcloud
// unchanged so the YAML shape existing parent/child stacks have on disk
// keeps parsing byte-identically.
type CloudRunInput struct {
	TemplateConfig `json:"templateConfig" yaml:"templateConfig"`
	Deployment     k8s.DeploymentConfig `json:"deployment" yaml:"deployment"`
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

// ToCloudRunConfig is the converter closure the pre-A2 world registered
// under cloudComposeConverterMapping. Post-A2 the plugin's
// ConvertStackConfig handler calls it directly after decoding the
// merged compose config from stackDir — same signature, same body.
func ToCloudRunConfig(tpl any, composeCfg compose.Config, stackCfg *api.StackConfigCompose) (any, error) {
	templateCfg, ok := tpl.(*TemplateConfig)
	if !ok {
		return nil, errors.Errorf("template config is not of type *gcp.TemplateConfig")
	}
	if templateCfg == nil {
		return nil, errors.Errorf("template config is nil")
	}

	res := &CloudRunInput{
		TemplateConfig: *templateCfg,
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
