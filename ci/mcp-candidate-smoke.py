#!/usr/bin/env python3
"""Smoke a real MCP stdio executable; do not start a daemon or exercise USB.

This checks protocol discovery and one read-only unavailable-daemon response.
It does not certify build provenance, hardware, service installation or playback.
Only the Python standard library is required, including on Windows.
"""

import argparse
import hashlib
import json
from pathlib import Path
import queue
import socket
import subprocess
import sys
import threading
import time

PROTOCOL = "2025-11-25"  # Supported by the pinned Go MCP SDK v1.8.0.
TOTAL_SECONDS = 15
STDERR_LIMIT = 16 * 1024
LINE_LIMIT = 1024 * 1024
STDOUT_LIMIT = 2 * LINE_LIMIT
TOOLS = {
    "list_devices", "get_device_state", "resolve_color", "preview_light",
    "run_light", "get_job", "cancel_job", "list_colors", "save_color",
    "list_patterns", "get_pattern", "save_pattern",
}


def require(condition, message):
    if not condition:
        raise ValueError(message)


def remaining(deadline):
    value = deadline - time.monotonic()
    if value <= 0:
        raise TimeoutError("MCP smoke exceeded its bounded time budget")
    return value


class StdoutReader(threading.Thread):
    """Use a thread, not select(), since Windows subprocess pipes are not sockets."""

    def __init__(self, pipe):
        super().__init__(daemon=True)
        self.pipe, self.messages = pipe, queue.Queue(maxsize=32)
        self.error, self.byte_count = None, 0

    def run(self):
        try:
            while True:
                raw = self.pipe.readline(LINE_LIMIT + 1)
                if not raw:
                    self.messages.put_nowait(None)
                    return
                self.byte_count += len(raw)
                require(len(raw) <= LINE_LIMIT and self.byte_count <= STDOUT_LIMIT,
                        "MCP stdout exceeded the bounded protocol output limit")
                require(raw.endswith(b"\n"), "MCP stdout contains an unterminated protocol line")
                self.messages.put_nowait(json.loads(raw.decode("utf-8")))
        except Exception as error:
            self.error = str(error) or type(error).__name__


class StderrReader(threading.Thread):
    def __init__(self, pipe):
        super().__init__(daemon=True)
        self.pipe, self.saved, self.byte_count = pipe, bytearray(), 0

    def run(self):
        try:
            while True:
                chunk = self.pipe.read(4096)
                if not chunk:
                    return
                self.byte_count += len(chunk)
                self.saved.extend(chunk[:max(0, STDERR_LIMIT - len(self.saved))])
        except (OSError, ValueError):
            return

    def report(self):
        return {"text": bytes(self.saved).decode("utf-8", errors="replace"),
                "bytes_observed": self.byte_count, "limit_bytes": STDERR_LIMIT,
                "truncated": self.byte_count > STDERR_LIMIT}


def receive(reader, deadline, expected_id, notifications):
    while True:
        require(reader.error is None, "invalid MCP stdout: " + str(reader.error))
        try:
            message = reader.messages.get(timeout=min(0.1, remaining(deadline)))
        except queue.Empty:
            continue
        require(isinstance(message, dict) and message.get("jsonrpc") == "2.0",
                "MCP stdout ended or contains non-JSON-RPC output")
        if "method" in message:
            require("id" not in message and isinstance(message["method"], str)
                    and len(notifications) < 16, "unexpected MCP server request or notification flood")
            notifications.append(message["method"])
            continue
        require(message.get("id") == expected_id and "error" not in message
                and isinstance(message.get("result"), dict), "unexpected MCP response or protocol error")
        return message["result"]


def send(process, message):
    process.stdin.write(json.dumps(message, separators=(",", ":")).encode("utf-8") + b"\n")
    process.stdin.flush()


def request(process, reader, deadline, request_id, method, params, notifications):
    send(process, {"jsonrpc": "2.0", "id": request_id, "method": method, "params": params})
    return receive(reader, deadline, request_id, notifications)


def inspect_tools(result):
    tools = result.get("tools")
    require(isinstance(tools, list) and len(tools) == len(TOOLS) and not result.get("nextCursor"),
            "tools/list must return all twelve known tools without pagination")
    require(all(isinstance(tool, dict) for tool in tools), "invalid tools/list entry")
    require({tool.get("name") for tool in tools} == TOOLS, "MCP tool names differ from the API v1 contract")
    summaries = []
    for tool in tools:
        schema = tool.get("inputSchema")
        require(isinstance(schema, dict) and schema.get("type") == "object"
                and isinstance(schema.get("properties"), dict),
                tool["name"] + ": missing structured object input schema")
        summaries.append({"name": tool["name"], "input_schema_type": schema["type"]})
    return sorted(summaries, key=lambda item: item["name"])


def inspect_unavailable(result, endpoint):
    require(result.get("isError") is True, "unavailable daemon must produce an MCP tool error")
    structured = result.get("structuredContent")
    require(isinstance(structured, dict) and structured.get("endpoint") == endpoint
            and "data" not in structured, "unavailable response lost its endpoint or claims device data")
    error = structured.get("error", {})
    # A bound, non-listening socket may refuse immediately or time out on the
    # host OS. Both are transport failures, never successful device discovery.
    require(isinstance(error, dict) and error.get("code") in {"daemon_unreachable", "request_timeout"}
            and isinstance(error.get("message"), str) and error["message"].strip()
            and endpoint in error["message"]
            and not error.get("outcome_unknown"), "unavailable daemon diagnostic is missing or ambiguous")
    content = result.get("content")
    require(isinstance(content, list) and len(content) == 1 and content[0].get("type") == "text"
            and json.loads(content[0]["text"]) == structured,
            "text fallback must match the structured unavailable-daemon response")
    return structured


