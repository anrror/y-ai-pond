{{/*
y-ai-pond Helm chart — 标准模板助手

Usage:
  {{ include "y-ai-pond.fullname" . }}
  {{ include "y-ai-pond.labels" . }}
  {{ include "y-ai-pond.selectorLabels" . }}
*/}}

{{- define "y-ai-pond.fullname" -}}
{{- .Release.Name | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{- define "y-ai-pond.chart" -}}
{{- printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{- define "y-ai-pond.labels" -}}
helm.sh/chart: {{ include "y-ai-pond.chart" . }}
app.kubernetes.io/name: {{ include "y-ai-pond.fullname" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
app.kubernetes.io/part-of: y-ai-pond
{{- end -}}

{{- define "y-ai-pond.selectorLabels" -}}
app.kubernetes.io/name: {{ include "y-ai-pond.fullname" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end -}}

{{/*
Component-specific labels and selectors
Usage: {{ include "y-ai-pond.componentLabels" (list . "server") }}
*/}}
{{- define "y-ai-pond.componentLabels" -}}
{{- $ctx := index . 0 -}}
{{- $component := index . 1 -}}
{{ include "y-ai-pond.selectorLabels" $ctx }}
app.kubernetes.io/component: {{ $component }}
{{- end -}}

{{/*
Component name suffix
Usage: {{ include "y-ai-pond.componentName" (list . "server") }}
*/}}
{{- define "y-ai-pond.componentName" -}}
{{- $ctx := index . 0 -}}
{{- $component := index . 1 -}}
{{ include "y-ai-pond.fullname" $ctx }}-{{ $component }}
{{- end -}}

{{/*
Server config YAML content. The ConfigMap stores the server configuration
referencing Kubernetes service names for inter-pod communication.
Values with $(VAR_NAME) placeholders are replaced by environment variables
injected from Secrets.
*/}}
{{- define "y-ai-pond.serverConfig" -}}
server:
  port: {{ .Values.config.server.port }}
  sse_timeout: {{ .Values.config.server.sseTimeout }}

mqtt:
  broker_url: {{ .Values.config.mqtt.brokerUrl | quote }}
  client_id: {{ .Values.config.mqtt.clientId | quote }}
  keepalive: {{ .Values.config.mqtt.keepalive }}
  session_expiry: {{ .Values.config.mqtt.sessionExpiry }}

database:
  postgres_dsn: {{ .Values.config.database.postgresDsn | quote }}
  influxdb:
    url: {{ .Values.config.database.influxdb.url | quote }}
    token: {{ .Values.config.database.influxdb.token | quote }}
    org: {{ .Values.config.database.influxdb.org | quote }}
  redis_addr: {{ .Values.config.database.redisAddr | quote }}

auth:
  jwt_secret: {{ .Values.config.auth.jwtSecret | quote }}
  token_ttl: {{ .Values.config.auth.tokenTtl }}

models:
  registry_dir: {{ .Values.config.models.registryDir | quote }}
  paths:
    forecast: {{ .Values.config.models.paths.forecast | quote }}
    growth: {{ .Values.config.models.paths.growth | quote }}
    rl: {{ .Values.config.models.paths.rl | quote }}
    dt_gnn: {{ .Values.config.models.paths.dtGnn | quote }}

edge:
  sensor_intervals:
    ph: {{ .Values.config.edge.sensorIntervals.ph }}
    do: {{ .Values.config.edge.sensorIntervals.do }}
    temp: {{ .Values.config.edge.sensorIntervals.temp }}
    nh3: {{ .Values.config.edge.sensorIntervals.nh3 }}
    turbidity: {{ .Values.config.edge.sensorIntervals.turbidity }}
    water_level: {{ .Values.config.edge.sensorIntervals.waterLevel }}
{{- end -}}
