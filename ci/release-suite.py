#!/usr/bin/env python3
"""Build and verify versioned native release archives, never install or use USB."""
import argparse
import importlib.util
import json
import os
from pathlib import Path
import re
import shlex
import shutil
import subprocess
import sys
import tempfile
import time

SPEC = importlib.util.spec_from_file_location("candidate_suite", Path(__file__).with_name("candidate-suite.py"))
BASE = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(BASE)
SPEC = importlib.util.spec_from_file_location("mcp_smoke", Path(__file__).with_name("mcp-candidate-smoke.py"))
MCP_SMOKE = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(MCP_SMOKE)
TARGETS = BASE.CLI.NATIVE_TARGETS
FOUNDATION = {"github.com/b1ug/blink1-go": "v0.0.2", "github.com/b1ug/gid": "v0.0.3",
              "github.com/b1ug/colorkit": "v0.1.0"}
TOOLCHAIN = "go1.27.4"
SCOPE = "Versioned native software package; physical USB, minimum-OS qualification and managed-service acceptance are separate."
VERSION = re.compile(r"v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\Z")
LICENSES = {"LICENSE.txt", "editor-LICENSES.txt", "fonts/MPLUSRounded1c-OFL.txt", "fonts/Fredoka-OFL.txt",
            "nightlab/compiler/GO-LICENSE.txt", "nightlab/compiler/NB1-LICENSE.txt",
            "nightlab/model-assets/LICENSE.txt", "nightlab/THREE-LICENSE.txt"}
SKILL_FILES = BASE.SKILL_FILES | {"LICENSE"}


def require(condition, message):
    if not condition:
        raise ValueError(message)


def identity(version, source):
    require(isinstance(version, str) and len(version) <= 128 and VERSION.fullmatch(version), "version must be stable vMAJOR.MINOR.PATCH")
    require(isinstance(source, str) and re.fullmatch(r"[0-9a-f]{40}", source), "source must be a full commit")


def run(argv, cwd=None, env=None, timeout=60):
    return subprocess.run([str(a) for a in argv], cwd=cwd, env=env, capture_output=True,
                          text=True, encoding="utf-8", check=True, timeout=timeout).stdout


def git(root, *args):
    return run(["git", *args], root).strip()


def context(root, environment):
    """Dispatch is always a rehearsal. Only an official tag already on master may publish."""
    event, ref = environment.get("GITHUB_EVENT_NAME"), environment.get("GITHUB_REF", "")
    source = git(root, "rev-parse", "HEAD")
    version = environment.get("INPUT_VERSION", "") if event in {"workflow_dispatch", "pull_request"} else ref.removeprefix("refs/tags/")
    identity(version, source)
    require(not git(root, "status", "--porcelain", "--untracked-files=all"), "release source must be clean")
    require(event in {"workflow_dispatch", "pull_request", "push"}, "unsupported release event")
    if event in {"workflow_dispatch", "pull_request"}:
        require(environment.get("INPUT_PUBLISH", "false") == "false", "manual dispatch cannot publish")
        return {"event": event, "version": version, "source": source, "ref": ref,
                "publication_eligible": False, "mode": "rehearsal"}
    require(environment.get("GITHUB_REPOSITORY") == "b1ug/nb1" and ref == "refs/tags/" + version,
            "publication requires the official repository and an exact version tag")
    require(git(root, "rev-parse", ref + "^{commit}") == source, "tag does not peel to the checked source")
    run(["git", "merge-base", "--is-ancestor", source, "refs/remotes/origin/master"], root)
    return {"event": event, "version": version, "source": source, "ref": ref,
            "publication_eligible": True, "mode": "tag-build"}


def check_context(value, version, source):
    require(isinstance(value, dict) and set(value) == {"event", "version", "source", "ref", "publication_eligible", "mode"},
            "unexpected build context")
    require(value["version"] == version and value["source"] == source, "context source/version mismatch")
    expected = value["event"] == "push"
    require(type(value["publication_eligible"]) is bool and value["publication_eligible"] == expected,
            "invalid publication eligibility")
    require((expected and value["mode"] == "tag-build" and value["ref"] == "refs/tags/" + version)
            or (not expected and value["event"] in {"workflow_dispatch", "pull_request"} and value["mode"] == "rehearsal"),
            "invalid release event/mode")


def names(version, target):
    suffix = version + "-" + target["GOOS"] + "-" + target["GOARCH"]
    extension = ".zip" if target["GOOS"] == "windows" else ".tar.gz"
    return {"cli": "nb1-" + suffix + extension, "mcp": "nb1-mcp-" + suffix + extension,
            "skill": "blink1-control-" + version + ".zip"}


