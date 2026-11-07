// SPDX-License-Identifier: MIT
// Copyright (c) Simple Container

package gcp

import (
	"context"
	"strings"
	"time"

	"google.golang.org/api/serviceusage/v1"

	"github.com/pkg/errors"
	"github.com/simple-container-com/api/pkg/api"
)

// Lifted from api `pkg/clouds/pulumi/gcp/services_api.go` + the
// `initServicesAPIClient` helper that lived in `kms_key.go`. The
// in-tree file also carries a mock-client harness the in-tree tests
// use; that stays in-tree until the test infrastructure lifts too.
// Only the production path lifts here.

// servicesAPIClient interface for enabling GCP services.
type servicesAPIClient interface {
	EnableService(ctx context.Context, authConfig any, apiName string) error
}

// defaultServicesAPIClient implements servicesAPIClient using real GCP APIs.
type defaultServicesAPIClient struct{}

// EnableService enables a GCP service API.
func (c *defaultServicesAPIClient) EnableService(ctx context.Context, authConfig any, apiName string) error {
	return enableServicesAPIImpl(ctx, authConfig, apiName)
}

// servicesAPIClientKey is a context key for the services API client.
type servicesAPIClientKey struct{}

// globalServicesAPIClient is the default-fallback client when the
// context has no explicit injection.
var globalServicesAPIClient servicesAPIClient = &defaultServicesAPIClient{}

// enableServicesAPI enables a GCP service API using the client from
// context, global, or default (in that order). The resource-provision
// path calls this before registering any resource that depends on an
// optional API (e.g. `redis.googleapis.com` for `gcp-redis`).
func enableServicesAPI(ctx context.Context, authConfig any, apiName string) error {
	client := getServicesAPIClient(ctx)
	return client.EnableService(ctx, authConfig, apiName)
}

// getServicesAPIClient gets the services API client from context,
// global, or returns default.
func getServicesAPIClient(ctx context.Context) servicesAPIClient {
	if client, ok := ctx.Value(servicesAPIClientKey{}).(servicesAPIClient); ok {
		return client
	}
	if globalServicesAPIClient != nil {
		return globalServicesAPIClient
	}
	return &defaultServicesAPIClient{}
}

// initServicesAPIClient builds a `serviceusage.Service` from the
// resource's auth config. Lifted from `kms_key.go` where it originally
// lived next to the KMS resource that first needed it.
func initServicesAPIClient(ctx context.Context, resourceConfig any) (*serviceusage.Service, error) {
	authCfg, ok := resourceConfig.(api.AuthConfig)
	if !ok {
		return nil, errors.Errorf("failed to convert config to api.AuthConfig")
	}
	svc, err := serviceusage.NewService(ctx, clientOptions(authCfg.CredentialsValue())...)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to init google API services client")
	}
	return svc, nil
}

// enableServicesAPIImpl is the production implementation. Enables an
// API if not already enabled, then polls the operation to completion
// (up to 60 seconds). Lifted verbatim.
func enableServicesAPIImpl(ctx context.Context, authConfig any, apiName string) error {
	svc, err := initServicesAPIClient(ctx, authConfig)
	if err != nil {
		return errors.Wrapf(err, "failed to init services API client")
	}

	if info, err := svc.Services.Get(apiName).Do(); err == nil {
		if info.State == "ENABLED" {
			return nil
		}
	}
	op, err := svc.Services.Enable(apiName, &serviceusage.EnableServiceRequest{}).Do()
	if err != nil {
		return errors.Wrapf(err, "failed to enable %s", apiName)
	}

	if op.Done {
		if op.Error != nil {
			return errors.Errorf("failed to enable API %q: %s", apiName, op.Error.Message)
		}
		return nil
	}

	maxRetries := 60 // Maximum 60 seconds
	for i := 0; i < maxRetries; i++ {
		select {
		case <-ctx.Done():
			return errors.Wrapf(ctx.Err(), "context cancelled while enabling API %q", apiName)
		default:
		}

		op, err = svc.Operations.Get(op.Name).Do()
		if err != nil {
			// Handle specific "DONE_OPERATION" error — the API's own
			// idempotence-check race.
			if strings.Contains(err.Error(), "DONE_OPERATION") {
				if info, checkErr := svc.Services.Get(apiName).Do(); checkErr == nil {
					if info.State == "ENABLED" {
						return nil
					}
				}
				return errors.Wrapf(err, "API enablement operation completed with error for %q", apiName)
			}
			return errors.Wrapf(err, "failed to check operation status for API %q", apiName)
		}

		if op.Done {
			if op.Error != nil {
				return errors.Errorf("failed to enable API %q: %s", apiName, op.Error.Message)
			}
			return nil
		}

		time.Sleep(1 * time.Second)
	}

	return errors.Errorf("timeout waiting for API %q to be enabled after %d seconds", apiName, maxRetries)
}
