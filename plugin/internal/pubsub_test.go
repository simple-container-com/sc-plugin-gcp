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

// TestURNContract_PubSubTopics_WrongType pins the type-guard refusal on
// the PubSubTopics Provision path — mirror of the equivalent bucket
// test. Code path returns before any RegisterResource call, so no mock
// harness needed.
func TestURNContract_PubSubTopics_WrongType(t *testing.T) {
	RegisterTestingT(t)

	m := &bucketMocks{}
	withBucketMockContext(t, m, func(ctx *pulumi.Context) error {
		input := api.ResourceInput{
			Descriptor: &api.ResourceDescriptor{
				Type:   "gcp-redis", // wrong type
				Name:   "x",
				Config: api.Config{Config: &gcpApi.PubSubConfig{}},
			},
		}
		_, err := PubSubTopics(ctx, api.Stack{Name: "test-stack"}, input, pApi.ProvisionParams{Log: logger.New()})
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("unsupported pubsub topics type"))
		return nil
	})
	Expect(m.sortedNames()).To(BeEmpty(), "no resources should be registered on type guard refusal")
}
