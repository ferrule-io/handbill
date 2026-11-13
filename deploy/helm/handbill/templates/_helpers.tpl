{{/*
Expand the name of the chart.
*/}}
{{- define "handbill.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{/*
Create a default fully qualified app name.
We truncate at 63 chars because some Kubernetes name fields are limited to this (by the DNS naming spec).
If release name contains chart name it will be used as a full name.
*/}}
{{- define "handbill.fullname" -}}
{{- if .Values.fullnameOverride -}}
{{- .Values.fullnameOverride | trunc 63 | trimSuffix "-" -}}
{{- else -}}
{{- $name := default .Chart.Name .Values.nameOverride -}}
{{- if contains $name .Release.Name -}}
{{- .Release.Name | trunc 63 | trimSuffix "-" -}}
{{- else -}}
{{- printf "%s-%s" .Release.Name $name | trunc 63 | trimSuffix "-" -}}
{{- end -}}
{{- end -}}
{{- end -}}

{{/*
Create chart name and version as used by the chart label.
*/}}
{{- define "handbill.chart" -}}
{{- printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{/*
Common labels
*/}}
{{- define "handbill.labels" -}}
helm.sh/chart: {{ include "handbill.chart" . }}
{{ include "handbill.selectorLabels" . }}
{{- if .Chart.AppVersion }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
{{- end }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end -}}

{{/*
Selector labels
*/}}
{{- define "handbill.selectorLabels" -}}
app.kubernetes.io/name: {{ include "handbill.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
app.kubernetes.io/component: app
{{- end -}}

{{/*
Create the name of the service account to use
*/}}
{{- define "handbill.serviceAccountName" -}}
{{- if .Values.serviceAccount.create -}}
    {{- default (include "handbill.fullname" .) .Values.serviceAccount.name -}}
{{- else -}}
    {{- default "default" .Values.serviceAccount.name -}}
{{- end -}}
{{- end -}}

{{/*
Derive app hostname from app.origin (strips scheme and port if present)
*/}}
{{- define "handbill.appHost" -}}
{{- $raw := .Values.app.origin | default "dash.example.com" -}}
{{- $noScheme := $raw | trimPrefix "https://" | trimPrefix "http://" -}}
{{- $hostAndPort := $noScheme | splitList "/" | first -}}
{{- $host := $hostAndPort | splitList ":" | first -}}
{{- $host -}}
{{- end -}}

{{/*
PostgreSQL fullname
*/}}
{{- define "handbill.postgresqlFullname" -}}
{{- printf "%s-postgresql" (include "handbill.fullname" .) | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{/*
PostgreSQL common labels
*/}}
{{- define "handbill.postgresqlLabels" -}}
helm.sh/chart: {{ include "handbill.chart" . }}
{{ include "handbill.postgresqlSelectorLabels" . }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end -}}

{{/*
PostgreSQL selector labels
*/}}
{{- define "handbill.postgresqlSelectorLabels" -}}
app.kubernetes.io/name: {{ include "handbill.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
app.kubernetes.io/component: postgresql
{{- end -}}

{{/*
PostgreSQL secret name
*/}}
{{- define "handbill.postgresqlSecretName" -}}
{{- if .Values.postgresql.auth.existingSecret -}}
    {{- .Values.postgresql.auth.existingSecret -}}
{{- else -}}
    {{- include "handbill.postgresqlFullname" . -}}
{{- end -}}
{{- end -}}

{{/*
PostgreSQL secret password key
*/}}
{{- define "handbill.postgresqlSecretPasswordKey" -}}
{{- if .Values.postgresql.auth.existingSecret -}}
    {{- .Values.postgresql.auth.existingSecretPasswordKey | default "password" -}}
{{- else -}}
    {{- "password" -}}
{{- end -}}
{{- end -}}

{{/*
PostgreSQL password value (lookup or generated)
*/}}
{{- define "handbill.postgresqlPassword" -}}
{{- if .Values.postgresql.auth.password -}}
    {{- .Values.postgresql.auth.password -}}
{{- else -}}
    {{- $secret := (lookup "v1" "Secret" .Release.Namespace (include "handbill.postgresqlSecretName" .)) -}}
    {{- if and $secret $secret.data (hasKey $secret.data (include "handbill.postgresqlSecretPasswordKey" .)) -}}
        {{- index $secret.data (include "handbill.postgresqlSecretPasswordKey" .) | b64dec -}}
    {{- else -}}
        {{- randAlphaNum 32 -}}
    {{- end -}}
{{- end -}}
{{- end -}}

{{/*
External database secret name
*/}}
{{- define "handbill.externalDatabaseSecretName" -}}
{{- if .Values.externalDatabase.existingSecret -}}
    {{- .Values.externalDatabase.existingSecret -}}
{{- else -}}
    {{- printf "%s-externaldb" (include "handbill.fullname" .) | trunc 63 | trimSuffix "-" -}}
{{- end -}}
{{- end -}}

{{/*
External database secret password key
*/}}
{{- define "handbill.externalDatabaseSecretPasswordKey" -}}
{{- if .Values.externalDatabase.existingSecret -}}
    {{- .Values.externalDatabase.existingSecretPasswordKey | default "password" -}}
{{- else -}}
    {{- "password" -}}
{{- end -}}
{{- end -}}

{{/*
Secret key secret name
*/}}
{{- define "handbill.secretKeySecretName" -}}
{{- if .Values.secretKey.existingSecret -}}
    {{- .Values.secretKey.existingSecret -}}
{{- else -}}
    {{- printf "%s-secret-key" (include "handbill.fullname" .) | trunc 63 | trimSuffix "-" -}}
{{- end -}}
{{- end -}}

{{/*
Secret key key in secret
*/}}
{{- define "handbill.secretKeyKey" -}}
{{- if .Values.secretKey.existingSecret -}}
    {{- .Values.secretKey.existingSecretKey | default "secret-key" -}}
{{- else -}}
    {{- "secret-key" -}}
{{- end -}}
{{- end -}}

{{/*
Secret key value (lookup or generated)
*/}}
{{- define "handbill.secretKeyValue" -}}
{{- if .Values.secretKey.value -}}
    {{- .Values.secretKey.value -}}
{{- else -}}
    {{- $secret := (lookup "v1" "Secret" .Release.Namespace (include "handbill.secretKeySecretName" .)) -}}
    {{- if and $secret $secret.data (hasKey $secret.data (include "handbill.secretKeyKey" .)) -}}
        {{- index $secret.data (include "handbill.secretKeyKey" .) | b64dec -}}
    {{- else -}}
        {{- randAlphaNum 64 -}}
    {{- end -}}
{{- end -}}
{{- end -}}

{{/*
SMTP secret name
*/}}
{{- define "handbill.smtpSecretName" -}}
{{- if .Values.smtp.existingSecret -}}
    {{- .Values.smtp.existingSecret -}}
{{- else -}}
    {{- printf "%s-smtp" (include "handbill.fullname" .) | trunc 63 | trimSuffix "-" -}}
{{- end -}}
{{- end -}}

{{/*
SMTP secret password key
*/}}
{{- define "handbill.smtpSecretPasswordKey" -}}
{{- if .Values.smtp.existingSecret -}}
    {{- .Values.smtp.existingSecretPasswordKey | default "password" -}}
{{- else -}}
    {{- "password" -}}
{{- end -}}
{{- end -}}
