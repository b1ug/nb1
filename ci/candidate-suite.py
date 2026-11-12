#!/usr/bin/env python3
"""Package a source-bound CLI, MCP and Skill candidate; never install or play it."""

import argparse
import gzip
import hashlib
import importlib.util
import io
import json
import os
from pathlib import Path, PurePosixPath
import re
import shutil
import stat
import subprocess
import tarfile
import tempfile
import zipfile

SPEC = importlib.util.spec_from_file_location("nb1_build_artifact", Path(__file__).with_name("check-build-artifact.py"))
CLI = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(CLI)
CORE = "github.com/b1ug/nb1"
MCP = CORE + "/adapters/mcp"
MCP_SDK = "github.com/modelcontextprotocol/go-sdk"
SKILL_FILES = {"SKILL.md", "agents/openai.yaml", "references/expressions.md",
               "references/direct-and-config.md", "references/patterns.md"}
MAX_BYTES = 128 * 1024 * 1024
JSON_BYTES = 2 * 1024 * 1024
SCOPE = "Candidate integrity and source binding only; no installation, runtime, USB, minimum-OS or signature acceptance."
MCP_README = b"""# nb1 MCP candidate

This is a candidate, not a published Release. Check the matching suite.json and
SHA256SUMS before use. The matching nb1 CLI contains the embedded Web debugger;
this MCP executable does not embed that Web bundle.

Start the intended nb1 daemon explicitly, then configure your MCP host to run:

    nb1-mcp --endpoint http://127.0.0.1:8931 --timeout 10s

On Windows the executable is nb1-mcp.exe. Stdout belongs to the MCP protocol.
The adapter uses HTTP, never opens USB and does not start a daemon or install
itself. Accepted Jobs continue when the MCP session closes.
"""


def sha(data):
    return hashlib.sha256(data).hexdigest()


def json_bytes(value):
    return (json.dumps(value, ensure_ascii=False, indent=2, sort_keys=True) + "\n").encode("utf-8")


def no_duplicate_keys(pairs):
    value = {}
    for key, item in pairs:
        if key in value:
            raise ValueError("duplicate JSON key: " + key)
        value[key] = item
    return value


def read_json(data):
    if len(data) > JSON_BYTES:
        raise ValueError("JSON exceeds size limit")
    return json.loads(data, object_pairs_hook=no_duplicate_keys)


def regular_bytes(path, limit=MAX_BYTES):
    if path.is_symlink() or not path.is_file() or path.stat().st_size > limit:
        raise ValueError("expected a bounded regular file: " + str(path))
    with path.open("rb") as stream:
        data = stream.read(limit + 1)
    if len(data) > limit:
        raise ValueError("file exceeds size limit")
    return data


def command(arguments, cwd):
    environment = dict(os.environ, GOWORK="off", GOTOOLCHAIN="local", GOFLAGS="")
    return subprocess.run([str(a) for a in arguments], cwd=cwd, env=environment,
                          capture_output=True, text=True, encoding="utf-8", check=True,
                          timeout=90).stdout


def documents(text):
    decoder = json.JSONDecoder(object_pairs_hook=no_duplicate_keys)
    values, rest = [], text.lstrip()
    while rest:
        value, end = decoder.raw_decode(rest)
        values.append(value)
        rest = rest[end:].lstrip()
    return values


