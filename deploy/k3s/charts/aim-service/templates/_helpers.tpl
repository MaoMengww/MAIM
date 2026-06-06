{{- define "aim-service.fullname" -}}
{{- printf "aim-%s" .Values.config.name | trunc 63 | trimSuffix "-" }}
{{- end }}

{{- define "aim-service.labels" -}}
app.kubernetes.io/name: {{ include "aim-service.fullname" . }}
app.kubernetes.io/part-of: aim
{{- end }}
