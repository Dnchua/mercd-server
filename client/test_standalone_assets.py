from pathlib import Path
import tempfile
import unittest
import xml.etree.ElementTree as ET

from merge_asset_manifest import generate
from standalone_manifest import configure, ANDROID, BOOT


class LocalAssetsTest(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.addCleanup(self.tmp.cleanup)
        self.root = Path(self.tmp.name)
        self.manifest = self.root / 'assets/backend/cn350/serve_asset_info_android4_v2_short.txt'
        self.manifest.parent.mkdir(parents=True)
        self.manifest.write_text('version,100@414,23,1,0,1,deadbeef')
        self.payload = b'UnityWeb\0test-content'
        self.bundle('unit/unit_414', self.payload)

    def bundle(self, name, data):
        path = self.root / ('assets/unity/' + name + '.Android.unity3d')
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_bytes(data)
        return path

    def test_strips_md5_and_keeps_five_fields_for_streaming_assets(self):
        legacy_index = self.root / 'assets/backend/local-assets.json'
        legacy_index.write_text('{"schema":1}')
        apktool_yml = self.root / 'apktool.yml'
        apktool_yml.write_text('doNotCompress:\n- arsc\n- png\n')
        result = generate(self.root, version=200)
        self.assertEqual(result['assets'], 1)
        self.assertEqual(result['bundles'][0]['file'], 'unit/unit_414.Android.unity3d')
        self.assertEqual(self.manifest.read_text(), 'version,200@414,23,1,0,1\n')
        self.assertFalse(legacy_index.exists())
        self.assertIn('- unity3d\n', apktool_yml.read_text())
        self.bundle('unit/unit_414', b'UnityRaw\0updated')
        generate(self.root, version=200)
        self.assertEqual(self.manifest.read_text(), 'version,201@414,23,1,0,1\n')

    def test_filtered_local_name_validates_alias_bundle_exists(self):
        self.manifest.write_text('version,100@bg-map,2,1,0,1,deadbeef')
        data = b'UnityRaw\0map'
        self.bundle('common/bg-map_1136', data)
        generate(self.root, version=200)
        rows = self.manifest.read_text().strip().split('@')
        self.assertIn('bg-map,2,1,0,1', rows)
        for row in rows[1:]:
            self.assertEqual(len(row.split(',')), 5)

    def test_missing_file_fails_without_rewriting_manifest(self):
        original = 'version,100@999,23,1,0,1'
        self.manifest.write_text(original)
        with self.assertRaisesRegex(ValueError, '清单资源缺失'):
            generate(self.root)
        self.assertEqual(self.manifest.read_text(), original)

    def test_bad_bundle_and_symlink_rejected(self):
        self.bundle('unit/unit_414', b'broken')
        with self.assertRaisesRegex(ValueError, '非法 Unity'):
            generate(self.root)
        path = self.bundle('unit/unit_414', self.payload)
        (path.parent / 'unit_999.Android.unity3d').symlink_to(path)
        with self.assertRaisesRegex(ValueError, '符号链接'):
            generate(self.root)

    def test_launcher_gate_is_idempotent(self):
        root = ET.fromstring('''<manifest xmlns:android="http://schemas.android.com/apk/res/android" package="test.app"><application><activity android:name=".Game"><intent-filter><action android:name="android.intent.action.MAIN"/><category android:name="android.intent.category.LAUNCHER"/></intent-filter></activity></application></manifest>''')
        configure(root)
        configure(root)
        activities = root.findall('application/activity')
        self.assertEqual(len(activities), 2)
        self.assertEqual(activities[0].findall('intent-filter'), [])
        self.assertEqual(activities[1].get(ANDROID + 'name'), BOOT)
        self.assertEqual(activities[1].find('meta-data').get(ANDROID + 'value'), 'test.app.Game')


if __name__ == '__main__':
    unittest.main()
