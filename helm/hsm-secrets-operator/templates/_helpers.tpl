{{/*
Expand the name of the chart.
*/}}
{{- define "hsm-secrets-operator.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Create a default fully qualified app name.
We truncate at 63 chars because some Kubernetes name fields are limited to this (by the DNS naming spec).
If release name contains chart name it will be used as a full name.
*/}}
{{- define "hsm-secrets-operator.fullname" -}}
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
{{- define "hsm-secrets-operator.chart" -}}
{{- printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Common labels
*/}}
{{- define "hsm-secrets-operator.labels" -}}
helm.sh/chart: {{ include "hsm-secrets-operator.chart" . }}
{{ include "hsm-secrets-operator.selectorLabels" . }}
{{- if .Chart.AppVersion }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
{{- end }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end }}

{{/*
Selector labels
*/}}
{{- define "hsm-secrets-operator.selectorLabels" -}}
app.kubernetes.io/name: {{ include "hsm-secrets-operator.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end }}

{{/*
Create the name of the service account to use
*/}}
{{- define "hsm-secrets-operator.serviceAccountName" -}}
{{- if .Values.serviceAccount.create }}
{{- default (include "hsm-secrets-operator.fullname" .) .Values.serviceAccount.name }}
{{- else }}
{{- default "default" .Values.serviceAccount.name }}
{{- end }}
{{- end }}

{{/*
Create the name of the controller manager
*/}}
{{- define "hsm-secrets-operator.controllerManagerName" -}}
{{- printf "%s-controller-manager" (include "hsm-secrets-operator.fullname" .) }}
{{- end }}

{{/*
Create the name of the metrics service
*/}}
{{- define "hsm-secrets-operator.metricsServiceName" -}}
{{- printf "%s-metrics-service" (include "hsm-secrets-operator.fullname" .) }}
{{- end }}

{{/*
Create the image reference
*/}}
{{- define "hsm-secrets-operator.image" -}}
{{- $tag := .Values.image.tag | default .Chart.AppVersion }}
{{- printf "%s:%s" .Values.image.repository $tag }}
{{- end }}

{{/*
Create system namespace name
*/}}
{{- define "hsm-secrets-operator.systemNamespace" -}}
{{- printf "%s-system" (include "hsm-secrets-operator.fullname" .) }}
{{- end }}