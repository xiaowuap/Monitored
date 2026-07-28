#!/bin/sh
set -eu

output_dir="${1:-./secrets/pki}"
agent_id="${2:-local-agent}"
server_name="${3:-server}"
server_sans="${4:-DNS:${server_name},DNS:localhost,IP:127.0.0.1}"

if ! command -v openssl >/dev/null 2>&1; then
  echo "openssl is required" >&2
  exit 1
fi

if [ -e "${output_dir}/ca.key" ] || [ -e "${output_dir}/server.key" ] || [ -e "${output_dir}/${agent_id}.key" ]; then
  echo "refusing to overwrite existing keys in ${output_dir}" >&2
  exit 1
fi

mkdir -p "${output_dir}"
chmod 700 "${output_dir}"

openssl genpkey -algorithm EC -pkeyopt ec_paramgen_curve:P-256 -out "${output_dir}/ca.key"
openssl req -x509 -new -sha256 -days 3650 \
  -key "${output_dir}/ca.key" \
  -subj "/CN=Monitored Agent CA" \
  -out "${output_dir}/ca.crt"

openssl genpkey -algorithm EC -pkeyopt ec_paramgen_curve:P-256 -out "${output_dir}/server.key"
openssl req -new -sha256 \
  -key "${output_dir}/server.key" \
  -subj "/CN=${server_name}" \
  -addext "subjectAltName=${server_sans}" \
  -out "${output_dir}/server.csr"
openssl x509 -req -sha256 -days 825 \
  -in "${output_dir}/server.csr" \
  -CA "${output_dir}/ca.crt" \
  -CAkey "${output_dir}/ca.key" \
  -CAcreateserial \
  -copy_extensions copy \
  -out "${output_dir}/server.crt"

openssl genpkey -algorithm EC -pkeyopt ec_paramgen_curve:P-256 -out "${output_dir}/${agent_id}.key"
openssl req -new -sha256 \
  -key "${output_dir}/${agent_id}.key" \
  -subj "/CN=${agent_id}" \
  -addext "subjectAltName=DNS:${agent_id},URI:spiffe://monitored.local/agent/${agent_id}" \
  -out "${output_dir}/${agent_id}.csr"
openssl x509 -req -sha256 -days 90 \
  -in "${output_dir}/${agent_id}.csr" \
  -CA "${output_dir}/ca.crt" \
  -CAkey "${output_dir}/ca.key" \
  -CAcreateserial \
  -copy_extensions copy \
  -out "${output_dir}/${agent_id}.crt"

rm "${output_dir}/server.csr" "${output_dir}/${agent_id}.csr"
chmod 600 "${output_dir}/ca.key"
chmod 640 "${output_dir}/server.key" "${output_dir}/${agent_id}.key"
chmod 644 "${output_dir}"/*.crt
echo "generated CA, server certificate, and 90-day agent certificate in ${output_dir}"
