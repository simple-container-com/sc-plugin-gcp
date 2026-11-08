// SPDX-License-Identifier: MIT
// Copyright (c) Simple Container

package gcp

import (
	"fmt"

	"github.com/pkg/errors"
	"github.com/pulumi/pulumi-gcp/sdk/v8/go/gcp/cloudrun"
	"github.com/pulumi/pulumi-gcp/sdk/v8/go/gcp/cloudrunv2"
	sdk "github.com/pulumi/pulumi/sdk/v3/go/pulumi"
	"github.com/samber/lo"
	"github.com/simple-container-com/api/pkg/api"
	"github.com/simple-container-com/api/pkg/api/logger"
	"github.com/simple-container-com/api/pkg/clouds/k8s"
	pApi "github.com/simple-container-com/api/pkg/clouds/pulumi/api"
	"github.com/simple-container-com/api/pkg/clouds/pulumi/kubernetes"

	gcpApi "github.com/simple-container-com/sc-plugin-gcp/api"
)

// Cloudrun provisions a Google Cloud Run v2 Service from a compose-style
// client stack. Supports:
//
//   - **Multi-container** revisions. Cloud Run v2 serves traffic through
//     one ingress container (the one that binds `$PORT`) and any number
//     of sidecars. The ingress mapping is derived from the compose
//     config via `k8s.FindIngressContainer`; everything else goes in as
//     a sidecar.
//   - **Image build + push** to a parent-stack `gcp-artifact-registry`
//     resource, using the same `kubernetes.BuildAndPushImages` helper
//     the gke-autopilot template uses. Operators configure the target
//     registry via the template's `artifactRegistryResource` field.
//   - **Scale knobs** from `StackConfigCompose.Scale` (min/max instance
//     count) and `MaxInstanceRequestConcurrency` (defaults to Cloud
//     Run's own 80).
//   - **Public access** via an `roles/run.invoker` → `allUsers` IAM
//     binding. Non-public services are a follow-on knob.
//   - **Custom domain** (optional) via `cloudrun.DomainMapping` on the
//     service's region plus a CNAME record provisioned through the
//     registrar bridge (same shape `gcp-static-website` uses).
//
// Required parent-stack resources — operator sets these on the template
// config in `server.yaml`:
//
//	templates:
//	  stack-per-app:
//	    type: cloudrun
//	    config:
//	      credentials: "${auth:gcloud}"
//	      projectId: "${auth:gcloud.projectId}"
//	      region: us-central1
//	      artifactRegistryResource: smoke-registry
//
// `artifactRegistryResource` names a `type: gcp-artifact-registry`
// resource in the same parent stack; the compose images are pushed
// there before the Cloud Run service's `Image:` references them.
func Cloudrun(ctx *sdk.Context, stack api.Stack, input api.ResourceInput, params pApi.ProvisionParams) (*api.ResourceOutput, error) {
	if input.Descriptor.Type != gcpApi.TemplateTypeGcpCloudrun {
		return nil, errors.Errorf("unsupported template type %q", input.Descriptor.Type)
	}

	cloudrunInput, ok := input.Descriptor.Config.Config.(*gcpApi.CloudRunInput)
	if !ok {
		return nil, errors.Errorf("failed to convert cloudrun config for %q", input.Descriptor.Type)
	}
	if params.ParentStack == nil {
		return nil, errors.Errorf("cloudrun template requires a parent stack (artifact-registry is read from parent exports); none was wired to the plugin. If you are on an older SC core, bump past api #567 which added parent_info_json to ProvisionRequest.")
	}

	region := cloudrunInput.Region
	if region == "" {
		return nil, errors.Errorf("cloudrun template `region` is required (Cloud Run is a regional resource)")
	}
	registryResource := cloudrunInput.ArtifactRegistryResource
	if registryResource == "" {
		return nil, errors.Errorf("cloudrun template `artifactRegistryResource` is required: images from docker-compose are built and pushed to this registry before the Cloud Run service references them. Declare a `gcp-artifact-registry` resource in the parent stack and reference it here.")
	}
	if cloudrunInput.Deployment.StackConfig == nil {
		return nil, errors.Errorf("cloudrun stack config is nil for %q", input.Descriptor.Name)
	}
	if len(cloudrunInput.Deployment.Containers) == 0 {
		return nil, errors.Errorf("cloudrun stack %q has no containers to deploy", input.Descriptor.Name)
	}
	if cloudrunInput.Deployment.IngressContainer == nil {
		return nil, errors.Errorf("cloudrun stack %q has no ingress container (one of the compose services must bind a port)", input.Descriptor.Name)
	}

	stackName := input.StackParams.StackName
	environment := input.StackParams.Environment
	suffix := lo.If(params.ParentStack.DependsOnResource != nil, "--"+lo.FromPtr(params.ParentStack.DependsOnResource).Name).Else("")
	serviceName := input.ToResName(input.Descriptor.Name)

	// Build the GCP provider inline from the template's credentials. Same
	// shape every other GCP template uses; Cloud Run is a regional resource
	// so we pin the provider's region too.
	gcpProvider, err := buildCloudrunProvider(ctx, serviceName, cloudrunInput, region)
	if err != nil {
		return nil, errors.Wrapf(err, "build gcp provider for cloudrun %q", serviceName)
	}

	// 1. Read the parent-stack artifact-registry URL. The plugin exports
	//    this under `<registryName>-url` at Provision time (see
	//    artifactregistry.go); the template consumer reads it here via
	//    the StackReference SC core already set up.
	registryName := toArtifactRegistryName(input, registryResource)
	params.Log.Info(ctx.Context(), "cloudrun %q: reading registry URL from parent %q", serviceName, params.ParentStack.FullReference)
	registryURL, err := pApi.GetValueFromStack[string](ctx,
		fmt.Sprintf("%s%s-cloudrun-registryurl", serviceName, suffix),
		params.ParentStack.FullReference, toRegistryUrlExport(registryName), false)
	if err != nil {
		return nil, errors.Wrapf(err, "read artifact registry URL %q from parent stack", registryResource)
	}
	if registryURL == "" {
		return nil, errors.Errorf("artifact registry URL %q from parent stack is empty for cloudrun %q", registryResource, serviceName)
	}

	// 2. Mint a short-lived docker-push token for the registry.
	params.Log.Info(ctx.Context(), "cloudrun %q: authenticating against registry %q", serviceName, registryURL)
	dockerCreds, err := getDockerCredentialsWithAuthToken(ctx, input)
	if err != nil {
		return nil, errors.Wrapf(err, "obtain docker credentials for registry %q", registryURL)
	}

	// 3. Build and push every container image declared in the compose
	//    config. Returns `[]*kubernetes.ContainerImage` with resolved
	//    `ImageRef` outputs pointing at the pushed image in GAR.
	params.Log.Info(ctx.Context(), "cloudrun %q: building and pushing %d image(s) to %q",
		serviceName, len(cloudrunInput.Deployment.Containers), registryURL)
	images, err := kubernetes.BuildAndPushImages(ctx, kubernetes.BuildArgs{
		RegistryURL:      registryURL,
		RegistryUsername: lo.ToPtr(dockerCreds.Username),
		RegistryPassword: lo.ToPtr(dockerCreds.Password),
		Stack:            stack,
		Input:            input,
		Params:           params,
		Deployment:       cloudrunInput.Deployment,
		Opts:             []sdk.ResourceOption{},
	})
	if err != nil {
		return nil, errors.Wrapf(err, "build and push images for cloudrun %q", serviceName)
	}

	// 4. Build the Cloud Run v2 service template. Containers go in as:
	//    - the ingress container first (the one binding $PORT)
	//    - every other compose service as a sidecar afterwards
	containers := cloudrunContainers(cloudrunInput.Deployment, images)
	scale := cloudrunInput.Deployment.StackConfig.Scale

	resourceLabels := map[string]string{
		"simple_container_com_env":   environment,
		"simple_container_com_stack": stackName,
	}
	templateArgs := &cloudrunv2.ServiceTemplateArgs{
		Containers: containers,
		Labels:     sdk.ToStringMap(resourceLabels),
	}
	if scale != nil {
		if scale.Min > 0 || scale.Max > 0 {
			templateArgs.Scaling = &cloudrunv2.ServiceTemplateScalingArgs{
				MinInstanceCount: sdk.Int(scale.Min),
				MaxInstanceCount: sdk.Int(scale.Max),
			}
		}
	}

	// 5. Create the service. Opt-in `Ingress: INGRESS_TRAFFIC_ALL` for the
	//    public shape; a future knob narrows to internal/internal-and-
	//    cloud-load-balancing.
	service, err := cloudrunv2.NewService(ctx, serviceName, &cloudrunv2.ServiceArgs{
		Name:        sdk.String(serviceName),
		Location:    sdk.String(region),
		Ingress:     sdk.String("INGRESS_TRAFFIC_ALL"),
		Template:    templateArgs,
		Labels:      sdk.ToStringMap(resourceLabels),
		Description: sdk.StringPtr(fmt.Sprintf("Cloud Run service for %s/%s (deployed by simple-container)", stackName, environment)),
	}, sdk.Provider(gcpProvider))
	if err != nil {
		return nil, errors.Wrapf(err, "create cloudrun service %q in %q", serviceName, environment)
	}

	// 6. Public invoker binding — Cloud Run services default to
	//    authenticated-only; the compose shape expects a public HTTP
	//    service, so grant `roles/run.invoker` to `allUsers`. A future
	//    `access:` knob (`public` / `internal` / `service-account:X`)
	//    gates this.
	_, err = cloudrunv2.NewServiceIamMember(ctx, fmt.Sprintf("%s-invoker", serviceName), &cloudrunv2.ServiceIamMemberArgs{
		Name:     service.Name,
		Location: service.Location,
		Role:     sdk.String("roles/run.invoker"),
		Member:   sdk.String("allUsers"),
	}, sdk.Provider(gcpProvider))
	if err != nil {
		return nil, errors.Wrapf(err, "grant public invoker on cloudrun %q", serviceName)
	}

	ctx.Export(fmt.Sprintf("%s-cloudrun-url", serviceName), service.Uri)
	ctx.Export(fmt.Sprintf("%s-cloudrun-name", serviceName), service.Name)
	ctx.Export(fmt.Sprintf("%s-cloudrun-location", serviceName), service.Location)

	// 7. Optional custom domain + CNAME. If the stack config has a
	//    `domain:` and we have a registrar on `params`, create a Cloud
	//    Run DomainMapping and point a CNAME at the mapping's resource
	//    record. Cloud Run's v1 DomainMapping is still the public API
	//    for this; the v2 SDK does not expose it.
	domain := cloudrunInput.Deployment.StackConfig.Domain
	if domain != "" && params.Registrar != nil {
		params.Log.Info(ctx.Context(), "cloudrun %q: wiring custom domain %q", serviceName, domain)
		if _, err := provisionCloudrunDomainMapping(ctx, serviceName, domain, region, service, gcpProvider, stack, params); err != nil {
			return nil, errors.Wrapf(err, "provision domain mapping for cloudrun %q", serviceName)
		}
	}

	return &api.ResourceOutput{Ref: service}, nil
}

