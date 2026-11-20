#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
DOCKER="${DOCKER:-docker}"
GO="${GO:-go}"
OCM="${OCM:-ocm}"
CONTRACT_TMP="$(mktemp -d)"
REGISTRY_NAME="ocm-kit-contracts-${CONTRACT_TMP##*/}"

cleanup() {
  "${DOCKER}" rm -f "${REGISTRY_NAME}" >/dev/null 2>&1 || true
  rm -rf "${CONTRACT_TMP}"
}
trap cleanup EXIT

cd "${SCRIPT_DIR}/.."
"${GO}" build -o "${CONTRACT_TMP}/ocm-kit" ./cmd/ocm-kit
"${DOCKER}" run -d --rm --name "${REGISTRY_NAME}" -p 127.0.0.1::5000 \
  -v "${SCRIPT_DIR}/fixtures/zot-config.json:/etc/zot/config.json:ro" \
  ghcr.io/project-zot/zot:v2.1.10 >/dev/null
REGISTRY_ADDRESS="$("${DOCKER}" port "${REGISTRY_NAME}" 5000/tcp)"
REGISTRY="http://${REGISTRY_ADDRESS}/contracts"
for ((attempt = 0; attempt < 30; attempt++)); do
  if curl -fsS "http://${REGISTRY_ADDRESS}/v2/" >/dev/null 2>&1; then break; fi
  sleep 1
done
curl -fsS "http://${REGISTRY_ADDRESS}/v2/" >/dev/null

for role in application cert-manager database-provider; do
  (
    cd "${SCRIPT_DIR}/fixtures/contracts/${role}"
    "${OCM}" add component-version --repository "${CONTRACT_TMP}/${role}" --constructor component-constructor.yaml
  )
  "${OCM}" transfer component-version \
    "ctf::${CONTRACT_TMP}/${role}//example.org/contract-${role}:1.0.0" "${REGISTRY}" --copy-resources
done

APPLICATION_REF="${REGISTRY}//example.org/contract-application:1.0.0"
CERT_MANAGER_REF="${REGISTRY}//example.org/contract-cert-manager:1.0.0"
DATABASE_PROVIDER_REF="${REGISTRY}//example.org/contract-database-provider:1.0.0"
"${CONTRACT_TMP}/ocm-kit" contract show "${CONTRACT_TMP}/application" -o yaml >"${CONTRACT_TMP}/local.yaml"
"${CONTRACT_TMP}/ocm-kit" contract show "${APPLICATION_REF}" -o yaml >"${CONTRACT_TMP}/remote.yaml"
diff -u "${CONTRACT_TMP}/local.yaml" "${CONTRACT_TMP}/remote.yaml"
"${CONTRACT_TMP}/ocm-kit" contract show "oci::${APPLICATION_REF}" -o yaml >"${CONTRACT_TMP}/typed.yaml"
diff -u "${CONTRACT_TMP}/local.yaml" "${CONTRACT_TMP}/typed.yaml"

"${CONTRACT_TMP}/ocm-kit" contract resolve "${APPLICATION_REF}" --against "${APPLICATION_REF}" \
  >"${CONTRACT_TMP}/missing.txt"
grep -q '\[MISSING\]' "${CONTRACT_TMP}/missing.txt"

"${CONTRACT_TMP}/ocm-kit" contract verify "${APPLICATION_REF}" --against "${CERT_MANAGER_REF}" \
  >"${CONTRACT_TMP}/partial.txt"
grep -q '3 satisfied, 2 missing, 0 unverified, 0 probe errors.' "${CONTRACT_TMP}/partial.txt"
"${CONTRACT_TMP}/ocm-kit" contract verify "${APPLICATION_REF}" \
  --against "${CERT_MANAGER_REF}" --against "${DATABASE_PROVIDER_REF}" \
  >"${CONTRACT_TMP}/multiple-providers.txt"
grep -q '5 satisfied, 0 missing, 0 unverified, 0 probe errors.' "${CONTRACT_TMP}/multiple-providers.txt"
grep -Fq "component:${CERT_MANAGER_REF}" "${CONTRACT_TMP}/multiple-providers.txt"
grep -Fq "component:${DATABASE_PROVIDER_REF}" "${CONTRACT_TMP}/multiple-providers.txt"
"${CONTRACT_TMP}/ocm-kit" contract verify "${APPLICATION_REF}" \
  --against "${CERT_MANAGER_REF}" --against "${DATABASE_PROVIDER_REF}" -o yaml \
  >"${CONTRACT_TMP}/multiple-providers.yaml"
grep -q '^missing: \[\]$' "${CONTRACT_TMP}/multiple-providers.yaml"
grep -Fq "component:${CERT_MANAGER_REF}" "${CONTRACT_TMP}/multiple-providers.yaml"
grep -Fq "component:${DATABASE_PROVIDER_REF}" "${CONTRACT_TMP}/multiple-providers.yaml"
echo "Component contract CTF and registry tests passed."
