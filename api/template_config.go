// SPDX-License-Identifier: MIT
// Copyright (c) Simple Container

package gcp

import "github.com/simple-container-com/api/pkg/api"

// TemplateConfig is the parent-side template config block every GCP
// plugin template type (cloudrun, gcp-static-website, gcp-gke-autopilot)
// reads its credentials/project out of. Embeds the GCP-side Credentials
// (which in turn embeds api.Credentials — the auth block common to
// providers, parent templates and resources) so a parent descriptor can
// reuse an auth: block via a YAML anchor without re-declaring keys.
//
// Lifted from api `pkg/clouds/gcloud/gcloud.go`.
type TemplateConfig struct {
	Credentials `json:",inline" yaml:",inline"`
}

// ReadTemplateConfig is the parse-time reader registered against every
// `type:` discriminator shared by GCP template types. It turns the raw
// `api.Config` the YAML decoder leaves behind into the typed
// `*TemplateConfig` downstream converters expect.
func ReadTemplateConfig(config *api.Config) (api.Config, error) {
	return api.ConvertConfig(config, &TemplateConfig{})
}
