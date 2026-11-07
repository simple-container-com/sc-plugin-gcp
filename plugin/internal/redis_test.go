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

// TestURNContract_Redis_WrongType pins the type-guard refusal on the
// Redis Provision path — mirror of the equivalent bucket + pubsub test.
// Code path returns before any RegisterResource call, so no mock
// harness needed.
func TestURNContract_Redis_WrongType(t *testing.T) {
	RegisterTestingT(t)

	m := &bucketMocks{}
	withBucketMockContext(t, m, func(ctx *pulumi.Context) error {
		input := api.ResourceInput{
			Descriptor: &api.ResourceDescriptor{
				Type:   "gcp-bucket", // wrong type
				Name:   "x",
				Config: api.Config{Config: &gcpApi.RedisConfig{}},
			},
		}
		_, err := Redis(ctx, api.Stack{Name: "test-stack"}, input, pApi.ProvisionParams{Log: logger.New()})
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("unsupported redis type"))
		return nil
	})
	Expect(m.sortedNames()).To(BeEmpty(), "no resources should be registered on type guard refusal")
}

// TestURNContract_AdoptRedis_RequiresAdoptFlag pins that AdoptRedis
// refuses when called with cfg.Adopt == false — the normal Redis path
// is responsible for delegating to adopt only when the operator set
// the flag. Guards against a future refactor that calls AdoptRedis
// unconditionally.
func TestURNContract_AdoptRedis_RequiresAdoptFlag(t *testing.T) {
	RegisterTestingT(t)

	m := &bucketMocks{}
	withBucketMockContext(t, m, func(ctx *pulumi.Context) error {
		input := api.ResourceInput{
			Descriptor: &api.ResourceDescriptor{
				Type:   gcpApi.ResourceTypeRedis,
				Name:   "my-redis",
				Config: api.Config{Config: &gcpApi.RedisConfig{Adopt: false}},
			},
		}
		_, err := AdoptRedis(ctx, api.Stack{Name: "test-stack"}, input, pApi.ProvisionParams{Log: logger.New()})
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("adopt flag not set"))
		return nil
	})
}
