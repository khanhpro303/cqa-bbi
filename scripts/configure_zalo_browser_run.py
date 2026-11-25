#!/usr/bin/env python3
"""Install Browser Run secrets for the production app, using a private stdin payload."""

import argparse
import fcntl
import json
import os
import re
import signal
import stat
import subprocess
import sys
import tempfile
import time
from pathlib import Path

ROOT = Path("/home/karien/cqa-personal-zalo")
ENV_FILE = ROOT.parent / ".cqa-zalo-browser-run.env"
DEPLOY_LOCK = ROOT.parent / "cqa-backups" / ".deploy.lock"
COMPOSE = [
    "docker", "compose", "-p", "cqa-personal-zalo",
    "-f", "docker-compose.yml", "-f", "docker-compose.browser-run.yml",
]
KEYS = (
    "ZALO_ORDER_SCREENSHOT_CF_ACCOUNT_ID",
    "ZALO_ORDER_SCREENSHOT_CF_API_TOKEN",
    "ZALO_ORDER_SCREENSHOT_PROFILE",
)
SAFE_ID = re.compile(r"[A-Za-z0-9_-]{1,128}")
MAX_PROFILE_BYTES = 48 * 1024
MAX_PAYLOAD_BYTES = 2 * MAX_PROFILE_BYTES + 4096


class ConfigurationError(Exception):
    """An error whose message is safe to log."""


def validate_settings(settings, group_id, scope="pilot"):
    if scope == "pilot":
        if not SAFE_ID.fullmatch(group_id):
            raise ConfigurationError("Invalid Zalo test group ID.")
    elif scope != "all_linked_groups" or group_id:
        raise ConfigurationError("Invalid Browser Run capture scope.")
    if not isinstance(settings, dict) or set(settings) != set(KEYS):
        raise ConfigurationError("Incomplete Browser Run settings.")
    if any(not isinstance(value, str) for value in settings.values()):
        raise ConfigurationError("Browser Run settings must be text.")
    account, token, raw_profile = (settings[key] for key in KEYS)
    if not re.fullmatch(r"[0-9a-fA-F]{32}", account):
        raise ConfigurationError("Missing or invalid Cloudflare account ID.")
    if not re.fullmatch(r"[A-Za-z0-9_.-]{20,256}", token):
        raise ConfigurationError("Missing or invalid Cloudflare API token.")
    if len(raw_profile.encode()) > MAX_PROFILE_BYTES:
        raise ConfigurationError("The Browser Run profile exceeds GitHub's secret size limit.")
    try:
        profile = json.loads(raw_profile)
    except ValueError:
        raise ConfigurationError("The Browser Run profile must be valid JSON.") from None
    if not isinstance(profile, dict):
        raise ConfigurationError("The Browser Run profile must be a JSON object.")
    groups = profile.get("zalo_group_ids", [])
    all_groups = profile.get("all_linked_groups", False)
    if scope == "pilot" and (all_groups is not False or groups != [group_id]):
        raise ConfigurationError("The pilot profile must allow exactly the approved test group.")
    if scope == "all_linked_groups" and (all_groups is not True or groups != []):
        raise ConfigurationError("The all-groups profile must explicitly enable all linked groups and omit group IDs.")
    for field in ("tenant_id", "channel_id"):
        value = profile.get(field)
        if not isinstance(value, str) or not SAFE_ID.fullmatch(value):
            raise ConfigurationError("The Browser Run profile must specify a valid tenant and channel.")
    zalo_oa_id = profile.get("zalo_oa_id")
    if not isinstance(zalo_oa_id, str) or not re.fullmatch(r"[0-9]{1,32}", zalo_oa_id):
        raise ConfigurationError("The Browser Run profile must specify a valid Zalo OA ID.")
    state = profile.get("storage_state")
    if not isinstance(state, dict) or not (state.get("cookies") or state.get("origins")):
        raise ConfigurationError("The Browser Run profile is missing a browser session.")
    # Encode the two dotenv-sensitive characters inside JSON strings itself.
    # The decoded profile is identical, including cookie and storage values.
    profile_json = json.dumps(profile, ensure_ascii=False, separators=(",", ":"))
    profile_json = profile_json.replace("'", "\\u0027").replace("$", "\\u0024")
    if len(profile_json.encode()) > MAX_PROFILE_BYTES:
        raise ConfigurationError("The normalized Browser Run profile is too large.")
    return {**settings, KEYS[2]: profile_json}


