"""Optional real-CLI integration: NB1_NOTIFY_TEST_BINARY=/path/to/nb1 python3 -m unittest ..."""
import contextlib
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
from urllib.parse import parse_qs, urlparse

BINARY = os.environ.get("NB1_NOTIFY_TEST_BINARY")
INSTANCE = "daemon_" + "b" * 32


class Fixture(http.server.BaseHTTPRequestHandler):
    def log_message(self, *_):
        pass

    def send_json(self, value, status=200, receipt=False):
        body = json.dumps(value).encode()
        self.send_response(status)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(body)))
        if receipt:
            self.send_header("X-NB1-Daemon-Instance-ID", INSTANCE)
            self.send_header("X-NB1-Replayed", "false")
        self.end_headers()
        try:
            self.wfile.write(body)
        except (BrokenPipeError, ConnectionResetError):
            pass

    def do_GET(self):
        self.server.gets.append(self.path)
        parsed = urlparse(self.path)
        if parsed.path == "/v1/instance":
            self.send_json({"daemon_instance_id": INSTANCE, "idempotency": {
                "supported": True, "request_id_prefix": "req_60000_"}})
        elif parsed.path == "/v1/devices/test-serial/requests/" + self.server.binding["request_id"]:
            assert parse_qs(parsed.query)["expected_daemon_instance_id"] == [INSTANCE]
            self.send_json(self.server.job, receipt=True)
        else:
            self.send_json({"error": {"code": "unexpected", "message": self.path}}, 404)

    def do_POST(self):
        self.server.posts.append(self.path)
        assert self.path == "/v1/devices/test-serial/jobs"
        payload = json.loads(self.rfile.read(int(self.headers["Content-Length"])))
        assert payload["expected_daemon_instance_id"] == INSTANCE
        assert payload["request_id"].startswith("req_60000_")
        assert payload["policy"] == "reject"
        self.server.binding = payload
        self.server.job = {"id": "job-fixture", "device": "test-serial", "status": "succeeded",
                           "program": payload["program"]}
        if self.server.mode == "unknown":
            self.connection.shutdown(socket.SHUT_RDWR)
            self.connection.close()
            return
        if self.server.mode == "timeout":
            time.sleep(0.3)
        self.send_json(self.server.job, status=202, receipt=True)


@unittest.skipUnless(BINARY, "set NB1_NOTIFY_TEST_BINARY to a current nb1 executable")
class RealCLIFixtureTests(unittest.TestCase):
    def invoke(self, mode, child_exit=0):
        server = http.server.ThreadingHTTPServer(("127.0.0.1", 0), Fixture)
        server.mode, server.posts, server.gets = mode, [], []
        server.binding = {}
        thread = threading.Thread(target=server.serve_forever, daemon=True)
        thread.start()
        with tempfile.TemporaryDirectory() as directory:
            report = Path(directory) / "report.json"
            command = [sys.executable, str(Path(__file__).with_name("notify.py")), "--execute",
                       "--nb1-binary", BINARY, "--endpoint", "http://127.0.0.1:{}".format(server.server_port),
                       "--serial", "test-serial", "--report", str(report),
                       "--notify-timeout", "0.15" if mode == "timeout" else "3", "--",
                       sys.executable, "-c", "import sys; print(sys.argv[1]); sys.exit({})".format(child_exit),
                       "literal; $(not-executed)"]
            try:
                result = subprocess.run(command, capture_output=True, text=True, timeout=8, shell=False)
                data = json.loads(report.read_text())
            finally:
                server.shutdown()
                server.server_close()
                thread.join()
        self.assertEqual(result.returncode, child_exit)
        self.assertEqual(result.stdout.strip(), "literal; $(not-executed)")
        self.assertEqual(server.posts, ["/v1/devices/test-serial/jobs"])
        self.assertEqual(data["notification"]["binding"]["request_id"], server.binding["request_id"])
        self.assertEqual(data["notification"]["binding"]["expected_daemon_instance_id"], INSTANCE)
        return data, server

    def test_real_cli_success_and_failure_command(self):
        for child_exit, color in ((0, "#00a000"), (7, "#b40000")):
            with self.subTest(child_exit=child_exit):
                data, server = self.invoke("success", child_exit)
                self.assertEqual(data["notification"]["state"], "succeeded")
                self.assertEqual(data["notification"]["program"]["steps"][0]["color"], color)
                self.assertEqual(data["notification"]["program"]["steps"][-1]["color"], "#000000")
                self.assertEqual(data["notification"]["program"]["on_finish"], "off")
                self.assertEqual(len(server.gets), 2)  # one prepare, one bound receipt read

    def test_real_cli_lost_response_is_not_retried(self):
        data, server = self.invoke("unknown")
        self.assertEqual(data["notification"]["state"], "unknown")
        self.assertTrue(data["notification"]["outcome_unknown"])
        self.assertEqual(server.gets, ["/v1/instance"])

    def test_real_cli_timeout_is_not_retried(self):
        data, server = self.invoke("timeout")
        self.assertEqual(data["notification"]["state"], "unknown")
        self.assertTrue(data["notification"]["outcome_unknown"])
        self.assertEqual(server.gets, ["/v1/instance"])


if __name__ == "__main__":
    unittest.main()