def build_info(text):
    """Keep Go's identity and dependencies, without the local executable path."""
    lines = text.splitlines()
    if not lines or ": go" not in lines[0]:
        raise ValueError("missing Go build info")
    result = {"go_version": lines[0].rsplit(": ", 1)[1], "module": None,
              "dependencies": [], "settings": {}}
    last_dependency = None
    for line in lines[1:]:
        parts = line.strip().split()
        if not parts:
            continue
        if parts[0] == "path":
            if len(parts) != 2 or result["module"] is not None:
                raise ValueError("invalid or duplicate module path")
            result["module"] = parts[1]
        elif parts[0] == "build":
            key, separator, value = " ".join(parts[1:]).partition("=")
            if not separator or key in result["settings"]:
                raise ValueError("invalid or duplicate build setting")
            result["settings"][key] = value
        elif parts[0] == "dep":
            if len(parts) not in (3, 4) or any(d["Path"] == parts[1] for d in result["dependencies"]):
                raise ValueError("invalid or duplicate dependency")
            last_dependency = {"Path": parts[1], "Version": parts[2], "Sum": parts[3] if len(parts) == 4 else ""}
            result["dependencies"].append(last_dependency)
        elif parts[0] == "=>":
            if len(parts) not in (3, 4) or last_dependency is None or "Replace" in last_dependency:
                raise ValueError("invalid replacement")
            last_dependency["Replace"] = {"Path": parts[1], "Version": parts[2],
                                          "Sum": parts[3] if len(parts) == 4 else ""}
        elif parts[0] != "mod":
            raise ValueError("unknown Go build info record")
    if result["module"] is None:
        raise ValueError("missing module path")
    return result


def check_identity(info, module, source, target):
    settings = info["settings"]
    if (info["module"] != module or not re.fullmatch(r"go1\.[0-9]+(?:\.[0-9]+)?", info["go_version"])
            or settings.get("vcs") != "git" or settings.get("vcs.revision") != source
            or settings.get("vcs.modified") != "false"
            or any(settings.get(key) != value for key, value in target.items())
            or settings.get("CGO_ENABLED") not in ("0", "1")):
        raise ValueError("binary must have the expected clean source and native target")


def module_records(values, root, linked_paths):
    """Only the same-checkout core replacement is permitted, even if unlinked."""
    selected, main = {}, []
    mcp_root = root / "adapters/mcp"
    for item in values:
        name = item["Path"]
        if item.get("Main"):
            main.append(item)
            if item.get("Replace") is not None:
                raise ValueError("main module cannot be replaced")
            continue
        if name in selected:
            raise ValueError("duplicate selected module")
        record = {key: item.get(key, "") for key in ("Path", "Version", "Sum")}
        if item.get("Replace") is not None:
            replacement = item["Replace"]
            path = replacement["Path"]
            if (name != CORE or replacement.get("Version")
                    or Path(path).is_absolute() or (mcp_root / path).resolve() != root
                    or Path(replacement.get("Dir", "")).resolve() != root
                    or Path(item.get("Dir", "")).resolve() != root):
                raise ValueError("MCP replacement must resolve only to this core checkout")
            record["Replace"] = {"Path": path, "Version": "(devel)", "Sum": ""}
        elif name == CORE or not record["Version"]:
            raise ValueError("MCP dependencies must be fixed public modules")
        selected[name] = record
    if (len(main) != 1 or main[0]["Path"] != MCP
            or Path(main[0].get("Dir", "")).resolve() != mcp_root
            or CORE not in selected or MCP_SDK not in selected):
        raise ValueError("expected the nested MCP module and same-checkout core")
    if not linked_paths.issubset(selected):
        raise ValueError("linked MCP dependency missing from source selection")
    # The lazy module graph includes unused dependencies whose sums need not be
    # downloaded. Check replacements across the whole graph, but publish only
    # modules actually linked into this executable.
    records = [selected[name] for name in sorted(linked_paths)]
    if any(not item["Sum"] and "Replace" not in item for item in records):
        raise ValueError("linked MCP dependencies require public module sums")
    return records


def check_mcp(info, modules, source, target):
    check_identity(info, MCP, source, target)
    selected = {item["Path"]: item for item in modules}
    if len(selected) != len(modules) or CORE not in selected or MCP_SDK not in selected:
        raise ValueError("missing or duplicate MCP modules")
    for item in modules:
        if "Replace" in item:
            replacement = item["Replace"]
            # Canonical resolution is proved at staging; the offline receipt
            # retains a relative path and never publishes a machine-local Dir.
            if (item["Path"] != CORE or PurePosixPath(replacement["Path"]).is_absolute()
                    or replacement["Path"].replace("\\", "/") not in ("../..", "../../")
                    or replacement["Version"] != "(devel)" or replacement["Sum"]):
                raise ValueError("unexpected MCP replacement")
        elif item["Path"] == CORE or not item["Version"] or not item["Sum"]:
            raise ValueError("unfixed MCP dependency")
    linked = {item["Path"]: item for item in info["dependencies"]}
    if len(linked) != len(info["dependencies"]) or set(linked) != set(selected) or CORE not in linked or MCP_SDK not in linked:
        raise ValueError("MCP must link the matching core and MCP SDK")
    if any(item != selected.get(name) for name, item in linked.items()):
        raise ValueError("MCP binary dependencies differ from selected modules")


