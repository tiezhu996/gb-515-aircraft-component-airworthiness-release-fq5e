#!/usr/bin/env sh
set -eu

project_root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
cd "$project_root"
set -a
if [ -f .env ]; then . ./.env; else . ./.env.example; fi
set +a

(command -v jq >/dev/null 2>&1) || { echo "jq is required for API validation" >&2; exit 1; }

(cd backend && go test ./... && go test -race ./... && go vet ./... && go build ./...)
(cd frontend && npm install --no-audit --no-fund && npm run typecheck && npm run build)
docker compose config --quiet
docker compose down -v --remove-orphans
docker compose up -d --build

cleanup() { docker compose down -v --remove-orphans; }
if [ "${KEEP_RUNNING:-0}" = "1" ]; then
  trap cleanup INT TERM
else
  trap cleanup EXIT INT TERM
fi

i=0
until curl -fsS "http://127.0.0.1:${BACKEND_PORT:-19515}/healthz" | jq -e '.data.status == "ok" and .data.database == "ready" and .data.redis == "ready"' >/dev/null; do
  i=$((i+1))
  [ "$i" -lt 60 ] || { docker compose logs; exit 1; }
  sleep 2
done
curl -fsS "http://127.0.0.1:${FRONTEND_PORT:-18515}/" >/dev/null

login_token() {
  curl -fsS -X POST "http://127.0.0.1:${BACKEND_PORT:-19515}/api/auth/login" \
    -H 'Content-Type: application/json' \
    -d "{\"username\":\"$1\",\"password\":\"Admin123!\"}" | jq -er '.data.token'
}

admin_token=$(login_token admin)
reviewer_token=$(login_token reviewer)
operator_token=$(login_token operator)
viewer_token=$(login_token viewer)

curl -fsS "http://127.0.0.1:${BACKEND_PORT}/api/session" -H "Authorization: Bearer $viewer_token" \
  | jq -e '.data.role == "viewer" and (.data.requestId | length > 0)' >/dev/null
curl -fsS "http://127.0.0.1:${BACKEND_PORT}/api/parts?page=1&pageSize=20" -H "Authorization: Bearer $viewer_token" \
  | jq -e '.data | length >= 3' >/dev/null

viewer_write_status=$(curl -sS -o /dev/null -w '%{http_code}' -X POST "http://127.0.0.1:${BACKEND_PORT}/api/parts" \
  -H "Authorization: Bearer $viewer_token" -H 'Content-Type: application/json' -d '{}')
[ "$viewer_write_status" = "403" ]

now=$(date -u '+%Y-%m-%dT%H:%M:%SZ')
suffix=$(date +%s)

inspection_payload=$(printf '{"code":"INSP-SMOKE-%s","name":"Validated inspection task","description":"Passed evidence for freeze","facility":"Validation Hangar","owner":"Inspection Desk","category":"engine","riskLevel":"medium","metricValue":100,"metricUnit":"percent","effectiveAt":"%s","evidence":"inspection report IR-SMOKE","relatedCode":"PART-SMOKE"}' "$suffix" "$now")
inspection=$(curl -fsS -X POST "http://127.0.0.1:${BACKEND_PORT}/api/inspections" \
  -H "Authorization: Bearer $operator_token" -H 'Content-Type: application/json' -H 'X-Request-ID: gb515-inspection-create' \
  -d "$inspection_payload")
inspection_id=$(printf '%s' "$inspection" | jq -er '.data.id')
inspection_version=$(printf '%s' "$inspection" | jq -er '.data.version')
inspection_running=$(curl -fsS -X POST "http://127.0.0.1:${BACKEND_PORT}/api/inspections/${inspection_id}/transition" \
  -H "Authorization: Bearer $operator_token" -H 'Content-Type: application/json' -H 'X-Request-ID: gb515-inspection-run' \
  -d "{\"status\":\"running\",\"expectedVersion\":${inspection_version},\"reason\":\"inspection work started\"}")
inspection_passed=$(curl -fsS -X POST "http://127.0.0.1:${BACKEND_PORT}/api/inspections/${inspection_id}/transition" \
  -H "Authorization: Bearer $operator_token" -H 'Content-Type: application/json' -H 'X-Request-ID: gb515-inspection-pass' \
  -d '{"status":"passed","expectedVersion":2,"reason":"inspection criteria satisfied"}')
inspection_passed_version=$(printf '%s' "$inspection_passed" | jq -er '.data.version')
[ "$inspection_passed_version" = "3" ]