def selected_modules(root, info, mcp=False):
    cwd = root / "adapters/mcp" if mcp else root
    values = BASE.documents(BASE.command(["go", "list", "-mod=readonly", "-m", "-json", "all"], cwd))
    selected = {v["Path"]: v for v in values}
    main = selected.get(BASE.MCP if mcp else BASE.CORE, {})
    require(main.get("Main") and main.get("GoVersion") == "1.25.0", "release source Go floor differs")
    for path, version in FOUNDATION.items():
        require(selected.get(path, {}).get("Version") == version and not selected[path].get("Replace"),
                "release requires the fixed public foundation versions")
    linked = {v["Path"] for v in info["dependencies"]}
    if mcp:
        records = BASE.module_records(values, root, linked)
    else:
        require(len([v for v in values if v.get("Main")]) == 1 and selected[BASE.CORE].get("Main"), "invalid root module")
        require(all(not v.get("Replace") for v in values), "CLI module graph contains a replacement")
        records = [{key: selected[path].get(key, "") for key in ("Path", "Version", "Sum")} for path in sorted(linked)]
        require(all(v["Version"] and v["Sum"] for v in records), "linked public modules require checksums")
        require({v["Path"]: v for v in records} == {v["Path"]: v for v in info["dependencies"]},
                "binary modules do not match source selection")
    return records


def linker_value(info, key):
    raw = info["settings"].get("-ldflags", "")
    if raw.startswith('"'):
        raw = json.loads(raw)
    flags = shlex.split(raw)
    values = []
    for index, flag in enumerate(flags):
        if flag == "-X" and index + 1 < len(flags) and flags[index + 1].startswith(key + "="):
            values.append(flags[index + 1].split("=", 1)[1])
        elif flag.startswith("-X=" + key + "="):
            values.append(flag.split("=", 2)[2])
    require(len(values) == 1, "missing or duplicate linked release version")
    return values[0]


def check_binary(info, modules, version, source, target, mcp=False):
    BASE.check_identity(info, BASE.MCP if mcp else BASE.CORE, source, target)
    require(info["go_version"] == TOOLCHAIN, "unexpected release toolchain")
    require(info["settings"]["CGO_ENABLED"] == ("0" if mcp else "1"), "unexpected native backend selection")
    key = "main.buildVersion" if mcp else BASE.CORE + "/config.GitSummary"
    # Go may omit -ldflags from build info under -trimpath. The mandatory
    # extracted native CLI JSON and MCP initialization below verify the actual
    # version in those exact hashed bytes, rather than assuming flag retention.
    if "-ldflags" in info["settings"]:
        require(linker_value(info, key) == version, "linked release version differs")
    if mcp:
        BASE.check_mcp(info, modules, source, target)
    else:
        require(all("Replace" not in v for v in modules), "CLI replacement is not public release provenance")
        require({v["Path"]: v for v in modules} == {v["Path"]: v for v in info["dependencies"]}, "CLI modules differ")
        linked = {v["Path"]: v["Version"] for v in modules}
        require(all(linked.get(p) == v for p, v in FOUNDATION.items()), "wrong foundation version")


def archived_build_info(data, name):
    # `go version -m` statically reads PE/ELF/Mach-O bytes. Never launch an
    # archive executable, including foreign targets, during offline verification.
    return BASE.build_info(BASE.binary_build_text(data, name, Path.cwd()))


def check_native_dependencies(value, goos):
    require(isinstance(value, dict), "missing native dependency record")
    if goos == "darwin":
        require(value.get("kind") == "mach-o" and isinstance(value.get("libraries"), list) and value["libraries"]
                and all(isinstance(p, str) and p.startswith(("/usr/lib/", "/System/Library/")) for p in value["libraries"])
                and re.fullmatch(r"[0-9]+(?:\.[0-9]+)+", value.get("minimum_os_from_binary", ""))
                and value.get("notarization_claimed") is False, "invalid macOS dependency record")
    elif goos == "linux":
        versions = value.get("glibc_versions")
        require(value.get("kind") == "elf", "invalid ELF dependency record")
        if value.get("linkage") == "static":
            require(versions == [] and value.get("required_glibc_from_binary") is None
                    and value.get("all_dependencies_resolved") is True, "invalid static ELF record")
            return
        require(value.get("linkage") == "dynamic" and isinstance(versions, list) and versions
                and all(isinstance(v, str) and re.fullmatch(r"[0-9]+(?:\.[0-9]+)+", v) for v in versions)
                and value.get("required_glibc_from_binary") == max(versions, key=lambda v: tuple(map(int, v.split('.'))))
                and value.get("all_dependencies_resolved") is True
                and isinstance(value.get("ldd"), str) and "not found" not in value["ldd"], "invalid Linux dependency record")
    else:
        names = value.get("DLLs")
        require(value.get("kind") == "pe" and value.get("system_DLLs_only") is True and isinstance(names, list) and names
                and all(isinstance(n, str) and re.fullmatch(r"[A-Za-z0-9_.-]+", n)
                        and not n.lower().startswith(("libgcc", "libstdc++", "libwinpthread")) for n in names),
                "invalid Windows dependency record")


def notice_prefix(module, source):
    version = source if module.get("Replace") else module["Version"]
    prefix = "licenses/modules/" + module["Path"] + "@" + version + "/"
    require(".." not in Path(prefix).parts and "\\" not in prefix, "unsafe module notice path")
    return prefix


