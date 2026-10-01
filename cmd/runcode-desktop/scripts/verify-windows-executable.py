#!/usr/bin/env python3
"""Reject a desktop PE with the wrong architecture or a console subsystem."""
import argparse
import json
from pathlib import Path
import struct
import subprocess


def validate_executable(path, arch, check_startup=False):
    data = Path(path).read_bytes()
    if len(data) < 64 or data[:2] != b'MZ':
        raise ValueError('missing DOS header')
    pe = struct.unpack_from('<I', data, 0x3c)[0]
    if pe + 24 + 70 > len(data) or data[pe:pe + 4] != bytes.fromhex('50450000'):
        raise ValueError('missing or truncated PE header')
    machine = struct.unpack_from('<H', data, pe + 4)[0]
    magic = struct.unpack_from('<H', data, pe + 24)[0]
    subsystem = struct.unpack_from('<H', data, pe + 24 + 68)[0]
    if machine != {'amd64': 0x8664, 'arm64': 0xaa64}[arch] or magic != 0x20b:
        raise ValueError(f'expected {arch} PE32+, got machine={machine:#x} magic={magic:#x}')
    if subsystem != 2:
        raise ValueError(f'expected GUI subsystem 2, got {subsystem}: desktop would open a console')
    result = {'executable': str(path), 'architecture': arch, 'subsystem': 'GUI'}
    if check_startup:
        info = json.loads(subprocess.check_output(
            [str(Path(path).resolve()), '--build-info'], text=True, encoding='utf-8', timeout=20))
        if set(info) != {'name', 'bundleID', 'version', 'product'} or any(not isinstance(v, str) or not v for v in info.values()):
            raise ValueError('native startup did not return a complete build identity')
        result['buildInfo'] = info
    return result


if __name__ == '__main__':
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('executable')
    parser.add_argument('--arch', choices=['amd64', 'arm64'], required=True)
    parser.add_argument('--check-startup', action='store_true')
    args = parser.parse_args()
    print(json.dumps(validate_executable(args.executable, args.arch, args.check_startup), ensure_ascii=False))
