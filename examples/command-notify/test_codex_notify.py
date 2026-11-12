"""Codex notify argv/privacy checks and current CLI against synthetic HTTP."""

import contextlib
import http.server
import io
import json
import os
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest
from unittest.mock import patch

import codex_notify as adapter
import notify as common
from test_cli_fixture import BINARY, Fixture, fixture_server


PROGRAM = [sys.executable, str(Path(__file__).with_name("codex_notify.py"))]
EVENT = {"type": "agent-turn-complete", "thread-id": "private-thread-913b9a",
         "turn-id": "private-turn-fc28de", "cwd": "/private/codex-fixture-worktree",
         "input-messages": ["private input; $(never-executed)"],
         "last-assistant-message": "private assistant message; $(never-executed)"}


class ReceiptFixture(Fixture):
    def do_POST(self):
        # Observe the actual disk receipt before the real CLI's HTTP admission.
        paths = list(self.server.report_dir.glob("codex-notify-*.json"))
        self.server.receipts_at_post.append([json.loads(path.read_text()) for path in paths])
        super().do_POST()


class CodexNotifyTests(unittest.TestCase):
    def setUp(self):
        self.directory = tempfile.TemporaryDirectory()
        self.addCleanup(self.directory.cleanup)
        self.root = Path(self.directory.name)
        self.reports = self.root / "reports"
        self.reports.mkdir(mode=0o700)
        config = self.root / "setting.yaml"
        config.write_text("{}\n")
        self.env = dict(os.environ, NB1_CONFIG=str(config))

    def invoke(self, args=(), raw=None):
        argv = PROGRAM + list(args) + ([] if raw is None else [raw])
        return subprocess.run(argv, stdin=subprocess.DEVNULL, capture_output=True, text=True,
                              timeout=8, env=self.env, shell=False)

    def options(self, endpoint="http://127.0.0.1:8932", binary=sys.executable):
        return ["--execute", "--nb1-binary", str(Path(binary).resolve()),
                "--endpoint", endpoint, "--serial", "test-serial", "--report-dir", str(self.reports)]

    def assert_private(self, text):
        for field in ("thread-id", "turn-id", "cwd", "last-assistant-message"):
            self.assertNotIn(EVENT[field], text)
        self.assertNotIn(EVENT["input-messages"][0], text)

    def test_preview_uses_single_argv_without_reading_open_stdin(self):
        process = subprocess.Popen(PROGRAM + [json.dumps(EVENT)], stdin=subprocess.PIPE,
                                   stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True, env=self.env)
        try:
            # Keep stdin open: a Stop-style stdin parser would block here.
            self.assertEqual(process.wait(timeout=2), 0)
            stdout, stderr = process.communicate(timeout=1)
        finally:
            if process.poll() is None:
                process.kill()
                process.communicate()
        self.assertEqual(stdout, "")
        self.assertIn("preview", stderr)
        self.assertIn("turn-ended", stderr)
        self.assert_private(stderr)
        self.assertEqual(list(self.reports.iterdir()), [])

    def test_non_target_and_malformed_events_ignore_without_side_effects(self):
        invalid = [None, "", "{", "[]", "null", '"event"', "[" * 1500,
                   json.dumps(dict(EVENT, type="approval-requested")),
                   json.dumps({"hook_event_name": "Stop", "session_id": "session", "turn_id": "turn"})]
        for field in ("thread-id", "turn-id"):
            for value in (None, "", "  ", 123, "x" * (adapter.MAX_ID_BYTES + 1), "é" * 129):
                invalid.append(json.dumps(dict(EVENT, **{field: value})))
        with patch.object(adapter, "notify") as notify, patch.object(adapter, "save_report") as save:
            for raw in invalid:
                with self.subTest(raw=str(raw)[:45]), contextlib.redirect_stdout(io.StringIO()) as out, contextlib.redirect_stderr(io.StringIO()) as err:
                    self.assertEqual(adapter.main([] if raw is None else [raw]), 0)
                    self.assertEqual(out.getvalue() + err.getvalue(), "")
        notify.assert_not_called()
        save.assert_not_called()

    def test_event_and_id_byte_limits_and_hash_identity(self):
        event = dict(EVENT, **{"thread-id": "é" * 128, "turn-id": "t" * 256, "padding": ""})
        raw = json.dumps(event, ensure_ascii=False)
        event["padding"] = "x" * (adapter.MAX_EVENT_BYTES - len(raw.encode("utf-8")))
        raw = json.dumps(event, ensure_ascii=False)
        self.assertEqual(len(raw.encode("utf-8")), adapter.MAX_EVENT_BYTES)
        self.assertRegex(adapter.event_key(raw), r"^[0-9a-f]{64}$")
        self.assertIsNone(adapter.event_key(raw + " "))
        self.assertIsNone(adapter.event_key(json.dumps(dict(EVENT, **{"turn-id": "\ud800"}))))
        key = adapter.event_key(json.dumps(EVENT))
        self.assertEqual(key, adapter.event_key(json.dumps(dict(EVENT, cwd="different", **{"last-assistant-message": "different"}))))
        self.assertNotEqual(key, adapter.event_key(json.dumps(dict(EVENT, **{"turn-id": "another-turn"}))))
        self.assertNotEqual(adapter.event_key(json.dumps(dict(EVENT, **{"thread-id": "a,b", "turn-id": "c"}))),
                            adapter.event_key(json.dumps(dict(EVENT, **{"thread-id": "a", "turn-id": "b,c"}))))

    def test_execute_requires_explicit_scoped_options_and_sanitizes_arg_errors(self):
        options = self.options()
        changes = {"--nb1-binary": ["nb1", str(self.root / "missing-nb1")],
                   "--endpoint": ["http://127.0.0.1", "http://127.0.0.1:0", "http://127.0.0.1:65536",
                                  "https://127.0.0.1:8932", "http://example.com:8932", "http://localhost:8932",
                                  "http://127.0.0.1:8932/path", "http://user@127.0.0.1:8932"],
                   "--serial": ["", "../serial", "x" * 129],
                   "--report-dir": ["reports", str(self.root / "missing"), str(self.root / "setting.yaml")]}
        cases = [["--execute"]]
        for flag, values in changes.items():
            for value in values:
                case = options[:]
                case[case.index(flag) + 1] = value
                cases.append(case)
        with patch.object(adapter, "notify") as notify, patch.object(adapter, "save_report") as save:
            for args in cases:
                with self.subTest(args=args), contextlib.redirect_stdout(io.StringIO()) as out, contextlib.redirect_stderr(io.StringIO()) as err:
                    self.assertEqual(adapter.main(args + [json.dumps(EVENT)]), 1)
                    self.assertEqual(out.getvalue(), "")
                    self.assert_private(err.getvalue())
            with contextlib.redirect_stderr(io.StringIO()) as err:
                self.assertEqual(adapter.main([json.dumps(EVENT), json.dumps(EVENT)]), 1)
                self.assert_private(err.getvalue())
        notify.assert_not_called()
        save.assert_not_called()
        for endpoint in ("http://127.0.0.1:8932/", "http://[::1]:8932"):
            self.assertEqual(adapter.parse_args(self.options(endpoint)).notify_timeout, 4)

    @unittest.skipUnless(os.name == "posix", "POSIX private directory mode check")
    def test_execute_rejects_public_or_symlink_report_directory(self):
        self.reports.chmod(0o755)
        result = self.invoke(self.options(), json.dumps(EVENT))
        self.assertEqual((result.returncode, result.stdout), (1, ""))
        self.reports.chmod(0o700)
        link = self.root / "report-link"
        link.symlink_to(self.reports, target_is_directory=True)
        args = self.options()
        args[args.index("--report-dir") + 1] = str(link)
        self.assertEqual(self.invoke(args, json.dumps(EVENT)).returncode, 1)

    def test_durable_job_id_write_failure_cannot_launch_cli(self):
        with patch.object(common, "save_report", side_effect=OSError("simulated disk failure")), patch.object(common.subprocess, "run") as run, contextlib.redirect_stderr(io.StringIO()) as err, contextlib.redirect_stdout(io.StringIO()) as out:
            self.assertEqual(adapter.main(self.options() + [json.dumps(EVENT)]), 0)
        run.assert_not_called()
        self.assertEqual(out.getvalue(), "")
        path, = self.reports.iterdir()
        report = json.loads(path.read_text())
        self.assertEqual(report["notification"]["state"], "not_submitted")
        self.assertIn("job_id", report["notification"])
        self.assertIn("simulated disk failure", report["notification"]["error"])
        self.assert_private(path.read_text() + err.getvalue())

    @unittest.skipUnless(BINARY, "set NB1_NOTIFY_TEST_BINARY to a current nb1 executable")
    def test_real_cli_success_unknown_privacy_and_duplicate_receipt(self):
        for mode in ("success", "unknown"):
            with self.subTest(mode=mode):
                server, thread = fixture_server(mode)
                server.RequestHandlerClass = ReceiptFixture
                server.report_dir, server.receipts_at_post = self.reports, []
                args = self.options("http://127.0.0.1:" + str(server.server_port), BINARY)
                try:
                    result = self.invoke(args, json.dumps(EVENT))
                    self.assertEqual((result.returncode, result.stdout), (0, ""), result.stderr)
                    path, = self.reports.iterdir()
                    self.assertRegex(path.name, r"^codex-notify-[0-9a-f]{64}\.json$")
                    text = path.read_text()
                    report = json.loads(text)
                    notification = report["notification"]
                    self.assertEqual(report["trigger"], "codex_notify")
                    self.assertEqual(report["meaning"], "turn_ended")
                    self.assertEqual(notification["state"], "succeeded" if mode == "success" else "unknown")
                    self.assertEqual(notification["job_id"], server.submission["id"])
                    self.assertEqual(server.submission["action"], common.action("#365f9f", 1500))
                    self.assertEqual(server.posts, ["/v1/jobs"])
                    saved_before_post, = server.receipts_at_post[0]
                    self.assertEqual(saved_before_post["notification"]["job_id"], server.submission["id"])
                    self.assertEqual(saved_before_post["notification"]["input"], {"action": server.submission["action"]})
                    self.assertTrue(saved_before_post["notification"]["outcome_unknown"])
                    self.assert_private(text + result.stderr)
                    if os.name == "posix":
                        self.assertEqual(path.stat().st_mode & 0o777, 0o600)
                    counts = (len(server.posts), len(server.gets))
                    duplicate = self.invoke(args, json.dumps(dict(EVENT, **{"last-assistant-message": "changed private text"})))
                    self.assertEqual((duplicate.returncode, duplicate.stdout), (0, ""))
                    self.assertEqual(path.read_text(), text)
                    self.assertEqual((len(server.posts), len(server.gets)), counts)
                    if mode == "unknown":
                        self.assertTrue(notification["outcome_unknown"])
                        self.assertEqual(notification["final_off"], "not_confirmed")
                        lookup = subprocess.run(notification["job_lookup_argv"], capture_output=True, text=True,
                                                timeout=6, env=self.env, check=True, shell=False)
                        self.assertEqual(json.loads(lookup.stdout), server.job)
                        self.assertEqual(server.posts, ["/v1/jobs"])
                    path.unlink()
                finally:
                    server.shutdown()
                    server.server_close()
                    thread.join()

    @unittest.skipUnless(BINARY, "set NB1_NOTIFY_TEST_BINARY to a current nb1 executable")
    def test_concurrent_duplicate_events_have_only_one_post_even_when_unknown(self):
        server, thread = fixture_server("unknown")
        args = self.options("http://127.0.0.1:" + str(server.server_port), BINARY)
        processes = []
        try:
            for _ in range(6):
                processes.append(subprocess.Popen(PROGRAM + args + [json.dumps(EVENT)], stdin=subprocess.DEVNULL,
                    stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True, env=self.env))
            for process in processes:
                stdout, stderr = process.communicate(timeout=8)
                self.assertEqual((process.returncode, stdout), (0, ""), stderr)
            path, = self.reports.iterdir()
            notification = json.loads(path.read_text())["notification"]
            self.assertEqual(notification["state"], "unknown")
            self.assertEqual(notification["job_id"], server.submission["id"])
            self.assertEqual(server.posts, ["/v1/jobs"])
            self.assertEqual(server.gets, [])
        finally:
            for process in processes:
                if process.poll() is None:
                    process.kill()
                    process.communicate()
            server.shutdown()
            server.server_close()
            thread.join()

    @unittest.skipUnless(BINARY and os.name == "posix" and os.geteuid() != 0,
                         "real CLI and ordinary POSIX user needed for unwritable directory check")
    def test_unwritable_receipt_directory_does_not_post_or_fail_the_task(self):
        server, thread = fixture_server("success")
        try:
            self.reports.chmod(0o500)
            result = self.invoke(self.options("http://127.0.0.1:" + str(server.server_port), BINARY), json.dumps(EVENT))
            self.assertEqual((result.returncode, result.stdout), (0, ""))
            self.assertIn("cannot create report", result.stderr)
            self.assertEqual(server.posts + server.gets, [])
            self.assertEqual(list(self.reports.iterdir()), [])
        finally:
            self.reports.chmod(0o700)
            server.shutdown()
            server.server_close()
            thread.join()


if __name__ == "__main__":
    unittest.main()