def collect_notices(root, modules, web=None, mcp=False):
    """Copy original top-level notices from the exact linked module selection."""
    cwd = root / "adapters/mcp" if mcp else root
    selected = {v["Path"]: v for v in BASE.documents(BASE.command(["go", "list", "-mod=readonly", "-m", "-json", "all"], cwd))}
    source = BASE.source_identity(root)
    files, records = {}, {}
    def add(path, data, origin):
        require(path not in files, "duplicate notice path")
        files[path] = data
        records[path] = {"source": origin, "bytes": len(data), "sha256": BASE.sha(data)}
    goroot = Path(BASE.command(["go", "env", "GOROOT"], cwd).strip())
    add("licenses/go/LICENSE", BASE.regular_bytes(goroot / "LICENSE", BASE.JSON_BYTES),
        {"kind": "go-toolchain", "version": TOOLCHAIN})
    for module in modules:
        row = selected[module["Path"]]
        require(row.get("Version") == module["Version"], "notice module version differs")
        if module.get("Replace"):
            require(module["Path"] == BASE.CORE and Path(row["Dir"]).resolve() == root, "notice replacement differs")
            origin = {"kind": "same-checkout", "module": module["Path"], "commit": source}
        else:
            require(not row.get("Replace") and row.get("Sum") == module["Sum"], "notice public module differs")
            origin = {"kind": "public-module", "module": module["Path"], "version": module["Version"], "sum": module["Sum"]}
        directory = Path(row["Dir"])
        notices = sorted(p for p in directory.iterdir() if p.name.upper().startswith(("LICENSE", "LICENCE", "COPYING", "NOTICE")) and not p.is_dir())
        require(notices and len(notices) <= 64, "no bounded top-level notices found for linked module " + module["Path"])
        for path in notices:
            add(notice_prefix(module, source) + path.name, BASE.regular_bytes(path, BASE.JSON_BYTES), origin)
    if web:
        for name in LICENSES:
            add("licenses/" + name, BASE.regular_bytes(root / "internal/webui/dist" / name, BASE.JSON_BYTES),
                {"kind": "embedded-web", "commit": web["source_commit"], "path": name})
    return files, records


def check_notices(records, modules, source, web):
    require(isinstance(records, dict) and 1 <= len(records) <= 512, "missing or excessive license inventory")
    groups = {module["Path"]: [] for module in modules}
    seen_go, seen_web = 0, set()
    for path, record in records.items():
        require(isinstance(path, str) and path.startswith("licenses/") and not Path(path).is_absolute()
                and ".." not in Path(path).parts and "\\" not in path, "unsafe notice path")
        require(isinstance(record, dict) and set(record) == {"source", "bytes", "sha256"}
                and type(record["bytes"]) is int and 0 < record["bytes"] <= BASE.JSON_BYTES
                and re.fullmatch(r"[0-9a-f]{64}", record["sha256"]), "invalid notice hash record")
        origin = record["source"]
        require(isinstance(origin, dict), "invalid notice source")
        if origin == {"kind": "go-toolchain", "version": TOOLCHAIN}:
            require(path == "licenses/go/LICENSE", "unexpected toolchain notice")
            seen_go += 1
        elif origin.get("kind") == "embedded-web":
            name = origin.get("path")
            require(web is not None and name in LICENSES and path == "licenses/" + name
                    and origin == {"kind": "embedded-web", "commit": web["source_commit"], "path": name}, "unexpected Web notice")
            seen_web.add(name)
        else:
            matching = [module for module in modules if origin.get("module") == module["Path"]]
            require(len(matching) == 1, "notice is not from a linked module")
            module = matching[0]
            expected = ({"kind": "same-checkout", "module": module["Path"], "commit": source} if module.get("Replace") else
                        {"kind": "public-module", "module": module["Path"], "version": module["Version"], "sum": module["Sum"]})
            prefix = notice_prefix(module, source)
            name = path.removeprefix(prefix)
            require(origin == expected and path.startswith(prefix) and "/" not in name
                    and name.upper().startswith(("LICENSE", "LICENCE", "COPYING", "NOTICE")), "notice source/path differs")
            groups[module["Path"]].append(name)
    require(seen_go == 1 and seen_web == (LICENSES if web is not None else set())
            and all(groups.values()), "incomplete linked-module, Go or Web notices")
    if any(m["Path"] == "github.com/b1ug/gid" and m["Version"] == "v0.0.3" for m in modules):
        require({"LICENSE", "LICENSE-old"}.issubset(groups["github.com/b1ug/gid"]), "GID original notices are incomplete")


def web_inventory(root):
    directory = root / "internal/webui/dist"
    lock = BASE.read_json(BASE.regular_bytes(root / "internal/webui/manifest.lock.json", 4096))
    raw = BASE.regular_bytes(directory / "manifest.json", BASE.JSON_BYTES)
    require(BASE.sha(raw) == lock["manifest_sha256"], "Web manifest differs from source lock")
    manifest = BASE.read_json(raw)
    require(manifest["source"]["commit"] == lock["source_commit"] and manifest["source"]["dirty"] is False,
            "Web source identity differs")
    expected = {"manifest.json"}
    for record in manifest["files"]:
        path = record["path"]
        require(path not in expected and not Path(path).is_absolute() and ".." not in Path(path).parts and "\\" not in path,
                "duplicate or unsafe Web path")
        data = BASE.regular_bytes(directory / path)
        require(len(data) == record["bytes"] and BASE.sha(data) == record["sha256"], "Web file hash mismatch")
        expected.add(path)
    require({p.relative_to(directory).as_posix() for p in directory.rglob("*") if p.is_file()} == expected,
            "Web inventory is not closed")
    return lock


