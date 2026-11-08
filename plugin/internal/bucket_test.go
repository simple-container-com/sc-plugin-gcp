// SPDX-License-Identifier: MIT
// Copyright (c) Simple Container

package gcp

import (
	"context"
	"sort"
	"sync"
	"testing"

	. "github.com/onsi/gomega"
	"github.com/pulumi/pulumi/sdk/v3/go/common/resource"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
	"github.com/simple-container-com/api/pkg/api"
	"github.com/simple-container-com/api/pkg/api/logger"
	pApi "github.com/simple-container-com/api/pkg/clouds/pulumi/api"

	gcpApi "github.com/simple-container-com/sc-plugin-gcp/api"
)

// Why this file exists: SC's extraction promise is that no pre-split
// stack destroys resources on first plugin-era deploy. That only holds
// if the plugin registers resources under the same Pulumi URN names the
// in-tree code did. These tests pin the URN contract for gcp-bucket.
//
// Why `pulumi.NewContext` + `RunInfo.Mocks` instead of `pulumi.RunErr`:
// `RunErr` ends in the context's unexported `wait()`, which drains the
// engine's RPC queue — the harness itself silently completes
// registrations the handler stranded. That hid a whole class of
// await-returns bugs behind green suites (Addendum R §R3 of
// 04-no-destroy-invariant.md). The pattern below lands assertions on
// the handler's RETURN, not the harness's drain. Every plugin's test
// suite must copy this pattern, not the RunErr one.

type bucketMocks struct {
	mu       sync.Mutex
	captured []pulumi.MockResourceArgs
}

func (m *bucketMocks) NewResource(args pulumi.MockResourceArgs) (string, resource.PropertyMap, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.captured = append(m.captured, args)
	return args.Name + "-mock-id", args.Inputs, nil
}

func (m *bucketMocks) Call(args pulumi.MockCallArgs) (resource.PropertyMap, error) {
	return args.Args, nil
}

func (m *bucketMocks) typesByName() map[string]string {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := map[string]string{}
	for _, a := range m.captured {
		out[a.Name] = a.TypeToken
	}
	return out
}

func (m *bucketMocks) sortedNames() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]string, 0, len(m.captured))
	for _, a := range m.captured {
		out = append(out, a.Name+"::"+a.TypeToken)
	}
	sort.Strings(out)
	return out
}

func withBucketMockContext(t *testing.T, mocks *bucketMocks, fn func(*pulumi.Context) error) {
	t.Helper()
	pctx, err := pulumi.NewContext(context.Background(), pulumi.RunInfo{
		Project: "sc-plugin-gcp-test",
		Stack:   "test-stack",
		Mocks:   mocks,
	})
	Expect(err).ToNot(HaveOccurred())
	defer pctx.Close()
	Expect(fn(pctx)).To(Succeed())
}

// Full URN-contract pinning (bucket + SA + 2 IAM members + HMAC) is
// deferred to a later slice: the mock harness's `NewResource` callbacks
// for pulumi-gcp's async-registered resources fire from background
// goroutines that are not awaited by `pctx.Close()` under mocks, so
// an assertion placed on handler-return observes only the synchronous
// Provider registration. Fixing it correctly either requires a
// WaitGroup drain anchored on each resource Output (bespoke scaffolding
// per resource type) or the `pulumi.RunErr + WithMocks` pattern that
// has its own trap (silent drain-hiding, see api Addendum R §R3).
//
// The minimum walking-skeleton proof of the lift is covered by the
// WrongType test below (code path that returns before the first
// RegisterResource call), the plugin binary builds + releases via GHA,
// and the config-decode round-trip tests in main_test.go (future). The
// real URN parity check happens in the live `sc preview` roundtrip
// once credentials are available.

// TestURNContract_PrivateBucket_WrongType pins the type-guard refusal.
func TestURNContract_PrivateBucket_WrongType(t *testing.T) {
	RegisterTestingT(t)

	m := &bucketMocks{}
	withBucketMockContext(t, m, func(ctx *pulumi.Context) error {
		input := api.ResourceInput{
			Descriptor: &api.ResourceDescriptor{
				Type:   "gcp-pubsub", // wrong type
				Name:   "x",
				Config: api.Config{Config: &gcpApi.GcpBucket{}},
			},
		}
		_, err := PrivateBucket(ctx, api.Stack{Name: "test-stack"}, input, pApi.ProvisionParams{Log: logger.New()})
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("unsupported bucket type"))
		return nil
	})
	Expect(m.sortedNames()).To(BeEmpty(), "no resources should be registered on type guard refusal")
}
