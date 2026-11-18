"""Keep the latest seven complete deploy backups and all protected backups.

Only numeric GitHub Actions backup directories qualify. A `.keep` file, a
restore verification report, a post-deploy snapshot, or any other extra file
protects the entire directory. Protected directories still count among the
newest seven when they have a complete deploy backup.
"""

import argparse
import gzip
import json
import os
from pathlib import Path
import re
import stat
import sys
import zlib


KEEP_COUNT = 7
RUN_ID = re.compile(r"[1-9][0-9]*")
COMMIT_SHA = re.compile(r"[0-9a-f]{40}")
REQUIRED_FILES = {"database.sql.gz", "previous-commit"}
STANDARD_FILES = REQUIRED_FILES | {"health.json", "deployment-verified"}


def file_identity(path):
    metadata = path.lstat()
    if not stat.S_ISREG(metadata.st_mode) or metadata.st_nlink != 1:
        return None
    return metadata.st_dev, metadata.st_ino, metadata.st_size, metadata.st_mtime_ns


def read_generation(directory):
    """Return the identity and files of a positively identified deploy backup."""
    metadata = directory.lstat()
    if not stat.S_ISDIR(metadata.st_mode) or not RUN_ID.fullmatch(directory.name):
        return None

    entries = {path.name: file_identity(path) for path in directory.iterdir()}
    if not REQUIRED_FILES.issubset(entries):
        return None
    if any(not entries[name] or entries[name][2] == 0 for name in REQUIRED_FILES):
        return None
    try:
        if not COMMIT_SHA.fullmatch((directory / "previous-commit").read_text().strip()):
            return None
        if entries.get("deployment-verified"):
            verified = (directory / "deployment-verified").read_text().startswith("COMPLETED ")
        elif entries.get("health.json"):
            health = json.loads((directory / "health.json").read_text())
            verified = isinstance(health, dict) and health.get("status") == "ok"
        else:
            return None
    except (UnicodeError, ValueError):
        return None
    if not verified:
        return None
    return (metadata.st_dev, metadata.st_ino), entries


def remove_generation(root_fd, run_id, generation):
    # Open the selected child without following symlinks. Relative unlinks stay
    # within that directory even if its path is replaced after the identity check.
    directory_fd = os.open(run_id, os.O_RDONLY | os.O_DIRECTORY | os.O_NOFOLLOW, dir_fd=root_fd)
    try:
        metadata = os.fstat(directory_fd)
        if (metadata.st_dev, metadata.st_ino) != generation[0]:
            raise RuntimeError("Backup directory changed: " + run_id)
        if set(os.listdir(directory_fd)) != set(generation[1]):
            raise RuntimeError("Backup files changed: " + run_id)
        for name, identity in generation[1].items():
            metadata = os.stat(name, dir_fd=directory_fd, follow_symlinks=False)
            current = metadata.st_dev, metadata.st_ino, metadata.st_size, metadata.st_mtime_ns
            if not stat.S_ISREG(metadata.st_mode) or metadata.st_nlink != 1 or current != identity:
                raise RuntimeError("Backup file changed: " + run_id + "/" + name)
        for name in sorted(generation[1]):
            os.unlink(name, dir_fd=directory_fd)
    finally:
        os.close(directory_fd)
    os.rmdir(run_id, dir_fd=root_fd)


def retain_backups(root, current_run):
    if not RUN_ID.fullmatch(current_run):
        raise RuntimeError("Current deploy run ID must be numeric")
    if root.absolute() != root.resolve(strict=True):
        raise RuntimeError("Backup root must be a canonical directory")
    generations = {}
    for directory in root.iterdir():
        generation = read_generation(directory)
        if generation is not None:
            generations[directory.name] = generation

    newest = sorted(generations, key=int, reverse=True)
    if not newest or current_run != newest[0]:
        raise RuntimeError("Current deploy is not the newest complete backup")
    if len(newest) <= KEEP_COUNT:
        return []

    # A damaged recent archive must not displace a usable older recovery point.
    for run_id in newest[:KEEP_COUNT]:
        with gzip.open(str(root / run_id / "database.sql.gz"), "rb") as backup:
            if not backup.read(1):
                raise RuntimeError("Retained deploy backup is empty: " + run_id)
            while backup.read(1024 * 1024):
                pass

    removed = []
    root_fd = os.open(str(root), os.O_RDONLY | os.O_DIRECTORY | os.O_NOFOLLOW)
    try:
        root_device = os.fstat(root_fd).st_dev
        for run_id in reversed(newest[KEEP_COUNT:]):
            generation = generations[run_id]
            entries = generation[1]
            if not entries.keys() <= STANDARD_FILES or not all(entries.values()):
                continue
            if generation[0][0] != root_device:
                continue
            remove_generation(root_fd, run_id, generation)
            print("Removed old deploy backup " + run_id, flush=True)
            removed.append(run_id)
    finally:
        os.close(root_fd)
    return removed


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("backup_root", type=Path)
    parser.add_argument("current_run")
    args = parser.parse_args()
    try:
        removed = retain_backups(args.backup_root, args.current_run)
    except (EOFError, OSError, RuntimeError, UnicodeError, zlib.error) as error:
        print("Backup retention stopped: " + str(error), file=sys.stderr)
        return 1
    print("Backup retention finished. Removed {} old deploy backups.".format(len(removed)))
    return 0


if __name__ == "__main__":
    sys.exit(main())
