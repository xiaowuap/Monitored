#!/bin/sh
set -eu

project="monitored-smoke-$$"
export MONITORED_HTTP_PORT=0
export MONITORED_GRPC_PORT=0
export MONITORED_WEB_PORT=0
export MONITORED_AUTH_DISABLED=true
export MONITORED_WORKER_INTERVAL=5s

cleanup() {
  docker compose -p "${project}" down --volumes --remove-orphans >/dev/null 2>&1 || true
}
trap cleanup EXIT INT TERM

docker compose -p "${project}" up -d --build postgres redis server worker

api_port="$(docker compose -p "${project}" port server 8080 | sed 's/.*://')"
attempt=0
until curl -fsS "http://127.0.0.1:${api_port}/healthz" >/dev/null; do
  attempt=$((attempt + 1))
  if [ "${attempt}" -ge 40 ]; then
    docker compose -p "${project}" logs --no-color server worker
    exit 1
  fi
  sleep 1
done

overview="$(curl --fail-with-body -sS "http://127.0.0.1:${api_port}/api/v1/overview")"
case "${overview}" in
  *'"assetCount":0'*'"sensorCount":0'*) ;;
  *)
    echo "Fresh Live database unexpectedly contains demo fixtures: ${overview}" >&2
    exit 1
    ;;
esac

for path in \
  overview assets sensors alerts incidents discovery/runs connections rules \
  maintenance-windows notification-channels notification-routes diagnoses \
  llm/suggestion-jobs agents users capabilities audit
do
  if ! curl --fail-with-body -sS "http://127.0.0.1:${api_port}/api/v1/${path}" >/dev/null; then
    echo "Smoke request failed: GET /api/v1/${path}" >&2
    exit 1
  fi
done

curl --fail-with-body -sS \
  -H "Content-Type: application/json" \
  -d '{"name":"Master health","kind":"http","endpoint":"http://server:8080/healthz","tlsMode":"verify","enabled":true,"config":{"method":"GET","expectedStatus":200},"secret":{}}' \
  "http://127.0.0.1:${api_port}/api/v1/connections" >/dev/null

attempt=0
until curl --fail-with-body -sS "http://127.0.0.1:${api_port}/api/v1/connections" | grep -q '"status":"healthy"'; do
  attempt=$((attempt + 1))
  if [ "${attempt}" -ge 20 ]; then
    docker compose -p "${project}" logs --no-color worker server
    echo "Master-side HTTP connection did not become healthy" >&2
    exit 1
  fi
  sleep 1
done

channel="$(
  curl --fail-with-body -sS \
    -H "Content-Type: application/json" \
    -d '{"name":"Smoke webhook","kind":"webhook","enabled":true,"config":{"url":"http://server:8080/healthz"},"secret":{}}' \
    "http://127.0.0.1:${api_port}/api/v1/notification-channels"
)"
channel_id="$(printf '%s' "${channel}" | node -e 'let b="";process.stdin.on("data",d=>b+=d).on("end",()=>process.stdout.write(JSON.parse(b).id))')"
curl --fail-with-body -sS \
  -H "Content-Type: application/json" \
  -d "{\"channelId\":\"${channel_id}\",\"minSeverity\":\"info\",\"assetKinds\":[],\"groupWindowSeconds\":0,\"repeatIntervalSeconds\":0,\"sendRecovery\":true,\"locale\":\"en-US\",\"enabled\":true}" \
  "http://127.0.0.1:${api_port}/api/v1/notification-routes" >/dev/null
curl --fail-with-body -sS -X POST \
  "http://127.0.0.1:${api_port}/api/v1/notification-channels/${channel_id}/test" >/dev/null

curl --fail-with-body -sS \
  -H "Authorization: Bearer local-development-agent-token" \
  -H "Content-Type: application/json" \
  -d '{"agentId":"smoke-agent","assets":[{"stableId":"smoke:host","kind":"host","name":"smoke-host"}],"sensors":[{"stableId":"smoke:temp","assetStableId":"smoke:host","source":"fixture","sourcePath":"fixture/temp","rawName":"temp","category":"temperature","unit":"Cel","scale":1,"intervalSeconds":10,"quality":"good"}],"samples":[{"sensorStableId":"smoke:temp","value":42,"source":"fixture","quality":"good"}]}' \
  "http://127.0.0.1:${api_port}/api/v1/ingest" >/dev/null

sensor_id="$(
  curl --fail-with-body -sS "http://127.0.0.1:${api_port}/api/v1/sensors" |
    node -e 'let b="";process.stdin.on("data",d=>b+=d).on("end",()=>process.stdout.write(JSON.parse(b).items[0].id))'
)"
curl --fail-with-body -sS \
  -H "Content-Type: application/json" \
  -d "{\"name\":\"Smoke always firing\",\"sensorId\":\"${sensor_id}\",\"ruleType\":\"threshold\",\"severity\":\"warning\",\"expression\":{\"operator\":\"<\",\"value\":1000000000},\"durationSeconds\":0,\"recoveryExpression\":{},\"missingDataMode\":\"ignore\",\"enabled\":true}" \
  "http://127.0.0.1:${api_port}/api/v1/rules" >/dev/null

curl --fail-with-body -sS \
  -H "Authorization: Bearer local-development-agent-token" \
  -H "Content-Type: application/json" \
  -d '{"agentId":"smoke-agent","assets":[{"stableId":"smoke:host","kind":"host","name":"smoke-host"}],"sensors":[{"stableId":"smoke:temp","assetStableId":"smoke:host","source":"fixture","sourcePath":"fixture/temp","rawName":"temp","category":"temperature","unit":"Cel","scale":1,"intervalSeconds":10,"quality":"good"}],"samples":[{"sensorStableId":"smoke:temp","value":43,"source":"fixture","quality":"good"}]}' \
  "http://127.0.0.1:${api_port}/api/v1/ingest" >/dev/null

attempt=0
until curl --fail-with-body -sS "http://127.0.0.1:${api_port}/api/v1/alerts?active=true" | grep -q '"state":"Firing"'; do
  attempt=$((attempt + 1))
  if [ "${attempt}" -ge 20 ]; then
    docker compose -p "${project}" logs --no-color worker server
    echo "Rule did not enter Firing" >&2
    exit 1
  fi
  sleep 1
done
curl --fail-with-body -sS "http://127.0.0.1:${api_port}/api/v1/incidents" | grep -q '"count":1'

curl --fail-with-body -sS \
  -H "Content-Type: application/json" \
  -d '{"provider":"ollama","model":"smoke-model","context":{"fixture":"smoke"}}' \
  "http://127.0.0.1:${api_port}/api/v1/diagnoses" >/dev/null
curl --fail-with-body -sS \
  -H "Content-Type: application/json" \
  -d "{\"sensorId\":\"${sensor_id}\",\"provider\":\"ollama\",\"model\":\"smoke-model\",\"context\":{\"fixture\":\"smoke\"}}" \
  "http://127.0.0.1:${api_port}/api/v1/llm/suggestions/generate" >/dev/null

attempt=0
until curl --fail-with-body -sS "http://127.0.0.1:${api_port}/api/v1/diagnoses" | grep -q '"status":"failed"'; do
  attempt=$((attempt + 1))
  if [ "${attempt}" -ge 20 ]; then
    docker compose -p "${project}" logs --no-color worker server
    echo "Diagnosis queue was not processed" >&2
    exit 1
  fi
  sleep 1
done
curl --fail-with-body -sS "http://127.0.0.1:${api_port}/api/v1/llm/suggestion-jobs" | grep -q '"status":"failed"'

echo "Compose smoke test passed on project ${project}"
