// SPDX-License-Identifier: MIT
// Copyright (c) Simple Container

package gcp

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	. "github.com/onsi/gomega"

	"github.com/simple-container-com/api/pkg/api/logger"

	gcpApi "github.com/simple-container-com/sc-plugin-gcp/api"
)

type gcloudCall struct {
	env  []string
	args []string
}

func stubGcloud(t *testing.T, fail error) *[]gcloudCall {
	t.Helper()
	calls := &[]gcloudCall{}
	prev := runGcloud
	runGcloud = func(extraEnv []string, args ...string) ([]byte, error) {
		*calls = append(*calls, gcloudCall{env: extraEnv, args: args})
		return nil, fail
	}
	t.Cleanup(func() { runGcloud = prev })
	return calls
}

func stateCfg(credentials string) *gcpApi.StateStorageConfig {
	cfg := &gcpApi.StateStorageConfig{BucketName: "state-bucket"}
	cfg.Credentials.Credentials.Credentials = credentials
	cfg.ProjectId = "test-project"
	return cfg
}

// SC core runs in another process: whatever it must see comes back as env.
// In key mode that is the key for the gs:// backend, and gcloud is signed in
// with the same key for GKE kubeconfigs.
func TestInitStateStore_KeyModeReturnsCredentialsAndActivatesGcloud(t *testing.T) {
	RegisterTestingT(t)
	calls := stubGcloud(t, nil)

	env, err := InitStateStore(context.Background(), stateCfg(`{"type":"service_account"}`), logger.New())
	Expect(err).ToNot(HaveOccurred())
	Expect(env).To(Equal(map[string]string{"GOOGLE_CREDENTIALS": `{"type":"service_account"}`}))
	Expect(*calls).To(HaveLen(1))
	Expect((*calls)[0].args[:3]).To(Equal([]string{"auth", "activate-service-account", "--key-file"}))
	_, statErr := os.Stat((*calls)[0].args[3])
	Expect(os.IsNotExist(statErr)).To(BeTrue(), "the temporary key file must not outlive the call")
}

// In ambient mode there is no key to hand over. gcloud is signed in to a
// private configuration, and core is told where it is; the directory must
// still exist after the call, because core uses it for the rest of the run.
func TestInitStateStore_AmbientModeReturnsPrivateGcloudConfig(t *testing.T) {
	RegisterTestingT(t)
	calls := stubGcloud(t, nil)
	credFile := filepath.Join(t.TempDir(), "adc.json")
	Expect(os.WriteFile(credFile, []byte("{}"), 0o600)).To(Succeed())
	t.Setenv("GOOGLE_APPLICATION_CREDENTIALS", credFile)
	t.Setenv("GITHUB_ACTIONS", "true")

	env, err := InitStateStore(context.Background(), stateCfg(""), logger.New())
	Expect(err).ToNot(HaveOccurred())
	Expect(env).ToNot(HaveKey("GOOGLE_CREDENTIALS"))
	configDir := env["CLOUDSDK_CONFIG"]
	Expect(configDir).ToNot(BeEmpty())
	t.Cleanup(func() { _ = os.RemoveAll(configDir) })
	Expect(configDir).To(BeADirectory())
	Expect(os.Getenv("CLOUDSDK_CONFIG")).ToNot(Equal(configDir), "the plugin's own environment is not switched")

	Expect(*calls).To(HaveLen(1))
	Expect((*calls)[0].env).To(Equal([]string{"CLOUDSDK_CONFIG=" + configDir}))
	Expect(strings.Join((*calls)[0].args, " ")).To(Equal("auth login --cred-file=" + credFile + " --quiet"))
}

func TestInitStateStore_AmbientModeOutsideGithubActionsLeavesGcloudAlone(t *testing.T) {
	RegisterTestingT(t)
	calls := stubGcloud(t, nil)
	t.Setenv("GOOGLE_APPLICATION_CREDENTIALS", "/nonexistent")
	t.Setenv("GITHUB_ACTIONS", "")

	env, err := InitStateStore(context.Background(), stateCfg(""), logger.New())
	Expect(err).ToNot(HaveOccurred())
	Expect(env).To(BeEmpty())
	Expect(*calls).To(BeEmpty())
}

// A failed sign-in must not hand core a configuration with no account in it.
func TestInitStateStore_AmbientSignInFailureReturnsNoConfig(t *testing.T) {
	RegisterTestingT(t)
	calls := stubGcloud(t, os.ErrPermission)
	credFile := filepath.Join(t.TempDir(), "adc.json")
	Expect(os.WriteFile(credFile, []byte("{}"), 0o600)).To(Succeed())
	t.Setenv("GOOGLE_APPLICATION_CREDENTIALS", credFile)
	t.Setenv("GITHUB_ACTIONS", "true")

	env, err := InitStateStore(context.Background(), stateCfg(""), logger.New())
	Expect(err).ToNot(HaveOccurred())
	Expect(env).To(BeEmpty())
	Expect(*calls).To(HaveLen(1))
	dir := strings.TrimPrefix((*calls)[0].env[0], "CLOUDSDK_CONFIG=")
	Expect(dir).ToNot(BeADirectory())
}