def runtime_dependencies(binary, goos):
    if goos == "darwin":
        libraries = run(["otool", "-L", binary])
        paths = re.findall(r"^\s+(\S+)\s+\(", libraries, re.M)
        require(paths and all(p.startswith(("/usr/lib/", "/System/Library/")) for p in paths), "non-system macOS library")
        loads = run(["otool", "-l", binary])
        minimum = re.findall(r"^\s+minos\s+([0-9.]+)", loads, re.M)
        if not minimum:
            minimum = re.findall(r"cmd LC_VERSION_MIN_MACOSX.*?\n\s+cmdsize[^\n]*\n\s+version\s+([0-9.]+)", loads, re.S)
        require(len(minimum) == 1, "missing or ambiguous Mach-O minimum OS")
        signature = subprocess.run(["codesign", "-d", "--verbose=2", str(binary)], capture_output=True, text=True, timeout=30)
        return {"kind": "mach-o", "libraries": paths, "minimum_os_from_binary": minimum[0],
                "signature_inspection_exit": signature.returncode,
                "signature_details": (signature.stdout + signature.stderr).replace(str(binary.resolve()), binary.name).replace(str(binary), binary.name),
                "notarization_claimed": False}
    if goos == "linux":
        dynamic = run(["readelf", "--dynamic", binary])
        program = run(["readelf", "--program-headers", binary])
        if "(NEEDED)" not in dynamic and "Requesting program interpreter" not in program:
            return {"kind": "elf", "linkage": "static", "ldd": "No dynamic NEEDED entries or program interpreter",
                    "glibc_versions": [], "required_glibc_from_binary": None, "all_dependencies_resolved": True}
        libraries = run(["ldd", binary])
        require("not found" not in libraries, "unresolved Linux runtime dependency")
        versions = run(["readelf", "--version-info", binary])
        glibc = sorted(set(re.findall(r"\bGLIBC_([0-9]+(?:\.[0-9]+)+)", versions)), key=lambda v: tuple(map(int, v.split('.'))))
        require(glibc, "missing glibc ABI information")
        libraries = re.sub(r"\(0x[0-9a-fA-F]+\)", "(runtime address omitted)", libraries)
        return {"kind": "elf", "linkage": "dynamic", "ldd": libraries.replace(str(binary), binary.name), "glibc_versions": glibc,
                "required_glibc_from_binary": glibc[-1], "all_dependencies_resolved": True}
    require(goos == "windows", "unsupported native dependency target")
    text = run(["objdump", "-p", binary])
    libraries = sorted(set(re.findall(r"DLL Name:\s*(\S+)", text, re.I)))
    system = Path(os.environ["SystemRoot"]) / "System32"
    require(libraries and all(name.lower().startswith(("api-ms-win-", "ext-ms-win-")) or
                             (system / name).is_file() for name in libraries), "non-system Windows DLL dependency")
    return {"kind": "pe", "DLLs": libraries, "system_DLLs_only": True}


def restricted_environment(goos, config_root):
    env = dict(os.environ)
    for key in list(env):
        if key.upper() in {"LD_LIBRARY_PATH", "LD_PRELOAD", "DYLD_LIBRARY_PATH", "DYLD_INSERT_LIBRARIES", "DYLD_FRAMEWORK_PATH"}:
            env.pop(key)
    if goos == "windows":
        system = Path(env["SystemRoot"])
        env["PATH"] = os.pathsep.join(map(str, [system / "System32", system, system / "System32/Wbem"]))
    else:
        env["PATH"] = "/usr/bin:/bin:/usr/sbin:/sbin"
    env.update(XDG_CONFIG_HOME=str(config_root), NB1_CONFIG=str(config_root / "config.yaml"))
    return env


