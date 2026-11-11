#!/usr/bin/env python3
"""Validate Actions candidates without enumerating USB or managing a daemon."""

import argparse
import hashlib
import json
import re
import shutil
import subprocess
import tempfile
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
MODULES = ("github.com/b1ug/blink1-go", "github.com/b1ug/gid")
NATIVE_TARGETS = {("linux", "amd64"), ("linux", "arm64"),
                  ("darwin", "amd64"), ("darwin", "arm64"), ("windows", "amd64")}
HELP_CHECKS = [
    (["--help"], ["Usage:", "nb1 [command]"]),
    (["control", "--help"], ["nb1 control [command]", "preview", "run", "job", "cancel"]),
    (["control", "run", "--help"], ["nb1 control run SERIAL FILE", "--id", "--wait"]),
]


def command(args, cwd=ROOT):
    return subprocess.run([str(a) for a in args], cwd=cwd, check=True,
                          capture_output=True, text=True, timeout=90).stdout


def digest(path):
    return hashlib.sha256(path.read_bytes()).hexdigest()


def write_json(path, value):
    path.write_text(json.dumps(value, ensure_ascii=False, indent=2) + "\n", encoding="utf-8")


def check_smoke(value):
    """Check CLI help evidence, without claiming compilation or execution."""
    if (not isinstance(value, dict) or set(value) != {"schema_version", "validation", "checks"}
            or type(value["schema_version"]) is not int or value["schema_version"] != 1
            or value["validation"] != "cli_help_only" or not isinstance(value["checks"], list)
            or len(value["checks"]) != len(HELP_CHECKS)):
        raise ValueError("expected only the bounded CLI help smoke evidence")
    for check, (arguments, markers) in zip(value["checks"], HELP_CHECKS):
        if (not isinstance(check, dict) or set(check) != {"arguments", "stdout"}
                or check["arguments"] != arguments or not isinstance(check["stdout"], str)
                or any(marker not in check["stdout"] for marker in markers)):
            raise ValueError("CLI help did not expose the expected API v1 command")
    return value


def smoke(binary):
    # Every invocation ends at Cobra help. No input, endpoint, server or device
    # command is executed. The explicit private config avoids user settings.
    with tempfile.TemporaryDirectory(prefix="nb1-build-smoke-") as temporary:
        scratch = Path(temporary)
        config = scratch / "setting.yaml"
        config.write_text("{}\n", encoding="utf-8")
        checks = [{"arguments": arguments,
                   "stdout": command([binary, "--config", config, *arguments], scratch)}
                  for arguments, _ in HELP_CHECKS]
        if config.read_text(encoding="utf-8") != "{}\n" or sorted(p.name for p in scratch.iterdir()) != ["setting.yaml"]:
            raise ValueError("CLI help changed the isolated configuration directory")
        return check_smoke({"schema_version": 1, "validation": "cli_help_only", "checks": checks})


def module_documents(text):
    decoder, result, rest = json.JSONDecoder(), [], text.lstrip()
    while rest:
        value, end = decoder.raw_decode(rest)
        result.append(value)
        rest = rest[end:].lstrip()
    if len(result) != 2 or {d.get("Path") for d in result} != set(MODULES):
        raise ValueError("expected exactly the SDK and GID public modules")
    for item in result:
        if item.get("Replace") is not None or item.get("Main") or not item.get("Version") or not item.get("Sum"):
            raise ValueError("a workspace/local replacement is not a public-module candidate")
    return [{key: d[key] for key in ("Path", "Version", "Sum")} for d in result]


def check_build_info(text, expected_source, expected_os, expected_arch, modules):
    if not re.fullmatch(r"[0-9a-f]{40}", expected_source):
        raise ValueError("expected-source must be a full commit")
    settings, dependencies, module_path = {}, {}, None
    for line in text.splitlines()[1:]:
        fields = line.strip().split()
        if not fields:
            continue
        if fields[0] == "path" and len(fields) == 2:
            module_path = fields[1]
        elif fields[0] == "build" and len(fields) == 2 and "=" in fields[1]:
            key, value = fields[1].split("=", 1)
            settings[key] = value
        elif fields[0] == "dep" and len(fields) == 4:
            dependencies[fields[1]] = (fields[2], fields[3])
        elif fields[0] == "=>":
            raise ValueError("built executable contains a replacement module")
    if (module_path != "github.com/b1ug/nb1" or (expected_os, expected_arch) not in NATIVE_TARGETS
            or settings.get("GOOS") != expected_os or settings.get("GOARCH") != expected_arch
            or settings.get("CGO_ENABLED") != "1" or settings.get("vcs.revision") != expected_source
            or settings.get("vcs.modified") != "false"):
        raise ValueError("binary is not the expected clean native-CGO source/target")
    for item in modules:
        if dependencies.get(item["Path"]) != (item["Version"], item["Sum"]):
            raise ValueError("binary dependency differs from the public module selection")
    return {key: settings[key] for key in ("GOOS", "GOARCH", "CGO_ENABLED", "vcs.revision", "vcs.modified")}


