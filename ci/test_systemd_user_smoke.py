"""Software-only safety/contract checks. No sudo, systemctl, accounts or USB."""
import argparse
import importlib.util
import json
import os
import pathlib
import stat
import subprocess
import sys
import tempfile
import unittest
from unittest import mock

SCRIPT = pathlib.Path(__file__).with_name("systemd-user-smoke.py")
spec = importlib.util.spec_from_file_location("systemd_user_smoke", SCRIPT)
smoke = importlib.util.module_from_spec(spec)
spec.loader.exec_module(smoke)


def args(**kwargs):
    values = {"execute": False, "binary": None, "fixture": None, "negative_case": [], "output": None, "actual_daemon": False,
              "homebrew_archive": None, "source_commit": None}
    values.update(kwargs)
    return argparse.Namespace(**values)


def account(uid=23001, marker="fixture-marker"):
    return argparse.Namespace(pw_name="nb1ci-0123456789ab", pw_uid=uid, pw_gid=uid,
                              pw_dir="/home/nb1ci-0123456789ab", pw_gecos=marker)


class GuardTests(unittest.TestCase):
    def test_homebrew_revision_is_required_before_any_host_action(self):
        with mock.patch.object(smoke, 'Smoke') as fixture, self.assertRaises(SystemExit):
            smoke.main(['--execute', '--binary', '/cli', '--fixture', '/fixture', '--homebrew-archive', '/archive'])
        fixture.assert_not_called()

    def test_three_guards_are_independent_and_exact(self):
        self.assertTrue(all(smoke.environment_guard({"GITHUB_ACTIONS": "true"}, "Linux", "systemd").values()))
        for env, system, pid1 in (({}, "Linux", "systemd"), ({"GITHUB_ACTIONS": "True"}, "Linux", "systemd"),
                                  ({"GITHUB_ACTIONS": "true"}, "Darwin", "systemd"),
                                  ({"GITHUB_ACTIONS": "true"}, "Linux", "bash")):
            self.assertFalse(all(smoke.environment_guard(env, system, pid1).values()))

    def test_default_does_not_run_even_when_guard_would_allow(self):
        runner = mock.Mock(side_effect=RuntimeError("must never execute"))
        with mock.patch.object(smoke, "environment_guard", return_value={"a": True}):
            report = smoke.Smoke(args(), run=runner).execute()
        self.assertEqual(report["status"], "PREPARED_NOT_RUN")
        self.assertEqual(report["commands"], [])
        self.assertTrue(all(item["status"] == "NOT_RUN" for item in report["negative_cases"].values()))
        runner.assert_not_called()

    def test_execute_guard_precedes_binary_and_host_tools(self):
        runner = mock.Mock(side_effect=RuntimeError("must never execute"))
        with mock.patch.object(smoke, "environment_guard", return_value={"linux": False}), \
             mock.patch.object(smoke, "checked_source") as source, mock.patch.object(smoke, "trusted_tool") as tools:
            report = smoke.Smoke(args(execute=True, binary="/no/cli", fixture="/no/fixture"), run=runner).execute()
        self.assertEqual(report["status"], "ENVIRONMENT_UNAVAILABLE")
        source.assert_not_called(); tools.assert_not_called(); runner.assert_not_called()

    def test_preexisting_generated_account_refused_before_create(self):
        runner = mock.Mock(side_effect=RuntimeError("must never execute"))
        with mock.patch.object(smoke, "environment_guard", return_value={"all": True}), \
             mock.patch.object(smoke, "checked_source", side_effect=[("/cli", "a"), ("/fixture", "b")]), \
             mock.patch.object(smoke, "trusted_tool", side_effect=lambda name: "/usr/bin/" + name), \
             mock.patch.object(smoke.pwd, "getpwnam", return_value=account()):
            report = smoke.Smoke(args(execute=True, binary="/cli", fixture="/fixture"), run=runner).execute()
        self.assertEqual(report["status"], "FAIL")
        self.assertIn("pre-existing", report["error"])
        runner.assert_not_called()

    def test_guards_survive_optimized_python(self):
        # Force a false GITHUB_ACTIONS guard on every host; this invokes only the
        # guarded script, never any privileged or service-manager subprocess.
        env = dict(os.environ, GITHUB_ACTIONS="false")
        result = subprocess.run([sys.executable, "-O", str(SCRIPT), "--execute", "--binary", "/not-a-cli", "--fixture", "/not-a-fixture"],
                                env=env, capture_output=True, text=True, check=False)
        self.assertEqual(result.returncode, 1)
        report = json.loads(result.stdout)
        self.assertEqual(report["status"], "ENVIRONMENT_UNAVAILABLE")
        self.assertEqual(report["commands"], [])


