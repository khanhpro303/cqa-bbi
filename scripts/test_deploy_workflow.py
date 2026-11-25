import re
from pathlib import Path
import subprocess
import unittest


ROOT = Path(__file__).resolve().parents[1]
WORKFLOW = ROOT / ".github" / "workflows" / "deploy.yml"
ROLLOUT = ROOT / "scripts" / "deploy_vps_images.sh"


class DeployWorkflowTest(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.workflow = WORKFLOW.read_text()
        cls.rollout = ROLLOUT.read_text()

    def test_only_main_can_reach_the_deploy_job(self):
        self.assertIn("branches:\n      - main", self.workflow)
        self.assertIn("workflow_dispatch:", self.workflow)
        self.assertRegex(
            self.workflow,
            r"(?m)^  deploy:\n    if: github\.ref == 'refs/heads/main'\n    needs: verify$",
        )

    def test_builds_exact_sha_images_before_ssh_credentials_are_exposed(self):
        build_start = self.workflow.index("- name: Build production images on GitHub Actions")
        deploy_start = self.workflow.index("- name: Stage images and deploy the exact main revision")
        build_step = self.workflow[build_start:deploy_start]
        deploy_step = self.workflow[deploy_start:]

        self.assertNotIn("secrets.", build_step)
        self.assertEqual(build_step.count("--platform linux/amd64"), 3)
        self.assertEqual(build_step.count("org.opencontainers.image.revision=$TARGET_SHA"), 3)
        for image in ("cqa-app", "cqa-nginx", "cqa-personal-zalo-gateway"):
            self.assertIn('"{}:ci-$TARGET_SHA"'.format(image), build_step)
        self.assertIn("VPS_KEY: ${{ secrets.VPS_SSH_KEY }}", deploy_step)

    def test_transfer_is_checksum_verified_and_host_key_pinned(self):
        self.assertIn("sha256sum images.tar.gz > SHA256SUMS", self.workflow)
        self.assertIn("sha256sum --check --strict SHA256SUMS", self.workflow)
        self.assertIn("StrictHostKeyChecking=yes", self.workflow)
        self.assertIn("HostKeyAlias=cqa-vps-deployment", self.workflow)
        self.assertIn("cqa-vps-deployment ssh-ed25519 ", self.workflow)
        self.assertNotIn("ssh-keyscan", self.workflow)
        self.assertIn(
            "SHA256:RBKTIUlSdGXAXXTXwFfui52CWuYadrjGCgW4eL1aLkg",
            self.workflow,
        )
        self.assertNotIn("actions/upload-artifact", self.workflow)
        self.assertNotIn("docker/login-action", self.workflow)

    def test_vps_rollout_uses_the_versioned_repo_script(self):
        self.assertIn(
            '"bash -s -- \'$TARGET_SHA\' \'$GITHUB_RUN_ID\' \'$stage\'" '
            "< scripts/deploy_vps_images.sh",
            self.workflow,
        )
        self.assertNotIn("/home/karien/cqa-personal-zalo/deploy.sh", self.workflow)

    def test_deploy_workflow_changes_do_not_recursively_deploy(self):
        for path in (
            ".github/workflows/deploy.yml",
            "scripts/deploy_vps_images.sh",
            "scripts/test_deploy_workflow.py",
            "scripts/retain_deploy_backups.py",
            "scripts/test_retain_deploy_backups.py",
        ):
            self.assertIn("- '{}'".format(path), self.workflow)

    def test_database_backup_precedes_container_recreation(self):
        backup = self.rollout.index("docker exec cqa-db sh -c")
        archive_verified = self.rollout.index('gzip -t "$backup_dir/database.sql.gz.partial"')
        rollout_started = self.rollout.index("rollout_started=1")
        recreate = self.rollout.index(
            '"${compose[@]}" up -d --no-build', rollout_started
        )
        self.assertLess(backup, archive_verified)
        self.assertLess(archive_verified, rollout_started)
        self.assertLess(rollout_started, recreate)

    def test_browser_run_override_follows_the_checked_out_revision(self):
        self.assertIn('compose+=(-f docker-compose.browser-run.yml)', self.rollout)
        self.assertIn('use_checkout_compose\nbefore_config=$(compose_snapshot)', self.rollout)
        self.assertIn(
            'git switch --detach "$revision"\ncheckout_changed=1\nuse_checkout_compose\nafter_config=$(compose_snapshot)',
            self.rollout,
        )
        self.assertIn(
            'git switch --detach "$previous_commit" || recovered=0\n    use_checkout_compose',
            self.rollout,
        )

    def test_retention_runs_after_every_success_gate(self):
        app_health = self.rollout.index('test "$healthy" = 1')
        schema = self.rollout.index('test "$table_count" = 12')
        frontend = self.rollout.index("ServiceQuality-*.js", schema)
        nginx = self.rollout.index("docker exec cqa-nginx nginx -t", frontend)
        gateway = self.rollout.index("http://127.0.0.1:3100/health", nginx)
        public = self.rollout.index(
            "mv -- \"$backup_dir/health.pending.json\"", gateway
        )
        retention = self.rollout.index(
            "python3 scripts/retain_deploy_backups.py", public
        )
        self.assertEqual(
            sorted((app_health, schema, frontend, nginx, gateway, public, retention)),
            [app_health, schema, frontend, nginx, gateway, public, retention],
        )

    def test_failure_path_restores_each_selected_service(self):
        subprocess.run(["bash", "-n", str(ROLLOUT)], check=True)
        self.assertIn("services=(app nginx personal-zalo-gateway)", self.rollout)
        self.assertIn(
            "services=(app nginx personal-zalo-gateway)",
            self.rollout,
        )
        self.assertIn('for service in "${selected[@]}"; do', self.rollout)
        self.assertIn(
            'docker image tag "${rollback_tags[$service]}" "${old_tags[$service]}"',
            self.rollout,
        )
        self.assertIn(
            '"${compose[@]}" up -d --no-build --pull never --no-deps "${selected[@]}"',
            self.rollout,
        )


if __name__ == "__main__":
    unittest.main()
