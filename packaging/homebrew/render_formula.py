#!/usr/bin/env python3
"""Offline source-Formula draft renderer; never fetch, install, tap or publish."""

import argparse
import json
import re
from pathlib import Path

DIRECTORY = Path(__file__).resolve().parent
FIELDS = {"schema_version", "fixture_only", "version", "source_url", "source_sha256",
          "source_commit", "min_macos", "components"}


def pairs(items):
    result = {}
    for key, value in items:
        if key in result:
            raise ValueError("duplicate input field: " + key)
        result[key] = value
    return result


def read_input(path):
    with path.open("rb") as stream:
        data = stream.read(65537)
    if len(data) > 65536:
        raise ValueError("release input exceeds 64 KiB")
    return json.loads(data.decode("utf-8"), object_pairs_hook=pairs,
                      parse_constant=lambda value: (_ for _ in ()).throw(ValueError("invalid JSON number")))


def validate(value):
    if not isinstance(value, dict) or set(value) != FIELDS:
        raise ValueError("exact explicit release fields are required; no defaults or unknown fields")
    if type(value["schema_version"]) is not int or value["schema_version"] != 1:
        raise ValueError("unsupported input schema")
    if type(value["fixture_only"]) is not bool:
        raise ValueError("fixture_only must be an explicit boolean")
    missing = [name for name in sorted(FIELDS - {"schema_version", "fixture_only"})
               if not isinstance(value[name], str) or not value[name]]
    if missing:
        raise ValueError("explicit nonempty release values required: " + ", ".join(missing))
    version = value["version"]
    if len(version) > 128 or not re.fullmatch(
            r"(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(?:-[0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*)?", version):
        raise ValueError("version must be SemVer without a leading v or build metadata")
    if "-" in version:
        for part in version.split("-", 1)[1].split("."):
            if part.isdigit() and len(part) > 1 and part.startswith("0"):
                raise ValueError("numeric prerelease components cannot have leading zeroes")
    if tuple(map(int, version.split("-", 1)[0].split("."))) <= (0, 0, 1):
        raise ValueError("historical v0.0.1 does not provide this draft's high-level preview test")
    expected_url = "https://github.com/b1ug/nb1/archive/refs/tags/v" + version + ".tar.gz"
    if value["source_url"] != expected_url:
        raise ValueError("source_url must be the exact versioned upstream tag archive; no Actions/latest/branch URL")
    for name, length in [("source_sha256", 64), ("source_commit", 40)]:
        text = value[name]
        if not re.fullmatch(r"[0-9a-f]{" + str(length) + r"}", text) or len(set(text)) < 2:
            raise ValueError(name + " must be an explicit lowercase digest, not a placeholder")
    if value["min_macos"] not in {"sequoia", "tahoe"}:
        raise ValueError("min_macos must be an explicit supported draft symbol: sequoia or tahoe")
    if value["components"] != "nb1-only":
        raise ValueError("this draft supports nb1-only; MCP bundling is not implemented or decided")
    return dict(value)


def render(value, allow_fixture=False):
    value = validate(value)
    if value["fixture_only"] and not allow_fixture:
        raise ValueError("synthetic fixture inputs can be checked in memory, never written as a Formula")
    template = (DIRECTORY / "nb1.rb.in").read_text(encoding="utf-8")
    for name in ("source_url", "version", "source_sha256", "source_commit", "min_macos"):
        token = "@@" + name.upper() + "@@"
        if token not in template:
            raise ValueError("template token missing: " + name)
        template = template.replace(token, json.dumps(value[name], ensure_ascii=True))
    if "@@" in template:
        raise ValueError("unresolved template token")
    return template


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--input", type=Path, required=True)
    action = parser.add_mutually_exclusive_group(required=True)
    action.add_argument("--check-only", action="store_true")
    action.add_argument("--out", type=Path)
    args = parser.parse_args()
    try:
        data = validate(read_input(args.input))
        text = render(data, allow_fixture=args.check_only)
        if args.out is not None:
            if args.out.suffix != ".rb":
                raise ValueError("--out must be an explicit new .rb file in a local draft directory")
            with args.out.open("x", encoding="utf-8") as stream:
                stream.write(text)
        print(json.dumps({"input_valid": True, "fixture_only": data["fixture_only"],
                          "formula_written": args.out is not None,
                          "remote_url_and_source_identity_verified": False,
                          "homebrew_install_or_audit_performed": False}))
    except (OSError, UnicodeError, ValueError, TypeError) as error:
        parser.exit(1, "Formula draft not generated: " + str(error) + "\n")


if __name__ == "__main__":
    main()