class ExecutionTests(unittest.TestCase):
    def prepared(self, runner=None):
        obj = smoke.Smoke(args(), run=runner or mock.Mock())
        obj.user = "nb1ci-0123456789ab"
        obj.account = account()
        obj.tools = {name: "/usr/bin/" + name for name in ("runuser", "env", "systemctl", "python3", "loginctl", "userdel")}
        obj.privilege = ["/usr/bin/sudo", "-n", "--"]
        return obj

    def test_all_user_commands_drop_environment_and_identity(self):
        captured = []
        def run(argv, **kwargs):
            captured.append((argv, kwargs))
            return subprocess.CompletedProcess(argv, 0, b"ok", b"")
        obj = self.prepared(run)
        with mock.patch.dict(os.environ, {"GITHUB_TOKEN": "not-to-inherit", "SUDO_UID": "0", "XDG_CONFIG_HOME": "/wrong"}):
            obj.user_command("test", ["/actual/nb1", "service", "status", "--json"])
        argv, options = captured[0]
        self.assertEqual(argv[:8], ["/usr/bin/sudo", "-n", "--", "/usr/bin/runuser", "--user", obj.user, "--", "/usr/bin/env"])
        self.assertEqual(argv[8], "-i")
        self.assertNotIn("not-to-inherit", json.dumps(captured))
        self.assertFalse(any("XDG_CONFIG_HOME=" in value for value in argv))
        self.assertFalse(any("SUDO_UID=" in value for value in argv))
        self.assertEqual(options["env"], {"PATH": "/usr/sbin:/usr/bin:/sbin:/bin", "LC_ALL": "C"})
        self.assertIn("HOME=/home/" + obj.user, argv)
        self.assertIn("XDG_RUNTIME_DIR=/run/user/23001", argv)
        self.assertIn("DBUS_SESSION_BUS_ADDRESS=unix:path=/run/user/23001/bus", argv)

    def test_bus_failure_override_changes_both_addresses_only(self):
        obj = self.prepared(lambda argv, **kw: subprocess.CompletedProcess(argv, 0, b"", b""))
        with self.assertRaises(smoke.SmokeError):
            obj.user_command("bad", ["true"], overrides={"HOME": "/root"})
        obj.user_command("negative", ["true"], overrides={"XDG_RUNTIME_DIR": "/missing", "DBUS_SESSION_BUS_ADDRESS": "unix:path=/missing/bus"})

    def test_command_does_not_persist_stderr_or_environment(self):
        obj = self.prepared(lambda argv, **kw: subprocess.CompletedProcess(argv, 1, b"", b"secret-raw-error"))
        with self.assertRaises(smoke.SmokeError):
            obj.command("fixture-command", ["dummy"])
        encoded = json.dumps(obj.report)
        self.assertNotIn("secret-raw-error", encoded)
        self.assertIn(smoke.digest(b"secret-raw-error"), encoded)

    def test_timeout_does_not_claim_native_cancellation(self):
        obj = self.prepared(mock.Mock(side_effect=subprocess.TimeoutExpired("dummy", 45)))
        with self.assertRaisesRegex(smoke.SmokeError, "does not prove"):
            obj.command("timed-out", ["dummy"])
        self.assertEqual(obj.report["commands"][-1]["status"], "TIMEOUT")

    def test_cleanup_is_called_after_partial_setup_failure(self):
        obj = smoke.Smoke(args(execute=True))
        obj.setup = mock.Mock(side_effect=smoke.SmokeError("partial setup failed"))
        obj.cleanup = mock.Mock()
        report = obj.execute()
        self.assertEqual(report["status"], "FAIL")
        obj.cleanup.assert_called_once()

    def test_cleanup_failure_prevents_pass(self):
        obj = smoke.Smoke(args(execute=True))
        obj.setup = mock.Mock()
        obj.positive = mock.Mock()
        obj.cleanup = lambda: obj.report.update(cleanup_failed=True)
        self.assertEqual(obj.execute()["status"], "FAIL")

    def test_actual_daemon_failure_prevents_pass_and_still_cleans_up(self):
        obj = smoke.Smoke(args(execute=True, actual_daemon=True))
        obj.setup = mock.Mock(); obj.positive = mock.Mock(); obj.cleanup = mock.Mock()
        obj.actual_daemon = mock.Mock(side_effect=smoke.SmokeError("actual daemon did not start"))
        report = obj.execute()
        self.assertEqual(report["status"], "FAIL")
        obj.actual_daemon.assert_called_once()
        obj.cleanup.assert_called_once()

    def test_actual_daemon_is_not_started_by_default(self):
        obj = smoke.Smoke(args(execute=True))
        obj.setup = mock.Mock(); obj.positive = mock.Mock(); obj.cleanup = mock.Mock()
        obj.actual_daemon = mock.Mock(side_effect=RuntimeError("must not run"))
        self.assertEqual(obj.execute()["status"], "PASS")
        obj.actual_daemon.assert_not_called()

    def test_homebrew_failure_cannot_pass_and_retains_cleanup(self):
        obj = smoke.Smoke(args(execute=True, homebrew_archive="/archive", source_commit="a"*40))
        obj.setup = mock.Mock(); obj.positive = mock.Mock(); obj.cleanup = mock.Mock()
        obj.homebrew_upgrade = mock.Mock(side_effect=smoke.SmokeError("upgrade failed"))
        self.assertEqual(obj.execute()["status"], "FAIL")
        obj.homebrew_upgrade.assert_called_once()
        obj.cleanup.assert_called_once()

    def test_changed_account_refuses_all_cleanup_actions(self):
        obj = self.prepared()
        obj.created = obj.creation_verified = True
        obj.creation_marker = "fixture-marker"
        obj.host = mock.Mock(side_effect=RuntimeError("must not run"))
        with mock.patch.object(smoke.pwd, "getpwnam", return_value=account(23002)):
            obj.cleanup()
        obj.host.assert_not_called()
        self.assertEqual(obj.report["cleanup"][0]["status"], "FAIL")

    def test_unselected_negative_cases_are_not_pass(self):
        obj = smoke.Smoke(args(execute=True))
        obj.setup = mock.Mock(); obj.positive = mock.Mock(); obj.cleanup = mock.Mock()
        report = obj.execute()
        self.assertEqual(report["status"], "PASS")
        self.assertTrue(all(item["status"] == "NOT_RUN" for item in report["negative_cases"].values()))


