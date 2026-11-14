"""Feed synthetic Codex events to the hook and use the real nb1 CLI."""

import json
from pathlib import Path
import re
import subprocess
import sys
import tempfile
import unittest

from test_cli_fixture import BINARY, fixture_server


HOOK = [sys.executable, str(Path(__file__).with_name("codex_stop.py"))]
EVENT = {
    "hook_event_name": "Stop",
    "session_id": "synthetic-session",
    "turn_id": "synthetic-turn",
    "stop_hook_active": False,
    "last_assistant_message": "Private fixture text; $(not-executed)",
    "transcript_path": "/fixture/private-transcript.jsonl",
}


def invoke(args, raw):
    return subprocess.run(HOOK + args, input=raw, capture_output=True, text=True, timeout=8)


class CodexStopTests(unittest.TestCase):
    def test_preview_and_malformed_input_return_empty_json_object(self):
        preview = invoke([], json.dumps(EVENT))
        self.assertEqual((preview.returncode, preview.stdout), (0, "{}\n"))
        self.assertIn("preview", preview.stderr)
        self.assertNotIn(EVENT["last_assistant_message"], preview.stderr)
        malformed = invoke([], "{")
        self.assertEqual((malformed.returncode, malformed.stdout, malformed.stderr), (0, "{}\n", ""))
        bad_config = invoke(["--execute"], json.dumps(EVENT))
        self.assertEqual(bad_config.returncode, 1)
        self.assertEqual(bad_config.stdout, "")
        help_result = invoke(["--help"], "")
        self.assertEqual(help_result.returncode, 0)
        self.assertNotIn("{}\n", help_result.stdout)

    @unittest.skipUnless(BINARY, "set NB1_NOTIFY_TEST_BINARY to a current nb1 executable")
    def test_initial_stop_submits_once_and_original_job_recovers_lost_response(self):
        for mode in ("success", "unknown"):
            with self.subTest(mode=mode), tempfile.TemporaryDirectory() as directory:
                server, thread = fixture_server(mode)
                args = ["--execute", "--nb1-binary", str(Path(BINARY).resolve()),
                        "--endpoint", "http://127.0.0.1:{}".format(server.server_port),
                        "--serial", "test-serial", "--report-dir", directory]
                try:
                    continued = dict(EVENT, stop_hook_active=True)
                    skipped = invoke(args, json.dumps(continued))
                    self.assertEqual((skipped.returncode, skipped.stdout), (0, "{}\n"))
                    self.assertEqual(server.posts + server.gets, [])
                    self.assertEqual(list(Path(directory).iterdir()), [])

                    result = invoke(args, json.dumps(EVENT))
                    self.assertEqual((result.returncode, result.stdout), (0, "{}\n"))
                    report_path, = Path(directory).iterdir()
                    self.assertRegex(report_path.name, re.compile(r"^codex-stop-[0-9a-f]{64}\.json$"))
                    report_text = report_path.read_text(encoding="utf-8")
                    report = json.loads(report_text)
                    notification = report["notification"]
                    self.assertEqual(report["trigger"], "codex_stop")
                    self.assertEqual(notification["state"], "succeeded" if mode == "success" else "unknown")
                    self.assertEqual(notification["job_id"], server.submission["id"])
                    self.assertEqual(server.submission["action"]["color"], "#365f9f")
                    self.assertEqual(server.submission["action"]["on_ms"], 1500)
                    self.assertEqual(server.submission["action"]["on_finish"], "off")
                    for field in ("session_id", "turn_id", "last_assistant_message", "transcript_path"):
                        self.assertNotIn(EVENT[field], report_text + result.stderr)

                    request_count = len(server.posts) + len(server.gets)
                    replay = invoke(args, json.dumps(EVENT))
                    self.assertEqual((replay.returncode, replay.stdout), (0, "{}\n"))
                    self.assertEqual(report_path.read_text(encoding="utf-8"), report_text)
                    self.assertEqual(len(server.posts) + len(server.gets), request_count)
                    self.assertEqual(server.posts, ["/v1/jobs"])

                    if mode == "unknown":
                        lookup = subprocess.run(notification["job_lookup_argv"], capture_output=True,
                                                text=True, timeout=8, check=True)
                        recovered = json.loads(lookup.stdout)
                        for field, expected in server.job.items():
                            self.assertEqual(recovered[field], expected)
                        self.assertEqual(len(server.posts), 1)
                finally:
                    server.shutdown()
                    server.server_close()
                    thread.join()
