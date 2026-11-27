#!/usr/bin/env bash
# Executed on the production host by the verified GitHub Actions deploy job.
set -Eeuo pipefail
umask 077

revision=${1:?target revision is required}
run_id=${2:?GitHub run ID is required}
stage=${3:?staging directory is required}
[[ "$revision" =~ ^[0-9a-f]{40}$ && "$run_id" =~ ^[1-9][0-9]*$ ]]
[[ "$stage" =~ ^/home/karien/\.cqa-image-stage\.${run_id}\.[[:alnum:]]+$ ]]
[[ -d "$stage" && ! -L "$stage" && -O "$stage" ]]

cd /home/karien/cqa-personal-zalo
backup_root=/home/karien/cqa-backups
backup_dir="$backup_root/$run_id"
compose=(docker compose -p cqa-personal-zalo -f docker-compose.yml)
use_checkout_compose() {
  compose=(docker compose -p cqa-personal-zalo -f docker-compose.yml)
  if [[ -f docker-compose.browser-run.yml ]]; then
    compose+=(-f docker-compose.browser-run.yml)
  fi
  if [[ -f docker-compose.override.yml ]]; then
    compose+=(-f docker-compose.override.yml)
  fi
}
services=(app nginx personal-zalo-gateway)
declare -A old_tags old_images new_tags new_images rollback_tags
selected=(app)
checkout_changed=0
rollout_started=0
images_loaded=0

app_health() {
  docker exec cqa-app wget -q -T 5 -O - http://127.0.0.1:8080/health
}

healthy_revision() {
  python3 -c 'import json, sys; status = json.load(sys.stdin); sys.exit(not (status.get("status") == "ok" and status.get("version") == sys.argv[1]))' "$1"
}

public_health() {
  curl -fsS --max-time 10 "https://crm.bbi.vn/health?deploy=$run_id" | healthy_revision "$revision"
}

reload_nginx() {
  docker exec cqa-nginx nginx -t && docker exec cqa-nginx nginx -s reload
}

on_exit() {
  local result=$? recovered=1 service
  trap - EXIT
  set +e
  if (( result != 0 && checkout_changed )); then
    git switch --detach "$previous_commit" || recovered=0
    use_checkout_compose
  fi
  if (( result != 0 && rollout_started )); then
    echo "Deployment failed. Restoring previous service images." >&2
    for service in "${selected[@]}"; do
      docker image tag "${rollback_tags[$service]}" "${old_tags[$service]}" || recovered=0
    done
    "${compose[@]}" up -d --no-build --pull never --no-deps "${selected[@]}" || recovered=0
    for attempt in $(seq 1 12); do
      if app_health >/dev/null 2>&1; then
        break
      fi
      sleep 5
    done
    reload_nginx || recovered=0
    app_health || recovered=0
    for service in "${selected[@]}"; do
      test "$(docker inspect -f '{{.Image}} {{.State.Running}}' "cqa-$service")" = "${old_images[$service]} true" || recovered=0
    done
    if (( recovered )); then
      echo "Previous app health restored. The database backup remains on the VPS."
    else
      echo "WARNING: Automatic recovery could not be verified. The database backup remains on the VPS." >&2
    fi
  fi
  if (( images_loaded )); then
    for service in "${services[@]}"; do
      # Only release this run's temporary import tag. Compose and rollback tags
      # continue to reference the images needed on this host.
      docker image rm "cqa-$service:ci-$revision" >/dev/null 2>&1 || true
    done
  fi
  rm -f -- "$stage/images.tar.gz" "$stage/SHA256SUMS"
  rmdir -- "$stage" || echo "WARNING: Staging directory could not be removed." >&2
  exit "$result"
}
trap on_exit EXIT
trap 'exit 130' INT
trap 'exit 143' HUP TERM

check_capacity() {
  for directory in "$PWD" "$backup_root" "$stage" /var/lib/docker; do
    df -B1 --output=size,avail "$directory" | awk '
      NR == 2 {
        reserve = $1 / 5
        if (reserve < 5368709120) reserve = 5368709120
        if ($2 - 8589934592 >= reserve) disk_ok = 1
      }
      END { exit !disk_ok }
    ' || { echo "Capacity blocked: disk reserve at $directory." >&2; return 1; }
  done
  # The successful off-host rollout used a 2 GiB load/recreate/migration allowance.
  # Keep the same 25%-of-host (minimum 512 MiB) reserve as the previous deploy.
  awk '
    /^MemTotal:/ { total = $2 }
    /^MemAvailable:/ { available = $2 }
    END {
      reserve = total / 4
      if (reserve < 524288) reserve = 524288
      minimum = reserve + 2097152
      printf "RAM available %.0f MiB, required %.0f MiB.\n", available / 1024, minimum / 1024
      exit (!total || available < minimum)
    }
  ' /proc/meminfo
}

