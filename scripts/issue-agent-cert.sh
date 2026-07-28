#!/bin/sh
set -eu

pki_dir="${1:-./secrets/pki}"
agent_id="${2:-}"

if [ -z "${agent_id}" ]; then
  echo "usage: $0 PKI_DIR AGENT_ID" >&2
  exit 2
fi
if ! command -v openssl >/dev/null 2>&1; then
  echo "openssl is required" >&2
  exit 1
fi
if [ ! -r "${pki_dir}/ca.crt" ] || [ ! -r "${pki_dir}/ca.key" ]; then
  echo "CA certificate and key are required in ${pki_dir}" >&2
  exit 1
fi
if [ -e "${pki_dir}/${agent_id}.key" ] || [ -e "${pki_dir}/${agent_id}.crt" ]; then
  echo "refusing to overwrite certificate for ${agent_id}" >&2
  exit 1
fi

openssl genpkey -algorithm EC -pkeyopt ec_paramgen_curve:P-256 \
  -out "${pki_dir}/${agent_id}.key"
openssl req -new -sha256 \
  -key "${pki_dir}/${agent_id}.key" \
  -subj "/CN=${agent_id}" \
  -addext "subjectAltName=DNS:${agent_id},URI:spiffe://monitored.local/agent/${agent_id}" \
  -out "${pki_dir}/${agent_id}.csr"
openssl x509 -req -sha256 -days 90 \
  -in "${pki_dir}/${agent_id}.csr" \
  -CA "${pki_dir}/ca.crt" \
  -CAkey "${pki_dir}/ca.key" \
  -CAcreateserial \
  -copy_extensions copy \
  -out "${pki_dir}/${agent_id}.crt"

rm "${pki_dir}/${agent_id}.csr"
chmod 600 "${pki_dir}/${agent_id}.key"
chmod 644 "${pki_dir}/${agent_id}.crt"
echo "issued 90-day certificate for ${agent_id}"