def verify(directory):
    if directory.is_symlink() or not directory.is_dir():
        raise ValueError("candidate must be a directory, not a symlink")
    manifest = json.loads((directory / "build.json").read_text(encoding="utf-8"))
    binary = "nb1.exe" if manifest["target"]["GOOS"] == "windows" else "nb1"
    names = ["LICENSE", "build.json", binary, "cli-help.json"]
    if sorted(p.name for p in directory.iterdir()) != sorted(names + ["SHA256SUMS"]):
        raise ValueError("candidate contains unexpected or missing files")
    if any((directory / name).is_symlink() or not (directory / name).is_file() for name in names + ["SHA256SUMS"]):
        raise ValueError("candidate files must be regular files")
    expected = "".join(digest(directory / name) + "  " + name + "\n" for name in sorted(names))
    if (directory / "SHA256SUMS").read_text(encoding="utf-8") != expected:
        raise ValueError("candidate SHA256SUMS mismatch")
    check_smoke(json.loads((directory / "cli-help.json").read_text(encoding="utf-8")))


def stage(args):
    binary, output = Path(args.binary).resolve(strict=True), Path(args.out)
    if output.exists() or output.is_symlink():
        raise ValueError("--out must be new; existing data is never overwritten")
    source = command(["git", "rev-parse", "HEAD"]).strip()
    if source != args.expected_source or command(["git", "status", "--porcelain"]):
        raise ValueError("source must be the expected clean checkout")
    environment = json.loads(command(["go", "env", "-json", "GOVERSION", "GOOS", "GOARCH",
                                      "CGO_ENABLED", "CC", "GOWORK", "GOTOOLCHAIN"]))
    if (environment["GOWORK"] != "off" or environment["CGO_ENABLED"] != "1"
            or environment["GOOS"] != args.expected_os or environment["GOARCH"] != args.expected_arch):
        raise ValueError("stage needs a native toolchain with CGO=1 and GOWORK=off")
    modules = module_documents(command(["go", "list", "-mod=readonly", "-m", "-json", *MODULES]))
    target = check_build_info(command(["go", "version", "-m", binary]), source,
                              args.expected_os, args.expected_arch, modules)
    help_evidence = smoke(binary)
    web_lock = json.loads((ROOT / "internal/webui/manifest.lock.json").read_text(encoding="utf-8"))
    if digest(ROOT / "internal/webui/dist/manifest.json") != web_lock["manifest_sha256"]:
        raise ValueError("embedded Web manifest differs from its pinned lock")
    # No output before all source, toolchain and CLI help checks pass.
    output.mkdir(mode=0o700)
    shutil.copy2(binary, output / ("nb1.exe" if args.expected_os == "windows" else "nb1"))
    shutil.copyfile(ROOT / "LICENSE", output / "LICENSE")
    write_json(output / "build.json", {"schema_version": 2, "artifact_kind": "actions-candidate",
               "release": False, "source": source, "target": target, "toolchain": environment,
               "modules": modules, "embedded_web": web_lock,
               "scope": "Native build and CLI help only; no preview compilation, daemon, physical USB or minimum-OS acceptance."})
    write_json(output / "cli-help.json", help_evidence)
    names = sorted(p.name for p in output.iterdir())
    (output / "SHA256SUMS").write_text("".join(digest(output / name) + "  " + name + "\n" for name in names), encoding="utf-8")
    verify(output)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    sub = parser.add_subparsers(dest="operation", required=True)
    smoke_parser = sub.add_parser("smoke", help="Check local CLI help only; no preview or runtime validation")
    smoke_parser.add_argument("--binary", required=True)
    package = sub.add_parser("stage")
    for name in ("binary", "out", "expected-source", "expected-os", "expected-arch"):
        package.add_argument("--" + name, required=True)
    check = sub.add_parser("verify")
    check.add_argument("--out", required=True)
    args = parser.parse_args()
    if args.operation == "smoke":
        print(json.dumps(smoke(Path(args.binary).resolve(strict=True))))
    elif args.operation == "stage":
        stage(args)
        print("candidate staged and verified; no USB execution")
    else:
        verify(Path(args.out))
        print("candidate checksums and CLI help evidence verified; no preview or runtime validation")


if __name__ == "__main__":
    main()