// buildCloudrunProvider builds a pulumi-gcp provider pinned to the
// Cloud Run region so the regional resource's provider doesn't fall
// back to a mismatched default.
func buildCloudrunProvider(ctx *sdk.Context, serviceName string, cloudrunInput *gcpApi.CloudRunInput, region string) (sdk.ProviderResource, error) {
	stackName := serviceName + "-cloudrun"
	provOut, err := Provider(ctx, api.Stack{Name: stackName}, api.ResourceInput{
		Descriptor: &api.ResourceDescriptor{
			Type:   gcpApi.ProviderType,
			Name:   stackName,
			Config: api.Config{Config: &cloudrunInput.CloudRunTemplate.Credentials},
		},
		StackParams: &api.StackParams{StackName: stackName},
	}, pApi.ProvisionParams{Log: logger.New()})
	if err != nil {
		return nil, errors.Wrap(err, "build gcp provider")
	}
	p, ok := provOut.Ref.(sdk.ProviderResource)
	if !ok {
		return nil, errors.Errorf("gcp Provider returned %T, not sdk.ProviderResource", provOut.Ref)
	}
	return p, nil
}

// cloudrunContainers maps the compose-derived containers to Cloud Run v2's
// ServiceTemplateContainer shape. Puts the ingress container first (Cloud
// Run requires exactly one port-exposing container, and convention has it
// listed first).
func cloudrunContainers(deployment k8s.DeploymentConfig, images []*kubernetes.ContainerImage) cloudrunv2.ServiceTemplateContainerArray {
	imageByName := map[string]sdk.StringOutput{}
	for _, img := range images {
		imageByName[img.Container.Name] = img.ImageName
	}

	ingressName := ""
	if deployment.IngressContainer != nil {
		ingressName = deployment.IngressContainer.Name
	}

	// Ordered iteration: ingress first, then sidecars in their declared
	// order. Cloud Run tolerates either ordering, but putting the ingress
	// at index 0 matches both the Google console and the convention Cloud
	// Run docs use.
	ordered := make([]k8s.CloudRunContainer, 0, len(deployment.Containers))
	if ingressName != "" {
		for _, c := range deployment.Containers {
			if c.Name == ingressName {
				ordered = append(ordered, c)
				break
			}
		}
	}
	for _, c := range deployment.Containers {
		if c.Name != ingressName {
			ordered = append(ordered, c)
		}
	}

	result := make(cloudrunv2.ServiceTemplateContainerArray, 0, len(ordered))
	for _, c := range ordered {
		container := &cloudrunv2.ServiceTemplateContainerArgs{
			Name:  sdk.String(c.Name),
			Image: imageByName[c.Name],
		}
		if len(c.Command) > 0 {
			container.Commands = sdk.ToStringArray(c.Command)
		}
		if len(c.Args) > 0 {
			container.Args = sdk.ToStringArray(c.Args)
		}
		envArgs := cloudrunv2.ServiceTemplateContainerEnvArray{}
		for k, v := range c.Env {
			envArgs = append(envArgs, &cloudrunv2.ServiceTemplateContainerEnvArgs{
				Name:  sdk.String(k),
				Value: sdk.String(v),
			})
		}
		if len(envArgs) > 0 {
			container.Envs = envArgs
		}
		// Ingress container exposes the port. Cloud Run v2's Ports is a
		// singleton pointer (one port per container), not an array.
		if c.Name == ingressName {
			if c.MainPort != nil {
				container.Ports = &cloudrunv2.ServiceTemplateContainerPortsArgs{
					ContainerPort: sdk.IntPtr(*c.MainPort),
				}
			} else if len(c.Ports) > 0 {
				container.Ports = &cloudrunv2.ServiceTemplateContainerPortsArgs{
					ContainerPort: sdk.IntPtr(c.Ports[0].Port),
				}
			}
		}
		result = append(result, container)
	}
	return result
}

