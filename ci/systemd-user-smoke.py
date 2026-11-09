#!/usr/bin/env python3
"""Opt-in native systemd-user acceptance, restricted to disposable GitHub VMs.

No --execute means preparation only. This script never runs nb1's real server:
the explicitly supplied stdlib fixture is the installed service executable.
"""
import argparse
import hashlib
import json
import os
import pathlib
import platform
import pwd
import re
import secrets
import shutil
import signal
import socket
import stat
import subprocess
import sys
import time
import urllib.request

UNIT = "nb1.service"
SERIAL = "00000000"  # argv fixture only; never used to open a device
USER_PATTERN = re.compile(r"nb1ci-[0-9a-f]{12}\Z")
PROPERTIES = (
    "Id", "LoadState", "ActiveState", "SubState", "FragmentPath", "SourcePath",
    "DropInPaths", "Transient", "NeedDaemonReload", "UnitFileState", "Job",
    "NoNewPrivileges", "MainPID", "ControlPID", "ExecMainCode", "ExecMainStatus", "Result",
)
NEGATIVES = {
    "external-enable": "An additional non-default enable link must prevent uninstall from deleting the unit.",
    "dropin": "An owned test drop-in must prevent start and remain untouched.",
    "mask": "A test user.control mask must prevent start and remain untouched.",
    "transient": "A test transient same-name unit must prevent install and remain registered.",
    "need-reload": "An externally changed unit awaiting reload must prevent start and remain untouched.",
    "bus-loss": "An invalid runtime plus invalid bus address must fail without changing disk or the real manager.",
}


class SmokeError(Exception):
    pass


def require(condition, message):
    # Deliberately not an assert: protection must survive python -O.
    if not condition:
        raise SmokeError(message)


def digest(data):
    return hashlib.sha256(data).hexdigest()


def environment_guard(environ=None, system=None, pid1=None):
    environ = os.environ if environ is None else environ
    system = platform.system() if system is None else system
    if pid1 is None:
        try:
            pid1 = pathlib.Path("/proc/1/comm").read_text().strip()
        except OSError:
            pid1 = "unavailable"
    return {
        "github_actions_true": environ.get("GITHUB_ACTIONS") == "true",
        "linux": system == "Linux",
        "systemd_pid1": pid1 == "systemd",
    }


def trusted_tool(name):
    path = shutil.which(name, path="/usr/sbin:/usr/bin:/sbin:/bin")
    require(path is not None, "required host tool unavailable: " + name)
    path = str(pathlib.Path(path).resolve(strict=True))
    info = os.stat(path)
    require(stat.S_ISREG(info.st_mode) and info.st_uid == 0 and info.st_mode & 0o111 and not info.st_mode & 0o022,
            "host tool is not a root-owned protected executable: " + name)
    return path


def checked_source(value, label):
    path = pathlib.Path(value)
    require(path.is_absolute(), label + " must be an explicit absolute path")
    info = path.lstat()
    require(stat.S_ISREG(info.st_mode) and info.st_mode & 0o111, label + " must be a regular executable, not a symlink")
    return str(path), digest(path.read_bytes())


def parse_show(output):
    values = {}
    for line in output.splitlines():
        require("=" in line, "malformed systemctl property output")
        key, value = line.split("=", 1)
        require(key in PROPERTIES and key not in values, "unknown or repeated systemctl property")
        require(not any(ord(c) < 32 for c in value), "control character in systemctl property")
        values[key] = value
    for key in ("Id", "LoadState", "ActiveState", "SubState"):
        require(key in values, "incomplete native manager output: " + key)
    require(values["Id"] == UNIT, "native manager returned another unit")
    return values


