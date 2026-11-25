import io
import json
import shutil
import stat
import subprocess
import tempfile
import unittest
from pathlib import Path
from contextlib import redirect_stderr
from unittest.mock import patch, Mock

import configure_zalo_browser_run as config


def fixtures():
    profile = {
        "tenant_id": "tenant-fixture",
        "channel_id": "channel-fixture",
        "zalo_oa_id": "1234567890",
        "zalo_group_ids": ["test-group"],
        "storage_state": {"cookies": [{"domain": ".zalo.me", "value": "fixture '${SAMPLE}\\🧪"}]},
    }
    return dict(zip(config.KEYS, ("a" * 32, "b" * 32, json.dumps(profile, ensure_ascii=False))))


class ConfigureZaloBrowserRunTests(unittest.TestCase):
    def test_all_groups_requires_explicit_scope_and_no_group_ids(self):
        raw = fixtures()
        profile = json.loads(raw[config.KEYS[2]])
        profile.pop("zalo_group_ids")
        profile["all_linked_groups"] = True
        raw[config.KEYS[2]] = json.dumps(profile)
        settings = config.validate_settings(raw, "", "all_linked_groups")
        self.assertEqual(json.loads(settings[config.KEYS[2]]), profile)
        for group_id, scope in (("test-group", "pilot"), ("test-group", "all_linked_groups")):
            with self.assertRaises(config.ConfigurationError):
                config.validate_settings(raw, group_id, scope)
        profile["zalo_group_ids"] = ["test-group"]
        raw[config.KEYS[2]] = json.dumps(profile)
        with self.assertRaises(config.ConfigurationError):
            config.validate_settings(raw, "", "all_linked_groups")
        with self.assertRaises(config.ConfigurationError):
            config.validate_settings(fixtures(), "", "all_linked_groups")

    def test_expected_group_and_literal_profile(self):
        raw = fixtures()
        settings = config.validate_settings(raw, "test-group")
        profile = settings[config.KEYS[2]]
        self.assertEqual(json.loads(raw[config.KEYS[2]]), json.loads(profile))
        self.assertNotIn("'", profile)
        self.assertNotIn("$", profile)
        self.assertEqual(settings, config.validate_settings(settings, "test-group"))
        with self.assertRaises(config.ConfigurationError):
            config.validate_settings(raw, "different-group")

    @unittest.skipUnless(shutil.which("docker"), "Docker Compose CLI not installed")
    def test_compose_loads_credentials_only_into_app(self):
        settings = config.validate_settings(fixtures(), "test-group")
        source = Path(__file__).resolve().parents[1]
        with tempfile.TemporaryDirectory() as tmp:
            parent = Path(tmp)
            root = parent / "project"
            root.mkdir()
            for name in ("docker-compose.yml", "docker-compose.browser-run.yml"):
                shutil.copyfile(source / name, root / name)
            (root / ".env").write_text("MYSQL_ROOT_PASSWORD=fixture\nDB_PASSWORD=fixture\nSAMPLE=should-not-interpolate\n")
            args = config.COMPOSE + ["config", "--format", "json"]
            before = json.loads(subprocess.check_output(args, cwd=root, stderr=subprocess.DEVNULL))
            config.write_private(parent / config.ENV_FILE.name, config.env_content(settings), 0o600)
            with patch.object(config, "ROOT", root):
                config.check_compose_change(before, settings)
            self.assertEqual(stat.S_IMODE((parent / config.ENV_FILE.name).stat().st_mode), 0o600)

    def test_rollout_error_restores_file_and_app(self):
        settings = config.validate_settings(fixtures(), "test-group")
        revision = "d" * 40
        rebuild = Mock()
        wait = Mock(side_effect=[config.ConfigurationError("unhealthy"), None])
        with tempfile.TemporaryDirectory() as tmp:
            backup = Path(tmp) / "existing.env"
            backup.write_bytes(b"original fixture\n")
            backup.chmod(0o640)
            lock = Path(tmp) / "lock"
            with patch.multiple(config, DEPLOY_LOCK=lock, ENV_FILE=backup):
                with patch.multiple(
                    config,
                    run=Mock(side_effect=[revision.encode(), revision.encode(), b"original-image"]),
                    app_image=Mock(return_value="original-image"),
                    runtime_settings=Mock(return_value={}),
                    is_healthy=Mock(return_value=True),
                    compose_config=Mock(return_value={"services": {"app": {"image": "fixture:app"}}}),
                    check_compose_change=Mock(),
                    recreate_app=rebuild,
                    wait_for_app=wait,
                ):
                    with self.assertRaisesRegex(config.ConfigurationError, "not a private regular file"):
                        config.apply_settings(settings, revision, "5")
                    rebuild.assert_not_called()
                    backup.chmod(0o600)
                    with redirect_stderr(io.StringIO()):
                        with self.assertRaisesRegex(config.ConfigurationError, "Previous Browser Run environment"):
                            config.apply_settings(settings, revision, "5")
            self.assertEqual(backup.read_bytes(), b"original fixture\n")
            self.assertEqual(stat.S_IMODE(backup.stat().st_mode), 0o600)
            self.assertEqual(rebuild.call_count, 2)
            wait.assert_called_with(revision, "original-image", {}, "5")


if __name__ == "__main__":
    unittest.main()
