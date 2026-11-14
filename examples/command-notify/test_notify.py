"""Notification control-flow tests; every subprocess call is mocked."""
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


class FakeCLI:
    def __init__(self, report, child_exit=0, mode="success"):
        self.report, self.child_exit, self.mode = report, child_exit, mode
        self.calls, self.inputs = [], []

    def __call__(self, argv, **kwargs):
        self.calls.append(argv[:])
        assert kwargs["shell"] is False
        if argv[0] != "fake nb1 executable":
            return subprocess.CompletedProcess(argv, self.child_exit)
        assert argv[1:4] == ["control", "--endpoint", "http://127.0.0.1:8932"]
        assert argv[4] == "--timeout" and 0 < float(argv[5][:-1]) <= 10
        assert argv[6:8] == ["run", "test-serial"]
        assert argv[9] == "--id" and argv[11:] == ["--wait"]
        payload = json.loads(Path(argv[8]).read_text())
        self.inputs.append(payload)
        saved = json.loads(self.report.read_text())["notification"]
        assert saved["job_id"] == argv[10]
        assert saved["input"] == payload
        assert saved["outcome_unknown"] is True
        if self.mode == "timeout":
            raise subprocess.TimeoutExpired(argv, .01)
        if self.mode in ("unknown", "rejected"):
            return subprocess.CompletedProcess(argv, 1, "", self.mode)
        if self.mode == "bad_output":
            return subprocess.CompletedProcess(argv, 0, '{"status":"succeeded"}', "")
        job = {"id": argv[10], "serial": "test-serial", "status": "succeeded"}
        if self.mode in ("failed", "cancelled", "completed"):
            job["status"] = self.mode
        if self.mode == "wrong_serial":
            job["serial"] = "another-device"
        if self.mode == "wrong_id":
            job["id"] = "another-job"
        return subprocess.CompletedProcess(argv, 1 if self.mode in ("failed", "cancelled") else 0,
                                           json.dumps(job), "")


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
            code = notify.main(argv)
        return code, json.loads(self.report.read_text()), cli, command

    def test_default_dry_run_runs_neither_child_nor_cli(self):
        with patch.object(notify.subprocess, "run") as runner, contextlib.redirect_stdout(io.StringIO()) as output:
            self.assertEqual(notify.main(["--serial", "test-serial", "--", "do-not-run"]), 0)
        runner.assert_not_called()
        value = json.loads(output.getvalue())
        self.assertEqual(value["mode"], "dry_run")
        self.assertEqual(value["notification"]["success_input"]["action"]["on_finish"], "off")
        self.assertFalse(self.report.exists())

    def test_success_submits_one_action_with_durable_id_and_final_off(self):
        code, report, cli, command = self.execute()
        self.assertEqual(code, 0)
        self.assertEqual(cli.calls[0], command)
        self.assertEqual(len(cli.calls), 2)
        self.assertEqual(cli.inputs, [{"action": notify.action("#00a000", 1500)}])
        notification = report["notification"]
        self.assertEqual(notification["state"], "succeeded")
        self.assertEqual(notification["final_off"], "daemon_job_confirmed")
        self.assertEqual(notification["job_lookup_argv"][-2:], ["job", notification["job_id"]])

    def test_child_failure_preserves_exit_code_and_uses_red(self):
        code, report, cli, _ = self.execute(child_exit=7)
        self.assertEqual(code, 7)
        self.assertEqual(report["command"]["exit_code"], 7)
        self.assertEqual(cli.inputs[0]["action"]["color"], "#b40000")

    def test_timeout_unknown_invalid_or_rejected_response_never_resubmits(self):
        for mode in ("timeout", "unknown", "bad_output", "rejected"):
            with self.subTest(mode=mode):
                self.report.unlink(missing_ok=True)
                code, report, cli, _ = self.execute(mode)
                self.assertEqual(code, 0)
                self.assertEqual(report["notification"]["state"], "unknown")
                self.assertTrue(report["notification"]["outcome_unknown"])
                self.assertEqual(report["notification"]["final_off"], "not_confirmed")
                self.assertEqual(len(cli.calls), 2)
                self.assertEqual(len(cli.inputs), 1)

    def test_notification_failure_does_not_change_failed_command_exit(self):
        code, report, cli, _ = self.execute("rejected", child_exit=4)
        self.assertEqual(code, 4)
        self.assertEqual(len(cli.inputs), 1)
        self.assertEqual(report["notification"]["state"], "unknown")

    def test_wrong_id_serial_or_nonterminal_job_does_not_confirm_off(self):
        for mode in ("wrong_id", "wrong_serial", "completed"):
            with self.subTest(mode=mode):
                self.report.unlink(missing_ok=True)
                _, report, cli, _ = self.execute(mode)
                self.assertEqual(report["notification"]["state"], "unknown")
                self.assertEqual(report["notification"]["final_off"], "not_confirmed")
                self.assertEqual(len(cli.inputs), 1)

    def test_terminal_failure_and_cancellation_do_not_confirm_off(self):
        for mode in ("failed", "cancelled"):
            with self.subTest(mode=mode):
                self.report.unlink(missing_ok=True)
                code, report, _, _ = self.execute(mode)
                self.assertEqual(code, 0)
                self.assertEqual(report["notification"]["state"], mode)
                self.assertFalse(report["notification"]["outcome_unknown"])
                self.assertEqual(report["notification"]["final_off"], "not_confirmed")

    def test_existing_report_is_never_overwritten_or_used_for_new_command(self):
        self.report.write_text("original unknown Job")
        with patch.object(notify.subprocess, "run") as runner, contextlib.redirect_stderr(io.StringIO()):
            code = notify.main(["--execute", "--serial", "test-serial", "--report", str(self.report), "--", "child"])
        self.assertEqual(code, 2)
        runner.assert_not_called()
        self.assertEqual(self.report.read_text(), "original unknown Job")

    def test_cannot_submit_if_job_id_report_cannot_be_saved(self):
        cli = FakeCLI(self.report)
        original = notify.save_report
        def save(path, report, create=False):
            if not create:
                raise OSError("disk full")
            return original(path, report, create)
        with patch.object(notify.subprocess, "run", cli), patch.object(notify, "save_report", save), contextlib.redirect_stderr(io.StringIO()):
            code = notify.main(["--execute", "--nb1-binary", "fake nb1 executable", "--serial", "test-serial",
                                "--report", str(self.report), "--", "child"])
        self.assertEqual(code, 0)
        self.assertEqual(len(cli.calls), 1)
        self.assertEqual(cli.inputs, [])


if __name__ == "__main__":
    unittest.main()
