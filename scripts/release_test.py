"""Exercise real packaging with fake builds, notice collection and publishers."""
import json
import os
from pathlib import Path
import shutil
import subprocess
import sys
import tarfile
import tempfile
import unittest

ROOT = Path(__file__).resolve().parent.parent


class ReleaseTests(unittest.TestCase):
    def test_all_archives_and_npm_packages_include_generated_notices(self):
        with tempfile.TemporaryDirectory() as temp:
            root = Path(temp)
            checkout = root / 'checkout'
            checkout.mkdir()
            for name in ('scripts', 'npm'):
                (checkout / name).mkdir()
            for name in ('scripts/release.sh', 'npm/brp.cjs', 'LICENSE', 'NOTICE', 'README.md'):
                shutil.copyfile(ROOT / name, checkout / name)
            mocks = root / 'mocks'
            mocks.mkdir()
            output = root / 'output'
            output.mkdir()
            mock = '''import json, os, pathlib, shutil, sys
name = pathlib.Path(sys.argv[0]).name
args = sys.argv[1:]
out = pathlib.Path(os.environ['FAKE_RELEASE_OUTPUT'])
with (out / 'calls').open('a') as log:
    log.write(name + '\\n')
if name == 'python3':
    if os.environ.get('FAKE_NOTICE_FAILURE'):
        sys.exit(1)
    pathlib.Path(args[1]).write_text('generated fixture notices\\n')
elif name == 'go':
    pathlib.Path(args[args.index('-o') + 1]).write_text('fake executable')
elif name == 'gh':
    if args[:2] == ['release', 'view']:
        print('5' if '.assets | length' in args else 'https://example.invalid/release')
    elif args[:2] == ['release', 'upload']:
        shutil.copyfile(args[3], out / pathlib.Path(args[3]).name)
elif name == 'npm':
    package = json.loads(pathlib.Path('package.json').read_text())
    assert 'THIRD_PARTY_NOTICES.txt' in package['files']
    assert pathlib.Path('THIRD_PARTY_NOTICES.txt').read_text() == 'generated fixture notices\\n'
    (out / (package['name'].split('/')[-1] + '.json')).write_text(json.dumps(package))
'''
            for name in ('python3', 'go', 'gh', 'npm'):
                path = mocks / name
                path.write_text(f'#!{sys.executable}\n' + mock)
                path.chmod(0o755)
            env = dict(os.environ, PATH=str(mocks) + os.pathsep + os.environ['PATH'],
                       FAKE_RELEASE_OUTPUT=str(output))
            subprocess.run(['bash', 'scripts/release.sh', 'v0.0.0-test'], cwd=checkout,
                           env=env, check=True, capture_output=True, text=True, timeout=60)
            self.assertEqual((output / 'calls').read_text().splitlines()[0], 'python3')
            archives = list(output.glob('*.tar.gz'))
            self.assertEqual(len(archives), 4)
            for archive in archives:
                with tarfile.open(archive) as tar:
                    self.assertEqual(tar.extractfile('THIRD_PARTY_NOTICES.txt').read(),
                                     b'generated fixture notices\n')
                    self.assertIn('LICENSE', tar.getnames())
                    self.assertIn('NOTICE', tar.getnames())
            self.assertEqual(len(list(output.glob('*.json'))), 5)
            self.assertFalse((checkout / 'THIRD_PARTY_NOTICES.txt').exists())
            (output / 'calls').unlink()
            failed = subprocess.run(
                ['bash', 'scripts/release.sh', 'v0.0.0-test'], cwd=checkout,
                env=dict(env, FAKE_NOTICE_FAILURE='1'), capture_output=True, timeout=60)
            self.assertNotEqual(failed.returncode, 0)
            self.assertEqual((output / 'calls').read_text().splitlines(), ['python3'])


if __name__ == '__main__':
    unittest.main()
