package gcp

import (
	"encoding/json"
	"testing"

	"github.com/simple-container-com/api/pkg/api"
	pApi "github.com/simple-container-com/api/pkg/clouds/pulumi/api"
	scpluginv1 "github.com/simple-container-com/api/pkg/plugin/proto/v1"
	"github.com/simple-container-com/api/pkg/plugin/runtime"
)

// A consumer in env "tenant-a" under parentEnv "production" must get the
// owner's "tenant-a" database. The request goes through the same decode and
// input construction as the ComputeProcess handler: the runtime swaps
// StackParams.Environment to the parent's, so the name has to come from
// ParentInfo.StackEnv, and StackEnv has to survive the wire.
func TestDependencyDatabaseName_ChildEnvUnderParentEnv(t *testing.T) {
	wire, err := json.Marshal(pApi.ParentInfo{ParentEnv: "production", StackEnv: "tenant-a", ResourceEnv: "tenant-a"})
	if err != nil {
		t.Fatal(err)
	}
	req := &scpluginv1.ComputeProcessRequest{
		Header:         &scpluginv1.RequestHeader{StackName: "worker--tenant-a", Environment: "tenant-a"},
		ResourceType:   "gcp-cloudsql-postgres",
		ResourceName:   "postgres",
		ParentInfoJson: wire,
	}
	parent, err := runtime.ParentInfoFromRequest(req)
	if err != nil {
		t.Fatal(err)
	}
	if parent == nil || parent.StackEnv != "tenant-a" {
		t.Fatalf("ParentInfo.StackEnv did not survive the wire: %+v", parent)
	}
	input := runtime.ResourceInputForComputeProcess(req, parent, nil)
	if input.StackParams.Environment != "production" {
		t.Fatalf("precondition: the runtime is expected to hand the parent env, got %q", input.StackParams.Environment)
	}
	got, err := dependencyDatabaseName("api", input, parent)
	if err != nil {
		t.Fatal(err)
	}
	if want := "api--tenant-a"; got != want {
		t.Fatalf("database = %q, want %q", got, want)
	}
}

func TestDependencyDatabaseName(t *testing.T) {
	in := func(env string) api.ResourceInput {
		return api.ResourceInput{StackParams: &api.StackParams{Environment: env}}
	}
	for name, tc := range map[string]struct {
		input   api.ResourceInput
		parent  *pApi.ParentInfo
		want    string
		wantErr bool
	}{
		"same env":                    {input: in("production"), parent: &pApi.ParentInfo{ParentEnv: "production", StackEnv: "production"}, want: "api--production"},
		"child env differs":           {input: in("production"), parent: &pApi.ParentInfo{ParentEnv: "production", StackEnv: "tenant-b"}, want: "api--tenant-b"},
		"child env wins without swap": {input: in("tenant-a"), parent: &pApi.ParentInfo{ParentEnv: "production", StackEnv: "tenant-a"}, want: "api--tenant-a"},
		// An explicit `env:` on the dependency (ResourceEnv) never chose the
		// database before the split either: it stays the consumer's own.
		"explicit dependency env is ignored": {input: in("production"), parent: &pApi.ParentInfo{ParentEnv: "production", StackEnv: "tenant-a", ResourceEnv: "production"}, want: "api--tenant-a"},
		"no parent info":                     {input: in("staging"), want: "api--staging"},
		// Environment is the parent's when ParentEnv is set: refuse to guess.
		"parent env without child env": {input: in("production"), parent: &pApi.ParentInfo{ParentEnv: "production"}, wantErr: true},
		"no environment at all":        {input: api.ResourceInput{}, wantErr: true},
	} {
		t.Run(name, func(t *testing.T) {
			got, err := dependencyDatabaseName("api", tc.input, tc.parent)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected an error, got database %q", got)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if got != tc.want {
				t.Fatalf("database = %q, want %q", got, tc.want)
			}
		})
	}
}
