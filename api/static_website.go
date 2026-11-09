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
	*api.StackConfigStatic `json:"-" yaml:"-"`
	StackDir               string `json:"stackDir" yaml:"stackDir"`
	StackName              string `json:"stackName" yaml:"stackName"`

	// Flat scalar fields the plugin body reads. Populated by
	// `ToStaticSiteConfig` from the embedded `*api.StackConfigStatic` so
	// they SURVIVE the plugin-bus JSON+YAML roundtrip. See the asymmetry
	// note on `StackConfigStatic` above the `json:"-" yaml:"-"` tag.
	//
	// Why this exists: `encoding/json` ignores the `,inline` tag (it's a
	// yaml.v3 convention, not a Go stdlib one), so a nested-inline-with-
	// pointer embed — `*api.StackConfigStatic` → inline Site
	// StaticSiteConfig → inline fields — serialises to JSON as a nested
	// object (keyed on the Go field name). On the plugin side yaml.v3's
	// `inline` decoder then expects FLATTENED fields at the top, can't
	// find them, and leaves every Site field zero. The body reading
	// `in.Site.Domain` got "" every time, and the CF plugin's
	// NewDnsRecord / NewOverrideHeaderRule received empty names that CF
	// rejected with `9000 DNS name is invalid` / `10022 Route pattern
	// must include zone name`. Live-caught on #568's preview-publish
	// workflow against `dist.simple-container.com`, 2026-11-08/09.
	//
	// The embed stays on the Go side (methods like `Uses()` on
	// `*StackConfigStatic` are called by SC core via interface assertion,
	// so the ambient fields must stay reachable), but is marked `json:"-"
	// yaml:"-"` so it doesn't get serialised with the broken inline
	// shape. Explicit scalar fields below carry what the plugin body
	// actually reads.
	Domain        string `json:"domain" yaml:"domain"`
	BaseDnsZone   string `json:"baseDnsZone" yaml:"baseDnsZone"`
	IndexDocument string `json:"indexDocument" yaml:"indexDocument"`
	ErrorDocument string `json:"errorDocument" yaml:"errorDocument"`
	BundleDir     string `json:"bundleDir" yaml:"bundleDir"`
	BucketName    string `json:"bucketName" yaml:"bucketName"`
	Location      string `json:"location" yaml:"location"`
}

// OverriddenBaseZone satisfies api.DnsConfigAware. Reads the flat
// BaseDnsZone field (set by ToStaticSiteConfig), which survives the
// plugin-bus roundtrip. The embedded Site chain does not — see the
// struct comment.
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
		// Flat scalar mirror of the embedded chain — survives the
		// plugin-bus JSON+YAML roundtrip (see struct comment).
		Domain:        stackCfg.Site.Domain,
		BaseDnsZone:   stackCfg.Site.BaseDnsZone,
		IndexDocument: stackCfg.Site.IndexDocument,
		ErrorDocument: stackCfg.Site.ErrorDocument,
		BundleDir:     stackCfg.BundleDir,
		BucketName:    stackCfg.BucketName,
		Location:      stackCfg.Location,
	}

	if stackCfg.Location == "" {
		return nil, errors.Errorf("location is required for gcp bucket")
	}

	return res, nil
}