# This helper always executes as the dedicated user with env -i. Its operations
# are restricted to that user's fresh HOME and /proc metadata, never /proc/environ.
USER_HELPER = r'''
import hashlib,json,os,pathlib,stat,sys
op=sys.argv[1]; args=sys.argv[2:]; home=pathlib.Path.home().resolve(strict=True)
def path(s):
 p=pathlib.Path(s)
 if not p.is_absolute() or ".." in p.parts or home not in p.parents: raise RuntimeError("outside fresh HOME")
 parent=p.parent.resolve(strict=False)
 if parent!=home and home not in parent.parents: raise RuntimeError("parent escapes fresh HOME")
 for ancestor in [home,*reversed(p.parent.relative_to(home).parents),p.parent]:
  if not ancestor.is_absolute(): ancestor=home/ancestor
  try: info=ancestor.lstat()
  except FileNotFoundError: continue
  if not stat.S_ISDIR(info.st_mode) or info.st_uid!=os.getuid(): raise RuntimeError("unsafe HOME ancestor")
 return p
def snapshot(p):
 try: s=p.lstat()
 except FileNotFoundError: return {"exists":False}
 out={"exists":True,"inode":s.st_ino,"device":s.st_dev,"uid":s.st_uid,"mode":stat.S_IMODE(s.st_mode)}
 if stat.S_ISLNK(s.st_mode): out.update(kind="symlink",target=os.readlink(p))
 elif stat.S_ISREG(s.st_mode): out.update(kind="file",sha256=hashlib.sha256(p.read_bytes()).hexdigest())
 else: out.update(kind="other")
 return out
if op=="snapshot": out={s:snapshot(path(s)) for s in args}
elif op=="create":
 p=path(args[0]); p.parent.mkdir(mode=0o700,parents=True,exist_ok=True)
 fd=os.open(p,os.O_WRONLY|os.O_CREAT|os.O_EXCL,0o600)
 with os.fdopen(fd,"wb") as f: f.write(bytes.fromhex(args[1]))
 out=snapshot(p)
elif op=="touch":
 p=path(args[0]); expected=json.loads(args[1])
 if snapshot(p)!=expected or p.is_symlink(): raise RuntimeError("test unit changed before timestamp update")
 s=p.stat(); os.utime(p,ns=(s.st_atime_ns,s.st_mtime_ns+2000000000))
 out={"snapshot":snapshot(p),"atime_ns":s.st_atime_ns,"mtime_ns":s.st_mtime_ns}
elif op=="restore-time":
 p=path(args[0]); expected=json.loads(args[1])
 if snapshot(p)!=expected or p.is_symlink(): raise RuntimeError("test unit changed before timestamp restoration")
 os.utime(p,ns=(int(args[2]),int(args[3]))); out=snapshot(p)
elif op=="link":
 p=path(args[0]); p.parent.mkdir(mode=0o700,parents=True,exist_ok=True)
 p.symlink_to(args[1]); out=snapshot(p)
elif op=="remove":
 p=path(args[0]); expected=json.loads(args[1])
 if snapshot(p)!=expected: raise RuntimeError("test artifact changed; refuse removal")
 p.unlink(); out={"removed":True}
elif op=="rmdir":
 path(args[0]).rmdir(); out={"removed":True}
elif op=="proc":
 pid=int(args[0]); p=pathlib.Path("/proc")/str(pid)
 try:
  status={}
  for line in (p/"status").read_text().splitlines():
   k,_,v=line.partition(":")
   if k in ("Name","Pid","PPid","Uid","NoNewPrivs"): status[k]=v.strip()
  # comm may contain spaces or parentheses; split after its final closing paren.
  tail=(p/"stat").read_text().rsplit(")",1)[1].split()
  out={"exists":True,"pid":pid,"start_ticks":tail[19],"exe":os.readlink(p/"exe"),"status":status}
 except FileNotFoundError: out={"exists":False,"pid":pid}
else: raise RuntimeError("unknown helper operation")
print(json.dumps(out,sort_keys=True))
'''