def env_content(settings):
    # validate_settings ensures the values are single-line and contain no apostrophes.
    return "".join(f"{key}='{settings[key]}'\n" for key in KEYS).encode()


def write_private(path, body, mode):
    fd, name = tempfile.mkstemp(prefix=".cqa-zalo-browser-run-", dir=path.parent)
    try:
        with os.fdopen(fd, "wb") as stream:
            os.fchmod(stream.fileno(), mode)
            stream.write(body)
            stream.flush()
            os.fsync(stream.fileno())
        os.replace(name, path)
    finally:
        Path(name).unlink(missing_ok=True)


def run(command, *, capture=False):
    # Compose errors and inspect output can contain confidential values.
    return subprocess.run(
        command,
        cwd=ROOT,
        stdin=subprocess.DEVNULL,
        stdout=subprocess.PIPE if capture else subprocess.DEVNULL,
        stderr=subprocess.DEVNULL,
        check=True,
        timeout=120,
    ).stdout


def compose_config():
    return json.loads(run(COMPOSE + ["config", "--format", "json"], capture=True))


def check_compose_change(previous, settings):
    updated = compose_config()
    app = updated["services"]["app"]
    app_env = app["environment"]
    if any(app_env.get(key) != value for key, value in settings.items()):
        raise ConfigurationError("Compose did not load the Browser Run settings exactly.")
    for key in KEYS:
        app_env.pop(key, None)
        previous["services"]["app"]["environment"].pop(key, None)
    if updated != previous:
        raise ConfigurationError("Compose configuration changed outside the Browser Run app settings.")


def app_image():
    status = run(["docker", "inspect", "-f", "{{.Image}} {{.State.Running}}", "cqa-app"], capture=True)
    image, running = status.decode().split()
    if running != "true":
        raise ConfigurationError("The current production app container is not running.")
    return image


def runtime_settings():
    data = run(["docker", "inspect", "-f", "{{json .Config.Env}}", "cqa-app"], capture=True)
    settings = dict(line.split("=", 1) for line in json.loads(data))
    return {key: settings[key] for key in KEYS if key in settings}


def is_healthy(command, revision):
    try:
        health = json.loads(run(command, capture=True))
        return health.get("status") == "ok" and health.get("version") == revision
    except (subprocess.SubprocessError, ValueError):
        return False


LOCAL_HEALTH = ["docker", "exec", "cqa-app", "wget", "-q", "-T", "5", "-O", "-", "http://127.0.0.1:8080/health"]


def public_health(run_id):
    return ["curl", "-fsS", "--max-time", "10", f"https://crm.bbi.vn/health?browser_run={run_id}"]


def wait_for_app(revision, image, settings, run_id):
    for _ in range(20):
        if is_healthy(LOCAL_HEALTH, revision):
            break
        time.sleep(3)
    else:
        raise ConfigurationError("Production app did not reach the expected internal health.")
    if app_image() != image or runtime_settings() != settings:
        raise ConfigurationError("Production app image or runtime settings do not match the expected values.")
    public = public_health(run_id)
    if is_healthy(public, revision):
        return
    run(["docker", "exec", "cqa-nginx", "nginx", "-t"])
    run(["docker", "exec", "cqa-nginx", "nginx", "-s", "reload"])
    for _ in range(5):
        if is_healthy(public, revision):
            return
        time.sleep(3)
    raise ConfigurationError("Production app did not reach the expected public health.")


def recreate_app():
    run(COMPOSE + ["up", "-d", "--no-build", "--pull", "never", "--no-deps", "app"])


