{{- define "akmatori.fullname" -}}
{{- if contains .Chart.Name .Release.Name -}}
{{- .Release.Name | trunc 50 | trimSuffix "-" -}}
{{- else -}}
{{- printf "%s-%s" .Release.Name .Chart.Name | trunc 50 | trimSuffix "-" -}}
{{- end -}}
{{- end -}}

{{- define "akmatori.labels" -}}
app.kubernetes.io/name: {{ .Chart.Name }}
app.kubernetes.io/instance: {{ .Release.Name }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
helm.sh/chart: {{ printf "%s-%s" .Chart.Name .Chart.Version }}
{{- end -}}

{{/* Selector labels for one component: include "akmatori.selector" (list . "api") */}}
{{- define "akmatori.selector" -}}
{{- $ctx := index . 0 -}}
app.kubernetes.io/name: {{ $ctx.Chart.Name }}
app.kubernetes.io/instance: {{ $ctx.Release.Name }}
app.kubernetes.io/component: {{ index . 1 }}
{{- end -}}

{{- define "akmatori.image" -}}
{{- $ctx := index . 0 -}}
{{- printf "%s/%s:%s" $ctx.Values.image.registry (index . 1) (default $ctx.Chart.AppVersion $ctx.Values.image.tag) -}}
{{- end -}}

{{- define "akmatori.secretName" -}}
{{- printf "%s-secrets" (include "akmatori.fullname" .) -}}
{{- end -}}

{{- define "akmatori.dataClaim" -}}
{{- default (printf "%s-data" (include "akmatori.fullname" .)) .Values.persistence.existingClaim -}}
{{- end -}}

{{- define "akmatori.apiHost" -}}{{ include "akmatori.fullname" . }}-api{{- end -}}
{{- define "akmatori.gatewayHost" -}}{{ include "akmatori.fullname" . }}-mcp-gateway{{- end -}}
{{- define "akmatori.frontendHost" -}}{{ include "akmatori.fullname" . }}-frontend{{- end -}}
{{- define "akmatori.postgresHost" -}}{{ include "akmatori.fullname" . }}-postgres{{- end -}}

{{/* DATABASE_URL env entry for the API and the gateway. */}}
{{- define "akmatori.databaseEnv" -}}
- name: DATABASE_URL
  valueFrom:
    secretKeyRef:
{{- if and (not .Values.postgresql.enabled) .Values.externalDatabase.existingSecret }}
      name: {{ .Values.externalDatabase.existingSecret }}
      key: {{ .Values.externalDatabase.existingSecretKey }}
{{- else }}
      name: {{ include "akmatori.secretName" . }}
      key: database-url
{{- end }}
{{- end -}}

{{/* Init container that waits for the bundled PostgreSQL (compose: depends_on service_healthy). */}}
{{- define "akmatori.waitForPostgres" -}}
{{- if .Values.postgresql.enabled }}
- name: wait-for-postgres
  image: {{ .Values.postgresql.image }}
  command: ["sh", "-c", "until pg_isready -h {{ include "akmatori.postgresHost" . }} -p 5432; do sleep 2; done"]
  resources:
    requests: { cpu: 10m, memory: 16Mi }
    limits: { memory: 64Mi }
{{- end }}
{{- end -}}

{{/* Outbound proxy env, same variables as docker-compose.yml. */}}
{{- define "akmatori.proxyEnv" -}}
{{- $internal := printf "%s,%s,%s,%s,localhost,127.0.0.1,.svc,.cluster.local" (include "akmatori.apiHost" .) (include "akmatori.gatewayHost" .) (include "akmatori.postgresHost" .) (include "akmatori.frontendHost" .) -}}
{{- $noProxy := ternary $internal (printf "%s,%s" .Values.proxy.noProxy $internal) (empty .Values.proxy.noProxy) -}}
{{- if or .Values.proxy.httpProxy .Values.proxy.httpsProxy }}
- { name: HTTP_PROXY, value: {{ .Values.proxy.httpProxy | quote }} }
- { name: http_proxy, value: {{ .Values.proxy.httpProxy | quote }} }
- { name: HTTPS_PROXY, value: {{ .Values.proxy.httpsProxy | quote }} }
- { name: https_proxy, value: {{ .Values.proxy.httpsProxy | quote }} }
- { name: NO_PROXY, value: {{ $noProxy | quote }} }
- { name: no_proxy, value: {{ $noProxy | quote }} }
{{- end }}
{{- end -}}