def native_smoke(cli, mcp, version, source, target):
    with tempfile.TemporaryDirectory(prefix="nb1-release-smoke-") as temporary:
        scratch = Path(temporary)
        config = scratch / "config.yaml"
        config.write_text("{}\n")
        env = restricted_environment(target["GOOS"], scratch)
        helps = []
        for arguments, markers in BASE.CLI.HELP_CHECKS:
            output = run([cli, "--config", config, *arguments], scratch, env)
            require(all(marker in output for marker in markers), "extracted CLI help differs")
            helps.append({"arguments": arguments, "stdout_sha256": BASE.sha(output.encode())})
        value = json.loads(run([cli, "--config", config, "version", "--json"], scratch, env))
        require(value["version"] == version and value["build"]["revision"] == source and value["build"]["modified"] == "false",
                "extracted CLI release version or source differs")
        require(value["build"]["os"] == target["GOOS"] and value["build"]["arch"] == target["GOARCH"]
                and value["build"]["cgo_enabled"] is True, "extracted CLI native target differs")
        dependencies = {v["path"]: v for v in value["build"]["dependencies"]}
        require(all(dependencies[p]["version"] == FOUNDATION[p] and dependencies[p]["source"] == "module"
                    for p in BASE.CLI.MODULES), "extracted CLI foundation identity differs")
        # The existing bounded MCP harness starts no daemon and calls no mutating tool.
        old_env = os.environ.copy()
        try:
            os.environ.clear(); os.environ.update(env)
            report = {}
            MCP_SMOKE.smoke(mcp, report, time.monotonic())
        finally:
            os.environ.clear(); os.environ.update(old_env)
        require(report["initialize"]["server_info"]["version"] == version, "MCP serverInfo release version differs")
        require(config.read_text() == "{}\n", "release smoke changed the private profile")
        return {"status": "PASS", "system_PATH_only": True, "CLI_version": value,
                "CLI_help": helps, "MCP_version": report["initialize"]["server_info"]["version"],
                "MCP_tools": len(report["tools"]), "MCP_protocol_only": report["stdout_protocol_only"],
                "USB_access": False, "daemon_started": False}


def readme(component, version):
    if component == "cli":
        text = f"""# nb1 {version}\n\nThe CLI, foreground server and bilingual Web debugger are in this executable.\nVerify the matching release.json and SHA256SUMS from the published GitHub release.\nA workflow rehearsal is not a published release. No Node server is needed.\n\nRun nb1 version --json, then nb1 list to find your device.\nStart nb1 server --devices YOUR_SERIAL and open http://127.0.0.1:8931/demo/.\nPreview is optional; Play explicitly controls the lamp.\nInstalling or unpacking does not start a daemon or register a service.\n\nWindows uses nb1.exe; automatic Windows service management is not included.\nFull guide: https://github.com/b1ug/nb1/blob/{version}/docs/install.md\n"""
    else:
        text = f"""# nb1 MCP {version}\n\nThis matching optional stdio adapter calls an explicitly started nb1 daemon over HTTP.\nRun nb1-mcp --endpoint http://127.0.0.1:8931 --timeout 10s.\nWindows uses nb1-mcp.exe. Stdout is reserved for JSON-RPC.\nIt never opens USB, executes the CLI, starts a daemon or installs itself.\nPreview is optional. Keep accepted Job IDs when responses are lost.\nCheck the matching published release.json and SHA256SUMS.\n"""
    return text.encode()


def stage(root, cli, mcp, output, version, source, build_context):
    identity(version, source); check_context(build_context, version, source)
    root = Path(root).resolve(strict=True); output = Path(output)
    require(not output.exists() and not output.is_symlink(), "output must be new")
    require(not output.resolve().is_relative_to(root), "release output must be outside source")
    require(BASE.source_identity(root) == source, "source checkout differs")
    data = {"cli": BASE.regular_bytes(Path(cli)), "mcp": BASE.regular_bytes(Path(mcp))}
    infos = {key: BASE.build_info(BASE.binary_build_text(value, "nb1" if key == "cli" else "nb1-mcp", root))
             for key, value in data.items()}
    target = {key: infos["cli"]["settings"][key] for key in ("GOOS", "GOARCH")}
    require(tuple(target.values()) in TARGETS, "unsupported native release target")
    modules = {key: selected_modules(root, info, key == "mcp") for key, info in infos.items()}
    for key in infos:
        check_binary(infos[key], modules[key], version, source, target, key == "mcp")
    web = web_inventory(root)
    license_text = BASE.regular_bytes(root / "LICENSE")
    archive_names = names(version, target); files = {}; components = {}
    windows = target["GOOS"] == "windows"
    # Inspect and run the exact bytes in isolated directories, not mutable build paths.
    with tempfile.TemporaryDirectory(prefix="nb1-release-native-") as temporary:
        scratch = Path(temporary)
        binaries = {}
        for key in data:
            directory = scratch / key; directory.mkdir()
            name = ("nb1" if key == "cli" else "nb1-mcp") + (".exe" if windows else "")
            path = directory / name; path.write_bytes(data[key]); path.chmod(0o755); binaries[key] = path
        deps = {key: runtime_dependencies(path, target["GOOS"]) for key, path in binaries.items()}
    for key in data:
        binary = ("nb1" if key == "cli" else "nb1-mcp") + (".exe" if windows else "")
        licenses, license_records = collect_notices(root, modules[key], web if key == "cli" else None, key == "mcp")
        check_notices(license_records, modules[key], source, web if key == "cli" else None)
        build = {"schema_version": 1, "artifact_kind": "nb1-release-component", "release": True,
                 "version": version, "source": source, "target": target, "build_info": infos[key],
                 "modules": modules[key], "embedded_web": web if key == "cli" else None,
                 "native_dependencies": deps[key], "licenses": license_records, "build_context": build_context, "scope": SCOPE}
        content = {binary: data[key], "LICENSE": license_text, "README.md": readme(key, version),
                   "build.json": BASE.json_bytes(build)}
        content.update(licenses)
        content["SHA256SUMS"] = BASE.checksums(content)
        archive = BASE.archive_bytes(content, binary, windows)
        files[archive_names[key]] = archive
        components[key] = {"archive": archive_names[key], "sha256": BASE.sha(archive), "binary": binary,
                           "binary_sha256": BASE.sha(data[key]), "build": build,
                           "files": {name: BASE.sha(value) for name, value in content.items()}}
    skill_root = root / "skills/blink1-control"
    require(not skill_root.is_symlink() and not any(p.is_symlink() for p in skill_root.rglob('*')), "symlink in Skill")
    skill = {p.relative_to(skill_root).as_posix(): BASE.regular_bytes(p, BASE.JSON_BYTES) for p in skill_root.rglob('*') if p.is_file()}
    require(set(skill) == BASE.SKILL_FILES, "Skill inventory differs")
    skill["LICENSE"] = license_text
    files[archive_names["skill"]] = BASE.archive_bytes(skill, windows=True)
    components["skill"] = {"archive": archive_names["skill"], "sha256": BASE.sha(files[archive_names["skill"]]),
                           "files": {name: BASE.sha(value) for name, value in skill.items()}}
    with tempfile.TemporaryDirectory(prefix="nb1-release-extracted-") as temporary:
        scratch = Path(temporary); binaries = {}
        for key in ("cli", "mcp"):
            record = components[key]; directory = scratch / key; directory.mkdir()
            content = BASE.read_archive(files[record["archive"]], set(record["files"]), record["binary"], windows)
            for name, value in content.items():
                path = directory / name; path.parent.mkdir(parents=True, exist_ok=True); path.write_bytes(value)
                path.chmod(0o755 if name == record["binary"] else 0o644)
            binaries[key] = directory / record["binary"]
        runtime = native_smoke(binaries["cli"], binaries["mcp"], version, source, target)
        runtime["extracted_archives"] = True
    platform = {"schema_version": 1, "artifact_kind": "nb1-release-platform", "release": True,
                "version": version, "source": source, "target": target, "embedded_web": web,
                "components": components, "runtime": runtime, "build_context": build_context, "scope": SCOPE}
    files["platform.json"] = BASE.json_bytes(platform)
    files["SHA256SUMS"] = BASE.checksums(files)
    require(BASE.source_identity(root) == source, "source changed during release staging")
    output.mkdir(mode=0o700)
    for name, value in files.items(): (output / name).write_bytes(value)
    verify_platform(output, version, source)
    return platform


