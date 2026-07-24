// Package manifest projects an application spec into the Kubernetes objects
// charts/app will render for it.
//
// The policy gate has to answer "would the cluster accept what I am about to
// commit?" before anything is written, so it needs the manifests in hand. Two
// ways to get them were available: shell out to `helm template`, or build them
// in Go. Shelling out would break Endurance's single-self-contained-binary
// promise and make the gate unavailable wherever helm is not installed, so the
// projection lives here — and TestProjectionMatchesHelmTemplate pins it against
// the real chart on any machine that does have helm, so the two cannot drift.
package manifest

import (
	"github.com/gc-ghub/endurance/internal/spec"
)

// Resource is one rendered Kubernetes object, in the generic form the policy
// engine walks.
type Resource struct {
	Kind      string
	Name      string
	Namespace string
	Object    map[string]any
}

// Render returns every object charts/app renders for app, plus the Pod each
// Deployment would create.
//
// The Pods are not written to the cluster by anything — they stand in for
// Kyverno's autogen behaviour, where a policy written against `kind: Pod` is
// automatically expanded to cover the pod templates of Deployments, DaemonSets
// and the rest. Without them, every Pod-matching policy in
// infra/kyverno_policy/ would silently match nothing at generation time and the
// gate would pass everything.
func Render(app spec.App) []Resource {
	a := app
	a.ApplyDefaults()

	var out []Resource
	for _, s := range a.Services {
		// One Deployment (and one synthesized Pod) per version, or exactly one
		// when the service declares none.
		for _, v := range s.Rollout() {
			name := s.WorkloadName(v)
			dep := deployment(a, s, v)
			out = append(out, Resource{
				Kind: "Deployment", Name: name, Namespace: a.Namespace, Object: dep,
			})
			out = append(out, Resource{
				Kind: "Pod", Name: name, Namespace: a.Namespace, Object: podFrom(a, s, v, dep),
			})
		}
		// One Service, fronting every version.
		out = append(out, Resource{
			Kind: "Service", Name: s.Name, Namespace: a.Namespace, Object: service(a, s),
		})
		if a.Mesh.Enabled && s.IsCanary() {
			out = append(out, Resource{
				Kind: "DestinationRule", Name: s.Name, Namespace: a.Namespace, Object: destinationRule(a, s),
			})
			out = append(out, Resource{
				Kind: "VirtualService", Name: s.Name, Namespace: a.Namespace, Object: virtualService(a, s),
			})
		}
	}
	return out
}

func labels(app spec.App, s spec.Service, version string) map[string]any {
	m := map[string]any{
		"app.kubernetes.io/name":       s.Name,
		"app.kubernetes.io/part-of":    app.Name,
		"app.kubernetes.io/managed-by": "endurance",
	}
	if version != "" {
		m["app.kubernetes.io/version"] = version
	}
	return m
}

// podLabels adds the labels that exist only on the pod template: the mesh
// opt-in, which must not appear on the Deployment's own metadata or on the
// immutable selector.
func podLabels(app spec.App, s spec.Service, version string) map[string]any {
	m := labels(app, s, version)
	if app.Mesh.Enabled {
		m["sidecar.istio.io/inject"] = "true"
	}
	return m
}

func selectorLabels(app spec.App, s spec.Service, version string) map[string]any {
	m := map[string]any{
		"app.kubernetes.io/name":    s.Name,
		"app.kubernetes.io/part-of": app.Name,
	}
	if version != "" {
		m["app.kubernetes.io/version"] = version
	}
	return m
}

func podSecurityContext(s spec.Service) map[string]any {
	return map[string]any{
		"runAsNonRoot": s.Security.RunAsNonRoot,
		"runAsUser":    s.Security.RunAsUser,
		"runAsGroup":   s.Security.RunAsGroup,
		"fsGroup":      s.Security.FSGroup,
	}
}

func containerSecurityContext(s spec.Service) map[string]any {
	drop := make([]any, 0, len(s.Security.DropCapabilities))
	for _, c := range s.Security.DropCapabilities {
		drop = append(drop, c)
	}
	return map[string]any{
		"allowPrivilegeEscalation": s.Security.AllowPrivilegeEscalation,
		"capabilities":             map[string]any{"drop": drop},
	}
}

func resources(s spec.Service) map[string]any {
	return map[string]any{
		"requests": map[string]any{
			"cpu":    s.Resources.Requests.CPU,
			"memory": s.Resources.Requests.Memory,
		},
		"limits": map[string]any{
			"cpu":    s.Resources.Limits.CPU,
			"memory": s.Resources.Limits.Memory,
		},
	}
}

