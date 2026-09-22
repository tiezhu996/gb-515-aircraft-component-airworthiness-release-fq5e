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
related="REL-SMOKE-${suffix}"

# 证据冻结校验：先证明缺失证据时提交保持草案并返回阻断编号。
blocked_payload=$(printf '{"code":"AUTH-BLOCK-%s","name":"Blocked component release","description":"Evidence freeze gate","facility":"Validation Hangar","owner":"Release Desk","category":"engine","riskLevel":"high","metricValue":100,"metricUnit":"percent","effectiveAt":"%s","evidence":"evidence not yet available","relatedCode":"%s"}' "$suffix" "$now" "$related")
blocked=$(curl -fsS -X POST "http://127.0.0.1:${BACKEND_PORT}/api/authorizations" \
  -H "Authorization: Bearer $operator_token" -H 'Content-Type: application/json' -H 'X-Request-ID: gb515-auth-block-create' \
  -d "$blocked_payload")
blocked_id=$(printf '%s' "$blocked" | jq -er '.data.id')
blocked_status_code=$(curl -sS -o /tmp/gb515-block.json -w '%{http_code}' -X POST "http://127.0.0.1:${BACKEND_PORT}/api/authorizations/${blocked_id}/transition" \
  -H "Authorization: Bearer $operator_token" -H 'Content-Type: application/json' -H 'X-Request-ID: gb515-auth-block-submit' \
  -d "{\"status\":\"review\",\"expectedVersion\":1,\"reason\":\"attempt submit before evidence is ready\"}")
[ "$blocked_status_code" = "422" ]
jq -e '.error == "evidence_blocked" and .details.blockCode == "EVIDENCE-INSPECTION-MISSING"' /tmp/gb515-block.json >/dev/null
# 任一缺失或状态不合就保持草案：状态、版本均不变。
curl -fsS "http://127.0.0.1:${BACKEND_PORT}/api/authorizations/${blocked_id}" -H "Authorization: Bearer $viewer_token" \
  | jq -e '.data.status == "draft" and .data.version == 1 and .data.evidenceStatus.consistent == false and .data.evidenceStatus.blockCode == "EVIDENCE-INSPECTION-MISSING"' >/dev/null

# 准备同一关联编号下的已通过检查任务和有效证书。
inspection_payload=$(printf '{"code":"INSP-SMOKE-%s","name":"Smoke inspection task","description":"Passed inspection evidence","facility":"Validation Hangar","owner":"Inspection Desk","category":"engine","riskLevel":"medium","metricValue":100,"metricUnit":"percent","effectiveAt":"%s","evidence":"inspection worksheet IW-SMOKE","relatedCode":"%s"}' "$suffix" "$now" "$related")
inspection=$(curl -fsS -X POST "http://127.0.0.1:${BACKEND_PORT}/api/inspections" \
  -H "Authorization: Bearer $operator_token" -H 'Content-Type: application/json' -H 'X-Request-ID: gb515-insp-create' \
  -d "$inspection_payload")
inspection_id=$(printf '%s' "$inspection" | jq -er '.data.id')
inspection=$(curl -fsS -X POST "http://127.0.0.1:${BACKEND_PORT}/api/inspections/${inspection_id}/transition" \
  -H "Authorization: Bearer $operator_token" -H 'Content-Type: application/json' -H 'X-Request-ID: gb515-insp-run' \
  -d '{"status":"running","expectedVersion":1,"reason":"inspection work started"}')
inspection=$(curl -fsS -X POST "http://127.0.0.1:${BACKEND_PORT}/api/inspections/${inspection_id}/transition" \
  -H "Authorization: Bearer $operator_token" -H 'Content-Type: application/json' -H 'X-Request-ID: gb515-insp-pass' \
  -d '{"status":"passed","expectedVersion":2,"reason":"inspection completed and passed"}')
inspection_version=$(printf '%s' "$inspection" | jq -er '.data.version')
[ "$inspection_version" = "3" ]
printf '%s' "$inspection" | jq -e '.data.status == "passed"' >/dev/null

certificate_payload=$(printf '{"code":"CERT-SMOKE-%s","name":"Validated airworthiness certificate","description":"Dual-control Compose validation","facility":"Validation Hangar","owner":"Certificate Desk","category":"engine","riskLevel":"medium","metricValue":100,"metricUnit":"percent","effectiveAt":"%s","evidence":"inspection report IR-SMOKE","relatedCode":"%s"}' "$suffix" "$now" "$related")
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
printf '%s' "$certificate_valid" | jq -e '.data.status == "valid" and .data.version == 2 and .data.preparedBy == "operator" and .data.verifiedBy == "reviewer" and (.data.revisions | length) == 2 and .data.revisions[1].requestId == "gb515-cert-publish"' >/dev/null
certificate_version=2

