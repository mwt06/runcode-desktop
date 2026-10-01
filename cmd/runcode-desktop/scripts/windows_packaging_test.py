import importlib.util
from pathlib import Path
import struct
import tempfile
import unittest

spec = importlib.util.spec_from_file_location('pecheck', Path(__file__).with_name('verify-windows-executable.py'))
checker = importlib.util.module_from_spec(spec)
spec.loader.exec_module(checker)


class WindowsExecutableTest(unittest.TestCase):
    def test_architecture_and_gui_are_required(self):
        with tempfile.TemporaryDirectory() as root:
            exe = Path(root) / 'app.exe'
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
