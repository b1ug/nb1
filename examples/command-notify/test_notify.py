"""Fake CLI contract tests; no USB, daemon or child command is started."""
import contextlib
import importlib.util
import io
import json
from pathlib import Path
import subprocess
import tempfile
import unittest
from unittest.mock import patch

SPEC = importlib.util.spec_from_file_location("nb1_notify", Path(__file__).with_name("notify.py"))
notify = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(notify)

BINDING = {"request_id": "req_60000_" + "a" * 32,
           "expected_daemon_instance_id": "daemon_" + "b" * 32}


class FakeCLI:
    def __init__(self, report, child_exit=0, mode="success"):
        self.report, self.child_exit, self.mode = report, child_exit, mode
        self.calls, self.programs = [], []

    def __call__(self, argv, **kwargs):
        self.calls.append(argv[:])
        assert kwargs["shell"] is False
        if argv[0] != "fake nb1 executable":
            return subprocess.CompletedProcess(argv, self.child_exit)
        assert argv[1:6] == ["control", "--mode", "daemon", "--endpoint", "http://127.0.0.1:8932"]
        assert argv[6] == "--timeout" and argv[7].endswith("s")
        assert 0 < float(argv[7][:-1]) <= 10
        if argv[8:] == ["prepare"]:
            if self.mode == "prepare_failure":
                return subprocess.CompletedProcess(argv, 1, "", "daemon unavailable")
            return subprocess.CompletedProcess(argv, 0, json.dumps(BINDING), "")
        assert argv[8:10] == ["run", "test-serial"]
        assert argv[11:] == ["--policy", "reject", "--request-id", BINDING["request_id"],
                            "--expected-daemon-instance-id", BINDING["expected_daemon_instance_id"], "--wait"]
        payload = json.loads(Path(argv[10]).read_text())
        self.programs.append(payload)
        saved = json.loads(self.report.read_text())
        assert saved["notification"]["binding"] == BINDING
        assert saved["notification"]["program"] == payload
        assert saved["notification"]["outcome_unknown"] is True
        if self.mode == "timeout":
            raise subprocess.TimeoutExpired(argv, 0.01)
        if self.mode == "unknown":
            return subprocess.CompletedProcess(argv, 1, "", "result unknown; do not automatically replace the binding")
        if self.mode == "bad_output":
            return subprocess.CompletedProcess(argv, 0, '{"status":"succeeded"}', "")
        job = {"id": "job-1", "device": "test-serial", "status": "succeeded", "program": payload}
        if self.mode == "job_failed":
            job.update(status="failed", error="cleanup/readback failed")
        if self.mode == "undefined_terminal":
            job["status"] = "completed"
        if self.mode == "wrong_device":
            job["device"] = "another-device"
        if self.mode == "wrong_program":
            job["program"] = notify.program("#ffffff", 1500)
        return subprocess.CompletedProcess(argv, 1 if self.mode == "job_failed" else 0, json.dumps(job), "")


