"""Current real CLI against synthetic HTTP; no daemon or USB is started."""
import http.server
import json
import os
from pathlib import Path
import socket
import subprocess
import sys
import tempfile
import threading
import time
import unittest

BINARY = os.environ.get("NB1_NOTIFY_TEST_BINARY")


class Fixture(http.server.BaseHTTPRequestHandler):
    def log_message(self, *_):
        pass

    def send_json(self, value, status=200):
        body = json.dumps(value).encode()
        self.send_response(status)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        try:
            self.wfile.write(body)
        except (BrokenPipeError, ConnectionResetError):
            pass

    def do_GET(self):
        self.server.gets.append(self.path)
        if self.server.job and self.path == "/v1/jobs/" + self.server.job["id"]:
            self.send_json(self.server.job)
        else:
            self.send_json({"error": {"code": "not_found", "message": self.path}}, 404)

    def do_POST(self):
        self.server.posts.append(self.path)
        if self.path != "/v1/jobs":
            self.send_json({"error": {"code": "unexpected", "message": self.path}}, 404)
            return
        payload = json.loads(self.rfile.read(int(self.headers["Content-Length"])))
        self.server.submission = payload
        if set(payload) != {"id", "serial", "action"} or payload["serial"] != "test-serial":
            self.send_json({"error": {"code": "invalid_request", "message": "unexpected input"}}, 400)
            return
        if self.server.mode in ("busy", "offline"):
            self.send_json({"error": {"code": "device_busy" if self.server.mode == "busy" else "device_offline",
                                      "message": self.server.mode}}, 409 if self.server.mode == "busy" else 503)
            return
        status = self.server.mode if self.server.mode in ("failed", "cancelled") else "succeeded"
        self.server.job = {"id": payload["id"], "serial": payload["serial"], "status": status,
                           "total_steps": 2, "completed_steps": 2,
                           "created_at": "2026-11-11T10:00:00Z", "started_at": "2026-11-11T10:00:00Z",
                           "finished_at": "2026-11-11T10:00:02Z"}
        if status == "failed":
            self.server.job["error"] = "fixture cleanup failed"
        if self.server.mode == "unknown":
            self.connection.shutdown(socket.SHUT_RDWR)
            self.connection.close()
            return
        if self.server.mode == "timeout":
            time.sleep(.3)
        if self.server.mode == "invalid":
            self.send_json(dict(self.server.job, serial="wrong-device"), status=202)
            return
        initial = dict(self.server.job, status="running", completed_steps=0)
        initial.pop("finished_at", None)
        self.send_json(initial, status=202)


def fixture_server(mode):
    server = http.server.ThreadingHTTPServer(("127.0.0.1", 0), Fixture)
    server.mode, server.posts, server.gets = mode, [], []
    server.submission, server.job = {}, None
    thread = threading.Thread(target=server.serve_forever, daemon=True)
    thread.start()
    return server, thread


@unittest.skipUnless(BINARY, "set NB1_NOTIFY_TEST_BINARY to a current nb1 executable")
class RealCLIFixtureTests(unittest.TestCase):
    def invoke(self, mode, child_exit=0):
        server, thread = fixture_server(mode)
        with tempfile.TemporaryDirectory() as directory:
            report = Path(directory) / "report.json"
            command = [sys.executable, str(Path(__file__).with_name("notify.py")), "--execute",
                       "--nb1-binary", BINARY, "--endpoint", "http://127.0.0.1:{}".format(server.server_port),
                       "--serial", "test-serial", "--report", str(report),
                       "--notify-timeout", ".15" if mode == "timeout" else "3", "--",
                       sys.executable, "-c", "import sys; print(sys.argv[1]); sys.exit({})".format(child_exit),
                       "literal; $(not-executed)"]
            try:
                result = subprocess.run(command, capture_output=True, text=True, timeout=8, shell=False)
                data = json.loads(report.read_text())
                notification = data["notification"]
                recovered = None
                if mode == "unknown":
                    lookup = subprocess.run(notification["job_lookup_argv"], capture_output=True,
                                            text=True, timeout=8, check=True)
                    recovered = json.loads(lookup.stdout)
            finally:
                server.shutdown()
                server.server_close()
                thread.join()
        self.assertEqual(result.returncode, child_exit, result.stderr)
        self.assertEqual(result.stdout.strip(), "literal; $(not-executed)")
        self.assertEqual(server.posts, ["/v1/jobs"])
        self.assertEqual(notification["job_id"], server.submission["id"])
        self.assertEqual(notification["input"]["action"], server.submission["action"])
        if recovered:
            self.assertEqual(recovered, server.job)
        return data, server

    def test_real_cli_success_failure_and_terminal_cleanup_result(self):
        for mode, child_exit, expected in (("success", 0, "succeeded"), ("success", 7, "succeeded"),
                                           ("failed", 0, "failed"), ("cancelled", 0, "cancelled")):
            with self.subTest(mode=mode, child_exit=child_exit):
                data, server = self.invoke(mode, child_exit)
                notification = data["notification"]
                self.assertEqual(notification["state"], expected)
                action = notification["input"]["action"]
                self.assertEqual(action, {"kind": "blink", "led": 0,
                                         "color": "#b40000" if child_exit else "#00a000",
                                         "on_ms": 1500, "off_ms": 10, "cycles": 1, "on_finish": "off"})
                self.assertEqual(server.gets, ["/v1/jobs/" + notification["job_id"]])
                self.assertEqual(notification["final_off"], "daemon_job_confirmed" if expected == "succeeded" else "not_confirmed")

    def test_real_cli_lost_response_is_not_retried_and_original_job_recovers(self):
        data, server = self.invoke("unknown")
        self.assertEqual(data["notification"]["state"], "unknown")
        self.assertTrue(data["notification"]["outcome_unknown"])
        self.assertEqual(server.gets, ["/v1/jobs/" + data["notification"]["job_id"]])

    def test_real_cli_timeout_busy_offline_and_invalid_result_do_not_replay(self):
        for mode in ("timeout", "busy", "offline", "invalid"):
            with self.subTest(mode=mode):
                data, server = self.invoke(mode)
                self.assertEqual(data["notification"]["state"], "unknown")
                self.assertTrue(data["notification"]["outcome_unknown"])
                self.assertEqual(data["notification"]["final_off"], "not_confirmed")
                self.assertEqual(server.gets, [])


if __name__ == "__main__":
    unittest.main()
