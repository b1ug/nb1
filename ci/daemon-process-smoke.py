#!/usr/bin/env python3
"""Exercise a real foreground nb1 daemon with an explicitly absent device.

No system service is installed. Only this script's Popen children are signalled.
Enumeration is metadata-only; no discovered device is selected or controlled.
"""

import argparse
import ctypes
import hashlib
import json
import os
from pathlib import Path
import platform
import shutil
import signal
import subprocess
import sys
import threading
import time
import urllib.error
import urllib.parse
import urllib.request


ABSENT_SERIAL = "00000000"
MAX_OUTPUT = 1 << 20
PROCESS_BUDGET = 20
STOP_BUDGET = 15


class SmokeError(RuntimeError):
    pass


def require(condition, message):
    if not condition:
        raise SmokeError(message)


def sha256(data):
    return hashlib.sha256(data).hexdigest()


def write_json(path, value):
    path.write_text(json.dumps(value, indent=2, ensure_ascii=False) + "\n", encoding="utf-8")
    if os.name != "nt":
        path.chmod(0o600)


def discovery_is_empty(text, require_no_devices):
    require(ABSENT_SERIAL not in text,
            "reserved absent serial appeared in metadata; refusing daemon startup")
    empty = text.strip() == "No blink(1) devices found."
    require(empty or not require_no_devices,
            "runner has blink(1) devices; refusing offline-only qualification")
    require(bool(text.strip()), "metadata discovery produced no result")
    return empty


def local_origin(value):
    """A child announcement is not permission to contact another host."""
    parsed = urllib.parse.urlsplit(value)
    try:
        port = parsed.port
    except ValueError as error:
        raise SmokeError("invalid daemon port") from error
    require(parsed.scheme == "http" and parsed.hostname == "127.0.0.1"
            and parsed.username is None and parsed.password is None
            and port is not None and 1 <= port <= 65535
            and parsed.path == "" and not parsed.query and not parsed.fragment,
            "daemon announcement is not a plain 127.0.0.1 HTTP origin")
    require(value == "http://127.0.0.1:" + str(port), "noncanonical daemon origin")
    return value


class NoRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, req, fp, code, msg, headers, newurl):
        return None


HTTP = urllib.request.build_opener(urllib.request.ProxyHandler({}), NoRedirect())


def request(origin, method, path, body=None, headers=None):
    local_origin(origin)
    require(path.startswith("/") and not path.startswith("//"), "invalid local API path")
    values = dict(headers or {})
    data = None
    if body is not None:
        values["Content-Type"] = "application/json"
        data = json.dumps(body, separators=(",", ":")).encode("utf-8")
    req = urllib.request.Request(origin + path, data=data, headers=values, method=method)
    try:
        response = HTTP.open(req, timeout=3)
    except urllib.error.HTTPError as error:
        response = error
    with response:
        raw = response.read(MAX_OUTPUT + 1)
        require(len(raw) <= MAX_OUTPUT, "HTTP response exceeded 1 MiB")
        require(response.headers.get_content_type() == "application/json", "non-JSON API response")
        return response.code, json.loads(raw)


def reachable(origin):
    try:
        request(origin, "GET", "/healthz")
        return True
    except (OSError, urllib.error.URLError):
        return False