class NotifyTests(unittest.TestCase):
    def setUp(self):
        self.directory = tempfile.TemporaryDirectory()
        self.addCleanup(self.directory.cleanup)
        self.report = Path(self.directory.name) / "report with spaces.json"

    def execute(self, mode="success", child_exit=0):
        cli = FakeCLI(self.report, child_exit, mode)
        command = ["child command", "literal; $(never-run)", "space argument", "--flag"]
        argv = ["--execute", "--nb1-binary", "fake nb1 executable", "--endpoint", "http://127.0.0.1:8932",
                "--serial", "test-serial", "--report", str(self.report), "--"] + command
        with patch.object(notify.subprocess, "run", cli), contextlib.redirect_stderr(io.StringIO()):
            exit_code = notify.main(argv)
        return exit_code, json.loads(self.report.read_text()), cli, command

    def test_default_dry_run_runs_neither_child_nor_cli(self):
        with patch.object(notify.subprocess, "run") as runner, contextlib.redirect_stdout(io.StringIO()) as output:
            code = notify.main(["--serial", "test-serial", "--", "do-not-run"])
        runner.assert_not_called()
        self.assertEqual(code, 0)
        self.assertEqual(json.loads(output.getvalue())["mode"], "dry_run")
        self.assertFalse(self.report.exists())

    def test_success_uses_real_cli_shape_one_bound_program_and_final_off(self):
        code, report, cli, command = self.execute()
        self.assertEqual(code, 0)
        self.assertEqual(cli.calls[0], command)
        self.assertEqual(len(cli.calls), 3)  # child, prepare, one run
        self.assertEqual(report["notification"]["state"], "succeeded")
        self.assertEqual(report["notification"]["final_off"], "daemon_job_confirmed")
        self.assertEqual(cli.programs[0], notify.program("#00a000", 1500))
        self.assertEqual(cli.programs[0]["steps"][-1]["color"], "#000000")
        self.assertEqual(cli.programs[0]["on_finish"], "off")

    def test_child_failure_is_red_and_preserves_exit_code(self):
        code, report, cli, _ = self.execute(child_exit=7)
        self.assertEqual(code, 7)
        self.assertEqual(report["command"]["exit_code"], 7)
        self.assertEqual(report["notification"]["state"], "succeeded")
        self.assertEqual(cli.programs[0], notify.program("#b40000", 1500))

    def test_timeout_unknown_and_invalid_output_never_resubmit_or_replace_binding(self):
        for mode in ("timeout", "unknown", "bad_output"):
            with self.subTest(mode=mode):
                if self.report.exists():
                    self.report.unlink()
                code, report, cli, _ = self.execute(mode)
                self.assertEqual(code, 0)
                notification = report["notification"]
                self.assertEqual(notification["state"], "unknown")
                self.assertTrue(notification["outcome_unknown"])
                self.assertEqual(notification["binding"], BINDING)
                self.assertEqual(notification["final_off"], "not_confirmed")
                self.assertEqual(len(cli.calls), 3)
                self.assertEqual(len(cli.programs), 1)
                self.assertIn(BINDING["request_id"], notification["receipt_lookup_argv"])
                self.assertIn(BINDING["expected_daemon_instance_id"], notification["receipt_lookup_argv"])

    def test_prepare_failure_never_submits_and_does_not_change_child_exit(self):
        code, report, cli, _ = self.execute("prepare_failure", child_exit=4)
        self.assertEqual(code, 4)
        self.assertEqual(report["notification"]["state"], "not_submitted")
        self.assertFalse(report["notification"]["outcome_unknown"])
        self.assertEqual(len(cli.calls), 2)

    def test_unknown_terminal_or_mismatched_job_does_not_confirm_off(self):
        for mode in ("undefined_terminal", "wrong_device", "wrong_program"):
            with self.subTest(mode=mode):
                if self.report.exists():
                    self.report.unlink()
                _, report, cli, _ = self.execute(mode)
                notification = report["notification"]
                self.assertEqual(notification["state"], "unknown")
                self.assertTrue(notification["outcome_unknown"])
                self.assertEqual(notification["binding"], BINDING)
                self.assertEqual(notification["final_off"], "not_confirmed")
                self.assertEqual(len(cli.programs), 1)
                self.assertEqual(len(cli.calls), 3)

    def test_terminal_failure_does_not_claim_confirmed_off(self):
        code, report, cli, _ = self.execute("job_failed")
        self.assertEqual(code, 0)
        self.assertEqual(report["notification"]["state"], "failed")
        self.assertEqual(report["notification"]["final_off"], "not_confirmed")
        self.assertFalse(report["notification"]["outcome_unknown"])
        self.assertEqual(len(cli.calls), 3)

    def test_existing_report_is_never_overwritten_or_used_for_new_command(self):
        self.report.write_text("original unknown binding")
        code, _, cli, _ = self.execute_existing()
        self.assertEqual(code, 2)
        self.assertEqual(self.report.read_text(), "original unknown binding")
        self.assertEqual(cli.calls, [])

    def execute_existing(self):
        cli = FakeCLI(self.report)
        with patch.object(notify.subprocess, "run", cli), contextlib.redirect_stderr(io.StringIO()):
            code = notify.main(["--execute", "--serial", "test-serial", "--report", str(self.report), "--", "do-not-run"])
        return code, None, cli, None

    def test_cannot_submit_if_prepared_binding_report_cannot_be_saved(self):
        cli = FakeCLI(self.report)
        real_save = notify.save_report
        def save(path, report, create=False):
            if not create:
                raise OSError("disk full")
            return real_save(path, report, create)
        with patch.object(notify.subprocess, "run", cli), patch.object(notify, "save_report", save), contextlib.redirect_stderr(io.StringIO()):
            code = notify.main(["--execute", "--nb1-binary", "fake nb1 executable", "--endpoint", "http://127.0.0.1:8932", "--serial", "test-serial", "--report", str(self.report), "--", "child"])
        self.assertEqual(code, 0)
        self.assertEqual(len(cli.calls), 2)
        self.assertEqual(cli.programs, [])


if __name__ == "__main__":
    unittest.main()