# Compare effective service configuration without exposing interpolated secrets.
compose_snapshot() {
  "${compose[@]}" config --format json | python3 -c '
import hashlib, json, sys

def digest(value):
    return hashlib.sha256(json.dumps(value, sort_keys=True).encode()).hexdigest()

config = json.load(sys.stdin)
services = config["services"]
if set(services) != {"app", "nginx", "personal-zalo-gateway", "db"}:
    sys.exit("Unsupported production Compose service set")
print("storage -", digest([config.get("volumes", {}), config.get("networks", {})]))
for name, service in sorted(services.items()):
    print(name, service.get("image") or config["name"] + "-" + name, digest(service))
'
}

# Provision an independent key for the app and personal Zalo gateway.
# Both services already read the VPS .env. Keep an existing value as-is.
provision_personal_zalo_chatbot_secret() {
  python3 - <<'PYTHON'
import os
import re
import secrets
import tempfile
from pathlib import Path

path = Path(".env").resolve(strict=True)
env = path.read_bytes()
pattern = rb"(?m)^[ \t]*(?:export[ \t]+)?PERSONAL_ZALO_CHATBOT_SECRET[ \t]*=[ \t]*([^\r\n]*)"
matches = list(re.finditer(pattern, env))
if any(match.group(1).strip().split(b"#", 1)[0].strip() not in (b"", b"''", b'""') for match in matches):
    raise SystemExit(0)

# Remove empty assignments so Compose sees a single unambiguous value.
env = re.sub(pattern + rb"(?:\r?\n|$)", b"", env)
line = b"PERSONAL_ZALO_CHATBOT_SECRET=" + secrets.token_hex(32).encode("ascii") + b"\n"
fd, tmp = tempfile.mkstemp(prefix=".env.chatbot.", dir=path.parent)
try:
    with os.fdopen(fd, "wb") as stream:
        stream.write(env)
        if env and not env.endswith(b"\n"):
            stream.write(b"\n")
        stream.write(line)
        stream.flush()
        os.fsync(stream.fileno())
    os.replace(tmp, path)
except BaseException:
    os.unlink(tmp)
    raise
PYTHON
}

# Provision a separate bearer token for CQA's management calls to the gateway.
# Both services read the same VPS .env, while the gateway never returns it.
provision_personal_zalo_gateway_admin_secret() {
  python3 - <<'PYTHON'
import os
import re
import secrets
import tempfile
from pathlib import Path

path = Path(".env").resolve(strict=True)
env = path.read_bytes()
pattern = rb"(?m)^[ \t]*(?:export[ \t]+)?PERSONAL_ZALO_GATEWAY_ADMIN_SECRET[ \t]*=[ \t]*([^\r\n]*)"
matches = list(re.finditer(pattern, env))
def value(match):
    result = match.group(1).strip().split(b"#", 1)[0].strip()
    if len(result) >= 2 and result[:1] == result[-1:] and result[:1] in (b"'", b'"'):
        result = result[1:-1]
    return result

configured = [value(match) for match in matches if value(match)]
if configured:
    if len(configured) != 1 or len(configured[0]) < 32:
        raise SystemExit("PERSONAL_ZALO_GATEWAY_ADMIN_SECRET must contain one value of at least 32 characters")
    chatbot_pattern = rb"(?m)^[ \t]*(?:export[ \t]+)?PERSONAL_ZALO_CHATBOT_SECRET[ \t]*=[ \t]*([^\r\n]*)"
    chatbot = [value(match) for match in re.finditer(chatbot_pattern, env) if value(match)]
    if configured[0] in chatbot:
        raise SystemExit("PERSONAL_ZALO_GATEWAY_ADMIN_SECRET must differ from PERSONAL_ZALO_CHATBOT_SECRET")
    raise SystemExit(0)

env = re.sub(pattern + rb"(?:\r?\n|$)", b"", env)
line = b"PERSONAL_ZALO_GATEWAY_ADMIN_SECRET=" + secrets.token_hex(32).encode("ascii") + b"\n"
fd, tmp = tempfile.mkstemp(prefix=".env.zalo-gateway-admin.", dir=path.parent)
try:
    with os.fdopen(fd, "wb") as stream:
        stream.write(env)
        if env and not env.endswith(b"\n"):
            stream.write(b"\n")
        stream.write(line)
        stream.flush()
        os.fsync(stream.fileno())
    os.replace(tmp, path)
except BaseException:
    os.unlink(tmp)
    raise
PYTHON
}

exec 9>"$backup_root/.deploy.lock"
flock -n 9