certificate_payload=$(printf '{"code":"CERT-SMOKE-%s","name":"Validated airworthiness certificate","description":"Valid evidence for freeze","facility":"Validation Hangar","owner":"Certificate Desk","category":"engine","riskLevel":"medium","metricValue":100,"metricUnit":"percent","effectiveAt":"%s","evidence":"inspection report IR-SMOKE","relatedCode":"PART-SMOKE"}' "$suffix" "$now")
certificate=$(curl -fsS -X POST "http://127.0.0.1:${BACKEND_PORT}/api/certificates" \
  -H "Authorization: Bearer $operator_token" -H 'Content-Type: application/json' -H 'X-Request-ID: gb515-cert-create' \
  -d "$certificate_payload")
certificate_id=$(printf '%s' "$certificate" | jq -er '.data.id')
certificate_version=$(printf '%s' "$certificate" | jq -er '.data.version')

operator_certificate_status=$(curl -sS -o /dev/null -w '%{http_code}' -X POST "http://127.0.0.1:${BACKEND_PORT}/api/certificates/${certificate_id}/transition" \
  -H "Authorization: Bearer $operator_token" -H 'Content-Type: application/json' -H 'X-Request-ID: gb515-cert-operator-denied' \
  -d "{\"status\":\"valid\",\"expectedVersion\":${certificate_version},\"reason\":\"operator must not publish\"}")
[ "$operator_certificate_status" = "403" ]

certificate_valid=$(curl -fsS -X POST "http://127.0.0.1:${BACKEND_PORT}/api/certificates/${certificate_id}/transition" \
  -H "Authorization: Bearer $reviewer_token" -H 'Content-Type: application/json' -H 'X-Request-ID: gb515-cert-publish' \
  -d "{\"status\":\"valid\",\"expectedVersion\":${certificate_version},\"reason\":\"independent certificate evidence review passed\"}")
certificate_valid_version=$(printf '%s' "$certificate_valid" | jq -er '.data.version')
printf '%s' "$certificate_valid" | jq -e '.data.status == "valid" and .data.preparedBy == "operator" and .data.verifiedBy == "reviewer" and (.data.revisions | length) == 2 and .data.revisions[1].requestId == "gb515-cert-publish"' >/dev/null

# Submission without evidence must keep the draft and report a blocker code.
missing_payload=$(printf '{"code":"AUTH-MISSING-%s","name":"Release without evidence","description":"Freeze rejection validation","facility":"Validation Hangar","owner":"Release Desk","category":"engine","riskLevel":"medium","metricValue":10,"metricUnit":"percent","effectiveAt":"%s","evidence":"none","relatedCode":"PART-MISSING-%s"}' "$suffix" "$now" "$suffix")
missing=$(curl -fsS -X POST "http://127.0.0.1:${BACKEND_PORT}/api/authorizations" \
  -H "Authorization: Bearer $operator_token" -H 'Content-Type: application/json' -H 'X-Request-ID: gb515-auth-missing-create' \
  -d "$missing_payload")
missing_id=$(printf '%s' "$missing" | jq -er '.data.id')
missing_block_status=$(curl -sS -o /tmp/gb515-missing-block.json -w '%{http_code}' -X POST "http://127.0.0.1:${BACKEND_PORT}/api/authorizations/${missing_id}/transition" \
  -H "Authorization: Bearer $operator_token" -H 'Content-Type: application/json' -H 'X-Request-ID: gb515-auth-missing-block' \
  -d '{"status":"review","expectedVersion":1,"reason":"attempt without evidence"}')
[ "$missing_block_status" = "409" ]
jq -e '.error == "evidence_blocked" and (.message | contains("INSPECTION_NOT_PASSED"))' /tmp/gb515-missing-block.json >/dev/null
curl -fsS "http://127.0.0.1:${BACKEND_PORT}/api/authorizations/${missing_id}" -H "Authorization: Bearer $viewer_token" \
  | jq -e '.data.status == "draft" and .data.version == 1 and .data.evidenceConsistent == false and .data.evidenceBlockCode == "INSPECTION_NOT_PASSED" and (.data.evidenceBlockReason | length > 0)' >/dev/null

authorization_payload=$(printf '{"code":"AUTH-SMOKE-%s","name":"Validated component release","description":"Dual-control Compose validation","facility":"Validation Hangar","owner":"Release Desk","category":"engine","riskLevel":"high","metricValue":100,"metricUnit":"percent","effectiveAt":"%s","evidence":"inspection IR-SMOKE and certificate CERT-SMOKE","relatedCode":"PART-SMOKE"}' "$suffix" "$now")
authorization=$(curl -fsS -X POST "http://127.0.0.1:${BACKEND_PORT}/api/authorizations" \
  -H "Authorization: Bearer $operator_token" -H 'Content-Type: application/json' -H 'X-Request-ID: gb515-auth-create' \
  -d "$authorization_payload")