class NativeContractTests(unittest.TestCase):
    def test_parse_requires_exact_machine_identity_and_fields(self):
        good = "Id=nb1.service\nLoadState=loaded\nActiveState=inactive\nSubState=dead\nJob=\nNoNewPrivileges=yes\n"
        self.assertEqual(smoke.parse_show(good)["Job"], "")
        for bad in (good + "Id=nb1.service\n", good + "Unexpected=1\n", good.replace("nb1.service", "other.service"),
                    good.replace("LoadState=loaded\n", ""), "Failed to connect to bus"):
            with self.assertRaises(smoke.SmokeError):
                smoke.parse_show(bad)

    def test_inactive_and_enabled_do_not_prove_no_new_privileges(self):
        obj = smoke.Smoke(args())
        obj.unit_path = "/home/test/.config/systemd/user/nb1.service"
        obj.wait_native = lambda *a: {"SubState": "dead", "MainPID": "0", "FragmentPath": obj.unit_path, "UnitFileState": "enabled", "NoNewPrivileges": "no"}
        with self.assertRaisesRegex(smoke.SmokeError, "NoNewPrivileges"):
            obj.installed_inactive()

    def test_fake_start_requires_same_kernel_identity(self):
        obj = smoke.Smoke(args())
        obj.unit_path, obj.link_path = "/unit", "/link"
        obj.cli = lambda operation, *a: {"config_state": "absent"} if operation == "status" else {}
        obj.wait_native = lambda *a: {}
        files = {"/unit": {"exists": True}, "/link": {"exists": True}}
        obj.install = lambda: files
        obj.own_files = lambda: files
        identities = iter(({"pid": 99, "start_ticks": "100"}, {"pid": 99, "start_ticks": "101"}))
        obj.started = lambda: next(identities)
        with self.assertRaisesRegex(smoke.SmokeError, "replaced the process"):
            obj.positive()

    def test_user_helper_never_reads_process_environment(self):
        compile(smoke.USER_HELPER, "user-helper", "exec")
        self.assertNotIn('/"environ"', smoke.USER_HELPER)
        self.assertNotIn('os.environ', smoke.USER_HELPER)
        self.assertNotIn('shutil.rmtree', smoke.USER_HELPER)


