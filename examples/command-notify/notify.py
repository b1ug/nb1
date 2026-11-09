#!/usr/bin/env python3
"""Run an argv command, then notify through the existing receipt-aware nb1 CLI."""

import argparse
import json
import math
import os
from pathlib import Path
import re
import subprocess
import sys
import tempfile
import time


def program(color, duration_ms):
    return {
        "steps": [
            {"led": 0, "color": color, "fade_ms": 0, "hold_ms": duration_ms},
            {"led": 0, "color": "#000000", "fade_ms": 0, "hold_ms": 0},
        ],
        "repeat": 1,
        "on_finish": "off",
    }


def save_report(path, report, create=False):
    """Keep the original request binding durable before the single submission."""
    encoded = json.dumps(report, ensure_ascii=False, indent=2) + "\n"
    if create:
        with path.open("x", encoding="utf-8") as output:
            output.write(encoded)
            output.flush()
            os.fsync(output.fileno())
        return
    fd, temporary = tempfile.mkstemp(prefix=".nb1-notify-", dir=str(path.parent))
    try:
        with os.fdopen(fd, "w", encoding="utf-8") as output:
            output.write(encoded)
            output.flush()
            os.fsync(output.fileno())
        os.replace(temporary, path)
    finally:
        if os.path.exists(temporary):
            os.unlink(temporary)


def cli_prefix(options, seconds):
    return [options.nb1_binary, "control", "--mode", "daemon", "--endpoint",
            options.endpoint, "--timeout", "{:.6f}s".format(seconds)]


def cli_json(argv, seconds):
    result = subprocess.run(argv, stdout=subprocess.PIPE, stderr=subprocess.PIPE,
                            encoding="utf-8", errors="replace", timeout=seconds,
                            shell=False)
    try:
        value = json.loads(result.stdout)
    except (ValueError, TypeError):
        value = None
    return result, value


def notify(options, report, report_path):
    notification = report["notification"]
    deadline = time.monotonic() + options.notify_timeout

    def remaining():
        budget = deadline - time.monotonic()
        if budget <= 0:
            raise subprocess.TimeoutExpired("nb1", options.notify_timeout)
        return budget

    # prepare only reads the daemon instance and generates a binding in the CLI.
    try:
        seconds = remaining()
        result, binding = cli_json(cli_prefix(options, seconds) + ["prepare"], seconds)
    except (OSError, subprocess.TimeoutExpired) as error:
        notification.update(state="not_submitted", error=str(error))
        return
    if (result.returncode != 0 or not isinstance(binding, dict)
            or not isinstance(binding.get("request_id"), str) or not binding["request_id"]
            or not isinstance(binding.get("expected_daemon_instance_id"), str)
            or not binding["expected_daemon_instance_id"]):
        notification.update(state="not_submitted", error="prepare did not return a valid binding",
                            cli_exit_code=result.returncode, cli_stderr=result.stderr)
        return

    notification.update(state="prepared", binding=binding)
    notification["receipt_lookup_argv"] = cli_prefix(options, options.notify_timeout) + [
        "request", options.serial, binding["request_id"],
        "--expected-daemon-instance-id", binding["expected_daemon_instance_id"]]
    # If this fails, no POST has been attempted. Never lose the binding and then
    # submit anyway. The report includes the exact Program and admission policy.
    save_report(report_path, report)

    with tempfile.TemporaryDirectory(prefix="nb1-notify-") as directory:
        path = Path(directory) / "notification.program.json"
        path.write_text(json.dumps(notification["program"]), encoding="utf-8")
        try:
            seconds = remaining()
        except subprocess.TimeoutExpired as error:
            notification.update(state="not_submitted", error=str(error))
            return
        argv = cli_prefix(options, seconds) + [
            "run", options.serial, str(path), "--policy", options.policy,
            "--request-id", binding["request_id"],
            "--expected-daemon-instance-id", binding["expected_daemon_instance_id"], "--wait"]
        # From this point, any interrupted/invalid CLI result is conservative:
        # the daemon may already have admitted the task. There is no second POST.
        notification.update(state="unknown", outcome_unknown=True)
        save_report(report_path, report)
        try:
            result, job = cli_json(argv, seconds)
        except (OSError, subprocess.TimeoutExpired) as error:
            notification["error"] = str(error)
            return
        notification.update(cli_exit_code=result.returncode, cli_stderr=result.stderr)
        if (isinstance(job, dict) and isinstance(job.get("id"), str) and job["id"]
                and job.get("device") == options.serial
                and job.get("program") == notification["program"]
                and job.get("status") in ("succeeded", "completed", "failed", "cancelled")):
            notification.update(job=job, outcome_unknown=False)
            if result.returncode == 0 and job["status"] in ("succeeded", "completed"):
                notification.update(state="succeeded", final_off="daemon_job_confirmed")
            else:
                notification.update(state="failed", final_off="not_confirmed")
        else:
            notification["error"] = "CLI did not confirm a matching terminal job; query the original receipt"