authorization_id=$(printf '%s' "$authorization" | jq -er '.data.id')
authorization_version=$(printf '%s' "$authorization" | jq -er '.data.version')
printf '%s' "$authorization" | jq -e '.data.status == "draft" and .data.version == 1 and .data.revisions[0].actor == "operator" and .data.revisions[0].requestId == "gb515-auth-create" and .data.evidenceConsistent == true' >/dev/null

authorization_review=$(curl -fsS -X POST "http://127.0.0.1:${BACKEND_PORT}/api/authorizations/${authorization_id}/transition" \
  -H "Authorization: Bearer $operator_token" -H 'Content-Type: application/json' -H 'X-Request-ID: gb515-auth-review' \
  -d "{\"status\":\"review\",\"expectedVersion\":${authorization_version},\"reason\":\"inspection and certificate evidence complete\"}")
authorization_review_version=$(printf '%s' "$authorization_review" | jq -er '.data.version')
printf '%s' "$authorization_review" | jq -e --arg inspCode "INSP-SMOKE-${suffix}" --arg certCode "CERT-SMOKE-${suffix}" \
  '.data.status == "review" and .data.submittedBy == "operator" and (.data.revisions | length) == 2
   and .data.frozenInspectionCode == $inspCode and .data.frozenInspectionVersion == 3
   and .data.frozenCertificateCode == $certCode and .data.frozenCertificateVersion == 2
   and .data.evidenceConsistent == true
   and .data.revisions[1].frozenInspectionCode == $inspCode and .data.revisions[1].frozenCertificateVersion == 2' >/dev/null

operator_approval_status=$(curl -sS -o /dev/null -w '%{http_code}' -X POST "http://127.0.0.1:${BACKEND_PORT}/api/authorizations/${authorization_id}/transition" \
  -H "Authorization: Bearer $operator_token" -H 'Content-Type: application/json' -H 'X-Request-ID: gb515-auth-operator-denied' \
  -d "{\"status\":\"approved\",\"expectedVersion\":${authorization_review_version},\"reason\":\"operator must not self approve\"}")
[ "$operator_approval_status" = "403" ]

authorization_approved=$(curl -fsS -X POST "http://127.0.0.1:${BACKEND_PORT}/api/authorizations/${authorization_id}/transition" \
  -H "Authorization: Bearer $reviewer_token" -H 'Content-Type: application/json' -H 'X-Request-ID: gb515-auth-approve' \
  -d "{\"status\":\"approved\",\"expectedVersion\":${authorization_review_version},\"reason\":\"independent airworthiness release review passed\"}")
printf '%s' "$authorization_approved" | jq -e --arg inspCode "INSP-SMOKE-${suffix}" \
  '.data.status == "approved" and .data.version == 3 and .data.submittedBy == "operator" and .data.reviewedBy == "reviewer" and (.data.revisions | length) == 3 and .data.revisions[2].requestId == "gb515-auth-approve" and .data.frozenInspectionCode == $inspCode and .data.frozenInspectionVersion == 3' >/dev/null

# A second release demonstrates frozen-evidence drift: approval is rejected
# with a blocker code, then return/resubmit freezes the new versions and
# the standard dual-control approval succeeds.
drift_payload=$(printf '{"code":"AUTH-DRIFT-%s","name":"Release with drifting evidence","description":"Freeze drift validation","facility":"Validation Hangar","owner":"Release Desk","category":"engine","riskLevel":"high","metricValue":80,"metricUnit":"percent","effectiveAt":"%s","evidence":"IR-SMOKE and CERT-SMOKE","relatedCode":"PART-SMOKE"}' "$suffix" "$now")
drift_id=$(curl -fsS -X POST "http://127.0.0.1:${BACKEND_PORT}/api/authorizations" \
  -H "Authorization: Bearer $operator_token" -H 'Content-Type: application/json' -H 'X-Request-ID: gb515-auth-drift-create' \
  -d "$drift_payload" | jq -er '.data.id')
curl -fsS -X POST "http://127.0.0.1:${BACKEND_PORT}/api/authorizations/${drift_id}/transition" \
  -H "Authorization: Bearer $operator_token" -H 'Content-Type: application/json' -H 'X-Request-ID: gb515-auth-drift-review' \
  -d '{"status":"review","expectedVersion":1,"reason":"evidence complete"}' >/dev/null
