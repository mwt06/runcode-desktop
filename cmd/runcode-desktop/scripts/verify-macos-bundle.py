#!/usr/bin/env python3
"""Validate the completed bundle, not just the plist used to build it."""
import argparse
import json
import os
from pathlib import Path
import plistlib
import struct
import subprocess


def validate_bundle(app, name, bundle_id, version, product, check_build=False, app_version=None):
    contents = Path(app) / "Contents"
    with (contents / "Info.plist").open("rb") as stream:
        info = plistlib.load(stream)
    expected = {"CFBundleName": name, "CFBundleExecutable": name,
                "CFBundleIdentifier": bundle_id, "CFBundleVersion": version,
                "CFBundleShortVersionString": version}
    for key, value in expected.items():
        if info.get(key) != value:
            raise ValueError(f"{key}: expected {value!r}, got {info.get(key)!r}")
    icon = info.get("CFBundleIconFile", "")
    if not icon or Path(icon).name != icon or "/" in icon or "\\" in icon:
        raise ValueError("CFBundleIconFile must name a resource in this bundle")
    if not icon.endswith(".icns"):
        icon += ".icns"
    data = (contents / "Resources" / icon).read_bytes()
    if len(data) <= 16 or data[:4] != b"icns" or struct.unpack(">I", data[4:8])[0] != len(data):
        raise ValueError("missing or invalid ICNS header/length")
    offset = 8
    while offset < len(data):
        if offset + 8 > len(data):
            raise ValueError("truncated ICNS entry")
        length = struct.unpack(">I", data[offset + 4:offset + 8])[0]
        if length <= 8 or offset + length > len(data):
            raise ValueError("invalid ICNS entry length")
        offset += length
    executable = contents / "MacOS" / name
    if not executable.is_file() or executable.stat().st_size == 0 or not os.access(executable, os.X_OK):
        raise ValueError("bundle executable is missing, empty, or not executable")
    if check_build:
        # Inspect live values: -trimpath deliberately omits -ldflags metadata.
        actual = json.loads(subprocess.check_output(
            [str(executable.resolve()), "--build-info"], text=True, encoding="utf-8", timeout=15))
        expected = {"name": name, "bundleID": bundle_id,
                    "version": app_version or version, "product": product}
        if actual != expected:
            raise ValueError(f"binary identity mismatch: expected {expected!r}, got {actual!r}")
    return {"bundle": str(app), "name": name, "id": bundle_id, "version": version, "icon": icon}


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("app")
    for flag in ["name", "bundle-id", "version", "product"]:
        parser.add_argument("--" + flag, required=True)
    parser.add_argument("--check-build", action="store_true")
    parser.add_argument("--app-version")
    args = parser.parse_args()
    print(json.dumps(validate_bundle(args.app, args.name, args.bundle_id, args.version,
                                    args.product, args.check_build, args.app_version), ensure_ascii=False))


if __name__ == "__main__":
    main()
