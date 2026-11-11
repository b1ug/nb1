#!/usr/bin/env python3
"""Send a neutral light notification when a Codex turn ends."""

import argparse
import hashlib
import json
from pathlib import Path
import re
import sys

from notify import action, notify, save_report


MAX_EVENT_BYTES = 256 * 1024


def parse_args(argv):
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--execute", action="store_true", help="enable light notification")
    parser.add_argument("--nb1-binary", help="absolute path to the nb1 executable")
    parser.add_argument("--endpoint", help="explicit loopback HTTP daemon origin")
    parser.add_argument("--serial", help="daemon-configured device serial")
    parser.add_argument("--report-dir", type=Path, help="existing private directory for Job reports")
    parser.set_defaults(notify_timeout=4.0)
    options = parser.parse_args(argv)
    if options.execute:
        if not options.nb1_binary or not Path(options.nb1_binary).is_absolute():
            parser.error("--execute requires an absolute --nb1-binary path")
        origin = re.fullmatch(r"http://(?:127\.0\.0\.1|\[::1\]):([0-9]{1,5})/?", options.endpoint or "")
        if not origin or not 1 <= int(origin[1]) <= 65535:
            parser.error("--execute requires an explicit loopback HTTP --endpoint with a port")
        if not re.fullmatch(r"[A-Za-z0-9_-]{1,128}", options.serial or ""):
            parser.error("--execute requires a valid --serial")
        if not options.report_dir or not options.report_dir.is_absolute() or not options.report_dir.is_dir():
            parser.error("--execute requires an existing absolute --report-dir")
    return options


def initial_stop_key(raw):
    if len(raw) > MAX_EVENT_BYTES:
        return None
    try:
        event = json.loads(raw)
    except (ValueError, RecursionError):
        return None
    if (not isinstance(event, dict) or event.get("hook_event_name") != "Stop"
            or event.get("stop_hook_active") is not False):
        return None
    ids = [event.get("session_id"), event.get("turn_id")]
    if not all(isinstance(value, str) and value for value in ids):
        return None
    return hashlib.sha256(json.dumps(ids, ensure_ascii=True).encode("ascii")).hexdigest()


def main(argv=None):
    try:
        options = parse_args(argv)
    except SystemExit as error:
        # Codex interprets exit 2 from a Stop hook as a request to continue.
        if error.code == 0:
            raise
        return 1

    key = initial_stop_key(sys.stdin.buffer.read(MAX_EVENT_BYTES + 1))
    if key is None:
        return 0
    if not options.execute:
        print("nb1-codex-stop: preview of a neutral turn-ended notification", file=sys.stderr)
        return 0

    path = options.report_dir / ("codex-stop-" + key + ".json")
    report = {"schema_version": 2, "mode": "execute", "trigger": "codex_stop",
              "notification": {"state": "not_submitted", "serial": options.serial,
                               "endpoint": options.endpoint,
                               "outcome_unknown": False, "final_off": "not_confirmed",
                               "input": {"action": action("#365f9f", 1500)}}}
    try:
        save_report(path, report, create=True)
    except FileExistsError:
        return 0
    except OSError as error:
        print("nb1-codex-stop: cannot create report: {}".format(error), file=sys.stderr)
        return 0

    try:
        notify(options, report, path)
    except (OSError, KeyboardInterrupt) as error:
        report["notification"]["error"] = str(error) or "interrupted"
    try:
        save_report(path, report)
    except OSError as error:
        print("nb1-codex-stop: cannot update report: {}".format(error), file=sys.stderr)
    return 0


if __name__ == "__main__":
    code = main()
    if code == 0:
        print("{}")
    sys.exit(code)