class CreationRecoveryTests(unittest.TestCase):
    def requested(self):
        obj = smoke.Smoke(args(execute=True, binary="/cli", fixture="/fixture"))
        obj.user = "nb1ci-0123456789ab"
        obj.creation_marker = "fixture-marker"
        obj.creation_attempted = True
        obj.creation_baseline = {"uids": {0, 1000}, "gids": {0, 1000}}
        return obj

    def test_partial_useradd_failure_or_timeout_recovers_own_account(self):
        for home_exists, timeout in ((False, False), (True, False), (False, True)):
            with self.subTest(home_exists=home_exists, timeout=timeout):
                obj = self.requested()
                obj.creation_attempted = False
                obj.report["guard"] = {"all": True}
                state = {"present": False}
                acct = account()
                group = argparse.Namespace(gr_name=obj.user, gr_gid=acct.pw_gid, gr_mem=[])
                def pw(_name):
                    if state["present"]: return acct
                    raise KeyError(_name)
                def gr(_name):
                    if state["present"]: return group
                    raise KeyError(_name)
                calls = []
                def host(label, tool, *argv, **kwargs):
                    calls.append((label, argv))
                    if tool == "useradd":
                        self.assertTrue(obj.creation_attempted)
                        self.assertTrue(obj.report["creation"]["attempted"])
                        self.assertEqual(argv[argv.index("--groups") + 1], "")
                        self.assertEqual(argv[argv.index("--comment") + 1], obj.creation_marker)
                        state["present"] = True
                        if timeout:
                            obj.report["commands"].append({"label": "create-fresh-user", "status": "TIMEOUT"})
                        else:
                            obj.report["commands"].append({"label": "create-fresh-user", "exit_code": 1})
                        raise smoke.SmokeError("simulated partial useradd failure")
                    if tool == "userdel": state["present"] = False
                    return "ActiveState=inactive\nSubState=dead\n", 0
                obj.host = host
                def home_stat(_path):
                    if home_exists and state["present"]:
                        return argparse.Namespace(st_mode=stat.S_IFDIR | 0o700, st_uid=acct.pw_uid, st_gid=acct.pw_gid, st_ino=42, st_dev=1)
                    raise FileNotFoundError()
                with mock.patch.object(smoke, "checked_source", side_effect=[("/cli", "a"), ("/fixture", "b")]), \
                     mock.patch.object(smoke, "trusted_tool", side_effect=lambda name: "/usr/bin/" + name), \
                     mock.patch.object(smoke.pwd, "getpwnam", side_effect=pw), \
                     mock.patch.object(smoke.pwd, "getpwall", side_effect=lambda: [acct] if state["present"] else []), \
                     mock.patch.object(smoke.grp, "getgrnam", side_effect=gr), \
                     mock.patch.object(smoke.grp, "getgrall", side_effect=lambda: [group] if state["present"] else []), \
                     mock.patch.object(smoke.os.path, "lexists", return_value=False), \
                     mock.patch.object(smoke.os, "lstat", side_effect=home_stat), \
                     mock.patch.object(smoke.pathlib.Path, "exists", return_value=False):
                    report = obj.execute()
                self.assertEqual(report["status"], "FAIL")  # setup failure stays failure
                self.assertFalse(state["present"])
                self.assertTrue(obj.creation_verified)
                self.assertIn("remove-fresh-account-home", [label for label, _ in calls])
                self.assertEqual(report["cleanup"][0]["step"], "recover-partial-creation")
                self.assertEqual(report["cleanup"][0]["status"], "PASS")
                if timeout:
                    self.assertTrue(report["cleanup_failed"])
                    self.assertIn("creation-completion", [step["step"] for step in report["cleanup"]])

    def test_unattributed_partial_states_record_residue_and_never_delete(self):
        for kind in ("none", "group-only", "home-only", "wrong-marker", "old-uid", "wrong-home", "unsafe-home"):
            with self.subTest(kind=kind):
                obj = self.requested()
                acct = account(marker="foreign" if kind == "wrong-marker" else obj.creation_marker)
                if kind == "old-uid": acct.pw_uid = 1000
                if kind == "wrong-home": acct.pw_dir = "/home/someone-else"
                group = argparse.Namespace(gr_name=obj.user, gr_gid=acct.pw_gid, gr_mem=[])
                present = kind not in {"none", "group-only", "home-only"}
                has_group = present or kind == "group-only"
                has_home = kind in {"home-only", "unsafe-home"}
                def pw(_name):
                    if present: return acct
                    raise KeyError(_name)
                def gr(_name):
                    if has_group: return group
                    raise KeyError(_name)
                def home_stat(_path):
                    if has_home:
                        return argparse.Namespace(st_mode=stat.S_IFDIR | 0o777, st_uid=acct.pw_uid, st_gid=acct.pw_gid, st_ino=42, st_dev=1)
                    raise FileNotFoundError()
                obj.host = mock.Mock(side_effect=RuntimeError("must not delete an unattributed account"))
                with mock.patch.object(smoke.pwd, "getpwnam", side_effect=pw), \
                     mock.patch.object(smoke.pwd, "getpwall", return_value=[acct] if present else []), \
                     mock.patch.object(smoke.grp, "getgrnam", side_effect=gr), \
                     mock.patch.object(smoke.grp, "getgrall", return_value=[group] if has_group else []), \
                     mock.patch.object(smoke.os, "lstat", side_effect=home_stat), \
                     mock.patch.object(smoke.os.path, "lexists", return_value=False):
                    obj.cleanup()
                obj.host.assert_not_called()
                self.assertTrue(obj.report["cleanup_failed"])
                residual = obj.report["cleanup"][0]["residual_observation"]
                self.assertFalse(residual["attributed_to_this_run"])
                self.assertEqual(residual["account"]["exists"], present)
                self.assertEqual(residual["group"]["exists"], has_group)
                self.assertEqual(residual["home"]["exists"], has_home)

    def test_explicit_empty_supplementary_groups_are_verified(self):
        obj = self.requested(); obj.account = account()
        obj.report["fixture_user"] = {}
        with mock.patch.object(smoke.os, "getgrouplist", return_value=[23001]):
            obj.verify_groups()
        self.assertEqual(obj.report["fixture_user"]["supplementary_group_ids"], [])
        with mock.patch.object(smoke.os, "getgrouplist", return_value=[23001, 27]):
            with self.assertRaisesRegex(smoke.SmokeError, "unexpected supplementary"):
                obj.verify_groups()