def verify_platform(directory, version, source):
    identity(version, source); directory = Path(directory)
    require(not directory.is_symlink(), "platform directory is a symlink")
    value = BASE.read_json(BASE.regular_bytes(directory / "platform.json", BASE.JSON_BYTES))
    require(set(value) == {"schema_version", "artifact_kind", "release", "version", "source", "target", "embedded_web",
                           "components", "runtime", "build_context", "scope"}, "unexpected platform fields")
    require(type(value["schema_version"]) is int and value["schema_version"] == 1 and value["artifact_kind"] == "nb1-release-platform" and value["release"] is True
            and value["version"] == version and value["source"] == source and value["scope"] == SCOPE, "invalid release platform")
    check_context(value["build_context"], version, source)
    require(set(value["target"]) == {"GOOS", "GOARCH"} and tuple(value["target"][k] for k in ("GOOS", "GOARCH")) in TARGETS,
            "unexpected release target")
    archive_names = names(version, value["target"])
    require(set(value["components"]) == set(archive_names), "missing release component")
    require({p.name for p in directory.iterdir()} == {"platform.json", "SHA256SUMS", *archive_names.values()}, "platform file inventory differs")
    outer = {p.name: BASE.regular_bytes(p) for p in directory.iterdir() if p.name != "SHA256SUMS"}
    require(BASE.regular_bytes(directory / "SHA256SUMS") == BASE.checksums(outer), "platform checksums differ")
    require(value["runtime"]["status"] == "PASS" and value["runtime"]["system_PATH_only"] is True
            and value["runtime"]["extracted_archives"] is True and value["runtime"]["MCP_tools"] == 12
            and value["runtime"]["MCP_protocol_only"] is True and value["runtime"]["MCP_version"] == version
            and value["runtime"]["USB_access"] is False and value["runtime"]["daemon_started"] is False
            and value["runtime"]["CLI_version"]["version"] == version
            and value["runtime"]["CLI_version"]["build"]["revision"] == source
            and value["runtime"]["CLI_version"]["build"]["modified"] == "false"
            and value["runtime"]["CLI_version"]["build"]["os"] == value["target"]["GOOS"]
            and value["runtime"]["CLI_version"]["build"]["arch"] == value["target"]["GOARCH"]
            and value["runtime"]["CLI_version"]["build"]["cgo_enabled"] is True,
            "missing native release smoke")
    component_licenses = []
    for key, name in archive_names.items():
        record = value["components"][key]
        require(record["archive"] == name and record["sha256"] == BASE.sha(outer[name]), "archive identity differs")
        expected = set(record["files"])
        if key == "skill":
            require(set(record) == {"archive", "sha256", "files"} and expected == SKILL_FILES, "Skill fields differ")
            content = BASE.read_archive(outer[name], expected, windows=True)
            component_licenses.append(content["LICENSE"])
        else:
            require(set(record) == {"archive", "sha256", "binary", "binary_sha256", "build", "files"}, "component fields differ")
            binary = ("nb1" if key == "cli" else "nb1-mcp") + (".exe" if value["target"]["GOOS"] == "windows" else "")
            expected_files = {binary, "LICENSE", "README.md", "build.json", "SHA256SUMS"}
            expected_files |= set(record["build"].get("licenses", {}))
            require(record["binary"] == binary and expected == expected_files, "component inventory differs")
            content = BASE.read_archive(outer[name], expected, binary, value["target"]["GOOS"] == "windows")
            require(BASE.sha(content[binary]) == record["binary_sha256"], "binary hash differs")
            build = BASE.read_json(content["build.json"])
            require(set(build) == {"schema_version", "artifact_kind", "release", "version", "source", "target", "build_info",
                                  "modules", "embedded_web", "native_dependencies", "licenses", "build_context", "scope"}
                    and type(build["schema_version"]) is int and build["schema_version"] == 1 and build["artifact_kind"] == "nb1-release-component"
                    and build["scope"] == SCOPE and build == record["build"] and build["version"] == version and build["source"] == source
                    and build["release"] is True and build["target"] == value["target"]
                    and build["build_context"] == value["build_context"]
                    and build["embedded_web"] == (value["embedded_web"] if key == "cli" else None), "component provenance differs")
            actual_info = archived_build_info(content[binary], binary)
            require(actual_info == build["build_info"], "archived binary build info differs from manifest")
            check_binary(actual_info, build["modules"], version, source, value["target"], key == "mcp")
            check_native_dependencies(build["native_dependencies"], value["target"]["GOOS"])
            check_notices(build["licenses"], build["modules"], source, value["embedded_web"] if key == "cli" else None)
            require(all(record == {"source": record["source"], "bytes": len(content[name]), "sha256": BASE.sha(content[name])}
                        for name, record in build["licenses"].items()), "notice content differs from original hash record")
            require(content["README.md"] == readme(key, version), "component instructions differ")
            component_licenses.append(content["LICENSE"])
            require(content["SHA256SUMS"] == BASE.checksums({n: b for n, b in content.items() if n != "SHA256SUMS"}),
                    "inner component checksums differ")
        require({n: BASE.sha(b) for n, b in content.items()} == record["files"], "archive file hashes differ")
    require(len(component_licenses) == 3 and all(data == component_licenses[0] for data in component_licenses), "component licenses differ")
    return value


