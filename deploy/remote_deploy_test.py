"""Exercise release replacement and rollback in scratch dirs with fake services."""
import io
import os
from pathlib import Path
import stat
import subprocess
import tarfile
import tempfile
import unittest


# The host's data/: the database, a published app update and an app
# download. A deploy only copies the database out of it.
HOST_DATA = {
    "data/alexmessage.db": b"SQLite format 3\x00 old database",
    "data/updates/1.0.0/1761580000/metadata.json": b'{"version":0}',
    "data/app/AlexMessages.apk": b"PK old apk",
}


def snapshot(root):
    """Every path under data/ with its type, mode and bytes."""
    found = {}
    for path in sorted((root / "data").rglob("*")):
        mode = path.lstat().st_mode
        found[str(path.relative_to(root))] = (stat.S_IFMT(mode), stat.S_IMODE(mode), path.read_bytes() if path.is_file() else None)
    return found


class DeployTest(unittest.TestCase):
    def run_release(self, *, failed_health=False, missing_web=False, extra=None):
        self.tmp = tempfile.TemporaryDirectory()
        self.addCleanup(self.tmp.cleanup)
        root = Path(self.tmp.name)
        (root / "bin").mkdir()
        (root / "bin/server").write_text("old binary")
        (root / "web/dist/assets").mkdir(parents=True)
        (root / "web/dist/index.html").write_text("old html")
        (root / "web/dist/assets/stale.js").write_text("old asset")
        for name, data in HOST_DATA.items():
            (root / name).parent.mkdir(parents=True, exist_ok=True)
            (root / name).write_bytes(data)
        self.data_before = snapshot(root)
        with tarfile.open(root / "release.tgz", "w:gz") as archive:
            files = {"bin/server": "new binary"}
            if not missing_web:
                files.update({"web/dist/index.html": "new html", "web/dist/assets/new.js": "new asset"})
            files.update(extra or {})
            for name, contents in files.items():
                data = contents.encode()
                info = tarfile.TarInfo(name)
                info.size = len(data)
                archive.addfile(info, io.BytesIO(data))
        fake = root / "fake"
        fake.mkdir()
        commands = {"sudo": "exit 0", "sleep": "exit 0", "curl": "printf 401"}
        if failed_health:
            commands["curl"] = "printf 000; exit 7"
        for name, command in commands.items():
            path = fake / name
            path.write_text("#!/bin/sh\n" + command + "\n")
            path.chmod(0o755)
        env = {**os.environ, "APP_DIR": str(root), "HEALTH_PORTS": "6002", "PATH": str(fake) + os.pathsep + os.environ["PATH"]}
        script = Path(__file__).with_name("remote_deploy.sh")
        result = subprocess.run(["bash", str(script)], env=env, capture_output=True, text=True)
        return root, result

    def assert_data_unchanged(self, root):
        self.assertEqual(snapshot(root), self.data_before)

    def test_success_replaces_react_assets(self):
        root, result = self.run_release()
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        self.assertEqual((root / "web/dist/index.html").read_text(), "new html")
        self.assertFalse((root / "web/dist/assets/stale.js").exists())
        self.assertEqual((root / "bin/server").read_text(), "new binary")
        self.assert_data_unchanged(root)

    def test_health_failure_restores_react_and_binary(self):
        root, result = self.run_release(failed_health=True)
        self.assertNotEqual(result.returncode, 0)
        self.assertEqual((root / "web/dist/index.html").read_text(), "old html")
        self.assertTrue((root / "web/dist/assets/stale.js").exists())
        self.assertFalse((root / "web/dist/assets/new.js").exists())
        self.assertEqual((root / "bin/server").read_text(), "old binary")
        self.assert_data_unchanged(root)

    def test_missing_react_build_aborts_and_restores(self):
        root, result = self.run_release(missing_web=True)
        self.assertNotEqual(result.returncode, 0)
        self.assertEqual((root / "web/dist/index.html").read_text(), "old html")
        self.assertEqual((root / "bin/server").read_text(), "old binary")

    def test_release_with_data_is_refused(self):
        root, result = self.run_release(extra={"data/app/AlexMessages.apk": "PK new apk"})
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("release contains data/", result.stderr)
        self.assertEqual((root / "bin/server").read_text(), "old binary")
        self.assertEqual((root / "web/dist/index.html").read_text(), "old html")
        self.assert_data_unchanged(root)


if __name__ == "__main__":
    unittest.main()
