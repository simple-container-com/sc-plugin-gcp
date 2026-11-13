// SPDX-License-Identifier: MIT
// Copyright (c) Simple Container

package gcp

import (
	"context"
	"fmt"
)

// mockServicesAPIClient stands in for the Service Usage API in tests.
type mockServicesAPIClient struct {
	enabledServices map[string]bool
	shouldFail      map[string]bool
}

func newMockServicesAPIClient() *mockServicesAPIClient {
	return &mockServicesAPIClient{enabledServices: map[string]bool{}, shouldFail: map[string]bool{}}
}

func (c *mockServicesAPIClient) EnableService(_ context.Context, _ any, apiName string) error {
	if c.shouldFail[apiName] {
		return fmt.Errorf("mock failure for service %s", apiName)
	}
	c.enabledServices[apiName] = true
	return nil
}

func setGlobalServicesAPIClient(client servicesAPIClient) { globalServicesAPIClient = client }

func resetGlobalServicesAPIClient() { globalServicesAPIClient = &defaultServicesAPIClient{} }