def assemble(platform_root, output, version, source):
    identity(version, source); platform_root = Path(platform_root); output = Path(output)
    require(not output.exists() and not output.is_symlink(), "assembly output must be new")
    directories = sorted(platform_root.iterdir())
    require(len(directories) == len(TARGETS) and all(p.is_dir() and not p.is_symlink() for p in directories), "exactly five platform directories required")
    platforms = {}; files = {}; common_context = common_web = None
    for directory in directories:
        value = verify_platform(directory, version, source)
        target = value["target"]; key = target["GOOS"] + "-" + target["GOARCH"]
        require(key not in platforms, "duplicate platform")
        if common_context is None: common_context, common_web = value["build_context"], value["embedded_web"]
        require(common_context == value["build_context"] and common_web == value["embedded_web"], "platform source contexts differ")
        platforms[key] = value
        for record in value["components"].values():
            name = record["archive"]; data = BASE.regular_bytes(directory / name)
            require(name not in files or files[name] == data, "shared Skill differs across targets")
            files[name] = data
    require({tuple(v["target"][k] for k in ("GOOS", "GOARCH")) for v in platforms.values()} == TARGETS,
            "release target set differs")
    manifest = {"schema_version": 1, "artifact_kind": "nb1-release-suite", "release": True,
                "version": version, "source": source, "build_context": common_context,
                "embedded_web": common_web, "platforms": platforms, "scope": SCOPE,
                "assets": {name: {"sha256": BASE.sha(data), "bytes": len(data)} for name, data in files.items()}}
    files["release.json"] = BASE.json_bytes(manifest)
    files["SHA256SUMS"] = BASE.checksums(files)
    output.mkdir(mode=0o700)
    for name, data in files.items(): (output / name).write_bytes(data)
    verify_release(output, version, source)
    return manifest