def parse_args(argv):
    parser = argparse.ArgumentParser(description=__doc__)
    modes = parser.add_mutually_exclusive_group()
    modes.add_argument("--execute", action="store_true", help="run the command and submit one light notification")
    modes.add_argument("--dry-run", action="store_true", help="preview only (the default); run neither command nor CLI")
    parser.add_argument("--nb1-binary", default="nb1", help="explicit nb1 executable or a name on PATH")
    parser.add_argument("--endpoint", default="http://127.0.0.1:8931")
    parser.add_argument("--serial", required=True, help="explicit daemon-configured device serial")
    parser.add_argument("--report", type=Path, help="new JSON report path, required for --execute; never overwrites an old run")
    parser.add_argument("--success-color", default="#00a000")
    parser.add_argument("--failure-color", default="#b40000")
    parser.add_argument("--duration-ms", type=int, default=1500)
    parser.add_argument("--notify-timeout", type=float, default=10.0, help="shared prepare/submit/wait budget in seconds")
    parser.add_argument("--policy", choices=("reject", "queue", "replace"), default="reject")
    parser.add_argument("command", nargs=argparse.REMAINDER, help="-- command and its literal argv")
    options = parser.parse_args(argv)
    if options.command and options.command[0] == "--":
        options.command = options.command[1:]
    if not options.command:
        parser.error("provide a command after --")
    if not re.fullmatch(r"[A-Za-z0-9_-]{1,128}", options.serial):
        parser.error("--serial must contain 1–128 letters, digits, underscores or hyphens")
    for color in (options.success_color, options.failure_color):
        if not re.fullmatch(r"#[0-9a-fA-F]{6}", color):
            parser.error("colors must be exact #RRGGBB values")
    options.success_color, options.failure_color = options.success_color.lower(), options.failure_color.lower()
    if not 10 <= options.duration_ms <= 10000 or options.duration_ms % 10:
        parser.error("--duration-ms must be 10–10000 in 10ms units")
    if not math.isfinite(options.notify_timeout) or not 0 < options.notify_timeout <= 120:
        parser.error("--notify-timeout must be finite, positive and at most 120 seconds")
    if options.execute and options.report is None:
        parser.error("--execute requires a new --report path to preserve the binding before submission")
    return options


def main(argv=None):
    options = parse_args(argv)
    report = {"schema_version": 1, "mode": "execute" if options.execute else "dry_run",
              "command": {"argv": options.command, "state": "not_run", "exit_code": None},
              "notification": {"state": "not_submitted", "serial": options.serial,
                               "endpoint": options.endpoint, "policy": options.policy,
                               "outcome_unknown": False, "final_off": "not_confirmed"}}
    if not options.execute:
        report["notification"]["success_program"] = program(options.success_color, options.duration_ms)
        report["notification"]["failure_program"] = program(options.failure_color, options.duration_ms)
        print(json.dumps(report, ensure_ascii=False, indent=2))
        return 0
    try:
        save_report(options.report, report, create=True)
    except OSError as error:
        print("nb1-notify: cannot create a new report: {}".format(error), file=sys.stderr)
        return 2
    try:
        # Keep command output intact. No string joining, eval or implicit shell.
        result = subprocess.run(options.command, shell=False)
        report["command"].update(state="exited", exit_code=result.returncode)
    except OSError as error:
        report["command"].update(state="launch_failed", exit_code=127, error=str(error))
    except KeyboardInterrupt:
        report["command"].update(state="interrupted", exit_code=130)
    exit_code = report["command"]["exit_code"]
    report["notification"]["program"] = program(options.success_color if exit_code == 0 else options.failure_color,
                                                  options.duration_ms)
    try:
        if report["command"]["state"] != "interrupted":
            notify(options, report, options.report)
    except (OSError, KeyboardInterrupt) as error:
        report["notification"]["error"] = str(error) or "interrupted"
    try:
        save_report(options.report, report)
    except OSError as error:
        report["report_write_error"] = str(error)
    print("nb1-notify result: " + json.dumps(report, ensure_ascii=False), file=sys.stderr)
    # Notification failure never changes the build/test result. Read the report
    # separately if an automation needs to require a successful light result.
    return exit_code if exit_code >= 0 else 128 - exit_code


if __name__ == "__main__":
    sys.exit(main())
