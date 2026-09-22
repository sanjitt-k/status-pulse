#!/usr/bin/env bash
set -euo pipefail

# Git Bash must pass Linux paths to Docker without rewriting them as Windows paths.
export MSYS_NO_PATHCONV=1

# Only resources created by this invocation are removed, even on failure.
image=${1:-statuspulse:ci}
container="statuspulse-smoke-${RANDOM}-${RANDOM}"
cleanup() {
  docker logs "$container" 2>/dev/null || true
  docker rm -fv "$container" >/dev/null 2>&1 || true
}
trap cleanup EXIT

docker run -d --name "$container" --read-only --cap-drop ALL \
  --security-opt no-new-privileges:true --tmpfs /tmp \
  --health-interval=1s --health-start-period=1s "$image" >/dev/null

healthy=false
for _ in {1..30}; do
  if [[ $(docker inspect -f '{{.State.Health.Status}}' "$container") == healthy ]]; then
    healthy=true
    break
  fi
  sleep 1
done
if [[ "$healthy" != true ]]; then
  echo 'Container did not become healthy.'
  exit 1
fi

[[ $(docker exec "$container" id -u) == 10001 ]]
docker exec "$container" test -r /etc/ssl/certs/ca-certificates.crt
docker exec "$container" wget -q -O /dev/null http://127.0.0.1:8080/api/services
docker stop --time 10 "$container" >/dev/null
[[ $(docker inspect -f '{{.State.ExitCode}}' "$container") == 0 ]]
docker logs "$container" 2>&1 | grep -q 'StatusPulse stopped'
echo 'Container health, API, non-root user, certificates, and shutdown checks passed.'