class CleanupOrderTests(unittest.TestCase):
    def test_changed_file_still_stops_manager_but_preserves_account_home(self):
        obj = smoke.Smoke(args())
        obj.user = "nb1ci-0123456789ab"; obj.creation_marker = "fixture-marker"
        obj.account = account(); obj.created = obj.creation_verified = True
        obj.linger_requested = obj.manager_requested = True
        obj.managed_files = {"/unit": {"exists": True, "inode": 7}}
        obj.snapshot = lambda *_args: {"/unit": {"exists": True, "inode": 8}}
        obj.helper = mock.Mock(side_effect=RuntimeError("changed file must not be deleted"))
        calls = []
        def host(label, *_args, **_kwargs):
            self.assertTrue(obj.user_commands_closed)
            calls.append(label)
            return "ActiveState=inactive\nSubState=dead\n", 0
        obj.host = host
        with mock.patch.object(smoke.pwd, "getpwnam", return_value=obj.account), \
             mock.patch.object(smoke.pathlib.Path, "exists", return_value=False):
            obj.cleanup()
        obj.helper.assert_not_called()
        self.assertIn("terminate-own-user", calls)
        self.assertIn("stop-own-manager", calls)
        self.assertIn("stop-own-runtime", calls)
        self.assertNotIn("remove-fresh-account-home", calls)
        self.assertTrue(obj.report["cleanup_failed"])
        self.assertEqual(obj.report["cleanup"][0]["status"], "FAIL")

    def test_every_runuser_helper_finishes_before_shutdown_and_no_reentry(self):
        obj = smoke.Smoke(args())
        obj.user = "nb1ci-0123456789ab"; obj.creation_marker = "fixture-marker"
        obj.account = account(); obj.created = obj.creation_verified = True
        obj.linger_requested = obj.manager_requested = True
        obj.managed_files = {"/unit": {"exists": True, "inode": 7}}
        obj.owned_artifacts = {"/injected": {"exists": True}}
        calls, present = [], {"account": True}
        def user_read(*_args):
            self.assertFalse(obj.user_commands_closed)
            calls.append("runuser-snapshot")
            return obj.managed_files
        def user_remove(*_args):
            self.assertFalse(obj.user_commands_closed)
            calls.append("runuser-remove")
        def injected(path):
            user_remove(); del obj.owned_artifacts[path]
        def pw(_name):
            if present["account"]: return obj.account
            raise KeyError(_name)
        def host(label, tool, *argv, **kwargs):
            self.assertTrue(obj.user_commands_closed)
            calls.append(label)
            if tool == "userdel": present["account"] = False
            return "ActiveState=inactive\nSubState=dead\n", 0
        obj.snapshot = user_read; obj.helper = user_remove; obj.remove_injected = injected; obj.host = host
        with mock.patch.object(smoke.pwd, "getpwnam", side_effect=pw), \
             mock.patch.object(smoke.grp, "getgrnam", side_effect=KeyError()), \
             mock.patch.object(smoke.pathlib.Path, "exists", return_value=False):
            obj.cleanup()
        first_host = calls.index("disable-own-linger")
        self.assertTrue(all(item.startswith("runuser-") for item in calls[:first_host]))
        self.assertTrue(all(not item.startswith("runuser-") for item in calls[first_host:]))
        self.assertIn("remove-fresh-account-home", calls)
        self.assertFalse(obj.report.get("cleanup_failed", False))
        with self.assertRaisesRegex(smoke.SmokeError, "runuser is forbidden"):
            obj.user_command("late", ["must-not-execute"])

    def test_tool_accepts_canonical_system_path_and_rejects_writable_parent(self):
        protected_file = argparse.Namespace(st_mode=stat.S_IFREG | 0o755, st_uid=0)
        for parent_mode in (0o755, 0o775):
            with self.subTest(parent_mode=parent_mode), \
                 mock.patch.object(smoke.shutil, "which", return_value="/bin/systemctl"), \
                 mock.patch.object(smoke.pathlib.Path, "resolve", return_value=pathlib.Path("/usr/bin/systemctl")), \
                 mock.patch.object(smoke.os, "stat", return_value=protected_file), \
                 mock.patch.object(smoke.pathlib.Path, "lstat", return_value=argparse.Namespace(st_mode=stat.S_IFDIR | parent_mode, st_uid=0)):
                if parent_mode == 0o755:
                    self.assertEqual(smoke.trusted_tool("systemctl"), "/usr/bin/systemctl")
                else:
                    with self.assertRaisesRegex(smoke.SmokeError, "parent"):
                        smoke.trusted_tool("systemctl")