# 证据齐备后放行草稿可提交复核，两端版本写入提交快照。
authorization_payload=$(printf '{"code":"AUTH-SMOKE-%s","name":"Validated component release","description":"Dual-control Compose validation","facility":"Validation Hangar","owner":"Release Desk","category":"engine","riskLevel":"high","metricValue":100,"metricUnit":"percent","effectiveAt":"%s","evidence":"inspection IR-SMOKE and certificate CERT-SMOKE","relatedCode":"%s"}' "$suffix" "$now" "$related")
authorization=$(curl -fsS -X POST "http://127.0.0.1:${BACKEND_PORT}/api/authorizations" \
  -H "Authorization: Bearer $operator_token" -H 'Content-Type: application/json' -H 'X-Request-ID: gb515-auth-create' \
  -d "$authorization_payload")
authorization_id=$(printf '%s' "$authorization" | jq -er '.data.id')
authorization_version=$(printf '%s' "$authorization" | jq -er '.data.version')
printf '%s' "$authorization" | jq -e '.data.status == "draft" and .data.version == 1 and .data.revisions[0].actor == "operator" and .data.revisions[0].requestId == "gb515-auth-create"' >/dev/null

authorization_review=$(curl -fsS -X POST "http://127.0.0.1:${BACKEND_PORT}/api/authorizations/${authorization_id}/transition" \
  -H "Authorization: Bearer $operator_token" -H 'Content-Type: application/json' -H 'X-Request-ID: gb515-auth-review' \
  -d "{\"status\":\"review\",\"expectedVersion\":${authorization_version},\"reason\":\"inspection and certificate evidence complete\"}")
authorization_review_version=$(printf '%s' "$authorization_review" | jq -er '.data.version')
printf '%s' "$authorization_review" | jq -e --arg insp "INSP-SMOKE-${suffix}" --arg cert "CERT-SMOKE-${suffix}" '.data.status == "review" and .data.submittedBy == "operator" and (.data.revisions | length) == 2 and .data.frozenInspectionCode == $insp and .data.frozenInspectionVersion == 3 and .data.frozenCertificateCode == $cert and .data.frozenCertificateVersion == 2 and .data.evidenceStatus.consistent == true' >/dev/null

operator_approval_status=$(curl -sS -o /dev/null -w '%{http_code}' -X POST "http://127.0.0.1:${BACKEND_PORT}/api/authorizations/${authorization_id}/transition" \
  -H "Authorization: Bearer $operator_token" -H 'Content-Type: application/json' -H 'X-Request-ID: gb515-auth-operator-denied' \
  -d "{\"status\":\"approved\",\"expectedVersion\":${authorization_review_version},\"reason\":\"operator must not self approve\"}")
[ "$operator_approval_status" = "403" ]

# 提交后证据版本变化：证书由复核员推进新版本，批准必须拒绝并提示重新提交。
curl -fsS -X POST "http://127.0.0.1:${BACKEND_PORT}/api/certificates/${certificate_id}/transition" \
  -H "Authorization: Bearer $reviewer_token" -H 'Content-Type: application/json' -H 'X-Request-ID: gb515-cert-expire' \
  -d '{"status":"expired","expectedVersion":2,"reason":"post-submission evidence version drift"}' >/dev/null
drift_status_code=$(curl -sS -o /tmp/gb515-drift.json -w '%{http_code}' -X POST "http://127.0.0.1:${BACKEND_PORT}/api/authorizations/${authorization_id}/transition" \
  -H "Authorization: Bearer $reviewer_token" -H 'Content-Type: application/json' -H 'X-Request-ID: gb515-auth-drift-denied' \
  -d "{\"status\":\"approved\",\"expectedVersion\":${authorization_review_version},\"reason\":\"approve while evidence drifted\"}")
[ "$drift_status_code" = "409" ]
jq -e '.error == "evidence_drifted" and (.details.blockCode == "DRIFT-CERTIFICATE-VERSION" or .details.blockCode == "DRIFT-CERTIFICATE-MISSING")' /tmp/gb515-drift.json >/dev/null
# 阻断不改变放行状态，刷新后仍可回读到冻结版本、当前版本与阻断原因。
curl -fsS "http://127.0.0.1:${BACKEND_PORT}/api/authorizations/${authorization_id}" -H "Authorization: Bearer $viewer_token" \
  | jq -e '.data.status == "review" and .data.version == 2 and .data.frozenCertificateVersion == 2 and .data.evidenceStatus.consistent == false and (.data.evidenceStatus.blockCode | startswith("DRIFT-CERTIFICATE"))' >/dev/null

# 复核员退回草案，操作员刷新证据（证书重新发布），重新提交冻结新版本后按双人复核批准。
authorization_draft=$(curl -fsS -X POST "http://127.0.0.1:${BACKEND_PORT}/api/authorizations/${authorization_id}/transition" \
  -H "Authorization: Bearer $reviewer_token" -H 'Content-Type: application/json' -H 'X-Request-ID: gb515-auth-return' \
  -d "{\"status\":\"draft\",\"expectedVersion\":${authorization_review_version},\"reason\":\"evidence drifted, please resubmit\"}")
