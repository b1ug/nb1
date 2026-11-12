#!/usr/bin/env python3
"""Notify through nb1 when Codex supplies an agent-turn-complete argv event."""

import argparse
import hashlib
import json
import os
from pathlib import Path
import re
import stat
import sys

from notify import action, notify, save_report


MAX_EVENT_BYTES = 256 * 1024
MAX_ID_BYTES = 256


class ArgumentParser(argparse.ArgumentParser):
    def error(self, _message):
        # argparse's default diagnostic can echo the private JSON argv.
        self.exit(1, "nb1-codex-notify: invalid arguments; use --help\n")


def parse_args(argv):
    parser = ArgumentParser(description=__doc__)
    parser.add_argument("--execute", action="store_true", help="enable one neutral light notification")
    parser.add_argument("--nb1-binary", help="absolute path to the nb1 executable")
    parser.add_argument("--endpoint", help="explicit loopback HTTP daemon origin with a port")
    parser.add_argument("--serial", help="daemon-configured device serial")
    parser.add_argument("--report-dir", type=Path, help="existing absolute private directory for Job reports")
    parser.add_argument("event", nargs="?", help="single JSON argument appended by Codex notify (not stdin)")
    parser.set_defaults(notify_timeout=4.0)
    options = parser.parse_args(argv)
    if options.execute:
        binary = Path(options.nb1_binary or "")
        if not binary.is_absolute() or not binary.is_file() or not os.access(binary, os.X_OK):
            parser.error("--execute requires an absolute executable --nb1-binary")
        origin = re.fullmatch(r"http://(?:127\.0\.0\.1|\[::1\]):([0-9]{1,5})/?", options.endpoint or "")
        if not origin or not 1 <= int(origin[1]) <= 65535:
            parser.error("--execute requires an explicit loopback origin and port")
        options.endpoint = options.endpoint.rstrip("/")
        if not re.fullmatch(r"[A-Za-z0-9_-]{1,128}", options.serial or ""):
            parser.error("--execute requires a valid --serial")
        if not options.report_dir or not options.report_dir.is_absolute():
            parser.error("--execute requires an existing absolute private --report-dir")
        try:
            info = options.report_dir.lstat()
        except OSError:
            parser.error("--report-dir must already exist")
        if not stat.S_ISDIR(info.st_mode):
            parser.error("--report-dir must be a directory, not a symlink")
        if os.name == "posix" and (info.st_uid != os.getuid() or stat.S_IMODE(info.st_mode) & 0o077):
            parser.error("--report-dir must be owned by this user and private")
    return options


def event_key(raw):
    if not isinstance(raw, str):
        return None
    try:
        if len(raw.encode("utf-8")) > MAX_EVENT_BYTES:
            return None
        event = json.loads(raw)
        if not isinstance(event, dict) or event.get("type") != "agent-turn-complete":
            return None
        ids = [event.get("thread-id"), event.get("turn-id")]
        if not all(isinstance(value, str) and value.strip()
                   and len(value.encode("utf-8")) <= MAX_ID_BYTES for value in ids):
            return None
    except (ValueError, UnicodeError, RecursionError):
        return None
    return hashlib.sha256(json.dumps(ids, ensure_ascii=True, separators=(",", ":")).encode("ascii")).hexdigest()


def main(argv=None):
    try:
        options = parse_args(argv)
    except SystemExit as error:
        return 0 if error.code == 0 else 1
    key = event_key(options.event)
    if key is None:
        return 0
    if not options.execute:
        print("nb1-codex-notify: preview of a neutral turn-ended notification; nothing submitted", file=sys.stderr)
        return 0

    path = options.report_dir / ("codex-notify-" + key + ".json")
    report = {"schema_version": 2, "mode": "execute", "trigger": "codex_notify", "meaning": "turn_ended",
              "notification": {"state": "not_submitted", "serial": options.serial, "endpoint": options.endpoint,
                               "outcome_unknown": False, "final_off": "not_confirmed",
                               "input": {"action": action("#365f9f", 1500)}}}
    try:
        # The receipt is the concurrency gate. An existing receipt is never
        # resumed or overwritten, even when its earlier outcome is unknown.
        save_report(path, report, create=True)
    except FileExistsError:
        return 0
    except OSError as error:
        print("nb1-codex-notify: cannot create report: {}".format(error), file=sys.stderr)
        return 0
    try:
        # notify persists its short Job ID before one CLI submission and retains
        # the original lookup argv after uncertainty. No event data is executed.
        notify(options, report, path)
    except (Exception, KeyboardInterrupt) as error:
        report["notification"]["error"] = str(error) or "interrupted"
    try:
        save_report(path, report)
    except OSError as error:
        print("nb1-codex-notify: cannot update report: {}".format(error), file=sys.stderr)
    print("nb1-codex-notify: {}; report={}".format(report["notification"]["state"], path), file=sys.stderr)
    # notify is an advisory external program, not a Stop hook response. Keep
    # stdout empty and never change the main task outcome on notification error.
    return 0


if __name__ == "__main__":
    sys.exit(main())
