#!/usr/bin/env python3
"""Run an argv command, then notify through the current nb1 Job CLI."""

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
import uuid


def action(color, duration_ms):
    # One blink holds the color, then black, and completes with off cleanup.
    return {"kind": "blink", "led": 0, "color": color, "on_ms": duration_ms,
            "off_ms": 10, "cycles": 1, "on_finish": "off"}


def save_report(path, report, create=False):
    """Keep the original Job ID and input durable before the single submission."""
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
    return [options.nb1_binary, "control", "--endpoint",
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

    job_id = "notify_" + uuid.uuid4().hex
    notification["job_id"] = job_id
    notification["job_lookup_argv"] = cli_prefix(options, options.notify_timeout) + ["job", job_id]
    # Never submit unless the exact input and original Job ID are durable.
    save_report(report_path, report)

    with tempfile.TemporaryDirectory(prefix="nb1-notify-") as directory:
        path = Path(directory) / "notification.action.json"
        path.write_text(json.dumps(notification["input"]), encoding="utf-8")
        try:
            seconds = remaining()
        except subprocess.TimeoutExpired as error:
            notification.update(state="not_submitted", error=str(error))
            return
        argv = cli_prefix(options, seconds) + [
            "run", options.serial, str(path), "--id", job_id, "--wait"]
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
        if (isinstance(job, dict) and job.get("id") == job_id
                and job.get("serial") == options.serial
                and job.get("status") in ("succeeded", "failed", "cancelled")):
            notification.update(job=job, outcome_unknown=False)
            if result.returncode == 0 and job["status"] == "succeeded":
                notification.update(state="succeeded", final_off="daemon_job_confirmed")
            else:
                notification.update(state=job["status"] if job["status"] != "succeeded" else "failed",
                                    final_off="not_confirmed")
        else:
            notification["error"] = "CLI did not confirm a matching terminal Job; query the original Job ID"


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
    parser.add_argument("--notify-timeout", type=float, default=10.0, help="shared submit/wait budget in seconds")
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
        parser.error("--execute requires a new --report path to preserve the Job ID before submission")
    return options


def main(argv=None):
    options = parse_args(argv)
    report = {"schema_version": 2, "mode": "execute" if options.execute else "dry_run",
              "command": {"argv": options.command, "state": "not_run", "exit_code": None},
              "notification": {"state": "not_submitted", "serial": options.serial,
                               "endpoint": options.endpoint,
                               "outcome_unknown": False, "final_off": "not_confirmed"}}
    if not options.execute:
        report["notification"]["success_input"] = {"action": action(options.success_color, options.duration_ms)}
        report["notification"]["failure_input"] = {"action": action(options.failure_color, options.duration_ms)}
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
    report["notification"]["input"] = {"action": action(options.success_color if exit_code == 0 else options.failure_color,
                                                       options.duration_ms)}
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
