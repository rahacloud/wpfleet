{{- define "wordpress.site" -}}
{{- required "site is required" .Values.site -}}
{{- if not (regexMatch "^[a-z0-9]([a-z0-9-]*[a-z0-9])?$" .Values.site) -}}
{{- fail "site must be lowercase letters, digits and hyphens" -}}
{{- end -}}
{{- end -}}

{{- define "wordpress.fullname" -}}
{{- include "wordpress.site" . | trunc 50 -}}
{{- end -}}

{{- define "wordpress.selectorLabels" -}}
app.kubernetes.io/name: wordpress
app.kubernetes.io/instance: {{ include "wordpress.site" . }}
{{- end -}}

{{- define "wordpress.labels" -}}
{{ include "wordpress.selectorLabels" . }}
app.kubernetes.io/part-of: wordpress
app.kubernetes.io/managed-by: {{ .Release.Service }}
app.kubernetes.io/version: {{ .Values.image.tag | quote }}
helm.sh/chart: {{ printf "%s-%s" .Chart.Name .Chart.Version }}
{{- end -}}

{{- define "wordpress.dbIdent" -}}
{{- printf "wp_%s" (include "wordpress.site" . | replace "-" "_") -}}
{{- end -}}

{{- define "wordpress.dbName" -}}
{{- $v := .Values.database.name | default (include "wordpress.dbIdent" .) -}}
{{- if not (regexMatch "^[A-Za-z0-9_]{1,64}$" $v) -}}{{- fail "database.name must be letters, digits and underscores" -}}{{- end -}}
{{- $v -}}
{{- end -}}

{{- define "wordpress.dbUser" -}}
{{- $v := .Values.database.user | default (include "wordpress.dbIdent" .) -}}
{{- if not (regexMatch "^[A-Za-z0-9_]{1,32}$" $v) -}}{{- fail "database.user must be letters, digits and underscores, at most 32" -}}{{- end -}}
{{- $v -}}
{{- end -}}

{{- define "wordpress.dbPassword" -}}
{{- $v := required "database.password is required" .Values.database.password -}}
{{- if not (regexMatch "^[A-Za-z0-9]{16,}$" $v) -}}{{- fail "database.password must be at least 16 letters and digits" -}}{{- end -}}
{{- $v -}}
{{- end -}}

{{- define "wordpress.hosts" -}}
{{- if .Values.domains -}}
{{- toJson .Values.domains -}}
{{- else -}}
{{- $suffix := required "domains or domainSuffix is required" .Values.domainSuffix -}}
{{- if not (regexMatch "^[a-z0-9-]*$" .Values.hostLabelSuffix) -}}
{{- fail "hostLabelSuffix must be lowercase letters, digits and hyphens" -}}
{{- end -}}
{{- toJson (list (printf "%s%s.%s" (include "wordpress.site" .) .Values.hostLabelSuffix $suffix)) -}}
{{- end -}}
{{- end -}}

{{- define "wordpress.url" -}}
{{- $hosts := include "wordpress.hosts" . | fromJsonArray -}}
{{- printf "%s://%s" (ternary "https" "http" .Values.ingress.tls.enabled) (first $hosts) -}}
{{- end -}}

{{- define "wordpress.singleWriter" -}}
{{- ne .Values.uploads.accessMode "ReadWriteMany" -}}
{{- end -}}

{{- define "wordpress.validateScaling" -}}
{{- if and (eq (include "wordpress.singleWriter" .) "true") (or .Values.autoscaling.enabled (gt (int .Values.replicas) 1)) -}}
{{- fail "more than one replica needs uploads.accessMode ReadWriteMany" -}}
{{- end -}}
{{- end -}}