func env(s spec.Service, v spec.Version) []any {
	list := s.EnvFor(v)
	if len(list) == 0 {
		return nil
	}
	out := make([]any, 0, len(list))
	for _, e := range list {
		out = append(out, map[string]any{"name": e.Name, "value": e.Value})
	}
	return out
}

func podSpec(s spec.Service, v spec.Version) map[string]any {
	container := map[string]any{
		"name":            s.Name,
		"image":           s.VersionRef(v),
		"imagePullPolicy": "IfNotPresent",
		"ports": []any{
			map[string]any{"name": "http", "containerPort": s.Port},
		},
		"resources":       resources(s),
		"securityContext": containerSecurityContext(s),
	}
	if e := env(s, v); e != nil {
		container["env"] = e
	}
	return map[string]any{
		"securityContext": podSecurityContext(s),
		"containers":      []any{container},
	}
}

func replicasOf(v spec.Version) int {
	if v.Replicas == 0 {
		return 1
	}
	return v.Replicas
}

func deployment(app spec.App, s spec.Service, v spec.Version) map[string]any {
	return map[string]any{
		"apiVersion": "apps/v1",
		"kind":       "Deployment",
		"metadata": map[string]any{
			"name":      s.WorkloadName(v),
			"namespace": app.Namespace,
			"labels":    labels(app, s, v.Name),
		},
		"spec": map[string]any{
			"replicas": replicasOf(v),
			"selector": map[string]any{"matchLabels": selectorLabels(app, s, v.Name)},
			"template": map[string]any{
				"metadata": map[string]any{"labels": podLabels(app, s, v.Name)},
				"spec":     podSpec(s, v),
			},
		},
	}
}

// podFrom lifts a Deployment's pod template into a standalone Pod object, which
// is what a Pod-matching Kyverno rule is really being asked about.
func podFrom(app spec.App, s spec.Service, v spec.Version, dep map[string]any) map[string]any {
	tmpl := dep["spec"].(map[string]any)["template"].(map[string]any)
	return map[string]any{
		"apiVersion": "v1",
		"kind":       "Pod",
		"metadata": map[string]any{
			"name":      s.WorkloadName(v),
			"namespace": app.Namespace,
			"labels":    tmpl["metadata"].(map[string]any)["labels"],
		},
		"spec": podSpec(s, v),
	}
}

func destinationRule(app spec.App, s spec.Service) map[string]any {
	subsets := make([]any, 0, len(s.Versions))
	for _, v := range s.Versions {
		subsets = append(subsets, map[string]any{
			"name":   v.Name,
			"labels": map[string]any{"app.kubernetes.io/version": v.Name},
		})
	}
	return map[string]any{
		"apiVersion": "networking.istio.io/v1",
		"kind":       "DestinationRule",
		"metadata": map[string]any{
			"name":      s.Name,
			"namespace": app.Namespace,
			"labels":    labels(app, s, ""),
		},
		"spec": map[string]any{"host": s.Name, "subsets": subsets},
	}
}

func virtualService(app spec.App, s spec.Service) map[string]any {
	routes := make([]any, 0, len(s.Versions))
	for _, v := range s.Versions {
		routes = append(routes, map[string]any{
			"destination": map[string]any{
				"host":   s.Name,
				"subset": v.Name,
				"port":   map[string]any{"number": s.Port},
			},
			"weight": v.Weight,
		})
	}
	return map[string]any{
		"apiVersion": "networking.istio.io/v1",
		"kind":       "VirtualService",
		"metadata": map[string]any{
			"name":      s.Name,
			"namespace": app.Namespace,
			"labels":    labels(app, s, ""),
		},
		"spec": map[string]any{
			"hosts": []any{s.Name},
			"http":  []any{map[string]any{"route": routes}},
		},
	}
}

func service(app spec.App, s spec.Service) map[string]any {
	return map[string]any{
		"apiVersion": "v1",
		"kind":       "Service",
		"metadata": map[string]any{
			"name":      s.Name,
			"namespace": app.Namespace,
			"labels":    labels(app, s, ""),
		},
		"spec": map[string]any{
			"type":     "ClusterIP",
			"selector": selectorLabels(app, s, ""),
			"ports": []any{
				map[string]any{"name": "http", "port": s.Port, "targetPort": "http"},
			},
		},
	}
}