class Child:
    """Bounded pipe draining and cleanup restricted to one owned Popen."""

    def __init__(self, arguments, cwd, environment, label):
        self.label = label
        self.buffers = {"stdout": bytearray(), "stderr": bytearray()}
        self.lock = threading.Lock()
        self.overflow = False
        self.reader_error = None
        self.forced = False
        self.stop_method = None
        self.started = time.monotonic()
        flags = subprocess.CREATE_NEW_PROCESS_GROUP if os.name == "nt" else 0
        self.process = subprocess.Popen([str(value) for value in arguments], cwd=cwd,
                                        env=environment, stdin=subprocess.DEVNULL,
                                        stdout=subprocess.PIPE, stderr=subprocess.PIPE,
                                        creationflags=flags)
        self.threads = []
        for name in self.buffers:
            thread = threading.Thread(target=self._read, args=(name,), daemon=True)
            thread.start()
            self.threads.append(thread)

    def _read(self, name):
        try:
            with getattr(self.process, name) as pipe:
                while True:
                    data = pipe.read1(4096)
                    if not data:
                        break
                    with self.lock:
                        room = MAX_OUTPUT - len(self.buffers[name])
                        self.buffers[name].extend(data[:max(0, room)])
                        if len(data) > room:
                            self.overflow = True
        except Exception as error:
            self.reader_error = type(error).__name__

    def text(self, name):
        with self.lock:
            return bytes(self.buffers[name]).decode("utf-8", errors="replace")

    def records(self):
        result = []
        for line in self.text("stdout").splitlines():
            try:
                value = json.loads(line)
            except (ValueError, UnicodeError):
                continue
            if isinstance(value, dict):
                result.append(value)
        return result

    def healthy_pipes(self):
        require(not self.overflow and self.reader_error is None,
                self.label + " output exceeded its bound or could not be read")

    def wait(self, timeout=PROCESS_BUDGET):
        try:
            result = self.process.wait(timeout=timeout)
        except subprocess.TimeoutExpired as error:
            raise SmokeError(self.label + " did not exit within its wait budget") from error
        for thread in self.threads:
            thread.join(timeout=2)
        require(not any(thread.is_alive() for thread in self.threads), self.label + " pipes stayed open")
        self.healthy_pipes()
        return result

    def cleanup(self):
        if self.process.poll() is None:
            self.forced = True
            self.stop_method = "forced_cleanup_only"
            self.process.kill()
            self.process.wait(timeout=5)
        for thread in self.threads:
            thread.join(timeout=2)

    def receipt(self):
        return {"label": self.label, "pid": self.process.pid,
                "exit_code": self.process.poll(), "forced_cleanup": self.forced,
                "stop_method": self.stop_method,
                "stdout_sha256": sha256(self.text("stdout").encode("utf-8")),
                "stderr_sha256": sha256(self.text("stderr").encode("utf-8")),
                "output_overflow": self.overflow, "reader_error": self.reader_error,
                "elapsed_ms": round((time.monotonic() - self.started) * 1000)}


class Console:
    """CTRL_BREAK requires the owned child to share the caller's console."""

    def __init__(self):
        self.allocated = False

    def prepare(self):
        if os.name == "nt":
            kernel = ctypes.WinDLL("kernel32", use_last_error=True)
            if kernel.GetConsoleCP() == 0:
                require(bool(kernel.AllocConsole()), "cannot allocate a console for targeted CTRL_BREAK")
                self.allocated = True

    def close(self):
        if self.allocated:
            ctypes.WinDLL("kernel32", use_last_error=True).FreeConsole()


