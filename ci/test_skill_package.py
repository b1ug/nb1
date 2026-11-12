"""Check the user-visible contract of a standalone copied Skill bundle."""
from pathlib import Path
import re
import shutil
import tempfile
import unittest
from urllib.parse import unquote, urlsplit

SOURCE = Path(__file__).resolve().parents[1] / 'skills/blink1-control'


class StandaloneSkillTests(unittest.TestCase):
    def test_installed_bundle_keeps_its_local_documentation(self):
        with tempfile.TemporaryDirectory() as directory:
            installed = Path(directory) / '.agents/skills/blink1-control'
            for item in SOURCE.rglob('*'):
                self.assertFalse(item.is_symlink(), 'bundle must not depend on a symlink target')
            shutil.copytree(SOURCE, installed)
            self.assertTrue((installed / 'SKILL.md').is_file())
            local_links = 0
            for document in installed.rglob('*.md'):
                text = document.read_text(encoding='utf-8')
                for link in re.findall(r'\[[^\]]*\]\(([^)]+)\)', text):
                    target = urlsplit(link)
                    if target.scheme or target.netloc:
                        self.assertEqual(target.scheme, 'https', link)
                        continue
                    if not target.path:
                        continue
                    resolved = (document.parent / unquote(target.path)).resolve()
                    self.assertTrue(resolved.is_relative_to(installed.resolve()),
                                    'reference escapes the installed Skill: '+link)
                    self.assertTrue(resolved.is_file(), 'missing bundled reference: '+link)
                    local_links += 1
            self.assertGreater(local_links, 0, 'standalone documentation must be exercised')


if __name__ == '__main__':
    unittest.main()