def smoke(binary, report, started):
    deadline, work_deadline = started + TOTAL_SECONDS, started + TOTAL_SECONDS - 3
    process = stdout = stderr = None
    reserved = socket.socket(socket.AF_INET, socket.SOCK_STREAM)
    try:
        if hasattr(socket, "SO_EXCLUSIVEADDRUSE"):
            reserved.setsockopt(socket.SOL_SOCKET, socket.SO_EXCLUSIVEADDRUSE, 1)
        # Keep this reservation open until the child exits. Never listen: HTTP
        # must fail, and another process must not acquire this test endpoint.
        reserved.bind(("127.0.0.1", 0))
        endpoint = "http://127.0.0.1:" + str(reserved.getsockname()[1])
        report["endpoint"] = {"origin": endpoint, "reservation": "bound_not_listening"}
        process = subprocess.Popen([str(binary), "--endpoint", endpoint, "--timeout", "1s"],
                                   stdin=subprocess.PIPE, stdout=subprocess.PIPE, stderr=subprocess.PIPE)
        stdout, stderr = StdoutReader(process.stdout), StderrReader(process.stderr)
        stdout.start()
        stderr.start()
        notifications = []
        initialized = request(process, stdout, work_deadline, 1, "initialize", {
            "protocolVersion": PROTOCOL, "capabilities": {},
            "clientInfo": {"name": "nb1-candidate-smoke", "version": "1"}}, notifications)
        require(initialized.get("protocolVersion") == PROTOCOL
                and isinstance(initialized.get("capabilities", {}).get("tools"), dict)
                and initialized.get("serverInfo", {}).get("name") == "nb1",
                "MCP initialization did not negotiate the expected nb1 tool server")
        report["initialize"] = {"requested_protocol": PROTOCOL, "negotiated_protocol": initialized["protocolVersion"],
                                "server_info": initialized["serverInfo"]}
        send(process, {"jsonrpc": "2.0", "method": "notifications/initialized"})
        report["tools"] = inspect_tools(request(process, stdout, work_deadline, 2,
                                               "tools/list", {}, notifications))
        result = request(process, stdout, work_deadline, 3, "tools/call",
                         {"name": "list_devices", "arguments": {}}, notifications)
        report["read_only_call"] = {"name": "list_devices", "arguments": {}, "result": result}
        inspect_unavailable(result, endpoint)
        report["server_notifications"] = notifications
    finally:
        try:
            if process is not None:
                if process.stdin is not None:
                    try:
                        process.stdin.close()
                    except (OSError, ValueError):
                        pass
                killed = False
                try:
                    process.wait(timeout=min(1, max(0.01, deadline - time.monotonic())))
                except subprocess.TimeoutExpired:
                    killed = True
                    process.kill()
                    process.wait(timeout=max(0.01, deadline - time.monotonic()))
                finally:
                    for reader in (stdout, stderr):
                        if reader is not None:
                            reader.join(timeout=min(0.2, max(0, deadline - time.monotonic())))
                    report["process"] = {"returncode": process.returncode, "killed_for_cleanup": killed}
                    report["stderr"] = stderr.report() if stderr is not None else None
                    for pipe in (process.stdout, process.stderr):
                        if pipe is not None:
                            pipe.close()
        finally:
            reserved.close()
    require(not killed and process.returncode == 0, "MCP process did not exit cleanly after stdin EOF")
    require(stdout.error is None, "invalid MCP stdout: " + str(stdout.error))
    # All three replies were consumed above; any remaining stdout must be EOF.
    while not stdout.messages.empty():
        require(stdout.messages.get_nowait() is None, "unexpected trailing MCP stdout")
    report["stdout_protocol_only"] = True
    report["stdout_bytes"] = stdout.byte_count


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--binary", required=True, type=Path)
    parser.add_argument("--output", required=True, type=Path, help="new JSON report file; never overwritten")
    args = parser.parse_args()
    if args.output.exists() or args.output.is_symlink():
        parser.error("--output must be a new file")
    started = time.monotonic()
    report = {"schema_version": 1, "validation": "mcp_stdio_discovery_and_unavailable_daemon",
              "status": "FAIL", "scope": {"hardware_tested": False, "daemon_started": False,
              "native_service_changed": False, "mutating_tool_calls": [],
              "build_provenance_certified": False}, "timeout_seconds": TOTAL_SECONDS}
    try:
        binary = args.binary.resolve(strict=True)
        require(binary.is_file(), "--binary must be an executable file")
        report["binary"] = {"path": str(binary), "sha256": hashlib.sha256(binary.read_bytes()).hexdigest()}
        smoke(binary, report, started)
        remaining(started + TOTAL_SECONDS)
        report["status"] = "PASS"
    except Exception as error:
        report["error"] = str(error) or type(error).__name__
    report["elapsed_seconds"] = round(time.monotonic() - started, 3)
    with args.output.open("x", encoding="utf-8") as output:
        json.dump(report, output, ensure_ascii=False, indent=2)
        output.write("\n")
    print(json.dumps({"status": report["status"], "report": str(args.output)}))
    return 0 if report["status"] == "PASS" else 1


if __name__ == "__main__":
    sys.exit(main())