def checksums(files):
    return "".join(sha(data) + "  " + name + "\n" for name, data in sorted(files.items())).encode("ascii")


def archive_bytes(files, binary=None, windows=False):
    result = io.BytesIO()
    if windows:
        with zipfile.ZipFile(result, "w", compression=zipfile.ZIP_DEFLATED) as archive:
            for name, data in sorted(files.items()):
                info = zipfile.ZipInfo(name, (1980, 1, 1, 0, 0, 0))
                info.create_system = 3
                info.external_attr = (stat.S_IFREG | (0o755 if name == binary else 0o644)) << 16
                info.compress_type = zipfile.ZIP_DEFLATED
                archive.writestr(info, data)
    else:
        with gzip.GzipFile(fileobj=result, mode="wb", filename="", mtime=0) as compressed:
            with tarfile.open(fileobj=compressed, mode="w") as archive:
                for name, data in sorted(files.items()):
                    info = tarfile.TarInfo(name)
                    info.size = len(data)
                    info.mode = 0o755 if name == binary else 0o644
                    archive.addfile(info, io.BytesIO(data))
    return result.getvalue()


def read_archive(data, expected, binary=None, windows=False):
    """Read regular whitelisted files, without extractall or filesystem writes."""
    if len(data) > MAX_BYTES:
        raise ValueError("archive exceeds size limit")
    files, total = {}, 0

    def accept(name, size, mode):
        nonlocal total
        if (name not in expected or name in files or "\\" in name
                or PurePosixPath(name).is_absolute() or ".." in PurePosixPath(name).parts
                or PurePosixPath(name).as_posix() != name):
            raise ValueError("unexpected, duplicate or unsafe archive path: " + name)
        if size < 0 or mode != (0o755 if name == binary else 0o644):
            raise ValueError("invalid archive file size or executable mode")
        total += size
        if total > MAX_BYTES:
            raise ValueError("archive expanded size exceeds limit")

    if windows:
        with zipfile.ZipFile(io.BytesIO(data)) as archive:
            for info in archive.infolist():
                mode = info.external_attr >> 16
                if info.create_system != 3 or stat.S_IFMT(mode) != stat.S_IFREG or info.is_dir() or info.flag_bits & 1:
                    raise ValueError("archive entries must be unencrypted regular files")
                accept(info.filename, info.file_size, stat.S_IMODE(mode))
                files[info.filename] = archive.read(info)
    else:
        # Bound the entire expanded tar before tarfile can consume GNU/PAX
        # metadata internally, before yielding a regular member to our loop.
        with gzip.GzipFile(fileobj=io.BytesIO(data), mode="rb") as compressed:
            raw_tar = compressed.read(MAX_BYTES + 1)
        if len(raw_tar) > MAX_BYTES:
            raise ValueError("expanded tar including metadata exceeds size limit")
        with tarfile.open(fileobj=io.BytesIO(raw_tar), mode="r:") as archive:
            for info in archive:
                if not info.isfile() or info.issym() or info.islnk():
                    raise ValueError("archive entries must be regular files")
                accept(info.name, info.size, info.mode)
                with archive.extractfile(info) as stream:
                    files[info.name] = stream.read(MAX_BYTES + 1)
                if len(files[info.name]) != info.size:
                    raise ValueError("truncated archive member")
    if set(files) != set(expected):
        raise ValueError("archive has missing files")
    return files


def names(source, target):
    suffix = source[:12] + "-" + target["GOOS"] + "-" + target["GOARCH"]
    extension = ".zip" if target["GOOS"] == "windows" else ".tar.gz"
    return {"cli": "nb1-" + suffix + extension, "mcp": "nb1-mcp-" + suffix + extension,
            "skill": "blink1-control-" + source[:12] + ".zip"}


