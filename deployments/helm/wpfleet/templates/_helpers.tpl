{{- define "wpfleet.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{- define "wpfleet.fullname" -}}
{{- if .Values.fullnameOverride -}}
{{- .Values.fullnameOverride | trunc 63 | trimSuffix "-" -}}
{{- else if contains (include "wpfleet.name" .) .Release.Name -}}
{{- .Release.Name | trunc 63 | trimSuffix "-" -}}
{{- else -}}
{{- printf "%s-%s" .Release.Name (include "wpfleet.name" .) | trunc 63 | trimSuffix "-" -}}
{{- end -}}
{{- end -}}

{{- define "wpfleet.selectorLabels" -}}
app.kubernetes.io/name: {{ include "wpfleet.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end -}}

{{- define "wpfleet.labels" -}}
{{ include "wpfleet.selectorLabels" . }}
app.kubernetes.io/version: {{ .Values.image.tag | default .Chart.AppVersion | quote }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
helm.sh/chart: {{ printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" }}
{{- end -}}

{{- define "wpfleet.serviceAccountName" -}}
{{- if .Values.serviceAccount.create -}}
{{- default (include "wpfleet.fullname" .) .Values.serviceAccount.name -}}
{{- else -}}
{{- required "serviceAccount.name is required when serviceAccount.create is false" .Values.serviceAccount.name -}}
{{- end -}}
{{- end -}}

{{/* wpfleet.yaml: config, with the listen addresses taken from ports. */}}
{{- define "wpfleet.config" -}}
{{- $config := deepCopy (.Values.config | default dict) -}}
{{- $listen := dict "listen" (printf ":%d" (int .Values.ports.http)) "metricsListen" (printf ":%d" (int .Values.ports.metrics)) -}}
{{- $_ := set $config "server" (mergeOverwrite (deepCopy (get $config "server" | default dict)) $listen) -}}
{{- toYaml $config -}}
{{- end -}}

{{/* The ingress host: ingress.host, else publicURL's host. */}}
{{- define "wpfleet.ingressHost" -}}
{{- $public := dig "server" "publicURL" "" (.Values.config | default dict) -}}
{{- $host := .Values.ingress.host | default (urlParse $public).host -}}
{{- required "ingress.host or config.server.publicURL is required for the Ingress" $host -}}
{{- end -}}