// provisionCloudrunDomainMapping attaches a custom hostname to the Cloud
// Run service via Cloud Run v1's DomainMapping (the v2 SDK does not
// expose it), then points a CNAME at the mapping's `ghs.googlehosted.com`
// target through the plugin's registrar bridge.
func provisionCloudrunDomainMapping(ctx *sdk.Context, serviceName, domain, region string, service *cloudrunv2.Service, provider sdk.ProviderResource, stack api.Stack, params pApi.ProvisionParams) (*api.ResourceOutput, error) {
	mapping, err := cloudrun.NewDomainMapping(ctx, fmt.Sprintf("%s-domain-mapping", serviceName), &cloudrun.DomainMappingArgs{
		Location: sdk.String(region),
		Name:     sdk.String(domain),
		Metadata: &cloudrun.DomainMappingMetadataArgs{
			Namespace: service.Project,
		},
		Spec: &cloudrun.DomainMappingSpecArgs{
			RouteName: service.Name,
		},
	}, sdk.Provider(provider))
	if err != nil {
		return nil, errors.Wrapf(err, "create domain mapping for %q → %q", domain, serviceName)
	}

	// Cloud Run DomainMapping returns `ghs.googlehosted.com` as the CNAME
	// target once the ResourceRecords[] field resolves. Create the CNAME
	// through the registrar bridge with the resolved target.
	cnameTarget := mapping.Statuses.ApplyT(func(statuses []cloudrun.DomainMappingStatus) string {
		for _, s := range statuses {
			for _, r := range s.ResourceRecords {
				if r.Type != nil && *r.Type == "CNAME" && r.Rrdata != nil {
					return *r.Rrdata
				}
			}
		}
		return "ghs.googlehosted.com"
	}).(sdk.StringOutput)
	dnsRecord, err := params.Registrar.NewRecord(ctx, api.DnsRecord{
		Name:     domain,
		Type:     "CNAME",
		ValueOut: cnameTarget,
		Proxied:  true,
	})
	if err != nil {
		return nil, errors.Wrapf(err, "create CNAME %q for cloudrun %q", domain, serviceName)
	}

	ctx.Export(fmt.Sprintf("%s-cloudrun-domain", serviceName), sdk.String(domain))
	ctx.Export(fmt.Sprintf("%s-cloudrun-domain-mapping", serviceName), mapping.Name)
	return dnsRecord, nil
}