curl -fsS -X PUT "http://127.0.0.1:${BACKEND_PORT}/api/inspections/${inspection_id}" \
  -H "Authorization: Bearer $operator_token" -H 'Content-Type: application/json' \
  -d '{"expectedVersion":3,"name":"Validated inspection task","description":"Post-freeze edit","facility":"Validation Hangar","owner":"Inspection Desk","category":"engine","riskLevel":"medium","metricValue":100,"metricUnit":"percent","effectiveAt":"'"$now"'","evidence":"inspection report IR-SMOKE revised","relatedCode":"PART-SMOKE"}' >/dev/null

drift_block_status=$(curl -sS -o /tmp/gb515-drift-block.json -w '%{http_code}' -X POST "http://127.0.0.1:${BACKEND_PORT}/api/authorizations/${drift_id}/transition" \
  -H "Authorization: Bearer $reviewer_token" -H 'Content-Type: application/json' -H 'X-Request-ID: gb515-auth-drift-denied' \
  -d '{"status":"approved","expectedVersion":2,"reason":"approval against drifted evidence"}')
[ "$drift_block_status" = "409" ]
jq -e '.error == "evidence_blocked" and (.message | contains("INSPECTION_VERSION_CHANGED"))' /tmp/gb515-drift-block.json >/dev/null
curl -fsS "http://127.0.0.1:${BACKEND_PORT}/api/authorizations/${drift_id}" -H "Authorization: Bearer $viewer_token" \
  | jq -e '.data.status == "review" and .data.version == 2 and .data.evidenceConsistent == false and .data.evidenceBlockCode == "INSPECTION_VERSION_CHANGED" and .data.currentInspectionVersion == 4 and .data.frozenInspectionVersion == 3' >/dev/null

curl -fsS -X POST "http://127.0.0.1:${BACKEND_PORT}/api/authorizations/${drift_id}/transition" \
  -H "Authorization: Bearer $reviewer_token" -H 'Content-Type: application/json' -H 'X-Request-ID: gb515-auth-drift-return' \
  -d '{"status":"draft","expectedVersion":2,"reason":"evidence drifted, return for resubmission"}' >/dev/null
drift_resubmit=$(curl -fsS -X POST "http://127.0.0.1:${BACKEND_PORT}/api/authorizations/${drift_id}/transition" \
  -H "Authorization: Bearer $operator_token" -H 'Content-Type: application/json' -H 'X-Request-ID: gb515-auth-drift-resubmit' \
  -d '{"status":"review","expectedVersion":3,"reason":"refreeze current evidence"}')
printf '%s' "$drift_resubmit" | jq -e '.data.status == "review" and .data.frozenInspectionVersion == 4 and .data.evidenceConsistent == true' >/dev/null
drift_approved=$(curl -fsS -X POST "http://127.0.0.1:${BACKEND_PORT}/api/authorizations/${drift_id}/transition" \
  -H "Authorization: Bearer $reviewer_token" -H 'Content-Type: application/json' -H 'X-Request-ID: gb515-auth-drift-approve' \
  -d '{"status":"approved","expectedVersion":4,"reason":"dual control approval after resubmit"}')
printf '%s' "$drift_approved" | jq -e '.data.status == "approved" and .data.version == 5 and .data.submittedBy == "operator" and .data.reviewedBy == "reviewer" and .data.frozenInspectionVersion == 4' >/dev/null

curl -fsS "http://127.0.0.1:${BACKEND_PORT}/api/audits/ReleaseAuthorization/${authorization_id}?limit=10" \
  -H "Authorization: Bearer $admin_token" \
  | jq -e '[.data[].requestId] | index("gb515-auth-create") != null and index("gb515-auth-review") != null and index("gb515-auth-approve") != null' >/dev/null
curl -fsS "http://127.0.0.1:${BACKEND_PORT}/api/audits/ReleaseAuthorization/${missing_id}?limit=10" \
  -H "Authorization: Bearer $admin_token" \
  | jq -e '[.data[].action] | index("evidence-block") != null' >/dev/null
curl -fsS "http://127.0.0.1:${BACKEND_PORT}/api/audits/ReleaseAuthorization/${drift_id}?limit=20" \
  -H "Authorization: Bearer $admin_token" \
  | jq -e '([.data[].requestId] | index("gb515-auth-drift-denied") != null) and ([.data[].action] | index("evidence-block") != null)' >/dev/null
curl -fsS "http://127.0.0.1:${BACKEND_PORT}/api/audit-summary?windowHours=24" -H "Authorization: Bearer $admin_token" \
  | jq -e '.data.total >= 5 and .data.transitions >= 3 and .data.uniqueActors >= 2' >/dev/null

docker compose ps
[ "${KEEP_RUNNING:-0}" = "1" ] && echo "KEEP_RUNNING=1: containers left running for built-in Browser validation"