def mcp_manifest(source, target, web, component):
    return {"schema_version": 1, "artifact_kind": "mcp-candidate", "release": False,
            "source": source, "target": target, "build_info": component["build_info"],
            "modules": component["modules"], "compatible_cli_embedded_web": web, "scope": SCOPE}


def verify(directory):
    directory = Path(directory)
    if directory.is_symlink() or not directory.is_dir():
        raise ValueError("suite must be a directory, not a symlink")
    suite_data = regular_bytes(directory / "suite.json", JSON_BYTES)
    suite = read_json(suite_data)
    if (set(suite) != {"schema_version", "artifact_kind", "release", "source", "target", "embedded_web", "components", "scope"}
            or type(suite["schema_version"]) is not int or suite["schema_version"] != 1
            or suite["artifact_kind"] != "candidate-suite" or suite["release"] is not False
            or not re.fullmatch(r"[0-9a-f]{40}", suite["source"]) or suite["scope"] != SCOPE
            or set(suite["target"]) != {"GOOS", "GOARCH"}
            or tuple(suite["target"].get(key) for key in ("GOOS", "GOARCH")) not in CLI.NATIVE_TARGETS
            or set(suite["components"]) != {"cli", "mcp", "skill"}):
        raise ValueError("invalid candidate suite manifest")
    source, target, web = suite["source"], suite["target"], suite["embedded_web"]
    if (set(web) != {"schema_version", "product", "source_commit", "manifest_sha256"}
            or type(web["schema_version"]) is not int or web["schema_version"] != 1 or web["product"] != "nb1-web"
            or not re.fullmatch(r"[0-9a-f]{40}", web["source_commit"])
            or not re.fullmatch(r"[0-9a-f]{64}", web["manifest_sha256"])):
        raise ValueError("invalid embedded Web provenance")
    archive_names = names(source, target)
    if {p.name for p in directory.iterdir()} != {"suite.json", "SHA256SUMS", *archive_names.values()}:
        raise ValueError("suite contains unexpected or missing files")
    outer = {"suite.json": suite_data}
    for component, name in archive_names.items():
        record = suite["components"][component]
        expected_keys = ({"archive", "sha256", "files", "total_bytes"} if component == "skill" else
                         {"archive", "sha256", "binary", "binary_sha256", "build_info", "modules"})
        if set(record) != expected_keys or record["archive"] != name:
            raise ValueError("invalid component manifest")
        outer[name] = regular_bytes(directory / name)
        if sha(outer[name]) != record["sha256"]:
            raise ValueError("archive checksum mismatch")
    if regular_bytes(directory / "SHA256SUMS", JSON_BYTES) != checksums(outer):
        raise ValueError("suite checksum list mismatch")
    windows = target["GOOS"] == "windows"
    binaries = {"cli": "nb1" + (".exe" if windows else ""), "mcp": "nb1-mcp" + (".exe" if windows else "")}
    contents = {}
    for key, binary in binaries.items():
        record = suite["components"][key]
        expected = {binary, "LICENSE", "build.json", "SHA256SUMS", "cli-help.json" if key == "cli" else "README.md"}
        files = read_archive(outer[archive_names[key]], expected, binary, windows)
        contents[key] = files
        if record["binary"] != binary or sha(files[binary]) != record["binary_sha256"]:
            raise ValueError("binary identity mismatch")
        if files["SHA256SUMS"] != checksums({name: data for name, data in files.items() if name != "SHA256SUMS"}):
            raise ValueError("inner checksum list mismatch")
        check_identity(record["build_info"], CORE if key == "cli" else MCP, source, target)
        if key == "cli":
            build = read_json(files["build.json"])
            if (build.get("schema_version") != 2 or build.get("release") is not False
                    or build.get("source") != source or build.get("embedded_web") != web
                    or build.get("modules") != record["modules"]
                    or build.get("target") != {k: record["build_info"]["settings"][k]
                                             for k in ("GOOS", "GOARCH", "CGO_ENABLED", "vcs.revision", "vcs.modified")}
                    or record["build_info"]["settings"]["CGO_ENABLED"] != "1"
                    or any("Replace" in dependency for dependency in record["build_info"]["dependencies"])):
                raise ValueError("CLI provenance differs from suite")
            public = CLI.module_documents("\n".join(json.dumps(item) for item in record["modules"]))
            linked = {item["Path"]: item for item in record["build_info"]["dependencies"]}
            if any(linked.get(item["Path"]) != item for item in public):
                raise ValueError("CLI dependency mismatch")
            with tempfile.TemporaryDirectory(prefix="nb1-suite-verify-") as temporary:
                scratch = Path(temporary)
                for name, data in files.items():
                    (scratch / name).write_bytes(data)
                CLI.verify(scratch)
        else:
            check_mcp(record["build_info"], record["modules"], source, target)
            if (read_json(files["build.json"]) != mcp_manifest(source, target, web, record)
                    or files["README.md"] != MCP_README):
                raise ValueError("MCP provenance differs from suite")
    if contents["cli"]["LICENSE"] != contents["mcp"]["LICENSE"]:
        raise ValueError("component licenses differ")
    skill = suite["components"]["skill"]
    files = read_archive(outer[archive_names["skill"]], SKILL_FILES, windows=True)
    expected = {name: {"sha256": sha(data), "bytes": len(data)} for name, data in files.items()}
    if skill["files"] != expected or skill["total_bytes"] != sum(len(data) for data in files.values()):
        raise ValueError("Skill files differ from suite")
    return suite


