package gcp

import (
	"testing"

	"github.com/simple-container-com/api/pkg/api"
)

func TestCredentialsValue_AmbientForms(t *testing.T) {
	key := `{"type":"service_account","project_id":"p","private_key":"k","client_email":"a@p.iam.gserviceaccount.com"}`
	type tcase struct {
		in      string
		want    string
		ambient bool
	}
	// same: the value must come back untouched and must not read as ambient.
	same := func(v string) tcase { return tcase{in: v, want: v} }
	for name, tc := range map[string]tcase{
		"empty":                         {in: "", want: "", ambient: true},
		"blank":                         {in: "  ", want: "  ", ambient: true},
		"envelope":                      {in: `{"credentials":"","projectId":"p"}`, want: "", ambient: true},
		"envelope with service account": {in: `{"credentials":"","projectId":"p","serviceAccount":"d@p.iam.gserviceaccount.com"}`, want: "", ambient: true},
		"envelope, credentials only":    {in: " {\"credentials\": \"\"}\n", want: "", ambient: true},
		"service account key":           same(key),
		"external account key":          same(`{"type":"external_account","audience":"x"}`),
		"authorized user":               same(`{"type":"authorized_user","refresh_token":"r","client_id":"c"}`),
		"impersonated service account":  same(`{"type":"impersonated_service_account","source_credentials":{}}`),
		"private key without type":      same(`{"private_key":"k","credentials":""}`),
		"key with an empty credentials": same(`{"type":"service_account","credentials":""}`),
		"key nested in the envelope":    same(`{"credentials":"{\"type\":\"service_account\"}","projectId":"p"}`),
		"non-string credentials":        same(`{"credentials":{"type":"service_account"},"projectId":"p"}`),
		"null credentials":              same(`{"credentials":null,"projectId":"p"}`),
		"blank credentials":             same(`{"credentials":" ","projectId":"p"}`),
		"unknown sibling":               same(`{"credentials":"","projectId":"p","refresh_token":"r"}`),
		"nested sibling":                same(`{"credentials":"","projectId":"p","config":{"credentials":{"private_key":"k"}}}`),
		"repeated member":               same(`{"credentials":"configured-key","credentials":""}`),
		"non-string projectId":          same(`{"credentials":"","projectId":1}`),
		"no credentials member":         same(`{"projectId":"p"}`),
		"trailing data":                 same(`{"credentials":""} {"x":1}`),
		"malformed json":                same(`{"credentials":""`),
		"placeholder":                   same("${auth:gcloud}"),
		"secret placeholder":            same("${secret:gcp-key}"),
		"placeholder in the envelope":   same(`{"credentials":"${secret:gcp-key}","projectId":"p"}`),
	} {
		t.Run(name, func(t *testing.T) {
			c := &Credentials{Credentials: api.Credentials{Credentials: tc.in}}
			if got := c.CredentialsValue(); got != tc.want {
				t.Fatalf("CredentialsValue() = %q, want %q", got, tc.want)
			}
			if got := c.UsesAmbientCredentials(); got != tc.ambient {
				t.Fatalf("UsesAmbientCredentials() = %v, want %v", got, tc.ambient)
			}
		})
	}
}