class Smoke:
    def __init__(self, args, run=subprocess.run):
        self.args, self.run = args, run
        self.report = {
            "schema_version": 1, "status": "PREPARED_NOT_RUN", "hardware_access": False,
            "guard": environment_guard(), "checks": [], "commands": [], "cleanup": [],
            "negative_cases": {key: {"status": "NOT_RUN", "purpose": value} for key, value in NEGATIVES.items()},
            "limits": ["Linux user-manager lifecycle only; no Linux USB, login/reboot, suspend or cross-distribution claim.",
                       "No real OS acceptance is claimed until this guarded tool finishes successfully on a runner.",
                       "Host setup/cleanup privileges are confined to a freshly created dedicated user."],
        }
        self.tools = {}
        self.user = "nb1ci-" + secrets.token_hex(6)
        self.account = None
        self.created = False
        self.user_ready = False
        self.manager_requested = False
        self.linger_requested = False
        self.fixture_identity = None
        self.managed_unit = None
        self.managed_files = {}
        self.owned_artifacts = {}  # absolute HOME path -> last observed snapshot

    def command(self, label, argv, scope="dedicated-user", allowed=(0,), timeout=45):
        before = time.monotonic()
        entry = {"label": label, "scope": scope}
        self.report["commands"].append(entry)
        try:
            result = self.run(argv, stdin=subprocess.DEVNULL, stdout=subprocess.PIPE, stderr=subprocess.PIPE,
                              timeout=timeout, check=False, env={"PATH": "/usr/sbin:/usr/bin:/sbin:/bin", "LC_ALL": "C"})
        except subprocess.TimeoutExpired:
            entry.update(status="TIMEOUT", elapsed_seconds=round(time.monotonic() - before, 3))
            raise SmokeError(label + " timed out; this does not prove the native operation was cancelled")
        entry.update(exit_code=result.returncode, elapsed_seconds=round(time.monotonic() - before, 3),
                     stderr_bytes=len(result.stderr), stderr_sha256=digest(result.stderr))
        require(len(result.stdout) <= 65536 and len(result.stderr) <= 65536, label + " output exceeded evidence budget")
        require(result.returncode in allowed, label + " failed (exit " + str(result.returncode) + ")")
        return result.stdout.decode("utf-8", "strict"), result.returncode

    def host(self, label, name, *args, allowed=(0,)):
        return self.command(label, self.privilege + [self.tools[name], *args], "host-fixture", allowed)

    def user_command(self, label, argv, allowed=(0,), overrides=None):
        require(self.account is not None, "fresh account has not been established")
        values = {
            "HOME": self.account.pw_dir, "USER": self.user, "LOGNAME": self.user,
            "PATH": "/usr/bin:/bin", "LANG": "C", "LC_ALL": "C",
            "XDG_RUNTIME_DIR": "/run/user/" + str(self.account.pw_uid),
            "DBUS_SESSION_BUS_ADDRESS": "unix:path=/run/user/" + str(self.account.pw_uid) + "/bus",
        }
        if overrides:
            require(set(overrides) <= {"XDG_RUNTIME_DIR", "DBUS_SESSION_BUS_ADDRESS"}, "unapproved environment override")
            values.update(overrides)
        env = [key + "=" + value for key, value in sorted(values.items())]
        command = self.privilege + [self.tools["runuser"], "--user", self.user, "--", self.tools["env"], "-i", *env, *argv]
        return self.command(label, command, allowed=allowed)

    def helper(self, op, *args):
        raw, _ = self.user_command("user-files-" + op, [self.tools["python3"], "-c", USER_HELPER, op, *args])
        value = json.loads(raw)
        require(isinstance(value, dict), "invalid user helper result")
        return value

    def snapshot(self, *paths):
        return self.helper("snapshot", *paths)

    def cli(self, operation, *args, reject=False, overrides=None):
        raw, rc = self.user_command("cli-" + operation, [self.cli_path, "service", operation, *args, "--json"],
                                    allowed=tuple(range(1, 126)) if reject else (0,), overrides=overrides)
        payload = json.loads(raw)
        require(isinstance(payload, dict) and payload.get("schema_version") == 1, "CLI did not return schema_version=1 status")
        require(payload.get("config_state") in {"not_checked", "absent", "managed", "conflict"}, "CLI config_state is unknown")
        # Deliberately avoid depending on platform-specific prose or copying arbitrary diagnostics.
        projection = {key: payload[key] for key in ("schema_version", "config_state", "registration", "registration_owned",
                      "managed_enabled_for_login", "enable_link_state") if key in payload}
        self.report["checks"].append({"check": "cli-" + operation, "status": "EXPECTED_REJECTION" if reject else "PASS",
                                      "exit_code": rc, "status_fields": projection})
        return payload

    def native(self):
        raw, rc = self.user_command("native-show", [self.tools["systemctl"], "--user", "--no-pager", "--no-ask-password",
                              "show", "--all", "--property=" + ",".join(PROPERTIES), UNIT], allowed=(0, 4))
        values = parse_show(raw)
        require(rc == 0 or values["LoadState"] == "not-found", "native show returned a non-absence failure")
        return values

    def native_action(self, action):
        require(action in {"daemon-reload", "stop", "reset-failed"}, "unapproved test manager action")
        argv = [self.tools["systemctl"], "--user", "--no-pager", "--no-ask-password", action]
        if action != "daemon-reload":
            argv.append(UNIT)
        return self.user_command("fixture-" + action, argv)

    def wait_native(self, active, loaded="loaded"):
        last = None
        deadline = time.monotonic() + 12
        while time.monotonic() < deadline:
            last = self.native()
            if last.get("LoadState") == loaded and last.get("ActiveState") == active and last.get("Job", "") in {"", "0"}:
                return last
            time.sleep(0.1)
        raise SmokeError("native state did not settle: " + json.dumps(last, sort_keys=True))

    def own_files(self):
        files = self.snapshot(self.unit_path, self.link_path)
        unit, link = files[self.unit_path], files[self.link_path]
        require(unit.get("kind") == "file" and unit.get("uid") == self.account.pw_uid, "unit is not an owned regular file")
        require(link.get("kind") == "symlink" and link.get("uid") == self.account.pw_uid and link.get("target") == self.unit_path,
                "persistent enable link is not exactly the expected owned absolute link")
        return files

    def installed_inactive(self):
        native = self.wait_native("inactive")
        require(native.get("SubState") == "dead" and native.get("MainPID") == "0", "default install unexpectedly started a process")
        require(native.get("FragmentPath") == self.unit_path and native.get("UnitFileState") == "enabled", "install was not persistently enabled")
        require(native.get("NoNewPrivileges") == "yes", "loaded service does not declare NoNewPrivileges=yes")
        require(native.get("DropInPaths", "") == "" and native.get("Transient") == "no" and native.get("NeedDaemonReload") == "no",
                "installed service has unexpected manager overrides")
        files = self.own_files()
        self.managed_unit = files[self.unit_path]
        self.managed_files = files
        self.report["checks"].append({"check": "install-enabled-inactive", "status": "PASS", "native": native, "files": files})
        return files

    def install(self):
        payload = self.cli("install", "--binary", self.fixture_path, "--devices", SERIAL, "--listen", self.listen)
        require(payload["config_state"] == "managed", "install did not report a managed configuration")
        return self.installed_inactive()

    def process(self, pid):
        require(isinstance(pid, int) and pid > 0, "invalid fixture PID")
        return self.helper("proc", str(pid))

    def started(self):
        native = self.wait_native("active")
        require(native.get("SubState") == "running" and native.get("NoNewPrivileges") == "yes", "service is not protected and running")
        pid = int(native.get("MainPID", "0"))
        opener = urllib.request.build_opener(urllib.request.ProxyHandler({}))
        deadline, health = time.monotonic() + 8, None
        while time.monotonic() < deadline:
            try:
                with opener.open("http://" + self.listen + "/health", timeout=1) as response:
                    data = response.read(4097)
                    require(len(data) <= 4096, "fixture health response too large")
                    health = json.loads(data)
                break
            except (OSError, ValueError):
                time.sleep(0.1)
        require(isinstance(health, dict) and health.get("fixture") is True and health.get("pid") == pid and
                health.get("devices") == [SERIAL] and health.get("listen") == self.listen, "HTTP health does not identify the expected fixture process")
        identity = self.process(pid)
        require(identity.get("exists") and identity.get("exe") == self.fixture_path, "MainPID is not the installed fixture executable")
        status = identity.get("status", {})
        require(status.get("Uid", "").split() == [str(self.account.pw_uid)] * 4, "fixture is not entirely the dedicated ordinary UID")
        require(status.get("NoNewPrivs") == "1", "kernel did not apply NoNewPrivs=1 to the fixture")
        self.fixture_identity = identity
        self.report["checks"].append({"check": "active-http-pid-kernel", "status": "PASS", "native": native, "http": health, "process": identity})
        return identity

    def exited(self, identity):
        deadline = time.monotonic() + 8
        while time.monotonic() < deadline:
            current = self.process(identity["pid"])
            if not current.get("exists") or current.get("start_ticks") != identity["start_ticks"]:
                self.report["checks"].append({"check": "prior-fixture-process-exited", "status": "PASS", "pid": identity["pid"], "start_ticks": identity["start_ticks"]})
                return
            time.sleep(0.1)
        raise SmokeError("stop did not prove that the prior fixture process exited")

    def positive(self):
        initial = self.cli("status")
        require(initial["config_state"] == "absent", "fresh user unexpectedly has a configuration")
        native = self.wait_native("inactive", "not-found")
        self.report["checks"].append({"check": "fresh-user-unit-absent", "status": "PASS", "native": native})
        installed = self.install()
        self.cli("start")
        first = self.started()
        self.cli("start")
        second = self.started()
        require((first["pid"], first["start_ticks"]) == (second["pid"], second["start_ticks"]), "idempotent start replaced the process")
        require(self.own_files() == installed, "start changed persistent owned configuration")
        self.report["checks"].append({"check": "start-idempotent-same-process", "status": "PASS"})
        self.cli("stop")
        self.installed_inactive()
        self.exited(second)
        stopped = self.native()
        require(stopped.get("Result") == "success" and stopped.get("ExecMainCode") == "1" and stopped.get("ExecMainStatus") == "0",
                "fixture did not exit normally with status 0 after SIGTERM")
        self.report["checks"].append({"check": "fixture-sigterm-clean-exit", "status": "PASS", "native": stopped})
        require(self.own_files() == installed, "stop did not retain the exact unit and persistent enable link")
        self.cli("stop")
        require(self.own_files() == installed, "idempotent stop changed owned files")
        self.cli("start")
        third = self.started()
        self.cli("uninstall", "--stop")
        self.exited(third)
        self.absent()

    def absent(self):
        native = self.wait_native("inactive", "not-found")
        files = self.snapshot(self.unit_path, self.link_path)
        require(all(not item["exists"] for item in files.values()), "uninstall left unit or enable link behind")
        require(native.get("MainPID", "0") == "0", "uninstalled manager still reports a process")
        self.report["checks"].append({"check": "uninstall-no-unit-link-process", "status": "PASS", "native": native, "files": files})

    def inject_file(self, path, content):
        state = self.helper("create", path, content.hex())
        self.owned_artifacts[path] = state
        return state

    def inject_link(self, path, target):
        state = self.helper("link", path, target)
        self.owned_artifacts[path] = state
        return state

    def remove_injected(self, path):
        self.helper("remove", path, json.dumps(self.owned_artifacts[path], sort_keys=True))
        del self.owned_artifacts[path]

    def negative(self, name):
        # Each optional case begins and ends without a product unit. Never invoke
        # broad systemctl enable/disable: their scope exceeds one known link.
        self.absent()
        if name == "transient":
            argv = [self.tools["systemd-run"], "--user", "--unit=" + UNIT, "--property=NoNewPrivileges=yes",
                    "--property=Restart=no", self.fixture_path, "server", "--devices", SERIAL, "--listen", self.listen]
            self.user_command("create-owned-transient-fixture", argv)
            native = self.wait_native("active")
            require(native.get("Transient") == "yes", "test setup did not create a transient unit")
            identity = self.started()
            self.cli("install", "--binary", self.fixture_path, "--devices", SERIAL, "--listen", self.listen, reject=True)
            after = self.native()
            require(after.get("Transient") == "yes" and after.get("MainPID") == str(identity["pid"]), "rejected install changed the external transient fixture")
            require(all(not s["exists"] for s in self.snapshot(self.unit_path, self.link_path).values()), "rejected install wrote disk configuration")
            self.native_action("stop")  # exact transient fixture created immediately above
            self.exited(identity)
            self.native_action("daemon-reload")
            self.absent()
        else:
            installed = self.install()
            before = self.snapshot(self.unit_path, self.link_path)
            injected = None
            if name == "external-enable":
                injected = self.config_root + "/systemd/user/multi-user.target.wants/" + UNIT
                self.inject_link(injected, self.unit_path)
                self.native_action("daemon-reload")
                self.cli("uninstall", reject=True)
                require(self.snapshot(self.unit_path)[self.unit_path] == before[self.unit_path], "conflicting enable source caused unit removal/change")
            elif name == "dropin":
                injected = self.config_root + "/systemd/user/nb1.service.d/10-smoke.conf"
                self.inject_file(injected, b"[Service]\nEnvironment=NB1_SMOKE_DROPIN=1\n")
                self.native_action("daemon-reload")
                require(self.native().get("DropInPaths", "") != "", "test drop-in was not loaded")
                self.cli("start", reject=True)
            elif name == "mask":
                # Persistent unit remains intact; the more-precedent .control
                # path is confined to this fresh user's configuration directory.
                injected = self.config_root + "/systemd/user.control/" + UNIT
                self.inject_link(injected, "/dev/null")
                self.native_action("daemon-reload")
                require(self.native().get("LoadState") == "masked", "test mask did not take effect")
                self.cli("start", reject=True)
            elif name == "need-reload":
                # Only mtime changes: the marker, bytes and inode remain valid.
                # This isolates manager freshness from a marker-validation error.
                touched = self.helper("touch", self.unit_path, json.dumps(before[self.unit_path], sort_keys=True))
                require(self.native().get("NeedDaemonReload") == "yes", "manager did not observe the test's pending reload")
                self.cli("start", reject=True)
                require(self.snapshot(self.unit_path)[self.unit_path] == touched["snapshot"], "refusal changed the timestamp-modified unit")
                self.helper("restore-time", self.unit_path, json.dumps(touched["snapshot"], sort_keys=True), str(touched["atime_ns"]), str(touched["mtime_ns"]))
                self.native_action("daemon-reload")
            elif name == "bus-loss":
                invalid = "/run/nb1-smoke-missing-" + self.user
                self.cli("start", reject=True, overrides={"XDG_RUNTIME_DIR": invalid, "DBUS_SESSION_BUS_ADDRESS": "unix:path=" + invalid + "/bus"})
            else:
                raise SmokeError("unknown negative scenario")
            require(self.native().get("ActiveState") == "inactive" and self.native().get("MainPID") == "0", "refusal unexpectedly started a process")
            if injected:
                require(self.snapshot(injected)[injected] == self.owned_artifacts[injected], "refusal modified the external test artifact")
                self.remove_injected(injected)
                if name == "dropin":
                    self.helper("rmdir", str(pathlib.Path(injected).parent))
                self.native_action("daemon-reload")
            if name in {"dropin", "mask", "bus-loss"}:
                require(self.own_files() == installed, "refusal changed persistent managed files")
            # external-enable can legitimately remove the one owned default link
            # before detecting another enabled source. Install restores only it.
            self.install()
            self.cli("uninstall")
            self.absent()
        self.report["negative_cases"][name]["status"] = "PASS"

    def setup(self):
        require(all(self.report["guard"].values()), "ENVIRONMENT_UNAVAILABLE: requires GITHUB_ACTIONS=true, Linux, and systemd PID 1")
        require(self.args.execute and self.args.binary and self.args.fixture, "--execute requires explicit --binary and --fixture")
        cli_source, cli_hash = checked_source(self.args.binary, "CLI binary")
        fixture_source, fixture_hash = checked_source(self.args.fixture, "fixture binary")
        require(cli_source != fixture_source and cli_hash != fixture_hash, "actual CLI and USB-free fixture must be different executables")
        self.tools = {name: trusted_tool(name) for name in ("sudo", "runuser", "env", "useradd", "userdel", "install", "loginctl", "systemctl", "systemd-run", "python3") if name != "sudo" or os.geteuid() != 0}
        self.privilege = [] if os.geteuid() == 0 else [self.tools["sudo"], "-n", "--"]
        require(USER_PATTERN.fullmatch(self.user), "uncontrolled fixture user name")
        try:
            pwd.getpwnam(self.user)
        except KeyError:
            pass
        else:
            raise SmokeError("refusing a pre-existing test user")
        fresh_home = "/home/" + self.user
        require(not os.path.lexists(fresh_home), "refusing a pre-existing fixture HOME")
        require(not os.path.lexists("/var/lib/systemd/linger/" + self.user), "refusing a pre-existing linger marker")
        self.host("create-fresh-user", "useradd", "--create-home", "--home-dir", fresh_home, "--user-group", "--shell", "/bin/sh", self.user)
        self.created = True
        self.account = pwd.getpwnam(self.user)
        require(self.account.pw_uid >= 1000 and self.account.pw_uid != os.getuid() and self.account.pw_dir == "/home/" + self.user,
                "created account does not have the expected ordinary UID and fresh HOME")
        require(not os.path.lexists("/run/user/" + str(self.account.pw_uid)), "refusing a pre-existing runtime for the newly assigned UID")
        self.report["fixture_user"] = {"name": self.user, "uid": self.account.pw_uid, "home": self.account.pw_dir}
        binary_dir = self.account.pw_dir + "/bin"
        self.host("create-user-bin", "install", "-d", "-m", "0755", "-o", self.user, "-g", str(self.account.pw_gid), binary_dir)
        self.cli_path, self.fixture_path = binary_dir + "/nb1-cli", binary_dir + "/nb1-systemd-fixture"
        for label, source, target in (("cli", cli_source, self.cli_path), ("fixture", fixture_source, self.fixture_path)):
            self.host("stage-" + label, "install", "-m", "0755", "-o", self.user, "-g", str(self.account.pw_gid), source, target)
        staged = self.snapshot(self.cli_path, self.fixture_path)
        require(staged[self.cli_path].get("sha256") == cli_hash and staged[self.fixture_path].get("sha256") == fixture_hash,
                "staged binaries do not match supplied source digests")
        self.report["binaries"] = {"cli_sha256": cli_hash, "fixture_sha256": fixture_hash}
        self.config_root = self.account.pw_dir + "/.config"
        self.unit_path = self.config_root + "/systemd/user/" + UNIT
        self.link_path = self.config_root + "/systemd/user/default.target.wants/" + UNIT
        # Reserve only to choose a currently available unprivileged loopback port;
        # no service is launched here, and HTTP PID checking detects port races.
        with socket.socket() as connection:
            connection.bind(("127.0.0.1", 0))
            self.listen = "127.0.0.1:" + str(connection.getsockname()[1])
        self.linger_requested = True  # an error can still have changed host state
        self.host("enable-fresh-user-linger", "loginctl", "enable-linger", self.user)
        self.manager_requested = True
        self.host("start-fresh-user-manager", "systemctl", "start", "user@" + str(self.account.pw_uid) + ".service")
        self.user_ready = True
        version, _ = self.user_command("native-version", [self.tools["systemctl"], "--version"])
        require(version.splitlines() and version.splitlines()[0].startswith("systemd "), "native systemctl version was not recognized")
        self.report["native_environment"] = {"systemctl_version": version.splitlines()[0], "architecture": platform.machine(), "kernel": platform.release()}
        self.report["status"] = "RUNNING"

    def matching_account(self):
        require(self.created and self.account is not None and USER_PATTERN.fullmatch(self.user), "no verified fresh account for cleanup")
        current = pwd.getpwnam(self.user)
        require(self.account.pw_uid >= 1000 and self.account.pw_dir == "/home/" + self.user, "fresh account scope is not confirmed")
        require((current.pw_uid, current.pw_gid, current.pw_dir) == (self.account.pw_uid, self.account.pw_gid, self.account.pw_dir),
                "fixture account changed; refusing host cleanup")

    def cleanup(self):
        if not self.created:
            return
        errors = []
        try:
            self.matching_account()
        except (SmokeError, KeyError) as error:
            self.report["cleanup"].append({"step": "verify-fresh-account", "status": "FAIL", "error": str(error)})
            return
        # These units/linger/home belong solely to our newly-created UID. Do not
        # touch the runner's user manager or use systemctl disable/kill/killall.
        steps = [
            ("disable-own-linger", "loginctl", ["disable-linger", self.user], (0,)),
            # logind may already have removed the user after disabling linger.
            # Exit 1 is not an absence assertion; native state is checked below.
            ("terminate-own-user", "loginctl", ["terminate-user", self.user], (0, 1)),
            ("stop-own-manager", "systemctl", ["stop", "user@" + str(self.account.pw_uid) + ".service"], (0,)),
            ("stop-own-runtime", "systemctl", ["stop", "user-runtime-dir@" + str(self.account.pw_uid) + ".service"], (0,)),
        ] if self.linger_requested or self.manager_requested else []
        for label, command, args, allowed in steps:
            try:
                self.matching_account()
                _, rc = self.host(label, command, *args, allowed=allowed)
                self.report["cleanup"].append({"step": label, "status": "PASS", "exit_code": rc,
                                              "native_absence_still_required": label == "terminate-own-user" and rc != 0})
            except (SmokeError, OSError, ValueError, KeyError) as error:
                errors.append(label)
                self.report["cleanup"].append({"step": label, "status": "FAIL", "error": str(error)})
        # userdel --remove is confined to the verified fresh user and its HOME.
        # Never delete an unverified UID/home after a failed identity check.
        try:
            self.matching_account()
            # A failed bus/stop command cannot be turned into success merely by
            # observing a missing pathname. Query the exact manager and runtime
            # template instances as well, before removing their fresh account.
            for template in ("user@", "user-runtime-dir@"):
                instance = template + str(self.account.pw_uid) + ".service"
                raw, _ = self.host("verify-" + template + "inactive", "systemctl", "show", "--property=ActiveState,SubState", instance)
                fields = dict(line.split("=", 1) for line in raw.splitlines())
                require(fields.get("ActiveState") == "inactive" and fields.get("SubState") == "dead", "fresh user manager/runtime is not confirmed inactive")
            require(not pathlib.Path("/run/user/" + str(self.account.pw_uid)).exists(), "fresh runtime remains before account removal")
            require(not pathlib.Path("/var/lib/systemd/linger/" + self.user).exists(), "fresh linger marker remains before account removal")
            # Remove only exact tracked test artifacts. Any changed artifact is
            # reported as cleanup failure, never blindly unlinked.
            for path in list(self.owned_artifacts):
                self.remove_injected(path)
            for path, expected in self.managed_files.items():
                current = self.snapshot(path)[path]
                if current["exists"]:
                    require(current == expected, "managed test artifact changed; refuse unlink")
                    self.helper("remove", path, json.dumps(expected, sort_keys=True))
            self.report["cleanup"].append({"step": "exact-tracked-unit-links-artifacts", "status": "PASS"})
            self.host("remove-fresh-account-home", "userdel", "--remove", self.user)
            try:
                pwd.getpwnam(self.user)
            except KeyError:
                pass
            else:
                raise SmokeError("fresh account remains after userdel")
            require(not pathlib.Path(self.account.pw_dir).exists(), "fresh HOME remains after cleanup")
            require(not pathlib.Path("/run/user/" + str(self.account.pw_uid)).exists(), "fresh runtime remains after cleanup")
            require(not pathlib.Path("/var/lib/systemd/linger/" + self.user).exists(), "fresh linger marker remains after cleanup")
            if self.fixture_identity is not None:
                proc = pathlib.Path("/proc") / str(self.fixture_identity["pid"])
                if proc.exists():
                    try:
                        tail = (proc / "stat").read_text().rsplit(")", 1)[1].split()
                    except FileNotFoundError:
                        tail = None
                    require(tail is None or tail[19] != self.fixture_identity["start_ticks"], "fixture process survived user-manager cleanup")
            self.report["cleanup"].append({"step": "account-home-runtime-linger-process-absent", "status": "PASS"})
        except (SmokeError, OSError, ValueError, KeyError) as error:
            errors.append("remove-fresh-account-home")
            self.report["cleanup"].append({"step": "remove-fresh-account-home", "status": "FAIL", "error": str(error)})
        if errors:
            self.report["cleanup_failed"] = True

    def execute(self):
        if not self.args.execute:
            self.report["prepared_cases"] = ["fresh-absent", "install-enabled-inactive", "start", "start-idempotent",
                                             "kernel-no-new-privileges", "stop-process-exit-retains-files", "stop-idempotent",
                                             "restart", "uninstall-active-no-residue"]
            return self.report
        try:
            self.setup()
            self.positive()
            for case in self.args.negative_case:
                self.negative(case)
            self.report["status"] = "PASS"
        except (SmokeError, OSError, ValueError, KeyError, KeyboardInterrupt) as error:
            self.report["status"] = "ENVIRONMENT_UNAVAILABLE" if str(error).startswith("ENVIRONMENT_UNAVAILABLE:") else "FAIL"
            self.report["error"] = str(error)
        finally:
            self.cleanup()
        if self.report.get("cleanup_failed") or any(step["status"] != "PASS" for step in self.report["cleanup"]):
            self.report["status"] = "FAIL"
        return self.report