draft_version=$(printf '%s' "$authorization_draft" | jq -er '.data.version')
printf '%s' "$authorization_draft" | jq -e '.data.status == "draft" and .data.frozenCertificateCode == ""' >/dev/null

# 重新建立有效证书（原证书已过期不可逆转），保持同一关联编号并发布为新版本。
new_certificate_payload=$(printf '{"code":"CERT-SMOKE-R-%s","name":"Reissued airworthiness certificate","description":"Post drift resubmission","facility":"Validation Hangar","owner":"Certificate Desk","category":"engine","riskLevel":"medium","metricValue":100,"metricUnit":"percent","effectiveAt":"%s","evidence":"inspection report IR-SMOKE revalidated","relatedCode":"%s"}' "$suffix" "$now" "$related")
new_certificate=$(curl -fsS -X POST "http://127.0.0.1:${BACKEND_PORT}/api/certificates" \
  -H "Authorization: Bearer $operator_token" -H 'Content-Type: application/json' -H 'X-Request-ID: gb515-cert-recreate' \
  -d "$new_certificate_payload")
new_certificate_id=$(printf '%s' "$new_certificate" | jq -er '.data.id')
curl -fsS -X POST "http://127.0.0.1:${BACKEND_PORT}/api/certificates/${new_certificate_id}/transition" \
  -H "Authorization: Bearer $reviewer_token" -H 'Content-Type: application/json' -H 'X-Request-ID: gb515-cert-republish' \
  -d '{"status":"valid","expectedVersion":1,"reason":"reissue valid certificate after evidence refresh"}' >/dev/null

authorization_resubmit=$(curl -fsS -X POST "http://127.0.0.1:${BACKEND_PORT}/api/authorizations/${authorization_id}/transition" \
  -H "Authorization: Bearer $operator_token" -H 'Content-Type: application/json' -H 'X-Request-ID: gb515-auth-resubmit' \
  -d "{\"status\":\"review\",\"expectedVersion\":${draft_version},\"reason\":\"resubmit against refreshed evidence\"}")
resubmit_version=$(printf '%s' "$authorization_resubmit" | jq -er '.data.version')
printf '%s' "$authorization_resubmit" | jq -e --arg cert "CERT-SMOKE-R-${suffix}" '.data.frozenInspectionVersion == 3 and .data.frozenCertificateCode == $cert and .data.frozenCertificateVersion == 2 and .data.evidenceStatus.consistent == true' >/dev/null

authorization_approved=$(curl -fsS -X POST "http://127.0.0.1:${BACKEND_PORT}/api/authorizations/${authorization_id}/transition" \
  -H "Authorization: Bearer $reviewer_token" -H 'Content-Type: application/json' -H 'X-Request-ID: gb515-auth-approve' \
  -d "{\"status\":\"approved\",\"expectedVersion\":${resubmit_version},\"reason\":\"independent airworthiness release review passed\"}")
printf '%s' "$authorization_approved" | jq -e --arg cert "CERT-SMOKE-R-${suffix}" '.data.status == "approved" and .data.submittedBy == "operator" and .data.reviewedBy == "reviewer" and .data.frozenCertificateCode == $cert and .data.evidenceStatus.consistent == true' >/dev/null
# 已批准记录保留当时证据，后续草稿式更新不得改写。
approve_final_status=$(curl -sS -o /dev/null -w '%{http_code}' -X PUT "http://127.0.0.1:${BACKEND_PORT}/api/authorizations/${authorization_id}" \
  -H "Authorization: Bearer $operator_token" -H 'Content-Type: application/json' \
  -d "{\"expectedVersion\":${resubmit_version},\"name\":\"Validated component release\",\"description\":\"late edit must be locked\",\"facility\":\"Validation Hangar\",\"owner\":\"Release Desk\",\"category\":\"engine\",\"riskLevel\":\"high\",\"metricValue\":100,\"metricUnit\":\"percent\",\"effectiveAt\":\"${now}\",\"evidence\":\"tampered evidence\",\"relatedCode\":\"${related}\"}")
[ "$approve_final_status" = "409" ]

curl -fsS "http://127.0.0.1:${BACKEND_PORT}/api/audits/ReleaseAuthorization/${authorization_id}?limit=10" \
  -H "Authorization: Bearer $admin_token" \
  | jq -e '[.data[].requestId] | index("gb515-auth-create") != null and index("gb515-auth-review") != null and index("gb515-auth-resubmit") != null and index("gb515-auth-approve") != null' >/dev/null
curl -fsS "http://127.0.0.1:${BACKEND_PORT}/api/audit-summary?windowHours=24" -H "Authorization: Bearer $admin_token" \
  | jq -e '.data.total >= 5 and .data.transitions >= 3 and .data.uniqueActors >= 2' >/dev/null

docker compose ps
[ "${KEEP_RUNNING:-0}" = "1" ] && echo "KEEP_RUNNING=1: containers left running for built-in Browser validation"
