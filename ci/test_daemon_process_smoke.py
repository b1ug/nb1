"""Harness boundary tests only; these do not pretend to qualify an nb1 daemon."""

import importlib.util
import http.server
import os
from pathlib import Path
import sys
import tempfile
import threading
import unittest
from unittest import mock


spec = importlib.util.spec_from_file_location("daemon_process_smoke", Path(__file__).with_name("daemon-process-smoke.py"))
smoke = importlib.util.module_from_spec(spec)
spec.loader.exec_module(smoke)


class BoundaryTests(unittest.TestCase):
    def test_only_canonical_ipv4_loopback_announcements(self):
        self.assertEqual(smoke.local_origin("http://127.0.0.1:12345"), "http://127.0.0.1:12345")
        for invalid in ("https://127.0.0.1:1", "http://localhost:1", "http://127.0.0.1:0",
                        "http://127.0.0.1:65536", "http://127.0.0.1:1/", "http://127.0.0.1:1?x=y",
                        "http://127.0.0.1:1#x", "http://user@127.0.0.1:1", "http://127.0.0.1:01",
                        "http://foreign.invalid:1", "http://127.0.0.1:1@foreign.invalid:2"):
            with self.subTest(value=invalid), self.assertRaises((smoke.SmokeError, ValueError)):
                smoke.local_origin(invalid)

    def test_reserved_serial_blocks_before_start_and_cloud_requires_empty(self):
        self.assertTrue(smoke.discovery_is_empty("No blink(1) devices found.\n", True))
        self.assertFalse(smoke.discovery_is_empty("Other attached device metadata\n", False))
        for text, cloud in (("00000000", False), ("Other attached device metadata", True), ("", False)):
            with self.subTest(cloud=cloud), self.assertRaises(smoke.SmokeError):
                smoke.discovery_is_empty(text, cloud)

    def test_http_client_refuses_redirects_without_contacting_target(self):
        received = []

        class Handler(http.server.BaseHTTPRequestHandler):
            def do_GET(self):
                received.append(self.path)
                if self.path == "/redirect":
                    self.send_response(302)
                    self.send_header("Location", "/target")
                else:
                    self.send_response(200)
                self.send_header("Content-Type", "application/json")
                self.end_headers()
                self.wfile.write(b'{}')

            def log_message(self, *args):
                pass

        server = http.server.HTTPServer(("127.0.0.1", 0), Handler)
        thread = threading.Thread(target=server.serve_forever, kwargs={"poll_interval": 0.01}, daemon=True)
        thread.start()
        try:
            origin = "http://127.0.0.1:" + str(server.server_port)
            self.assertEqual(smoke.request(origin, "GET", "/redirect"), (302, {}))
            self.assertEqual(received, ["/redirect"])
            with self.assertRaises(smoke.SmokeError):
                smoke.request(origin, "GET", "//foreign.invalid/")
        finally:
            server.shutdown()
            server.server_close()
            thread.join(timeout=2)

    def test_child_output_is_bounded_and_failure_is_explicit(self):
        with tempfile.TemporaryDirectory(prefix="nb1 harness boundary ") as temporary:
            child = smoke.Child([sys.executable, "-c", "import sys; sys.stdout.buffer.write(b'x' * (1048576 + 8192))"],
                                temporary, os.environ.copy(), "output-boundary")
            try:
                with self.assertRaises(smoke.SmokeError):
                    child.wait()
                self.assertTrue(child.overflow)
                self.assertEqual(len(child.buffers["stdout"]), smoke.MAX_OUTPUT)
                self.assertEqual(child.process.returncode, 0)
            finally:
                child.cleanup()

    def test_forced_cleanup_reaps_only_owned_process_and_is_never_graceful(self):
        with tempfile.TemporaryDirectory(prefix="nb1 harness boundary ") as temporary:
            child = smoke.Child([sys.executable, "-c", "import time; time.sleep(30)"],
                                temporary, os.environ.copy(), "cleanup-boundary")
            try:
                with self.assertRaises(smoke.SmokeError):
                    child.wait(timeout=0.05)
            finally:
                child.cleanup()
            self.assertIsNotNone(child.process.poll())
            self.assertTrue(child.receipt()["forced_cleanup"])
            self.assertEqual(child.receipt()["stop_method"], "forced_cleanup_only")

    def test_private_profile_and_environment_do_not_inherit_nb1_overrides(self):
        with tempfile.TemporaryDirectory(prefix="nb1 harness boundary ") as temporary:
            output = Path(temporary)
            original_home = os.environ.get("HOME")
            with mock.patch.dict(os.environ, {"NB1_DEVICE": "private-real-device", "NB1_PORT": "8931", "NB1_CONFIG": "other.yaml"}):
                probe = smoke.Smoke(Path(sys.executable).resolve(), output)
            self.assertFalse(any(key.upper().startswith("NB1_") for key in probe.environment))
            self.assertEqual(probe.environment.get("HOME"), original_home)
            self.assertTrue(probe.profile.is_absolute())
            self.assertIn(" ", str(probe.profile))
            self.assertIn('"00000000"', probe.profile.read_text(encoding="utf-8"))
            self.assertEqual(probe.children, [])


if __name__ == "__main__":
    unittest.main()