def parser():
    command = argparse.ArgumentParser(description=__doc__)
    command.add_argument("--execute", action="store_true", help="opt in to host fixture creation; requires all environment guards")
    command.add_argument("--binary", help="explicit absolute path to the real Linux nb1 CLI")
    command.add_argument("--fixture", help="explicit absolute path to the separately built USB-free Linux fixture")
    command.add_argument("--negative-case", choices=sorted(NEGATIVES), action="append", default=[], help="optional independent native refusal case")
    command.add_argument("--output", type=pathlib.Path, help="new evidence directory; refuses an existing directory")
    return command


def main(argv=None):
    args = parser().parse_args(argv)
    if args.execute and (not args.binary or not args.fixture):
        parser().error("--execute requires --binary and --fixture")
    if args.output is not None:
        # Allocate before any host action; never overwrite another run's evidence.
        args.output.mkdir(parents=True, exist_ok=False)
    previous_term = None
    if args.execute:
        def interrupted(_signum, _frame):
            raise SmokeError("runner termination requested; attempting exact fresh-user cleanup")
        previous_term = signal.signal(signal.SIGTERM, interrupted)
    try:
        report = Smoke(args).execute()
    finally:
        if previous_term is not None:
            signal.signal(signal.SIGTERM, previous_term)
    encoded = json.dumps(report, indent=2, sort_keys=True) + "\n"
    if args.output is not None:
        (args.output / "results.json").write_text(encoded)
    print(encoded, end="")
    return 0 if report["status"] in {"PREPARED_NOT_RUN", "PASS"} else 1


if __name__ == "__main__":
    sys.exit(main())