def source_identity(root):
    if Path(command(["git", "rev-parse", "--show-toplevel"], root).strip()).resolve() != root:
        raise ValueError("--root must be the checkout root")
    source = command(["git", "rev-parse", "HEAD"], root).strip()
    if not re.fullmatch(r"[0-9a-f]{40}", source) or command(["git", "status", "--porcelain", "--untracked-files=all"], root):
        raise ValueError("source checkout must be clean")
    return source


def binary_build_text(data, name, root):
    # Inspect the exact bytes that will be archived, rather than a mutable
    # input path which could change between reading it and invoking Go.
    with tempfile.TemporaryDirectory(prefix="nb1-suite-buildinfo-") as temporary:
        path = Path(temporary) / name
        path.write_bytes(data)
        return command(["go", "version", "-m", path], root)


def stage(root, cli_dir, mcp_binary, output):
    root, cli_dir, mcp_binary, output = map(Path, (root, cli_dir, mcp_binary, output))
    root = root.resolve(strict=True)
    if output.exists() or output.is_symlink():
        raise ValueError("--output must be new")
    if output.resolve().is_relative_to(root):
        raise ValueError("--output must be outside the source checkout")
    source = source_identity(root)
    CLI.verify(cli_dir)
    cli_files = {p.name: regular_bytes(p) for p in cli_dir.iterdir()}
    build = read_json(cli_files["build.json"])
    target = {key: build["target"][key] for key in ("GOOS", "GOARCH")}
    binary = "nb1.exe" if target["GOOS"] == "windows" else "nb1"
    web = read_json(regular_bytes(root / "internal/webui/manifest.lock.json", JSON_BYTES))
    if (build.get("release") is not False or build.get("source") != source or build.get("embedded_web") != web
            or sha(regular_bytes(root / "internal/webui/dist/manifest.json", JSON_BYTES)) != web["manifest_sha256"]
            or cli_files["LICENSE"] != regular_bytes(root / "LICENSE", JSON_BYTES)):
        raise ValueError("CLI stage must match the current source and Web lock")
    module_text = command(["go", "list", "-mod=readonly", "-m", "-json", *CLI.MODULES], root)
    cli_modules = CLI.module_documents(module_text)
    cli_text = binary_build_text(cli_files[binary], binary, root)
    CLI.check_build_info(cli_text, source, target["GOOS"], target["GOARCH"], cli_modules)
    if build.get("modules") != cli_modules:
        raise ValueError("CLI staged dependencies differ from source")
    cli_info = build_info(cli_text)
    mcp_data = regular_bytes(mcp_binary)
    mcp_root = root / "adapters/mcp"
    mcp_info = build_info(binary_build_text(mcp_data, "nb1-mcp" + (".exe" if target["GOOS"] == "windows" else ""), mcp_root))
    modules = module_records(documents(command(["go", "list", "-mod=readonly", "-m", "-json", "all"], mcp_root)), root,
                             {item["Path"] for item in mcp_info["dependencies"]})
    check_mcp(mcp_info, modules, source, target)
    skill_root = root / "skills/blink1-control"
    entries = list(skill_root.rglob("*"))
    if (skill_root.is_symlink() or any(p.is_symlink() for p in entries)
            or {p.relative_to(skill_root).as_posix() for p in entries if p.is_file()} != SKILL_FILES
            or {p.relative_to(skill_root).as_posix() for p in entries if p.is_dir()} != {"agents", "references"}):
        raise ValueError("Skill source must contain exactly the five production files")
    skill_files = {name: regular_bytes(skill_root / name, JSON_BYTES) for name in SKILL_FILES}
    if source_identity(root) != source:
        raise ValueError("source changed during staging")
    archive_names = names(source, target)
    components = {
        "cli": {"archive": archive_names["cli"], "binary": binary, "binary_sha256": sha(cli_files[binary]),
                "build_info": cli_info, "modules": cli_modules},
        "mcp": {"archive": archive_names["mcp"], "binary": "nb1-mcp" + (".exe" if target["GOOS"] == "windows" else ""),
                "binary_sha256": sha(mcp_data), "build_info": mcp_info, "modules": modules},
        "skill": {"archive": archive_names["skill"], "files": {name: {"sha256": sha(data), "bytes": len(data)} for name, data in skill_files.items()},
                  "total_bytes": sum(len(data) for data in skill_files.values())}}
    mcp_files = {components["mcp"]["binary"]: mcp_data, "LICENSE": cli_files["LICENSE"], "README.md": MCP_README,
                 "build.json": json_bytes(mcp_manifest(source, target, web, components["mcp"]))}
    mcp_files["SHA256SUMS"] = checksums(mcp_files)
    windows = target["GOOS"] == "windows"
    files = {archive_names["cli"]: archive_bytes(cli_files, binary, windows),
             archive_names["mcp"]: archive_bytes(mcp_files, components["mcp"]["binary"], windows),
             archive_names["skill"]: archive_bytes(skill_files, windows=True)}
    for key, component in components.items():
        component["sha256"] = sha(files[archive_names[key]])
    files["suite.json"] = json_bytes({"schema_version": 1, "artifact_kind": "candidate-suite", "release": False,
        "source": source, "target": target, "embedded_web": web, "components": components, "scope": SCOPE})
    files["SHA256SUMS"] = checksums(files)
    # Fully validate a private scratch directory before making the new output.
    with tempfile.TemporaryDirectory(prefix="nb1-suite-stage-", dir=output.parent) as temporary:
        scratch = Path(temporary)
        for name, data in files.items():
            (scratch / name).write_bytes(data)
        verify(scratch)
        output.mkdir(mode=0o700)
        try:
            for path in scratch.iterdir():
                shutil.copyfile(path, output / path.name)
            verify(output)
        except BaseException:
            shutil.rmtree(output)
            raise
    return output


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    sub = parser.add_subparsers(dest="operation", required=True)
    create = sub.add_parser("stage", help="Package a clean matching checkout; never execute either binary")
    for name in ("root", "cli-dir", "mcp-binary", "output"):
        create.add_argument("--" + name, required=True)
    check = sub.add_parser("verify", help="Offline bounded archive, provenance and checksum verification")
    check.add_argument("--directory", required=True)
    args = parser.parse_args()
    try:
        if args.operation == "stage":
            stage(args.root, args.cli_dir, args.mcp_binary, args.output)
        else:
            verify(args.directory)
    except (ValueError, OSError, KeyError, TypeError, subprocess.SubprocessError, tarfile.TarError, zipfile.BadZipFile) as error:
        parser.exit(1, "candidate suite rejected: " + str(error) + "\n")
    print("candidate suite verified; no installation, USB or runtime execution")


if __name__ == "__main__":
    main()
