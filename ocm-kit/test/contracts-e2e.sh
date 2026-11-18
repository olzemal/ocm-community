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

for role in consumer provider; do
  (
    cd "${SCRIPT_DIR}/fixtures/contracts/${role}"
    "${OCM}" add component-version --repository "${CONTRACT_TMP}/${role}" --constructor component-constructor.yaml
  )
  "${OCM}" transfer component-version \
    "ctf::${CONTRACT_TMP}/${role}//example.org/contract-${role}:1.0.0" "${REGISTRY}" --copy-resources
done

CONSUMER_REF="${REGISTRY}//example.org/contract-consumer:1.0.0"
PROVIDER_REF="${REGISTRY}//example.org/contract-provider:1.0.0"
"${CONTRACT_TMP}/ocm-kit" contract show "${CONTRACT_TMP}/consumer" -o yaml >"${CONTRACT_TMP}/local.yaml"
"${CONTRACT_TMP}/ocm-kit" contract show "${CONSUMER_REF}" -o yaml >"${CONTRACT_TMP}/remote.yaml"
diff -u "${CONTRACT_TMP}/local.yaml" "${CONTRACT_TMP}/remote.yaml"
"${CONTRACT_TMP}/ocm-kit" contract show "oci::${CONSUMER_REF}" -o yaml >"${CONTRACT_TMP}/typed.yaml"
diff -u "${CONTRACT_TMP}/local.yaml" "${CONTRACT_TMP}/typed.yaml"

"${CONTRACT_TMP}/ocm-kit" contract resolve "${CONSUMER_REF}" --against "${PROVIDER_REF}" \
  -o yaml >"${CONTRACT_TMP}/result.yaml"
grep -q '^unverified: \[\]$' "${CONTRACT_TMP}/result.yaml"
grep -q '^missing: \[\]$' "${CONTRACT_TMP}/result.yaml"
grep -q "component:${PROVIDER_REF}" "${CONTRACT_TMP}/result.yaml"
"${CONTRACT_TMP}/ocm-kit" contract verify "${CONSUMER_REF}" --against "${PROVIDER_REF}"

"${CONTRACT_TMP}/ocm-kit" contract resolve "${CONSUMER_REF}" --against "${CONSUMER_REF}" \
  >"${CONTRACT_TMP}/missing.txt"
grep -q '\[MISSING\]' "${CONTRACT_TMP}/missing.txt"
echo "Component contract CTF and registry tests passed."