[[ -f "$stage/images.tar.gz" && ! -L "$stage/images.tar.gz" ]]
[[ -f "$stage/SHA256SUMS" && ! -L "$stage/SHA256SUMS" ]]
(
  cd "$stage"
  [[ "$(sha256sum -- images.tar.gz)" == "$(cat SHA256SUMS)" ]]
)
check_capacity
test "$(docker inspect -f '{{.State.Health.Status}}' cqa-db)" = healthy
app_health >/dev/null
previous_commit=$(git rev-parse HEAD)
use_checkout_compose
before_config=$(compose_snapshot)

git fetch --no-tags origin refs/heads/main
test "$(git rev-parse FETCH_HEAD)" = "$revision" || {
  echo "Deploy skipped because this workflow no longer targets main HEAD." >&2
  exit 1
}
git merge-base --is-ancestor "$previous_commit" "$revision"
test ! -e "$backup_dir" || {
  echo "This run already has a DB snapshot. Start a new workflow run to retry safely." >&2
  exit 1
}

images_loaded=1
docker image load --input "$stage/images.tar.gz"
for service in "${services[@]}"; do
  container="cqa-$service"
  incoming="$container:ci-$revision"
  test "$(docker image inspect -f '{{index .Config.Labels "org.opencontainers.image.revision"}} {{.Os}}/{{.Architecture}}' "$incoming")" = "$revision linux/amd64"
  new_images[$service]=$(docker image inspect -f '{{.Id}}' "$incoming")
  old_tags[$service]=$(docker inspect -f '{{.Config.Image}}' "$container")
  old_images[$service]=$(docker inspect -f '{{.Image}}' "$container")
  rollback_tags[$service]="$container:rollback-$run_id"
done

provision_personal_zalo_chatbot_secret
provision_personal_zalo_gateway_admin_secret

git switch --detach "$revision"
checkout_changed=1
use_checkout_compose
after_config=$(compose_snapshot)
for service in storage db; do
  if [[ "$(awk -v s="$service" '$1 == s' <<< "$before_config")" != "$(awk -v s="$service" '$1 == s' <<< "$after_config")" ]]; then
    echo "Production DB, network, or volume configuration changed; image rollout stopped before replacing containers." >&2
    exit 1
  fi
done
if ! git diff --quiet "$previous_commit" "$revision" -- docker/ ||
  [[ "$(awk '$1 == "nginx"' <<< "$before_config")" != "$(awk '$1 == "nginx"' <<< "$after_config")" ]]; then
  selected+=(nginx)
fi
if ! git diff --quiet "$previous_commit" "$revision" -- personal-zalo-gateway/ ||
  [[ "$(awk '$1 == "personal-zalo-gateway"' <<< "$before_config")" != "$(awk '$1 == "personal-zalo-gateway"' <<< "$after_config")" ]]; then
  selected+=(personal-zalo-gateway)
fi

check_capacity
mkdir -- "$backup_dir"
printf '%s\n' "$previous_commit" > "$backup_dir/previous-commit"
docker exec cqa-db sh -c 'export MYSQL_PWD="$MYSQL_ROOT_PASSWORD"; exec mysqldump --user=root --single-transaction --quick --routines --triggers --no-tablespaces --set-gtid-purged=OFF "$MYSQL_DATABASE"' |
  gzip -1 > "$backup_dir/database.sql.gz.partial"
test -s "$backup_dir/database.sql.gz.partial"
gzip -t "$backup_dir/database.sql.gz.partial"
mv -- "$backup_dir/database.sql.gz.partial" "$backup_dir/database.sql.gz"
echo "Database backup saved and verified."

for service in "${selected[@]}"; do
  new_tags[$service]=$(awk -v s="$service" '$1 == s { print $2 }' <<< "$after_config")
  test -n "${new_tags[$service]}"
  docker image tag "${old_images[$service]}" "${rollback_tags[$service]}"
done
check_capacity
echo "Recreating services: ${selected[*]}"
rollout_started=1
for service in "${selected[@]}"; do
  docker image tag "${new_images[$service]}" "${new_tags[$service]}"
done
"${compose[@]}" up -d --no-build --pull never --no-deps "${selected[@]}"

healthy=0
for attempt in $(seq 1 36); do
  if app_health > "$backup_dir/health.pending.json" &&
    healthy_revision "$revision" < "$backup_dir/health.pending.json" 2>/dev/null; then
    healthy=1
    break
  fi
  sleep 5
done
test "$healthy" = 1
for service in "${selected[@]}"; do
  test "$(docker inspect -f '{{.Image}} {{.State.Running}} {{.RestartCount}}' "cqa-$service")" = "${new_images[$service]} true 0"
