{{/*
Common labels applied to every object rendered for a service.
"part-of" ties all of an application's services together so `launchpad status`
can select them with app.kubernetes.io/part-of=<app>.
*/}}
{{- define "app.labels" -}}
app.kubernetes.io/name: {{ .svc.name }}
app.kubernetes.io/part-of: {{ .root.Values.app.name }}
app.kubernetes.io/managed-by: launchpad
{{- end -}}

{{- define "app.selectorLabels" -}}
app.kubernetes.io/name: {{ .svc.name }}
app.kubernetes.io/part-of: {{ .root.Values.app.name }}
{{- end -}}
