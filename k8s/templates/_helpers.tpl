{{/*
Expand the name of the chart.
*/}}
{{- define "llm-supervisor-proxy.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Create a default fully qualified app name.
*/}}
{{- define "llm-supervisor-proxy.fullname" -}}
{{- if .Values.fullnameOverride }}
{{- .Values.fullnameOverride | trunc 63 | trimSuffix "-" }}
{{- else }}
{{- $name := default .Chart.Name .Values.nameOverride }}
{{- if contains $name .Release.Name }}
{{- .Release.Name | trunc 63 | trimSuffix "-" }}
{{- else }}
{{- printf "%s-%s" .Release.Name $name | trunc 63 | trimSuffix "-" }}
{{- end }}
{{- end }}
{{- end }}

{{/*
Create chart name and version as used by the chart label.
*/}}
{{- define "llm-supervisor-proxy.chart" -}}
{{- printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Common labels
*/}}
{{- define "llm-supervisor-proxy.labels" -}}
helm.sh/chart: {{ include "llm-supervisor-proxy.chart" . }}
{{ include "llm-supervisor-proxy.selectorLabels" . }}
{{- if .Chart.AppVersion }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
{{- end }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end }}

{{/*
Selector labels
*/}}
{{- define "llm-supervisor-proxy.selectorLabels" -}}
app.kubernetes.io/name: {{ include "llm-supervisor-proxy.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end }}

{{/*
Create the name of the service account to use
*/}}
{{- define "llm-supervisor-proxy.serviceAccountName" -}}
{{- if .Values.serviceAccount.create }}
{{- default (include "llm-supervisor-proxy.fullname" .) .Values.serviceAccount.name }}
{{- else }}
{{- default "default" .Values.serviceAccount.name }}
{{- end }}
{{- end }}

{{/*
Three-state OAuth2 mode guard (AR-22 / D15 / ADR-001).

The chart renders THREE states:
  edge-only     = internalOAuth2.enabled=false                             (default; today's render)
  overlap       = internalOAuth2.enabled=true AND oauth2Proxy.enabled=true
                  AND internalOAuth2.overlap=true                            (edge annotations STAY, app env renders, /oauth2 -> app)
  internal-only = internalOAuth2.enabled=true AND oauth2Proxy.enabled=false
                  AND oauth2Proxy.replicas=0                                (annotations drop, edge objects stay at 0 replicas)

The ONLY render-time failure is the genuinely contradictory input:
internalOAuth2.enabled=true AND oauth2Proxy.enabled=true WITHOUT overlap=true.

Plus the AR-48 `required`-style guard: when internal mode is on, issuerURL,
externalURL and the client-id source (the secret that holds CLIENT_ID /
CLIENT_SECRET / COOKIE_SECRET) MUST be present.
*/}}
{{- define "llm-supervisor-proxy.oauth2ModeGuard" -}}
{{- if and .Values.internalOAuth2.enabled .Values.oauth2Proxy.enabled (not .Values.internalOAuth2.overlap) -}}
{{- fail "internalOAuth2.enabled=true with oauth2Proxy.enabled=true requires internalOAuth2.overlap=true (three-state model: edge-only / overlap / internal-only)" -}}
{{- end -}}
{{- if .Values.internalOAuth2.enabled -}}
{{- if not .Values.internalOAuth2.issuerURL -}}
{{- fail "internalOAuth2.issuerURL is required when internalOAuth2.enabled=true (OIDC issuer base URL, e.g. https://door.daoduc.org)" -}}
{{- end -}}
{{- if not .Values.internalOAuth2.externalURL -}}
{{- fail "internalOAuth2.externalURL is required when internalOAuth2.enabled=true (public base URL of the deployment, e.g. https://llm.daoduc.org)" -}}
{{- end -}}
{{- if not .Values.internalOAuth2.existingSecret -}}
{{- fail "internalOAuth2.existingSecret is required when internalOAuth2.enabled=true (the secret holding CLIENT_ID / CLIENT_SECRET / COOKIE_SECRET — the client-id source is mounted via secretKeyRef, never as a literal)" -}}
{{- end -}}
{{- end -}}
{{- end }}
