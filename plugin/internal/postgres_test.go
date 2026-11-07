// SPDX-License-Identifier: MIT
// Copyright (c) Simple Container

package gcp

import (
	"testing"

	. "github.com/onsi/gomega"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
	"github.com/simple-container-com/api/pkg/api"
	"github.com/simple-container-com/api/pkg/api/logger"
	pApi "github.com/simple-container-com/api/pkg/clouds/pulumi/api"

	gcpApi "github.com/simple-container-com/sc-plugin-gcp/api"
)

// TestURNContract_Postgres_WrongType pins the type-guard refusal on the
// Postgres Provision path — mirror of bucket/pubsub/redis tests. Code
// path returns before any RegisterResource call, so no mock harness
// needed beyond the shared `bucketMocks`.
func TestURNContract_Postgres_WrongType(t *testing.T) {
	RegisterTestingT(t)

	m := &bucketMocks{}
	withBucketMockContext(t, m, func(ctx *pulumi.Context) error {
		input := api.ResourceInput{
			Descriptor: &api.ResourceDescriptor{
				Type:   "gcp-bucket", // wrong type
				Name:   "x",
				Config: api.Config{Config: &gcpApi.PostgresGcpCloudsqlConfig{}},
			},
		}
		_, err := Postgres(ctx, api.Stack{Name: "test-stack"}, input, pApi.ProvisionParams{Log: logger.New()})
		Expect(err).To(HaveOccurred())
		return nil
	})
	Expect(m.sortedNames()).To(BeEmpty())
}

// TestPostgresConfig_Validate exercises the Validate() invariants
// in-tree carries — availabilityType whitelist, privateNetwork path
// shape, proxyPrivateIp requires privateNetwork, publicIpEnabled
// coherence. These run at parse-time via the api.ConvertConfig path
// (in-tree today, opaque-path plugin side post-removal — Trap 19 —
// which is why Validate() must be called explicitly in Provision when
// the extraction removes in-tree gcloud).
func TestPostgresConfig_Validate(t *testing.T) {
	RegisterTestingT(t)

	// Public IP disabled without private network.
	publicOff := false
	cfg := &gcpApi.PostgresGcpCloudsqlConfig{PublicIpEnabled: &publicOff}
	err := cfg.Validate()
	Expect(err).To(HaveOccurred())
	Expect(err.Error()).To(ContainSubstring("requires privateNetwork"))

	// proxyPrivateIp true without private network.
	proxyPrivate := true
	cfg = &gcpApi.PostgresGcpCloudsqlConfig{ProxyPrivateIp: &proxyPrivate}
	err = cfg.Validate()
	Expect(err).To(HaveOccurred())
	Expect(err.Error()).To(ContainSubstring("proxyPrivateIp"))

	// availabilityType invalid.
	badAvail := "BOGUS"
	cfg = &gcpApi.PostgresGcpCloudsqlConfig{AvailabilityType: &badAvail}
	err = cfg.Validate()
	Expect(err).To(HaveOccurred())
	Expect(err.Error()).To(ContainSubstring("availabilityType"))
}
