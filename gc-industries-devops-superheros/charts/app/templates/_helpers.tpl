{{/*
Common labels applied to every object rendered for a service.
"part-of" ties all of an application's services together so `endurance status`
can select them with app.kubernetes.io/part-of=<app>.

"version" is set only for a canary service's per-version workloads. It is the
label Istio DestinationRule subsets select on, so it must appear on the pods and
must NOT appear on the Service selector — a canary's single Service has to front
every version, and Istio is what decides which subset a request reaches.
*/}}
{{- define "app.labels" -}}
app.kubernetes.io/name: {{ .svc.name }}
app.kubernetes.io/part-of: {{ .root.Values.app.name }}
app.kubernetes.io/managed-by: endurance
{{- if .version }}
app.kubernetes.io/version: {{ .version }}
{{- end }}
{{- end -}}

{{/*
Selector labels. The Service passes no version, so it selects every version's
pods; a Deployment passes its own version, so each version owns its ReplicaSet.
*/}}
{{- define "app.selectorLabels" -}}
app.kubernetes.io/name: {{ .svc.name }}
app.kubernetes.io/part-of: {{ .root.Values.app.name }}
{{- if .version }}
app.kubernetes.io/version: {{ .version }}
{{- end }}
{{- end -}}
