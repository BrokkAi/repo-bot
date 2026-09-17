import io
import json
from pathlib import Path
import tempfile
import unittest
from unittest.mock import patch

import notices


class NoticesTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        self.module = self.root / 'module'
        self.module.mkdir()
        (self.module / 'LICENSE').write_text('dependency permission\n')
        (self.module / 'nested').mkdir()
        (self.module / 'nested/NOTICE.txt').write_text('nested attribution\n')
        (self.module / 'nested/source.go').write_text('not a notice')
        self.goroot = self.root / 'go'
        self.goroot.mkdir()
        (self.goroot / 'LICENSE').write_text('runtime permission\n')
        (self.goroot / 'PATENTS').write_text('runtime patent grant\n')
        self.modules = [{'Path': 'example.org/main', 'Main': True},
                        {'Path': 'example.org/lib', 'Version': 'v1.2.3'}]
        self.calls = []
        self.go_patch = patch.object(notices, 'go', side_effect=self.go)
        self.go_patch.start()
        self.addCleanup(self.go_patch.stop)
        self.unicode_patch = patch.object(notices, 'unicode_notice', return_value='Unicode permission\n')
        self.unicode_patch.start()
        self.addCleanup(self.unicode_patch.stop)

    def go(self, *args):
        self.calls.append(args)
        if args == ('list', '-m', '-json', 'all'):
            return '\n'.join(json.dumps(m) for m in self.modules)
        if args == ('mod', 'download', '-json', 'example.org/lib@v1.2.3'):
            return json.dumps({'Dir': str(self.module)})
        if args == ('mod', 'verify'):
            return 'all modules verified'
        if args == ('env', '-json', 'GOROOT', 'GOVERSION'):
            return json.dumps({'GOROOT': str(self.goroot), 'GOVERSION': 'go1.27.1'})
        self.fail(f'unexpected Go call: {args}')

    def test_exact_versions_nested_notices_and_runtime(self):
        report = notices.generate()
        for expected in ('example.org/lib@v1.2.3', 'nested attribution',
                         'dependency permission', 'runtime permission',
                         'runtime patent grant', 'Unicode permission'):
            self.assertIn(expected, report)
        self.assertNotIn('not a notice', report)
        self.assertIn(('mod', 'verify'), self.calls)
        self.assertEqual(report, notices.generate())

    def test_replacement_rejected_before_download(self):
        self.modules[1]['Replace'] = {'Path': '../local'}
        with self.assertRaisesRegex(ValueError, 'replacements'):
            notices.generate()
        self.assertEqual(len(self.calls), 1)

    def test_missing_license_rejected(self):
        (self.module / 'LICENSE').unlink()
        with self.assertRaisesRegex(ValueError, 'no license text'):
            notices.generate()

    def test_collection_failure_does_not_create_output(self):
        output = self.root / 'THIRD_PARTY_NOTICES.txt'
        with patch('sys.argv', ['notices.py', str(output)]), patch.object(
                notices, 'unicode_notice', side_effect=OSError('offline')):
            with self.assertRaises(SystemExit):
                notices.main()
        self.assertFalse(output.exists())

    def test_unicode_response_validated(self):
        self.unicode_patch.stop()
        with patch('urllib.request.urlopen', return_value=io.BytesIO(b'<html>failure</html>')):
            with self.assertRaisesRegex(ValueError, 'Unicode server'):
                notices.unicode_notice()
        with patch('urllib.request.urlopen', return_value=io.BytesIO(
                b'UNICODE LICENSE V3\nCOPYRIGHT AND PERMISSION NOTICE\nterms')):
            self.assertIn('terms', notices.unicode_notice())


if __name__ == '__main__':
    unittest.main()