class Smoke:
    def __init__(self, binary, output, require_no_devices=False):
        self.binary = binary
        self.output = output
        self.require_no_devices = require_no_devices
        self.children = []
        self.checks = []
        self.console = Console()
        self.receipt = {"schema_version": 1, "validation": "actual_foreground_offline_daemon",
                        "status": "FAIL", "platform": platform.platform(),
                        "machine": platform.machine(), "python": platform.python_version(),
                        "binary_sha256": sha256(binary.read_bytes()),
                        "selected_serial": ABSENT_SERIAL, "checks": self.checks,
                        "limits": ["No system service or Task Scheduler task is installed or changed.",
                                   "Windows automatic service installation remains unsupported.",
                                   "Metadata enumeration is not physical USB I/O qualification.",
                                   "Absent-device shutdown does not qualify blocked native I/O or C crash isolation.",
                                   "Restart from another explicit executable path is not managed upgrade validation.",
                                   "Daemon shutdown records are best-effort; absent records are not cleanup evidence."]}
        self.work = output / "private work with spaces"
        self.work.mkdir(mode=0o700)
        self.library = self.work / "local library"
        self.locks = self.work / "device locks"
        self.profile = self.work / "private config.yaml"
        write_json(self.profile, {"device": ABSENT_SERIAL,
                                 "server": {"devices": [ABSENT_SERIAL], "listen": "127.0.0.1:0"},
                                 "library_dir": str(self.library)})
        self.environment = {key: value for key, value in os.environ.items()
                            if not key.upper().startswith("NB1_")}
        self.environment["XDG_CONFIG_HOME"] = str(self.work / "xdg config")
        if os.name == "nt":
            self.environment["APPDATA"] = str(self.work / "native app data")

    def check(self, name, **evidence):
        self.checks.append({"name": name, "status": "PASS", **evidence})

    def spawn(self, binary, arguments, label):
        child = Child([binary, "--config", self.profile, *arguments], self.work,
                      self.environment, label)
        self.children.append(child)
        return child

    def cli(self, binary, arguments, label, expect_success=True):
        child = self.spawn(binary, arguments, label)
        code = child.wait()
        require((code == 0) == expect_success, label + " returned an unexpected exit code")
        return child

    def control(self, binary, origin, arguments, label):
        child = self.cli(binary, ["control", "--endpoint", origin, "--timeout", "3s", *arguments], label)
        return json.loads(child.text("stdout"))

    def copy_binary(self, name):
        directory = self.work / name
        directory.mkdir(mode=0o700)
        copy = directory / ("nb1.exe" if os.name == "nt" else "nb1")
        shutil.copy2(self.binary, copy)
        require(sha256(copy.read_bytes()) == self.receipt["binary_sha256"], "executable copy differs")
        return copy

    def start(self, binary, label, address="127.0.0.1:0"):
        child = self.spawn(binary, ["server", "--devices", ABSENT_SERIAL, "--listen", address,
                                   "--lock-dir", self.locks, "--library-dir", self.library,
                                   "--shutdown-timeout", "10s"], label)
        until = time.monotonic() + PROCESS_BUDGET
        while time.monotonic() < until:
            child.healthy_pipes()
            require(child.process.poll() is None, label + " exited before startup")
            for record in child.records():
                if record.get("event") == "daemon_listening":
                    origin = local_origin(record.get("url", ""))
                    return child, origin
            time.sleep(0.05)
        raise SmokeError(label + " did not announce a local listener")

    def offline(self, child, origin):
        until = time.monotonic() + PROCESS_BUDGET
        while time.monotonic() < until:
            require(child.process.poll() is None, "daemon exited while waiting for offline state")
            status, state = request(origin, "GET", "/v1/devices/" + ABSENT_SERIAL)
            require(status == 200 and state.get("serial") == ABSENT_SERIAL, "wrong configured device")
            if state.get("connection") == "offline":
                require(state.get("observed") is None and not state.get("active_job"), "offline device has live observation/job")
                return state
            time.sleep(0.05)
        raise SmokeError("absent device did not reach offline state")

    def stop(self, child, origin, label):
        require(child.process.poll() is None, "daemon exited before the stop request")
        child.stop_method = "CTRL_BREAK_EVENT" if os.name == "nt" else "SIGTERM"
        child.process.send_signal(signal.CTRL_BREAK_EVENT if os.name == "nt" else signal.SIGTERM)
        began = time.monotonic()
        code = child.wait(STOP_BUDGET)
        require(code == 0 and not child.forced, "daemon did not exit normally after its targeted signal")
        require(not reachable(origin), "daemon endpoint remained reachable after exit")
        results = [record for record in child.records() if record.get("event") == "daemon_shutdown_result"]
        if results:
            require(all(record.get("outcome") == "succeeded" and record.get("engine_done") is True
                        and record.get("http_shutdown") == "none" and record.get("engine_close") == "none"
                        for record in results), "observed shutdown record did not confirm successful cleanup")
        self.check(label, signal=child.stop_method, exit_code=code,
                   elapsed_ms=round((time.monotonic() - began) * 1000),
                   shutdown_result="observed_success" if results else "not_observed_best_effort",
                   scope="process-level graceful exit for an absent device")

    def run(self):
        self.console.prepare()
        first = self.copy_binary("install one")
        second = self.copy_binary("install two")
        self.check("executable_and_data_paths_with_spaces", identical_binary_copies=True)
        discovery = self.cli(first, ["list"], "metadata-discovery")
        text = discovery.text("stdout")
        empty = discovery_is_empty(text, self.require_no_devices)
        self.check("metadata_discovery_guard", no_devices=empty, require_no_devices=self.require_no_devices,
                   raw_metadata_exported=False)
        daemon, origin = self.start(first, "daemon-first")
        status, health = request(origin, "GET", "/healthz")
        require(status == 200 and health == {"status": "ok", "api_version": "v1"}, "health contract mismatch")
        self.check("health", http_status=status)
        # Exercise the byte-verified embedded bundle in the built executable,
        # so checkout transformations cannot hide behind a successful build.
        for path, content_type in [("/demo/", "text/html"),
                                   ("/demo/action-icons.js", "text/javascript"),
                                   ("/demo/fonts/fredoka-700-latin.woff2", "font/woff2")]:
            with HTTP.open(origin + path, timeout=3) as response:
                data = response.read(MAX_OUTPUT + 1)
                require(response.code == 200 and 0 < len(data) <= MAX_OUTPUT
                        and response.headers.get_content_type() == content_type,
                        "embedded Web asset failed: " + path)
                require("connect-src 'self'" in response.headers.get("Content-Security-Policy", ""),
                        "embedded Web asset lost its same-origin policy")
        self.check("embedded_web_assets", assets=3, same_origin=True,
                   browser_interaction_tested=False)
        state = self.offline(daemon, origin)
        status, devices = request(origin, "GET", "/v1/devices")
        require(status == 200 and devices == [state], "unexpected configured device set")
        self.check("absent_device_offline", observed_is_null=True, configured_devices=1)
        cli_devices = self.control(first, origin, ["devices"], "cli-devices")
        cli_state = self.control(first, origin, ["state", ABSENT_SERIAL], "cli-state")
        # CLI JSON omits a nil observation, while the HTTP contract emits null.
        # Compare the same state without requiring identical encoders.
        for value in [*cli_devices, cli_state]:
            value.setdefault("observed", None)
        require(cli_devices == devices and cli_state == state, "CLI cached device state mismatch")
        self.check("cli_connectivity", commands=["control devices", "control state"])
        action = {"action": {"kind": "pulse", "color": "lime", "cycles": 1}}
        preview_file = self.work / "preview action.json"
        write_json(preview_file, action)
        status, preview = request(origin, "POST", "/v1/preview", {**action, "serial": ABSENT_SERIAL})
        cli_preview = self.control(first, origin, ["preview", preview_file, "--serial", ABSENT_SERIAL], "cli-preview")
        require(status == 200 and preview == cli_preview and preview["device_check"]["status"] == "unknown"
                and preview["total_steps"] > 0, "pure preview or offline capability semantics mismatch")
        self.check("http_and_cli_preview", device_check="unknown", execution_requested=False)
        status, rejection = request(origin, "POST", "/v1/jobs", {"id": "smoke-offline-job", "serial": ABSENT_SERIAL,
                                                               "action": {"kind": "set", "color": "red"}})
        require(status == 503 and rejection.get("error", {}).get("code") == "unavailable", "offline execution was not refused")
        status, missing = request(origin, "GET", "/v1/jobs/smoke-offline-job")
        require(status == 404 and missing.get("error", {}).get("code") == "not_found", "refused offline job was retained")
        self.check("offline_execution_refused", http_status=503, accepted_job=False)
        status, rejected = request(origin, "GET", "/healthz", headers={"Origin": "https://foreign.invalid"})
        require(status == 403 and rejected.get("error", {}).get("code") == "local_only", "foreign Origin was accepted")
        self.check("foreign_origin_refused", http_status=status)
        address = urllib.parse.urlsplit(origin).netloc
        duplicate = self.spawn(second, ["server", "--devices", ABSENT_SERIAL, "--listen", address,
                                       "--lock-dir", self.locks, "--library-dir", self.library], "duplicate-port")
        require(duplicate.wait() != 0 and not any(record.get("event") == "daemon_listening" for record in duplicate.records()),
                "second daemon did not fail before announcing a listener")
        require(request(origin, "GET", "/healthz")[0] == 200, "port conflict disrupted the original daemon")
        self.check("duplicate_port_refused", original_daemon_healthy=True)
        status, color = request(origin, "PUT", "/v1/colors/smoke-ok", {"color": "#13579b"})
        require(status == 200 and color == {"name": "smoke-ok", "color": "#13579b"}, "color save failed")
        status, pattern = request(origin, "PUT", "/v1/patterns/smoke-pattern",
                                  {"action": {"kind": "set", "color": "@smoke-ok"}, "title": "Runner smoke"})
        require(status == 200 and pattern.get("id") == "smoke-pattern" and pattern.get("sequence"), "Pattern save failed")
        self.check("local_library_saved", color="smoke-ok", pattern="smoke-pattern", hardware_writes=False)
        status, metrics = request(origin, "GET", "/v1/transport-metrics")
        require(status == 200 and metrics == [], "absent fixture unexpectedly recorded native device I/O")
        self.check("no_native_device_io_metrics", metrics=[])
        self.stop(daemon, origin, "first_graceful_shutdown")
        unavailable = self.cli(first, ["control", "--endpoint", origin, "--timeout", "3s", "state", ABSENT_SERIAL],
                               "cli-unavailable", expect_success=False)
        combined = unavailable.text("stdout") + unavailable.text("stderr")
        require("cannot connect to daemon" in combined and "Start a daemon explicitly" in combined,
                "unavailable daemon did not provide explicit startup guidance")
        require(not reachable(origin), "failed CLI invocation started a daemon")
        time.sleep(0.2)
        require(not reachable(origin), "failed CLI invocation later started a daemon")
        self.check("unavailable_does_not_autostart", cli_exit=unavailable.process.returncode,
                   evidence="explicit connection failure and original endpoint stayed closed")
        restarted, restarted_origin = self.start(second, "daemon-restarted-from-second-path")
        require(restarted.process.pid != daemon.process.pid, "restart did not establish a new process identity")
        self.offline(restarted, restarted_origin)
        status, colors = request(restarted_origin, "GET", "/v1/colors")
        require(status == 200 and colors == [color], "color did not survive process restart")
        status, patterns = request(restarted_origin, "GET", "/v1/patterns")
        require(status == 200 and patterns == [{"id": "smoke-pattern", "title": "Runner smoke"}], "Pattern summary did not survive restart")
        status, restored = request(restarted_origin, "GET", "/v1/patterns/smoke-pattern")
        require(status == 200 and restored == pattern, "frozen Pattern did not survive process restart")
        self.check("library_survives_process_restart", alternate_explicit_binary_path=True,
                   frozen_sequence_equal=True)
        self.stop(restarted, restarted_origin, "restarted_graceful_shutdown")
        self.receipt["status"] = "PASS"

    def finish(self):
        cleanup_errors = []
        for child in reversed(self.children):
            try:
                child.cleanup()
            except Exception as error:
                cleanup_errors.append(child.label + ": " + type(error).__name__)
        self.console.close()
        if cleanup_errors or any(child.forced for child in self.children):
            self.receipt["status"] = "FAIL"
        self.receipt["processes"] = [child.receipt() for child in self.children]
        self.receipt["cleanup"] = {"all_owned_children_exited": all(child.process.poll() is not None for child in self.children),
                                   "forced_cleanup_count": sum(child.forced for child in self.children),
                                   "errors": cleanup_errors,
                                   "system_service_operations": 0}
        # Discovery stdout includes attached devices' private metadata. Export
        # only its digest/empty assertion, never the raw table or environment.
        for child in self.children:
            if child.label != "metadata-discovery":
                for name in ("stdout", "stderr"):
                    (self.output / (child.label + "." + name + ".log")).write_text(child.text(name), encoding="utf-8")
        write_json(self.output / "result.json", self.receipt)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--binary", required=True, help="actual native nb1 executable")
    parser.add_argument("--output", required=True, help="new private evidence directory")
    parser.add_argument("--require-no-devices", action="store_true", help="require metadata discovery to find no blink(1), for cloud runners")
    args = parser.parse_args()
    binary = Path(args.binary).resolve(strict=True)
    require(binary.is_file(), "binary must be a regular file")
    output = Path(args.output).resolve()
    output.mkdir(mode=0o700, parents=True, exist_ok=False)
    smoke = Smoke(binary, output, args.require_no_devices)
    try:
        smoke.run()
    except Exception as error:
        smoke.receipt["error"] = {"type": type(error).__name__, "message": str(error)}
    finally:
        smoke.finish()
    print(json.dumps({"status": smoke.receipt["status"], "result": str(output / "result.json")}))
    return 0 if smoke.receipt["status"] == "PASS" else 1


if __name__ == "__main__":
    sys.exit(main())
