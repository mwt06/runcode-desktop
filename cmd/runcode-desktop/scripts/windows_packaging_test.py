import importlib.util
import json
import os
import subprocess
import sys
from pathlib import Path
import struct
import tempfile
import unittest
from unittest.mock import patch

spec = importlib.util.spec_from_file_location('pecheck', Path(__file__).with_name('verify-windows-executable.py'))
checker = importlib.util.module_from_spec(spec)
spec.loader.exec_module(checker)


class WindowsExecutableTest(unittest.TestCase):
    def test_architecture_and_gui_are_required(self):
        with tempfile.TemporaryDirectory() as root:
            exe = Path(root) / '智开.exe'
            for arch, machine in [('amd64', 0x8664), ('arm64', 0xaa64)]:
                data = bytearray(512)
                data[:2] = b'MZ'
                struct.pack_into('<I', data, 0x3c, 128)
                data[128:132] = bytes.fromhex('50450000')
                struct.pack_into('<H', data, 132, machine)
                struct.pack_into('<H', data, 152, 0x20b)
                struct.pack_into('<H', data, 220, 2)
                exe.write_bytes(data)
                self.assertEqual(checker.validate_executable(exe, arch)['subsystem'], 'GUI')
                cli = subprocess.run([sys.executable, str(Path(checker.__file__)), str(exe), '--arch', arch],
                                     capture_output=True, check=True, timeout=15,
                                     env={**os.environ, 'PYTHONIOENCODING': 'cp1252', 'PYTHONUTF8': '0'})
                self.assertEqual(json.loads(cli.stdout.decode('ascii'))['executable'], str(exe))
                info = {'name': 'Example', 'bundleID': 'test.example', 'version': '1.0.20', 'product': 'example'}
                with patch.object(checker.subprocess, 'check_output', return_value=json.dumps(info)) as run:
                    self.assertEqual(checker.validate_executable(exe, arch, True)['buildInfo'], info)
                    run.assert_called_once_with([str(exe.resolve()), '--build-info'], text=True, encoding='utf-8', timeout=20)
                with patch.object(checker.subprocess, 'check_output', return_value='{}'):
                    with self.assertRaisesRegex(ValueError, 'identity'):
                        checker.validate_executable(exe, arch, True)
                with self.assertRaises(ValueError):
                    checker.validate_executable(exe, 'arm64' if arch == 'amd64' else 'amd64')
                struct.pack_into('<H', data, 220, 3)
                exe.write_bytes(data)
                with self.assertRaisesRegex(ValueError, 'console'):
                    checker.validate_executable(exe, arch)
            for data in [b'', b'MZ', b'MZ' + bytes(100)]:
                exe.write_bytes(data)
                with self.assertRaises(ValueError):
                    checker.validate_executable(exe, 'amd64')


if __name__ == '__main__':
    unittest.main()