class UserHelperFilesystemTests(unittest.TestCase):
    """Fresh temporary fake HOME only; no accounts, manager or native process."""
    def helper(self, home, *args, success=True):
        result = subprocess.run([sys.executable, "-c", smoke.USER_HELPER, *args], capture_output=True,
                                env={"HOME": str(home), "PATH": "/usr/bin:/bin"}, check=False)
        if success:
            self.assertEqual(result.returncode, 0, result.stderr.decode())
            return json.loads(result.stdout)
        self.assertNotEqual(result.returncode, 0)
        return result

    def test_exact_link_and_changed_inode_preserve_unknown_replacement(self):
        with tempfile.TemporaryDirectory() as tmp:
            home = pathlib.Path(tmp).resolve()
            unit, link = home / "unit", home / "wants/nb1.service"
            original = self.helper(home, "create", str(unit), b"fixture-unit".hex())
            linked = self.helper(home, "link", str(link), str(unit))
            self.assertEqual(linked["target"], str(unit))
            replacement = home / "replacement"
            replacement.write_bytes(b"fixture-unit")
            os.replace(replacement, unit)
            self.helper(home, "remove", str(unit), json.dumps(original), success=False)
            self.assertEqual(unit.read_bytes(), b"fixture-unit")
            self.helper(home, "remove", str(link), json.dumps(linked))
            self.assertFalse(link.exists())

    def test_need_reload_setup_preserves_bytes_and_inode(self):
        with tempfile.TemporaryDirectory() as tmp:
            home = pathlib.Path(tmp).resolve()
            name = home / "unit"
            original = self.helper(home, "create", str(name), b"fixture-unit".hex())
            before = name.stat()
            touched = self.helper(home, "touch", str(name), json.dumps(original))
            self.assertEqual(touched["snapshot"], original)
            self.assertNotEqual(name.stat().st_mtime_ns, before.st_mtime_ns)
            restored = self.helper(home, "restore-time", str(name), json.dumps(touched["snapshot"]),
                                   str(touched["atime_ns"]), str(touched["mtime_ns"]))
            self.assertEqual(restored, original)
            self.assertEqual(name.stat().st_mtime_ns, before.st_mtime_ns)

    def test_outside_dotdot_and_symlinked_parent_are_rejected(self):
        with tempfile.TemporaryDirectory() as tmp, tempfile.TemporaryDirectory() as outside:
            home = pathlib.Path(tmp).resolve()
            (home / "escape").symlink_to(outside, target_is_directory=True)
            for name in (pathlib.Path(outside) / "unknown", home / ".." / "unknown", home / "escape" / "unknown"):
                self.helper(home, "create", str(name), b"must-not-write".hex(), success=False)
            self.assertFalse((pathlib.Path(outside) / "unknown").exists())


if __name__ == "__main__":
    unittest.main()