def apply_settings(settings, revision, run_id):
    os.umask(0o077)
    with DEPLOY_LOCK.open("a") as lock:
        fcntl.flock(lock, fcntl.LOCK_EX | fcntl.LOCK_NB)
        if run(["git", "rev-parse", "HEAD"], capture=True).decode().strip() != revision:
            raise ConfigurationError("Production is not running the workflow revision. Complete its deploy first.")
        image = app_image()
        old_settings = runtime_settings()
        if not is_healthy(LOCAL_HEALTH, revision) or not is_healthy(public_health(run_id), revision):
            raise ConfigurationError("Production app is not healthy on the expected revision.")
        info = ENV_FILE.lstat() if os.path.lexists(ENV_FILE) else None
        mode = stat.S_IMODE(info.st_mode) if info else 0o600
        if info and (not stat.S_ISREG(info.st_mode) or mode & 0o077 or info.st_uid != os.getuid() or info.st_size > MAX_PAYLOAD_BYTES):
            raise ConfigurationError("Existing Browser Run environment file is not a private regular file.")
        old_file = ENV_FILE.read_bytes() if info else None
        original = compose_config()
        app_reference = original["services"]["app"].get("image") or f"{original['name']}-app"
        tagged_image = run(["docker", "image", "inspect", "-f", "{{.Id}}", app_reference], capture=True)
        if tagged_image.decode().strip() != image:
            raise ConfigurationError("The Compose app image does not match the running production image.")
        if any(key in original["services"].get("personal-zalo-gateway", {}).get("environment", {}) for key in KEYS):
            raise ConfigurationError("Browser Run settings must be removed from the shared environment first.")
        new_file = env_content(settings)
        if old_file == new_file and old_settings == settings:
            print("Production Browser Run settings already match.")
            return

        recreate_started = False
        try:
            write_private(ENV_FILE, new_file, mode)
            check_compose_change(original, settings)
            recreate_started = True
            recreate_app()
            wait_for_app(revision, image, settings, run_id)
        except (ConfigurationError, OSError, subprocess.SubprocessError, ValueError, KeyError, TypeError):
            print("Browser Run configuration failed. Restoring previous app settings.", file=sys.stderr)
            try:
                if old_file is None:
                    ENV_FILE.unlink(missing_ok=True)
                else:
                    write_private(ENV_FILE, old_file, mode)
                if recreate_started:
                    recreate_app()
                    wait_for_app(revision, image, old_settings, run_id)
            except (ConfigurationError, OSError, subprocess.SubprocessError, ValueError, KeyError, TypeError):
                raise ConfigurationError("Automatic rollback could not be verified.") from None
            raise ConfigurationError("Previous Browser Run environment and app settings restored.") from None
        print("Production app is healthy on the same revision with Browser Run settings applied.")


def abort(*_):
    raise ConfigurationError("Configuration interrupted.")


def main():
    parser = argparse.ArgumentParser()
    mode = parser.add_mutually_exclusive_group(required=True)
    parser.add_argument("--scope", choices=("pilot", "all_linked_groups"), default="pilot")
    mode.add_argument("--emit", metavar="GROUP_ID")
    mode.add_argument("--apply", nargs=3, metavar=("REVISION", "GROUP_ID", "RUN_ID"))
    args = parser.parse_args()
    try:
        if args.emit:
            settings = validate_settings({key: os.environ.get(key, "") for key in KEYS}, args.emit, args.scope)
            sys.stdout.write(json.dumps(settings, ensure_ascii=False))
            return
        revision, group_id, run_id = args.apply
        if not re.fullmatch(r"[0-9a-f]{40}", revision) or not re.fullmatch(r"[1-9][0-9]*", run_id):
            raise ConfigurationError("Invalid revision or workflow run ID.")
        payload = sys.stdin.buffer.read(MAX_PAYLOAD_BYTES + 1)
        if len(payload) > MAX_PAYLOAD_BYTES:
            raise ConfigurationError("Browser Run settings payload is too large.")
        settings = validate_settings(json.loads(payload), group_id, args.scope)
        for signum in (signal.SIGHUP, signal.SIGTERM, signal.SIGINT):
            signal.signal(signum, abort)
        apply_settings(settings, revision, run_id)
    except ConfigurationError as error:
        sys.exit(str(error))
    except (OSError, subprocess.SubprocessError, ValueError, KeyError, TypeError):
        sys.exit("Unable to validate or apply Browser Run settings.")


if __name__ == "__main__":
    main()
