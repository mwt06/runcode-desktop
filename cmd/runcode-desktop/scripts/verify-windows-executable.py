#!/usr/bin/env python3
"""Reject a desktop PE with the wrong architecture or a console subsystem."""
import argparse
import json
from pathlib import Path
import struct


def validate_executable(path, arch):
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
    return {'executable': str(path), 'architecture': arch, 'subsystem': 'GUI'}


if __name__ == '__main__':
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('executable')
    parser.add_argument('--arch', choices=['amd64', 'arm64'], required=True)
    args = parser.parse_args()
    print(json.dumps(validate_executable(args.executable, args.arch), ensure_ascii=False))
