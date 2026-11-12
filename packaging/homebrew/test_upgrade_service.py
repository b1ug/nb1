"""Guard tests only: never invoke brew or a native service manager."""
import subprocess
import sys
import tempfile
from types import SimpleNamespace
import unittest
from unittest import mock
from pathlib import Path

from check_upgrade_service import Acceptance, CheckError


class UpgradeGuardTests(unittest.TestCase):
    def test_candidate_environment_and_ownership_match_full_profile(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            baseline = root/'baseline'
            baseline.write_bytes(b'guard-test-only')
            args = SimpleNamespace(output=root/'evidence', brew=root/'brew', baseline_cli=baseline,
                                   source_commit='a'*40, service_mode='available')
            with mock.patch.dict('os.environ', {'NB1_DEVICE': 'foreign', 'NB1_PORT': '8931'}):
                candidate = Acceptance(args)
            self.assertFalse(any(key.upper().startswith('NB1_') for key in candidate.env))
            self.assertEqual(candidate.env['XDG_CONFIG_HOME'], str(candidate.xdg))
            candidate.entry, candidate.rack, candidate.address = root/'opt/nb1', root/'Cellar/nb1', '127.0.0.1:12345'
            config = {'binary': str(candidate.entry), 'binary_root': str(candidate.rack),
                      'devices': ['00000000'], 'listen': candidate.address,
                      'config_file': str(candidate.config), 'user_config_root': str(candidate.xdg),
                      'lock_dir': str(candidate.locks)}
            self.assertTrue(candidate.own_service({'config_state': 'managed', 'config': config}))
            for field in ('config_file', 'user_config_root', 'lock_dir'):
                with self.subTest(field=field):
                    foreign = dict(config, **{field: '/foreign'})
                    self.assertFalse(candidate.own_service({'config_state': 'managed', 'config': foreign}))

    def test_missing_execute_never_creates_output_or_inspects_inputs(self):
        with tempfile.TemporaryDirectory() as temporary:
            destination = Path(temporary)/'evidence'
            script = Path(__file__).with_name('check_upgrade_service.py')
            result = subprocess.run([sys.executable, str(script), '--brew', '/not-a-brew',
                                     '--source-archive', '/not-an-archive', '--source-commit', 'a'*40,
                                     '--template', '/not-a-template', '--baseline-cli', '/not-a-cli',
                                     '--output', str(destination)], capture_output=True, text=True, timeout=10)
            self.assertEqual(result.returncode, 2)
            self.assertIn('requires explicit execution', result.stderr)
            self.assertFalse(destination.exists())

    def test_existing_output_is_not_overwritten(self):
        with tempfile.TemporaryDirectory() as temporary:
            destination = Path(temporary)
            marker = destination/'result.json'
            marker.write_text('previous evidence')
            script = Path(__file__).with_name('check_upgrade_service.py')
            result = subprocess.run([sys.executable, str(script), '--execute', '--brew', '/not-a-brew',
                                     '--source-archive', '/not-an-archive', '--source-commit', 'a'*40,
                                     '--template', '/not-a-template', '--service-mode', 'unavailable',
                                     '--output', str(destination)], capture_output=True, text=True, timeout=10)
            self.assertNotEqual(result.returncode, 0)
            self.assertEqual(marker.read_text(), 'previous evidence')

    def test_cleanup_retains_package_and_tap_until_process_exit_is_confirmed(self):
        candidate = Acceptance.__new__(Acceptance)
        candidate.may_have_service = candidate.may_have_package = candidate.tapped = True
        candidate.child = None
        candidate.known_processes = [{'pid': 12345, 'start': 'candidate'}]
        candidate.report = {'cleanup': []}
        candidate.service = mock.Mock(return_value={'config_state': 'absent', 'registration': 'absent'})
        candidate.own_service = mock.Mock(return_value=False)
        candidate.wait_process_exit = mock.Mock(side_effect=CheckError('old daemon process did not exit'))
        candidate.brew_run = mock.Mock()
        candidate.cleanup()
        candidate.wait_process_exit.assert_called_once_with(candidate.known_processes[0])
        candidate.brew_run.assert_not_called()
        self.assertEqual(candidate.report['cleanup'][0]['status'], 'FAIL')


if __name__ == '__main__':
    unittest.main()