def verify_release(directory, version, source, publish=False):
    identity(version, source); directory = Path(directory)
    require(not directory.is_symlink(), "release directory is a symlink")
    manifest = BASE.read_json(BASE.regular_bytes(directory / "release.json", 8 * BASE.JSON_BYTES))
    require(set(manifest) == {"schema_version", "artifact_kind", "release", "version", "source", "build_context",
                              "embedded_web", "platforms", "scope", "assets"}, "unexpected release fields")
    require(type(manifest["schema_version"]) is int and manifest["schema_version"] == 1 and manifest["artifact_kind"] == "nb1-release-suite" and manifest["release"] is True
            and manifest["version"] == version and manifest["source"] == source and manifest["scope"] == SCOPE, "invalid release suite")
    check_context(manifest["build_context"], version, source)
    require(not publish or manifest["build_context"]["publication_eligible"], "rehearsal cannot publish")
    expected_targets = {os_name + "-" + arch for os_name, arch in TARGETS}
    require(set(manifest["platforms"]) == expected_targets, "missing release platforms")
    expected_assets = set()
    actual_targets = set()
    for key, value in manifest["platforms"].items():
        target = value["target"]
        require(set(target) == {"GOOS", "GOARCH"}, "unexpected release target fields")
        actual = (target["GOOS"], target["GOARCH"])
        require(actual in TARGETS and key == "-".join(actual) and actual not in actual_targets,
                "release platform key or unique target differs")
        actual_targets.add(actual)
        require(value["build_context"] == manifest["build_context"] and value["embedded_web"] == manifest["embedded_web"], "platform context drift")
        expected_assets |= set(names(version, value["target"]).values())
    require(actual_targets == TARGETS, "release actual target set differs")
    require(set(manifest["assets"]) == expected_assets and {p.name for p in directory.iterdir()} == expected_assets | {"release.json", "SHA256SUMS"},
            "release assets are not a closed inventory")
    files = {name: BASE.regular_bytes(directory / name) for name in expected_assets | {"release.json"}}
    require(BASE.regular_bytes(directory / "SHA256SUMS") == BASE.checksums(files), "release checksums differ")
    for name, record in manifest["assets"].items():
        require(record == {"sha256": BASE.sha(files[name]), "bytes": len(files[name])}, "release asset size/hash differs")
    # Reconstruct each strict platform envelope for full inner archive verification.
    for value in manifest["platforms"].values():
        with tempfile.TemporaryDirectory(prefix="nb1-release-offline-") as temporary:
            scratch = Path(temporary); subset = {"platform.json": BASE.json_bytes(value)}
            subset.update({name: files[name] for name in names(version, value["target"]).values()})
            subset["SHA256SUMS"] = BASE.checksums(subset)
            for name, data in subset.items(): (scratch / name).write_bytes(data)
            verify_platform(scratch, version, source)
    return manifest


def verify_download(expected, downloaded):
    expected, downloaded = Path(expected), Path(downloaded)
    names = {p.name for p in expected.iterdir()}
    require(names == {p.name for p in downloaded.iterdir()}, "downloaded asset inventory differs")
    for name in names:
        require(BASE.regular_bytes(expected / name) == BASE.regular_bytes(downloaded / name), "downloaded asset bytes differ: " + name)
    return {"version": "verified-download", "source": "byte-identical"}


def main():
    p = argparse.ArgumentParser(description=__doc__); sub = p.add_subparsers(dest="operation", required=True)
    c = sub.add_parser("context"); c.add_argument("--root", required=True, type=Path); c.add_argument("--output", required=True, type=Path)
    d = sub.add_parser("verify-download"); d.add_argument("--expected", required=True, type=Path); d.add_argument("--downloaded", required=True, type=Path)
    s = sub.add_parser("stage")
    for name in ("root", "cli", "mcp", "output", "context"): s.add_argument("--" + name, required=True, type=Path)
    for name in ("version", "source"): s.add_argument("--" + name, required=True)
    for operation in ("verify-platform", "assemble", "verify"):
        command = sub.add_parser(operation)
        command.add_argument("--directory" if operation != "assemble" else "--platform-root", required=True, type=Path)
        if operation == "assemble": command.add_argument("--output", required=True, type=Path)
        for name in ("version", "source"): command.add_argument("--" + name, required=True)
        if operation == "verify": command.add_argument("--for-publication", action="store_true")
    args = p.parse_args()
    try:
        if args.operation == "context":
            value = context(args.root, os.environ)
            with args.output.open("x") as f: f.write(BASE.json_bytes(value).decode())
            if os.environ.get("GITHUB_OUTPUT"):
                with open(os.environ["GITHUB_OUTPUT"], "a") as f:
                    f.write("version=" + value["version"] + "\nsource=" + value["source"] + "\npublish=" + str(value["publication_eligible"]).lower() + "\n")
        elif args.operation == "verify-download": value = verify_download(args.expected, args.downloaded)
        elif args.operation == "stage":
            value = stage(args.root, args.cli, args.mcp, args.output, args.version, args.source, BASE.read_json(args.context.read_bytes()))
        elif args.operation == "verify-platform": value = verify_platform(args.directory, args.version, args.source)
        elif args.operation == "assemble": value = assemble(args.platform_root, args.output, args.version, args.source)
        else: value = verify_release(args.directory, args.version, args.source, args.for_publication)
    except (ValueError, OSError, KeyError, TypeError, subprocess.SubprocessError) as error:
        p.exit(1, "release suite rejected: " + str(error) + "\n")
    print(json.dumps({"status": "PASS", "operation": args.operation, "version": value["version"], "source": value["source"]}))


if __name__ == "__main__": main()