done
table_count=$(docker exec -i cqa-db sh -c 'export MYSQL_PWD="$MYSQL_ROOT_PASSWORD"; exec mysql --user=root --batch --skip-column-names "$MYSQL_DATABASE"' <<'SQL'
SELECT COUNT(*) FROM information_schema.tables WHERE table_schema=DATABASE() AND table_name IN ('service_resolutions','messenger_label_states','messenger_label_snapshots','messenger_intake_attributions','messenger_journey_sequences','messenger_journey_events','messenger_journey_customers','messenger_journey_orders','messenger_journey_coverages','messenger_journey_erasures','messenger_journey_receipts','order_actions');
SQL
)
test "$table_count" = 12
docker exec cqa-app sh -c 'test -n "$(find /app/static/assets -name "ServiceQuality-*.js" -print -quit)"'
docker exec cqa-nginx nginx -t
docker exec cqa-personal-zalo-gateway wget -q -T 5 -O /dev/null http://127.0.0.1:3100/health
docker exec cqa-personal-zalo-gateway node --input-type=module -e '
  import { setTimeout as delay } from "node:timers/promises";

  const deadline = Date.now() + 45_000;
  while (true) {
    const response = await fetch("http://127.0.0.1:3100/ready", { signal: AbortSignal.timeout(5000) });
    const { ready, accounts, pool, sync } = await response.json();
    if (response.status !== (ready ? 200 : 503) || !accounts || !pool || !sync) {
      throw new Error("Gateway readiness probe unavailable");
    }
    const starting = !sync.running || pool.connectingAccounts > 0 || pool.scheduledReconnects > 0 ||
      pool.connectedAccounts + pool.disconnectedAccounts + pool.qrPendingAccounts < pool.totalAccounts;
    if ((!starting && sync.inFlight === 0) || Date.now() >= deadline) {
      console.log("Gateway readiness:", JSON.stringify({ready, accounts, pool, sync}));
      break;
    }
    await delay(2500);
  }
'
if [[ -f docker-compose.override.yml ]]; then
  test "$(docker inspect -f '{{len .HostConfig.PortBindings}}' cqa-personal-zalo-gateway)" = 0
fi
gateway_started_at=$(docker inspect -f '{{.State.StartedAt}}' cqa-personal-zalo-gateway)
docker exec cqa-app node --input-type=module -e '
  const secret = process.env.PERSONAL_ZALO_GATEWAY_ADMIN_SECRET?.trim();
  if (!secret || secret.length < 32) throw new Error("Gateway admin secret unavailable");
  const response = await fetch("http://personal-zalo-gateway:3100/api/v1/accounts", {
    headers: { Authorization: `Bearer ${secret}` },
    redirect: "error",
    signal: AbortSignal.timeout(5000),
  });
  if (!response.ok) throw new Error(`Gateway management probe failed: ${response.status}`);
  const payload = await response.json();
  const allowed = new Set([
    "id", "tenantId", "channelId", "accountExternalId", "status", "displayName",
    "avatarUrl", "zaloUid", "qrImage", "qrGeneratedAt", "hasSession", "lastError",
    "lastImportedAt", "syncBlockedReason", "syncBlockedAt", "createdAt", "updatedAt",
  ]);
  if (!Array.isArray(payload.accounts)) throw new Error("Gateway account response is invalid");
  for (const account of payload.accounts) {
    if (!account || typeof account !== "object" || Array.isArray(account) ||
        Object.keys(account).some((key) => !allowed.has(key)) || typeof account.hasSession !== "boolean") {
      throw new Error("Gateway account response exposes an invalid field set");
    }
  }
  const sessions = payload.accounts.filter((account) => account.hasSession).length;
  const startedAt = Date.parse(process.argv[1]);
  if (!Number.isFinite(startedAt)) throw new Error("Gateway startup time unavailable");
  const imported = payload.accounts.filter((account) => Date.parse(account.lastImportedAt) >= startedAt).length;
  console.log(`Personal Zalo gateway management API verified: accounts=${payload.accounts.length}, reusable_sessions=${sessions}, imported_since_start=${imported}`);
' "$gateway_started_at"
if ! public_health; then
  reload_nginx
  for attempt in $(seq 1 5); do
    sleep 3
    if public_health; then
      break
    fi
  done
  public_health
fi

mv -- "$backup_dir/health.pending.json" "$backup_dir/health.json"
echo "Verified revision $revision, $table_count schema tables and public health."
rollout_started=0
checkout_changed=0

if ! python3 scripts/retain_deploy_backups.py "$backup_root" "$run_id"; then
  echo "WARNING: Backup retention did not complete. The verified deployment is still active."
fi
