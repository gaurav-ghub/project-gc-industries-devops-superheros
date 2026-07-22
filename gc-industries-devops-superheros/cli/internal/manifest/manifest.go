// Package manifest projects an application spec into the Kubernetes objects
// charts/app will render for it.
//
// The policy gate has to answer "would the cluster accept what I am about to
// commit?" before anything is written, so it needs the manifests in hand. Two
// ways to get them were available: shell out to `helm template`, or build them
// in Go. Shelling out would break LaunchPad's single-self-contained-binary
// promise and make the gate unavailable wherever helm is not installed, so the
// projection lives here — and TestProjectionMatchesHelmTemplate pins it against
// the real chart on any machine that does have helm, so the two cannot drift.
package manifest

import (
	"github.com/gc-ghub/launchpad/internal/spec"
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
		dep := deployment(a, s)
		out = append(out, Resource{
			Kind: "Deployment", Name: s.Name, Namespace: a.Namespace, Object: dep,
		})
		out = append(out, Resource{
			Kind: "Pod", Name: s.Name, Namespace: a.Namespace, Object: podFrom(a, s, dep),
		})
		out = append(out, Resource{
			Kind: "Service", Name: s.Name, Namespace: a.Namespace, Object: service(a, s),
		})
	}
	return out
}

func labels(app spec.App, s spec.Service) map[string]any {
	return map[string]any{
		"app.kubernetes.io/name":       s.Name,
		"app.kubernetes.io/part-of":    app.Name,
		"app.kubernetes.io/managed-by": "launchpad",
	}
}

func selectorLabels(app spec.App, s spec.Service) map[string]any {
	return map[string]any{
		"app.kubernetes.io/name":    s.Name,
		"app.kubernetes.io/part-of": app.Name,
	}
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

func podSpec(s spec.Service) map[string]any {
	return map[string]any{
		"securityContext": podSecurityContext(s),
		"containers": []any{
			map[string]any{
				"name":            s.Name,
				"image":           s.Ref(),
				"imagePullPolicy": "IfNotPresent",
				"ports": []any{
					map[string]any{"name": "http", "containerPort": s.Port},
				},
				"resources":       resources(s),
				"securityContext": containerSecurityContext(s),
			},
		},
	}
}

func deployment(app spec.App, s spec.Service) map[string]any {
	return map[string]any{
		"apiVersion": "apps/v1",
		"kind":       "Deployment",
		"metadata": map[string]any{
			"name":      s.Name,
			"namespace": app.Namespace,
			"labels":    labels(app, s),
		},
		"spec": map[string]any{
			"replicas": s.Replicas,
			"selector": map[string]any{"matchLabels": selectorLabels(app, s)},
			"template": map[string]any{
				"metadata": map[string]any{"labels": labels(app, s)},
				"spec":     podSpec(s),
			},
		},
	}
}

// podFrom lifts a Deployment's pod template into a standalone Pod object, which
// is what a Pod-matching Kyverno rule is really being asked about.
func podFrom(app spec.App, s spec.Service, dep map[string]any) map[string]any {
	tmpl := dep["spec"].(map[string]any)["template"].(map[string]any)
	return map[string]any{
		"apiVersion": "v1",
		"kind":       "Pod",
		"metadata": map[string]any{
			"name":      s.Name,
			"namespace": app.Namespace,
			"labels":    tmpl["metadata"].(map[string]any)["labels"],
		},
		"spec": podSpec(s),
	}
}

func service(app spec.App, s spec.Service) map[string]any {
	return map[string]any{
		"apiVersion": "v1",
		"kind":       "Service",
		"metadata": map[string]any{
			"name":      s.Name,
			"namespace": app.Namespace,
			"labels":    labels(app, s),
		},
		"spec": map[string]any{
			"type":     "ClusterIP",
			"selector": selectorLabels(app, s),
			"ports": []any{
				map[string]any{"name": "http", "port": s.Port, "targetPort": "http"},
			},
		},
	}
}
