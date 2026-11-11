#!/usr/bin/env python3
"""Explicit local Homebrew install/upgrade/service acceptance on macOS or Linux."""
import argparse
import ctypes
import gzip
import hashlib
import json
import os
from pathlib import Path
import re
import signal
import subprocess
import sys
import time
import uuid
import socket
from urllib.error import HTTPError, URLError
from urllib.request import ProxyHandler, Request, build_opener


class CheckError(RuntimeError):
    pass


def require(value, message):
    if not value:
        raise CheckError(message)


class Acceptance:
    def __init__(self, args):
        self.args = args
        self.out = args.output.resolve()
        self.brew = args.brew.absolute()  # Keep the brew launcher, not its repository target.
        self.seed = args.baseline_cli.resolve(strict=True) if args.baseline_cli else None
        self.env = dict(os.environ, HOMEBREW_NO_AUTO_UPDATE='1', HOMEBREW_NO_ANALYTICS='1',
                        HOMEBREW_NO_INSTALL_CLEANUP='1', HOMEBREW_NO_ENV_HINTS='1', HOMEBREW_MAKE_JOBS='2',
                        HOMEBREW_NO_INSTALLED_DEPENDENTS_CHECK='1')
        self.serial = '00000000'
        self.tap = 'nb1/upgrade-' + uuid.uuid4().hex[:10]
        self.formula = self.tap + '/nb1'
        self.version1 = '0.0.0-dev.' + args.source_commit[:12] + '.1'
        self.version2 = '0.0.0-dev.' + args.source_commit[:12] + '.2'
        self.tapped = self.may_have_package = self.may_have_service = False
        self.child = None
        self.known_processes = []
        self.output_owned = False
        self.opener = build_opener(ProxyHandler({}))
        self.report = {'status': 'RUNNING', 'checks': [], 'commands': [], 'cleanup': [],
                       'platform': sys.platform, 'uid': os.getuid(), 'source_commit': args.source_commit,
                       'versions': [self.version1, self.version2], 'service_mode': args.service_mode,
                       'real_device_commands': False, 'native_discovery_may_run': True}

    def run(self, label, argv, expected=0, timeout=30, retain=True):
        began = time.monotonic()
        result = subprocess.run([str(x) for x in argv], capture_output=True, text=True,
                                timeout=timeout, env=self.env, stdin=subprocess.DEVNULL)
        self.report['commands'].append({'label': label, 'exit_code': result.returncode,
                                       'seconds': round(time.monotonic()-began, 3)})
        if retain:
            stem = self.out/(str(len(self.report['commands'])).zfill(2)+'-'+label)
            stem.with_suffix('.stdout').write_text(result.stdout)
            stem.with_suffix('.stderr').write_text(result.stderr)
        require(result.returncode == expected, label+' failed: '+result.stderr[-1600:])
        require('panic:' not in result.stdout+result.stderr, label+' produced a panic')
        return result

    def check(self, label, condition):
        require(condition, label)
        self.report['checks'].append({'check': label, 'status': 'PASS'})
        print(label+': PASS', file=sys.stderr, flush=True)

    def brew_run(self, label, *args):
        return self.run(label, [self.brew, *args], timeout=900)

    def cli(self, label, *args, expected=0, seed=False, binary=None):
        executable = binary or (self.seed if seed else self.entry)
        require(executable is not None, 'a trusted baseline CLI is required')
        return self.run(label, [executable, '--config', self.config, *args], expected=expected, timeout=20)

    def service(self, label, *args, expected=0, seed=False):
        return json.loads(self.cli(label, 'service', *args, '--json', expected=expected, seed=seed).stdout)

    def own_service(self, state):
        config = state.get('config') or {}
        return (state.get('config_state') == 'managed' and config.get('binary') == str(self.entry)
                and config.get('binary_root') == str(self.rack) and config.get('devices') == [self.serial]
                and config.get('listen') == self.address)

    def archive(self):
        source = self.args.source_archive.resolve(strict=True)
        require(source.stat().st_size <= 64 << 20, 'source archive exceeds the candidate budget')
        with gzip.open(source, 'rb') as stream:
            prefix = stream.read(8192)
        result = subprocess.run(['git', 'get-tar-commit-id'], input=prefix, capture_output=True, timeout=10)
        require(result.returncode == 0 and result.stdout.decode().strip() == self.args.source_commit,
                'Git archive identity does not match the selected commit')
        digest = hashlib.sha256(source.read_bytes()).hexdigest()
        self.report['source_archive_sha256'] = digest
        self.report['template_sha256'] = hashlib.sha256(self.args.template.read_bytes()).hexdigest()
        return source, digest

    def stage_formula(self, version):
        source, digest = self.archive()
        text = self.args.template.read_text()
        for key, value in dict(source_url=source.as_uri(), version=version, source_sha256=digest,
                               source_commit=self.args.source_commit, min_macos='sequoia').items():
            text = text.replace('@@'+key.upper()+'@@', json.dumps(value))
        require('@@' not in text and 'config.ServiceBinaryRoot=' in text, 'candidate template binding is incomplete')
        (self.tap_source/'Formula/nb1.rb').write_text('# LOCAL ACCEPTANCE CANDIDATE; NOT A PUBLISHED RELEASE.\n'+text)
        self.run('stage-tap', ['git', '-C', self.tap_source, 'add', 'Formula/nb1.rb'])
        self.run('commit-tap', ['git', '-C', self.tap_source, '-c', 'user.name=nb1-validation',
                              '-c', 'user.email=validation@localhost', '-c', 'commit.gpgsign=false',
                              '-c', 'core.hooksPath=/dev/null', 'commit', '-qm', 'Validate candidate '+version])
        if self.tapped:
            checkout = self.brew_run('tap-path', '--repository', self.tap).stdout.strip()
            self.run('refresh-local-tap', ['git', '-C', checkout, 'pull', '--ff-only'])

    def request(self, path, data=None):
        body = None if data is None else json.dumps(data).encode()
        req = Request(self.endpoint+path, data=body, headers={'Content-Type': 'application/json'})
        try:
            response = self.opener.open(req, timeout=2)
        except HTTPError as error:
            response = error
        with response:
            data = response.read(65537)
            require(len(data) <= 65536, 'HTTP evidence exceeds the response budget')
            return response.code, json.loads(data)

    def ready(self):
        end = time.monotonic()+10
        while time.monotonic() < end:
            try:
                code, _ = self.request('/healthz')
                status, devices = self.request('/v1/devices')
                if code == status == 200 and self.serial in json.dumps(devices) and '"offline"' in json.dumps(devices):
                    return
            except (OSError, ValueError):
                pass
            time.sleep(.1)
        raise CheckError('installed daemon did not provide healthy offline HTTP')

    def identity(self, executable=True):
        if self.args.service_mode == 'unavailable':
            pid = self.child.pid
        elif sys.platform == 'darwin':
            result = self.run('launchd-pid', ['/bin/launchctl', 'print', 'gui/'+str(os.getuid())+'/org.b1ug.nb1'], retain=False)
            match = re.search(r'^\tpid = ([0-9]+)$', result.stdout, re.M)
            require(match is not None, 'launchd did not report a live PID')
            pid = int(match.group(1))
        else:
            result = self.run('systemd-pid', ['/usr/bin/systemctl', '--user', '--no-pager', 'show', '--property=MainPID', '--value', 'nb1.service'])
            pid = int(result.stdout.strip())
        require(pid > 0, 'service did not expose a valid PID')
        if sys.platform == 'linux':
            process = Path('/proc')/str(pid)
            status = dict(line.split(':', 1) for line in (process/'status').read_text().splitlines() if ':' in line)
            require(status['Uid'].split() == [str(os.getuid())]*4, 'daemon UID differs from the ordinary caller')
            if self.args.service_mode == 'available':
                require(status.get('NoNewPrivs', '').strip() == '1', 'kernel NoNewPrivs is not enabled')
            start = (process/'stat').read_text().rsplit(')', 1)[1].split()[19]
            actual = os.readlink(process/'exe') if executable else ''
        else:
            result = self.run('mac-process-identity', ['/bin/ps', '-p', str(pid), '-o', 'uid=,lstart='])
            uid, start = result.stdout.strip().split(None, 1)
            require(int(uid) == os.getuid(), 'daemon UID differs from the ordinary caller')
            actual = ''
            if executable:
                library = ctypes.CDLL('/usr/lib/libproc.dylib')
                library.proc_pidpath.argtypes = [ctypes.c_int, ctypes.c_void_p, ctypes.c_uint32]
                library.proc_pidpath.restype = ctypes.c_int
                buffer = ctypes.create_string_buffer(4096)
                require(library.proc_pidpath(pid, buffer, len(buffer)) > 0, 'cannot inspect the running executable')
                actual = buffer.value.decode()
        return {'pid': pid, 'start': start, 'executable': actual}

    def start(self, version):
        if self.args.service_mode == 'available':
            state = self.service('start-'+version[-1:], 'start')
            require(self.own_service(state), 'service start no longer matches this candidate')
        else:
            log = (self.out/('foreground-'+str(len(self.report['commands']))+'-'+version[-1:]+'.log')).open('w')
            self.child = subprocess.Popen([str(self.entry), '--config', str(self.config), 'server', '--devices', self.serial,
                                           '--listen', self.address], stdout=log, stderr=log, env=self.env, stdin=subprocess.DEVNULL)
            log.close()
        self.ready()
        identity = self.identity()
        self.known_processes.append(identity)
        expected = self.rack/version/'bin/nb1'
        self.check('running executable matches '+version, Path(identity['executable']).resolve() == expected.resolve())
        self.report.setdefault('processes', []).append(dict(identity, version=version))
        version_output = self.cli('version-'+version[-1:], 'version').stdout
        self.check('installed source and version match '+version, self.args.source_commit in version_output and version in version_output)
        self.report.setdefault('binaries', []).append({'version': version, 'sha256': hashlib.sha256(expected.read_bytes()).hexdigest()})
        self.cli('cli-devices', 'control', '--endpoint', self.endpoint, 'devices')
        code, _ = self.request('/v1/preview', {'action': {'kind': 'set', 'color': 'red'}})
        self.check('installed preview and offline rejection', code == 200 and self.request('/v1/jobs', {
            'serial': self.serial, 'action': {'kind': 'set', 'color': 'red'}})[0] == 503)
        return identity

    def stop(self, identity):
        if self.args.service_mode == 'available':
            self.service('stop-service', 'stop')
        else:
            self.child.terminate()
            require(self.child.wait(timeout=12) == 0, 'foreground daemon did not exit normally')
            self.child = None
        self.wait_process_exit(identity)

    def wait_process_exit(self, identity):
        end = time.monotonic()+10
        while time.monotonic() < end:
            if sys.platform == 'linux':
                try:
                    current = (Path('/proc')/str(identity['pid'])/'stat').read_text().rsplit(')', 1)[1].split()[19]
                except FileNotFoundError:
                    return
            else:
                result = subprocess.run(['/bin/ps', '-p', str(identity['pid']), '-o', 'lstart='],
                                        capture_output=True, text=True, timeout=3)
                if result.returncode == 1 and not result.stdout.strip():
                    return
                require(result.returncode == 0, 'could not establish candidate process exit')
                current = result.stdout.strip()
            if current != identity['start']:
                return  # The PID was reused; do not act on the replacement.
            time.sleep(.1)
        raise CheckError('old daemon process did not exit')

    def service_snapshot(self):
        return {'inode': self.unit.stat().st_ino, 'sha256': hashlib.sha256(self.unit.read_bytes()).hexdigest()}

    def exercise(self):
        self.out.mkdir(mode=0o700, exist_ok=False)
        self.output_owned = True
        self.config = self.out/'setting.yaml'; self.config.write_text('{}\n')
        self.archive()  # Reject a mismatched archive before any brew/service changes.
        prefix = self.brew_run('brew-prefix', '--prefix').stdout.strip()
        cellar = self.brew_run('brew-cellar', '--cellar').stdout.strip()
        self.entry = Path(prefix)/'opt/nb1/bin/nb1'; self.rack = Path(cellar)/'nb1'
        self.report.update(prefix=prefix, rack=str(self.rack), service_entry=str(self.entry),
                           homebrew=self.brew_run('brew-version', '--version').stdout.splitlines()[0],
                           dependency_installation='normal Homebrew dependency resolution')
        installed = self.brew_run('installed-baseline', 'list', '--formula', '--full-name').stdout.splitlines()
        require(not any(name.split('/')[-1] == 'nb1' for name in installed) and not self.rack.exists(),
                'refusing to replace an existing nb1 package')
        if self.args.service_mode == 'available':
            before = self.service('service-baseline', 'status', seed=True)
            require(before['config_state'] == before['registration'] == 'absent', 'refusing an existing service configuration or registration')
        with socket.socket() as sock:
            sock.bind(('127.0.0.1', 0)); self.address = '127.0.0.1:'+str(sock.getsockname()[1])
        self.endpoint = 'http://'+self.address
        self.tap_source = self.out/'tap'; (self.tap_source/'Formula').mkdir(parents=True)
        self.run('init-local-tap', ['git', 'init', '-q', self.tap_source])
        self.stage_formula(self.version1)
        self.tapped = True  # A failed tap operation can still have created files.
        self.brew_run('tap-candidate', 'tap', self.tap, self.tap_source)
        self.may_have_package = True
        options = ['--build-from-source']
        self.brew_run('install-v1', 'install', *options, self.formula)
        self.brew_run('formula-test-v1', 'test', self.formula)
        discovered = self.cli('native-discovery', 'list')
        self.check('target serial is absent from native discovery', self.serial not in discovered.stdout)
        if self.args.service_mode == 'available':
            self.may_have_service = True
            state = self.service('install-service-default-path', 'install', '--devices', self.serial, '--listen', self.address)
            self.check('Homebrew service stores the stable package entry', self.own_service(state))
            self.unit = Path(state.get('plist_path') or state['unit_path'])
            snapshot = self.service_snapshot()
            self.report['initial_service_file'] = snapshot
            self.check('service installation does not start the daemon', state['registration'] in ('absent', 'loaded'))
        else:
            for extra in ([], ['--start']):
                result = self.cli('container-service-unavailable', 'service', 'install', '--devices', self.serial, *extra, expected=1)
                self.check('container service reports missing user manager', 'systemd user manager' in result.stderr)
            snapshot = None
        first = self.start(self.version1)
        old_cli = self.rack/self.version1/'bin/nb1'
        self.stage_formula(self.version2)
        self.brew_run('upgrade-v2', 'upgrade', *options, self.formula)
        self.check('upgrade retains the running process', self.identity(False)['pid'] == first['pid'] and self.request('/healthz')[0] == 200)
        stale = self.cli('stale-binary-install', 'service', 'install', '--devices', self.serial, expected=1, binary=old_cli)
        self.check('stale binary cannot silently bind the new package', 'active package executable differs' in stale.stderr)
        self.brew_run('cleanup-old-keg', 'cleanup', self.formula)
        self.check('Homebrew cleanup removed the original version', not (self.rack/self.version1).exists())
        self.stop(first)
        second = self.start(self.version2)
        self.check('explicit restart enters V2', (first['pid'], first['start']) != (second['pid'], second['start']))
        if snapshot:
            self.check('upgrade needs no service reinstall', self.service_snapshot() == snapshot)
        self.brew_run('formula-test-v2', 'test', self.formula)
        self.brew_run('linkage-v2', 'linkage', '--test', self.formula)
        self.run('installed-build-info', [Path(prefix)/'opt/go/bin/go', 'version', '-m', self.entry])
        self.stage_formula(self.version1)
        self.brew_run('rollback-v1', 'reinstall', *options, self.formula)
        self.stop(second)
        third = self.start(self.version1)
        self.check('explicit rollback enters V1 without rewriting the service', not snapshot or self.service_snapshot() == snapshot)
        self.stop(third)
        if self.args.service_mode == 'available':
            self.service('uninstall-service', 'uninstall')
            final = self.service('service-final', 'status', seed=True)
            self.check('service removed after test', final['config_state'] == final['registration'] == 'absent')
        self.report['status'] = 'PASS'

    def cleanup(self):
        service_clean = True
        package_clean = not self.may_have_package
        if self.may_have_service:
            try:
                state = self.service('cleanup-service-status', 'status', seed=True)
                if self.own_service(state):
                    if state['registration'] in ('registered', 'loaded'):
                        try:
                            self.known_processes.append(self.identity(executable=False))
                        except CheckError as error:
                            if str(error) not in ('launchd did not report a live PID', 'service did not expose a valid PID'):
                                raise
                    self.service('cleanup-service-uninstall', 'uninstall', '--stop', seed=True)
                elif state['config_state'] != 'absent' or state['registration'] != 'absent':
                    raise CheckError('service changed; refusing to remove an unknown configuration')
                state = self.service('cleanup-service-final', 'status', seed=True)
                require(state['config_state'] == state['registration'] == 'absent', 'temporary service remains')
                for identity in self.known_processes:
                    self.wait_process_exit(identity)
                self.report['cleanup'].append({'step': 'service', 'status': 'PASS'})
            except Exception as error:
                service_clean = False
                self.report['cleanup'].append({'step': 'service', 'status': 'FAIL', 'error': str(error)})
        if self.child is not None:
            try:
                self.child.terminate(); self.child.wait(timeout=12)
                self.report['cleanup'].append({'step': 'foreground', 'status': 'PASS'})
            except Exception as error:
                self.child.kill(); self.child.wait(timeout=3)
                self.report['cleanup'].append({'step': 'foreground', 'status': 'FAIL', 'error': str(error)})
        if self.may_have_package and service_clean:
            try:
                packages = self.brew_run('cleanup-installed-packages', 'list', '--formula', '--full-name').stdout.splitlines()
                versions = self.brew_run('cleanup-package-versions', 'list', '--versions', 'nb1').stdout.split()[1:] if any(p.split('/')[-1] == 'nb1' for p in packages) else []
                require(set(versions) <= {self.version1, self.version2}, 'package version changed; refusing to uninstall it')
                if versions:
                    self.brew_run('uninstall-candidate', 'uninstall', '--force', self.formula)
                require(not self.entry.exists() and not self.entry.parent.parent.is_symlink(), 'candidate executable or opt link remains')
                package_clean = True
                self.report['cleanup'].append({'step': 'package', 'status': 'PASS'})
            except Exception as error:
                self.report['cleanup'].append({'step': 'package', 'status': 'FAIL', 'error': str(error)})
        if self.tapped and service_clean and package_clean:
            try:
                taps = self.brew_run('cleanup-taps', 'tap').stdout.splitlines()
                if self.tap in taps:
                    self.brew_run('untap-candidate', 'untap', self.tap)
                self.report['cleanup'].append({'step': 'tap', 'status': 'PASS'})
            except Exception as error:
                self.report['cleanup'].append({'step': 'tap', 'status': 'FAIL', 'error': str(error)})

    def execute(self):
        try:
            self.exercise()
        except Exception as error:
            self.report.update(status='FAIL', error=str(error))
        finally:
            if self.output_owned:
                self.cleanup()
                if any(row['status'] != 'PASS' for row in self.report['cleanup']):
                    self.report['status'] = 'FAIL'
                (self.out/'result.json').write_text(json.dumps(self.report, indent=2)+'\n')
        return self.report


def main(argv=None):
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--execute', action='store_true')
    parser.add_argument('--brew', type=Path, required=True)
    parser.add_argument('--source-archive', type=Path, required=True)
    parser.add_argument('--source-commit', required=True)
    parser.add_argument('--template', type=Path, required=True)
    parser.add_argument('--baseline-cli', type=Path)
    parser.add_argument('--output', type=Path, required=True)
    parser.add_argument('--service-mode', choices=['available', 'unavailable'], default='available')
    args = parser.parse_args(argv)
    if not args.execute or sys.platform not in ('darwin', 'linux') or os.getuid() <= 0 or os.getuid() != os.geteuid():
        parser.error('requires explicit execution as the current ordinary user on macOS or Linux')
    if not re.fullmatch('[0-9a-f]{40}', args.source_commit):
        parser.error('source-commit must be the exact Git archive revision')
    if args.service_mode == 'available' and args.baseline_cli is None:
        parser.error('service acceptance requires a trusted baseline CLI')
    if args.output.exists():
        parser.error('output directory must be new; previous evidence is retained')
    report = Acceptance(args).execute()
    print(json.dumps(report, sort_keys=True))
    return 0 if report['status'] == 'PASS' else 1


if __name__ == '__main__':
    raise SystemExit(main())
