import importlib.util
import json
from pathlib import Path
import plistlib
import struct
import tempfile
import unittest
from unittest.mock import patch

ROOT = Path(__file__).resolve().parents[1]
spec = importlib.util.spec_from_file_location("bundlecheck", Path(__file__).with_name("verify-macos-bundle.py"))
checker = importlib.util.module_from_spec(spec)
spec.loader.exec_module(checker)


class PackagingContractTest(unittest.TestCase):
    def test_all_plists_reference_the_packaged_icon(self):
        paths = [*ROOT.glob("build/darwin/Info*.plist"), *ROOT.glob("build/brands/*/Info.plist")]
        self.assertGreaterEqual(len(paths), 4)
        for path in paths:
            with self.subTest(path=path):
                info = plistlib.loads(path.read_bytes())
                self.assertEqual(info["CFBundleIconFile"], "icons.icns")
                self.assertTrue((ROOT / "build/darwin" / info["CFBundleIconFile"]).is_file())
        tasks = (ROOT / "build/darwin/Taskfile.yml").read_text(encoding="utf-8")
        self.assertIn('cp build/darwin/icons.icns', tasks)
        self.assertIn('-ldflags="-w -s {{.LDFLAGS_EXTRA}}"', tasks)

    def test_brand_identities_are_distinct(self):
        for brand, name, bundle_id in [("zhikai", "智开", "cn.ouconline.ai.zhikai"),
                                       ("zhikai-guokai", "智开（国开版）", "cn.ouconline.ai.zhikai.guokai")]:
            info = plistlib.loads((ROOT / "build/brands" / brand / "Info.plist").read_bytes())
            self.assertEqual(info["CFBundleName"], name)
            self.assertEqual(info["CFBundleExecutable"], name)
            self.assertEqual(info["CFBundleIdentifier"], bundle_id)

    def fixture(self, root):
        app = Path(root) / "Example.app"
        contents = app / "Contents"
        (contents / "Resources").mkdir(parents=True)
        (contents / "MacOS").mkdir()
        info = {"CFBundleName": "Example", "CFBundleExecutable": "Example", "CFBundleIdentifier": "test.example",
                "CFBundleVersion": "1.2.0", "CFBundleShortVersionString": "1.2.0", "CFBundleIconFile": "icons.icns"}
        (contents / "Info.plist").write_bytes(plistlib.dumps(info))
        # Structural ICNS fixture; real decoding is additionally checked by iconutil on macOS CI.
        (contents / "Resources/icons.icns").write_bytes(b"icns" + struct.pack(">I", 20) + b"ic07" + struct.pack(">I", 12) + b"data")
        exe = contents / "MacOS/Example"
        exe.write_bytes(b"synthetic executable")
        exe.chmod(0o755)
        return app

    def test_completed_bundle_and_rejection_paths(self):
        with tempfile.TemporaryDirectory() as directory:
            app = self.fixture(directory)
            args = (app, "Example", "test.example", "1.2.0", "example")
            self.assertEqual(checker.validate_bundle(*args)["icon"], "icons.icns")
            with self.assertRaises(ValueError):
                checker.validate_bundle(app, "Example", "wrong.identity", "1.2.0", "example")
            path = app / "Contents/Info.plist"
            info = plistlib.loads(path.read_bytes())
            info["CFBundleIconFile"] = "iconfile"
            path.write_bytes(plistlib.dumps(info))
            with self.assertRaises(FileNotFoundError):
                checker.validate_bundle(*args)
            info["CFBundleIconFile"] = "../icons.icns"
            path.write_bytes(plistlib.dumps(info))
            with self.assertRaises(ValueError):
                checker.validate_bundle(*args)

    def test_binary_identity_is_required_and_exact(self):
        with tempfile.TemporaryDirectory() as directory:
            app = self.fixture(directory)
            identity = {"name": "Example", "bundleID": "test.example", "version": "1.2.0-rc.1", "product": "example"}
            with patch.object(checker.subprocess, "check_output", return_value=json.dumps(identity)) as run:
                checker.validate_bundle(app, "Example", "test.example", "1.2.0", "example", True, "1.2.0-rc.1")
                run.assert_called_once_with([str((app / "Contents/MacOS/Example").resolve()), "--build-info"],
                                            text=True, encoding="utf-8", timeout=15)
                with self.assertRaises(ValueError):
                    checker.validate_bundle(app, "Example", "test.example", "1.2.0", "example", True)
            for bad in [{}, {**identity, "name": "XRUN"}, {**identity, "bundleID": "wrong"},
                        {**identity, "product": "xrun"}, {**identity, "version": "0.0.0-dev"}]:
                with self.subTest(identity=bad), patch.object(checker.subprocess, "check_output", return_value=json.dumps(bad)):
                    with self.assertRaises(ValueError):
                        checker.validate_bundle(app, "Example", "test.example", "1.2.0", "example", True, "1.2.0-rc.1")


if __name__ == "__main__":
    unittest.main()
