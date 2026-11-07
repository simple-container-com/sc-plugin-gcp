// SPDX-License-Identifier: MIT
// Copyright (c) Simple Container

package gcp

import (
	"github.com/pkg/errors"
	"github.com/simple-container-com/api/pkg/api"
)

// TemplateTypeStaticWebsite is the template discriminator for a GCS-
// backed static website. Preserved as "gcp-static-website" for
// byte-identical URN continuity across the plugin split.
const TemplateTypeStaticWebsite = "gcp-static-website"

// StaticSiteInput is the typed Input SC core's PrepareStaticForDeploy
// (now the plugin's ConvertStackConfig handler) produces for a
// `type: gcp-static-website` client stack. Satisfies
// api.ResourceAware (via embedded StackConfigStatic.Uses) and
// api.DnsConfigAware (OverriddenBaseZone).
type StaticSiteInput struct {
	TemplateConfig         `json:"templateConfig" yaml:"templateConfig"`
	*api.StackConfigStatic `json:",inline" yaml:",inline"`
	StackDir               string `json:"stackDir" yaml:"stackDir"`
	StackName              string `json:"stackName" yaml:"stackName"`
	Location               string `json:"location" yaml:"location"`
	BaseDnsZone            string `json:"baseDnsZone" yaml:"baseDnsZone"`
}

// OverriddenBaseZone satisfies api.DnsConfigAware.
func (i *StaticSiteInput) OverriddenBaseZone() string {
	return i.BaseDnsZone
}

// ToStaticSiteConfig is the converter closure the pre-A2 world
// registered under cloudStaticSiteConverterMapping. Post-A2 the
// plugin's ConvertStackConfig handler calls it directly after
// decoding stackCfg from the bus. Lifted verbatim from in-tree
// `pkg/clouds/gcloud/static_website.go`.
func ToStaticSiteConfig(tpl any, stackDir, stackName string, stackCfg *api.StackConfigStatic) (any, error) {
	templateCfg, ok := tpl.(*TemplateConfig)
	if !ok {
		return nil, errors.Errorf("template config is not of type *gcp.TemplateConfig")
	}
	if templateCfg == nil {
		return nil, errors.Errorf("template config is nil")
	}

	res := &StaticSiteInput{
		TemplateConfig:    *templateCfg,
		StackConfigStatic: stackCfg,
		StackDir:          stackDir,
		StackName:         stackName,
		BaseDnsZone:       stackCfg.Site.BaseDnsZone,
		Location:          stackCfg.Location,
	}

	if stackCfg.Location == "" {
		return nil, errors.Errorf("location is required for gcp bucket")
	}

	return res, nil
}
