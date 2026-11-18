import gzip
import json
import os
from pathlib import Path
import tempfile
import unittest

from retain_deploy_backups import retain_backups


class RetainDeployBackupsTest(unittest.TestCase):
    def setUp(self):
        self.temporary = tempfile.TemporaryDirectory()
        self.addCleanup(self.temporary.cleanup)
        self.root = Path(self.temporary.name).resolve()

    def generation(self, run_id):
        directory = self.root / str(run_id)
        directory.mkdir()
        (directory / "database.sql.gz").write_bytes(gzip.compress(b"SELECT 1;"))
        (directory / "previous-commit").write_text("a" * 40 + "\n")
        (directory / "health.json").write_text(json.dumps({"status": "ok"}))
        return directory

    def test_retains_seven_with_numeric_order_and_is_idempotent(self):
        for run_id in range(8, 16):
            self.generation(run_id)
        self.assertEqual(retain_backups(self.root, "15"), ["8"])
        self.assertEqual(retain_backups(self.root, "15"), [])
        self.assertEqual({p.name for p in self.root.iterdir()}, {str(i) for i in range(9, 16)})

    def test_keeps_messenger_restore_points_and_unknown_layouts(self):
        (self.root / "messenger-shadow-20261106").mkdir()
        (self.generation(1) / ".keep").touch()
        restored = self.generation(2)
        (restored / "restore-verified").write_text("RESTORE_VERIFIED")
        (restored / "database.post-deploy.sql.gz").touch()
        (self.generation(3) / "database.sql.gz.partial").touch()
        for run_id in range(4, 12):
            self.generation(run_id)
        (self.root / "11" / "deployment-verified").write_text("COMPLETED 2026-11-19T00:00:00Z\n")
        (self.root / "11" / "restore-verified").touch()

        self.assertEqual(retain_backups(self.root, "11"), ["4"])
        for name in ("1", "2", "3", "11", "messenger-shadow-20261106"):
            self.assertTrue((self.root / name).is_dir())

    def test_preserves_symlinks_hardlinks_and_incomplete_backups(self):
        symlink = self.generation(1)
        source = self.generation(2)
        (source / ".keep").touch()
        (symlink / "health.json").unlink()
        (symlink / "health.json").symlink_to(source / "health.json")
        linked = self.generation(3)
        os.link(str(linked / "database.sql.gz"), str(self.root / "external-link"))
        incomplete = self.generation(4)
        (incomplete / "health.json").rename(incomplete / "health.pending.json")
        for run_id in range(5, 13):
            self.generation(run_id)
        (self.root / "99").symlink_to(self.root / "12", target_is_directory=True)
        self.assertEqual(retain_backups(self.root, "12"), ["5"])
        for path in (symlink, linked, incomplete):
            self.assertTrue(path.is_dir())
        self.assertTrue((self.root / "99").is_symlink())

    def test_stops_when_current_backup_is_not_newest_or_valid(self):
        for run_id in range(1, 9):
            latest = self.generation(run_id)
        with self.assertRaisesRegex(RuntimeError, "must be numeric"):
            retain_backups(self.root, "../8")
        with self.assertRaisesRegex(RuntimeError, "not the newest"):
            retain_backups(self.root, "7")
        archive = latest / "database.sql.gz"
        archive.write_bytes(archive.read_bytes()[:-2])
        with self.assertRaises(EOFError):
            retain_backups(self.root, "8")
        self.assertTrue((self.root / "1" / "database.sql.gz").is_file())

    def test_keeps_old_backup_when_any_recent_archive_is_damaged(self):
        for run_id in range(1, 9):
            self.generation(run_id)
        archive = self.root / "2" / "database.sql.gz"
        archive.write_bytes(archive.read_bytes()[:-2])
        with self.assertRaises(EOFError):
            retain_backups(self.root, "8")
        self.assertTrue((self.root / "1" / "database.sql.gz").is_file())


if __name__ == "__main__":
    unittest.main()
