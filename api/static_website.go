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
	// NOTE: `Location` + `BaseDnsZone` are intentionally NOT declared
	// here — they are inherited from the embedded *api.StackConfigStatic
	// (Location directly, BaseDnsZone via the embedded Site). Declaring
	// either at this level built an in-tree struct with two YAML tags for
	// the same key, which yaml.v3 refuses on unmarshal with
	// `panic: duplicated key '<field>' in struct gcp.StaticSiteInput`.
	// The in-tree path never exercised the yaml.v3 round-trip because SC
	// core decoded structs via mapstructure; the plugin bus does yaml-pack
	// (pkg/plugin/runtime/decode.go), so the duplicate blew up on the
	// first gcp-static-website provision.
}

// OverriddenBaseZone satisfies api.DnsConfigAware.
func (i *StaticSiteInput) OverriddenBaseZone() string {
	if i.StackConfigStatic == nil {
		return ""
	}
	return i.Site.BaseDnsZone
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
	}

	if stackCfg.Location == "" {
		return nil, errors.Errorf("location is required for gcp bucket")
	}

	return res, nil
}
