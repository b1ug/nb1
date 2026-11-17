"""Candidate packaging and hostile archive checks; no binaries are executed."""

import copy
import importlib.util
import io
import json
from pathlib import Path
import stat
import tarfile
import tempfile
import unittest
from unittest.mock import patch
import warnings
import zipfile

SPEC = importlib.util.spec_from_file_location("candidate_suite", Path(__file__).with_name("candidate-suite.py"))
SUITE = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(SUITE)
SOURCE = "1" * 40
MODULES = [{"Path": name, "Version": "v0.0.1", "Sum": "h1:public"} for name in SUITE.CLI.MODULES]
MCP_MODULES = [{"Path": SUITE.CORE, "Version": "v0.0.0", "Sum": "",
                "Replace": {"Path": "../..", "Version": "(devel)", "Sum": ""}},
               {"Path": SUITE.MCP_SDK, "Version": "v1.8.0", "Sum": "h1:mcp"}]


def build_text(module, target, dependencies):
    lines = ["/private/not-published/candidate: go1.27.3", "\tpath\t" + module,
             "\tmod\t" + module + "\t(devel)"]
    for item in dependencies:
        lines.append("\tdep\t{Path}\t{Version}\t{Sum}".format(**item))
        if "Replace" in item:
            lines.append("\t=>\t{Path}\t{Version}\t{Sum}".format(**item["Replace"]))
    settings = {"GOOS": target["GOOS"], "GOARCH": target["GOARCH"], "CGO_ENABLED": "1",
                "vcs": "git", "vcs.revision": SOURCE, "vcs.modified": "false", "-trimpath": "true"}
    lines += ["\tbuild\t" + key + "=" + value for key, value in settings.items()]
    return "\n".join(lines) + "\n"


class Fixture:
    def __init__(self, directory, goos="darwin"):
        self.root = directory / "source"
        self.root.mkdir()
        self.cli = directory / "cli"
        self.cli.mkdir()
        self.mcp = directory / "nb1-mcp-input"
        self.mcp.write_bytes(b"not an executable; never run me")
        self.output = directory / "candidate"
        self.target = {"GOOS": goos, "GOARCH": "amd64" if goos == "windows" else "arm64"}
        self.binary = "nb1.exe" if goos == "windows" else "nb1"
        self.web = {"schema_version": 1, "product": "nb1-web", "source_commit": "2" * 40,
                    "manifest_sha256": SUITE.sha(b"{}\n")}
        (self.root / "internal/webui/dist").mkdir(parents=True)
        (self.root / "internal/webui/dist/manifest.json").write_bytes(b"{}\n")
        (self.root / "internal/webui/manifest.lock.json").write_bytes(SUITE.json_bytes(self.web))
        (self.root / "LICENSE").write_bytes(b"license\n")
        (self.root / "adapters/mcp").mkdir(parents=True)
        for name in SUITE.SKILL_FILES:
            path = self.root / "skills/blink1-control" / name
            path.parent.mkdir(parents=True, exist_ok=True)
            path.write_bytes((name + "\n").encode())
        self.texts = {SUITE.CORE: build_text(SUITE.CORE, self.target, MODULES),
                      SUITE.MCP: build_text(SUITE.MCP, self.target, MCP_MODULES)}
        self.selected = [
            {"Path": SUITE.MCP, "Main": True, "Dir": str(self.root / "adapters/mcp")},
            {"Path": SUITE.CORE, "Version": "v0.0.0", "Dir": str(self.root),
             "Replace": {"Path": "../..", "Dir": str(self.root)}},
            dict(MCP_MODULES[1])]
        info = SUITE.build_info(self.texts[SUITE.CORE])
        build = {"schema_version": 2, "artifact_kind": "actions-candidate", "release": False,
                 "source": SOURCE, "target": {key: info["settings"][key] for key in
                    ("GOOS", "GOARCH", "CGO_ENABLED", "vcs.revision", "vcs.modified")},
                 "modules": MODULES, "embedded_web": self.web, "toolchain": {"GOVERSION": "go1.27.3"}}
        self.files = {self.binary: b"also not executable", "LICENSE": b"license\n", "build.json": SUITE.json_bytes(build),
                      "cli-help.json": SUITE.json_bytes({"schema_version": 1, "validation": "cli_help_only", "checks": [
                          {"arguments": arguments, "stdout": "\n".join(markers)} for arguments, markers in SUITE.CLI.HELP_CHECKS]})}
        self.files["SHA256SUMS"] = SUITE.checksums(self.files)
        for name, data in self.files.items():
            (self.cli / name).write_bytes(data)

    def command(self, arguments, cwd):
        args = list(map(str, arguments))
        if args == ["git", "rev-parse", "--show-toplevel"]:
            return str(self.root) + "\n"
        if args == ["git", "rev-parse", "HEAD"]:
            return SOURCE + "\n"
        if args[:2] == ["git", "status"]:
            return ""
        if args[:3] == ["go", "version", "-m"]:
            name = Path(args[3]).name
            expected = self.mcp.read_bytes() if name.startswith("nb1-mcp") else self.files[self.binary]
            self_data = Path(args[3]).read_bytes()
            if self_data != expected:
                raise AssertionError("Go must inspect the exact archived binary bytes")
            return self.texts[SUITE.MCP if name.startswith("nb1-mcp") else SUITE.CORE]
        if args[:5] == ["go", "list", "-mod=readonly", "-m", "-json"]:
            values = self.selected if args[-1] == "all" else MODULES
            return "\n".join(map(json.dumps, values))
        raise AssertionError("unexpected command (especially an executed binary): " + str(args))

    def stage(self):
        with patch.object(SUITE, "command", self.command):
            SUITE.stage(self.root, self.cli, self.mcp, self.output)
        return SUITE.verify(self.output)


def refresh_outer(directory, suite):
    (directory / "suite.json").write_bytes(SUITE.json_bytes(suite))
    files = {p.name: p.read_bytes() for p in directory.iterdir() if p.name != "SHA256SUMS"}
    (directory / "SHA256SUMS").write_bytes(SUITE.checksums(files))


def rewrite_archive(fixture, suite, key, mutate):
    component = suite["components"][key]
    path = fixture.output / component["archive"]
    binary = component.get("binary")
    windows = key == "skill" or fixture.target["GOOS"] == "windows"
    expected = SUITE.SKILL_FILES if key == "skill" else {
        binary, "LICENSE", "build.json", "SHA256SUMS", "cli-help.json" if key == "cli" else "README.md"}
    files = SUITE.read_archive(path.read_bytes(), expected, binary, windows)
    mutate(files)
    data = SUITE.archive_bytes(files, binary, windows)
    path.write_bytes(data)
    component["sha256"] = SUITE.sha(data)
    refresh_outer(fixture.output, suite)


class CandidateSuiteTests(unittest.TestCase):
    def test_round_trip_mac_and_windows_never_executes_binaries(self):
        for goos in ("darwin", "windows"):
            with self.subTest(goos=goos), tempfile.TemporaryDirectory() as temporary:
                fixture = Fixture(Path(temporary), goos)
                suite = fixture.stage()
                self.assertIs(suite["release"], False)
                self.assertEqual(suite["source"], SOURCE)
                self.assertEqual(suite["embedded_web"], fixture.web)
                self.assertEqual(set(suite["components"]["skill"]["files"]), SUITE.SKILL_FILES)
                self.assertEqual(len(list(fixture.output.iterdir())), 5)
                self.assertNotIn(b"/private/not-published", (fixture.output / "suite.json").read_bytes())
                with patch.object(SUITE, "command", side_effect=AssertionError("offline verification ran an external tool")), \
                        patch.object(SUITE.CLI, "command", side_effect=AssertionError("offline verification executed CLI")):
                    self.assertEqual(suite, SUITE.verify(fixture.output))

    def test_output_is_new_and_preserves_existing_data(self):
        with tempfile.TemporaryDirectory() as temporary:
            fixture = Fixture(Path(temporary))
            fixture.output.mkdir()
            (fixture.output / "keep").write_text("original")
            with self.assertRaises(ValueError):
                fixture.stage()
            self.assertEqual((fixture.output / "keep").read_text(), "original")

    def test_dirty_source_and_source_change_are_rejected_without_output(self):
        with tempfile.TemporaryDirectory() as temporary:
            fixture = Fixture(Path(temporary))
            with patch.object(SUITE, "command", fixture.command), patch.object(SUITE, "source_identity", side_effect=[SOURCE, "3" * 40]):
                with self.assertRaises(ValueError):
                    SUITE.stage(fixture.root, fixture.cli, fixture.mcp, fixture.output)
            self.assertFalse(fixture.output.exists())
            def dirty(args, cwd):
                return "?? local-test.log\n" if list(args)[:2] == ["git", "status"] else fixture.command(args, cwd)
            with patch.object(SUITE, "command", dirty), self.assertRaises(ValueError):
                SUITE.source_identity(fixture.root)

    def test_wrong_or_missing_mcp_vcs_and_target_rejected(self):
        mutations = [("vcs.revision=" + SOURCE, "vcs.revision=" + "3" * 40),
                     ("vcs.modified=false", "vcs.modified=true"), ("GOARCH=arm64", "GOARCH=amd64"),
                     ("vcs=git", "other=git"), ("vcs.revision=" + SOURCE, "other.revision=" + SOURCE)]
        for old, new in mutations:
            with self.subTest(new=new), tempfile.TemporaryDirectory() as temporary:
                fixture = Fixture(Path(temporary))
                fixture.texts[SUITE.MCP] = fixture.texts[SUITE.MCP].replace(old, new)
                with self.assertRaises(ValueError):
                    fixture.stage()
                self.assertFalse(fixture.output.exists())

    def test_missing_extra_changed_or_symlink_outer_files_rejected(self):
        for mutation in ("missing", "extra", "changed", "symlink"):
            with self.subTest(mutation=mutation), tempfile.TemporaryDirectory() as temporary:
                fixture = Fixture(Path(temporary))
                suite = fixture.stage()
                archive = fixture.output / suite["components"]["mcp"]["archive"]
                if mutation == "missing":
                    archive.unlink()
                elif mutation == "extra":
                    (fixture.output / "process-notes.md").write_text("local only")
                elif mutation == "changed":
                    archive.write_bytes(b"changed")
                else:
                    archive.unlink()
                    try:
                        archive.symlink_to(fixture.mcp)
                    except OSError:
                        continue
                with self.assertRaises(ValueError):
                    SUITE.verify(fixture.output)

    def test_inner_extra_files_and_binary_tampering_rejected_even_with_outer_checksums_updated(self):
        for key in ("cli", "mcp", "skill"):
            for extra in (False, True):
                with self.subTest(key=key, extra=extra), tempfile.TemporaryDirectory() as temporary:
                    fixture = Fixture(Path(temporary))
                    suite = fixture.stage()
                    target = "process-screenshot.png" if extra else suite["components"][key].get("binary", "SKILL.md")
                    rewrite_archive(fixture, suite, key, lambda files: files.update({target: b"tampered"}))
                    with self.assertRaises(ValueError):
                        SUITE.verify(fixture.output)

    def test_component_source_web_dependency_and_skill_receipts_are_bound(self):
        mutations = [lambda s: s.update(source="4" * 40),
                     lambda s: s["embedded_web"].update(source_commit="4" * 40),
                     lambda s: s["components"]["mcp"]["build_info"]["settings"].update({"vcs.revision": "4" * 40}),
                     lambda s: s["components"]["cli"]["modules"][0].update(Sum="h1:other"),
                     lambda s: s["components"]["skill"].update(total_bytes=0),
                     lambda s: s["components"]["skill"]["files"]["SKILL.md"].update(sha256="0" * 64)]
        for index, mutate in enumerate(mutations):
            with self.subTest(index=index), tempfile.TemporaryDirectory() as temporary:
                fixture = Fixture(Path(temporary))
                suite = fixture.stage()
                mutate(suite)
                refresh_outer(fixture.output, suite)
                with self.assertRaises(ValueError):
                    SUITE.verify(fixture.output)

    def test_skill_source_extra_and_symlink_rejected(self):
        with tempfile.TemporaryDirectory() as temporary:
            fixture = Fixture(Path(temporary))
            extra = fixture.root / "skills/blink1-control/screenshot.png"
            extra.write_bytes(b"local")
            with self.assertRaises(ValueError):
                fixture.stage()
            self.assertFalse(fixture.output.exists())
            extra.unlink()
            source = fixture.root / "skills/blink1-control/SKILL.md"
            source.unlink()
            try:
                source.symlink_to(fixture.root / "LICENSE")
            except OSError:
                return
            with self.assertRaises(ValueError):
                fixture.stage()

    def test_module_graph_rejects_other_replacement_even_when_not_linked(self):
        with tempfile.TemporaryDirectory() as temporary:
            fixture = Fixture(Path(temporary))
            invalid = copy.deepcopy(fixture.selected)
            invalid.append({"Path": "example.com/unlinked", "Version": "v1", "Sum": "h1:public",
                            "Replace": {"Path": "example.com/fork", "Version": "v2"}})
            with self.assertRaises(ValueError):
                SUITE.module_records(invalid, fixture.root, {SUITE.CORE, SUITE.MCP_SDK})
            invalid = copy.deepcopy(fixture.selected)
            invalid[1]["Replace"]["Dir"] = str(fixture.root.parent)
            with self.assertRaises(ValueError):
                SUITE.module_records(invalid, fixture.root, {SUITE.CORE, SUITE.MCP_SDK})
            invalid[1]["Replace"]["Dir"] = str(fixture.root)
            invalid[1]["Replace"]["Path"] = "../../other-checkout"
            with self.assertRaises(ValueError):
                SUITE.module_records(invalid, fixture.root, {SUITE.CORE, SUITE.MCP_SDK})

    def test_unused_lazy_graph_module_does_not_require_a_downloaded_sum(self):
        with tempfile.TemporaryDirectory() as temporary:
            fixture = Fixture(Path(temporary))
            fixture.selected.append({"Path": "example.com/unused", "Version": "v1.0.0"})
            suite = fixture.stage()
            self.assertEqual({m["Path"] for m in suite["components"]["mcp"]["modules"]}, {SUITE.CORE, SUITE.MCP_SDK})
            fixture.output.rename(fixture.output.with_name("previous"))
            fixture.selected[-2].pop("Sum")
            with self.assertRaises(ValueError):
                fixture.stage()


class HostileArchiveTests(unittest.TestCase):
    def raw_archive(self, entries, windows=False):
        stream = io.BytesIO()
        if windows:
            with warnings.catch_warnings(), zipfile.ZipFile(stream, "w") as archive:
                warnings.simplefilter("ignore", UserWarning)
                for name, mode, data in entries:
                    info = zipfile.ZipInfo(name)
                    info.create_system = 3
                    info.external_attr = mode << 16
                    archive.writestr(info, data)
        else:
            with tarfile.open(fileobj=stream, mode="w:gz") as archive:
                for name, mode, data in entries:
                    info = tarfile.TarInfo(name)
                    info.mode = stat.S_IMODE(mode)
                    kind = stat.S_IFMT(mode)
                    if kind == stat.S_IFLNK:
                        info.type, info.linkname = tarfile.SYMTYPE, "outside"
                    elif kind == stat.S_IFIFO:
                        info.type = tarfile.FIFOTYPE
                    elif kind == stat.S_IFDIR:
                        info.type = tarfile.DIRTYPE
                    else:
                        info.size = len(data)
                    archive.addfile(info, io.BytesIO(data) if info.isfile() else None)
        return stream.getvalue()

    def test_path_traversal_absolute_duplicate_and_special_entries_rejected(self):
        cases = [[("../outside", stat.S_IFREG | 0o644, b"x")],
                 [("/outside", stat.S_IFREG | 0o644, b"x")],
                 [("C:\\outside", stat.S_IFREG | 0o644, b"x")],
                 [("good", stat.S_IFREG | 0o644, b"x")] * 2,
                 [("good", stat.S_IFLNK | 0o644, b"outside")],
                 [("good", stat.S_IFIFO | 0o644, b"")],
                 [("good", stat.S_IFDIR | 0o644, b"")],
                 [("good", stat.S_IFREG | 0o4755, b"x")]]
        for windows in (False, True):
            for entries in cases:
                with self.subTest(windows=windows, entries=entries), self.assertRaises(ValueError):
                    SUITE.read_archive(self.raw_archive(entries, windows), {"good"}, windows=windows)

    def test_missing_executable_bit_and_expanded_size_limit_rejected(self):
        for windows in (False, True):
            with self.subTest(windows=windows):
                data = self.raw_archive([("nb1", stat.S_IFREG | 0o644, b"x")], windows)
                with self.assertRaises(ValueError):
                    SUITE.read_archive(data, {"nb1"}, "nb1", windows)
                data = SUITE.archive_bytes({"good": b"0" * 4096}, windows=windows)
                with patch.object(SUITE, "MAX_BYTES", 2048), self.assertRaises(ValueError):
                    SUITE.read_archive(data, {"good"}, windows=windows)

    def test_hardlink_and_duplicate_json_keys_rejected(self):
        stream = io.BytesIO()
        with tarfile.open(fileobj=stream, mode="w:gz") as archive:
            info = tarfile.TarInfo("good")
            info.mode, info.type, info.linkname = 0o644, tarfile.LNKTYPE, "outside"
            archive.addfile(info)
        with self.assertRaises(ValueError):
            SUITE.read_archive(stream.getvalue(), {"good"})
        with self.assertRaises(ValueError):
            SUITE.read_json(b'{"release":false,"release":true}')

    def test_expanded_pax_metadata_is_bounded_before_tar_iteration(self):
        stream = io.BytesIO()
        with tarfile.open(fileobj=stream, mode="w:gz", format=tarfile.PAX_FORMAT) as archive:
            info = tarfile.TarInfo("good")
            info.mode, info.size = 0o644, 1
            info.pax_headers = {"comment": "a" * 65536}
            archive.addfile(info, io.BytesIO(b"x"))
        self.assertLess(len(stream.getvalue()), 1024)
        with patch.object(SUITE, "MAX_BYTES", 1024), self.assertRaises(ValueError):
            SUITE.read_archive(stream.getvalue(), {"good"})


RELEASE_SPEC = importlib.util.spec_from_file_location("release_suite", Path(__file__).with_name("release-suite.py"))
RELEASE = importlib.util.module_from_spec(RELEASE_SPEC)
RELEASE_SPEC.loader.exec_module(RELEASE)
RELEASE_VERSION = "v0.1.0"


class ReleaseFixture:
    """Structured release fixtures; bytes deliberately are not executable."""
    def __init__(self, directory, goos="darwin", arch="arm64", tagged=False):
        self.old = Fixture(directory, goos)
        self.old.target["GOARCH"] = arch
        self.target, self.root, self.out = self.old.target, self.old.root, self.old.output
        self.context = {"event": "push" if tagged else "workflow_dispatch", "version": RELEASE_VERSION,
                        "source": SOURCE, "ref": "refs/tags/" + RELEASE_VERSION if tagged else "refs/heads/release-test",
                        "publication_eligible": tagged, "mode": "tag-build" if tagged else "rehearsal"}
        for name in RELEASE.LICENSES:
            path = self.root / "internal/webui/dist" / name
            path.parent.mkdir(parents=True, exist_ok=True)
            path.write_bytes((name + "\n").encode())
        self.cli_input = directory / "release-cli-input"
        self.cli_input.write_bytes(b"non-executable CLI fixture")
        self.modules = {
            "cli": [{"Path": path, "Version": version, "Sum": "h1:fixture"} for path, version in RELEASE.FOUNDATION.items()],
            "mcp": copy.deepcopy(MCP_MODULES)}
        self.info = {}
        for key, module in [("cli", SUITE.CORE), ("mcp", SUITE.MCP)]:
            info = SUITE.build_info(build_text(module, self.target, self.modules[key]))
            info["go_version"] = RELEASE.TOOLCHAIN
            info["settings"]["CGO_ENABLED"] = "0" if key == "mcp" else "1"
            flag = "main.buildVersion" if key == "mcp" else SUITE.CORE + "/config.GitSummary"
            info["settings"]["-ldflags"] = "-X " + flag + "=" + RELEASE_VERSION
            self.info[key] = info
        self.refresh_binary_bytes()

    def refresh_binary_bytes(self):
        self.cli_input.write_bytes(b"release-fixture:" + SUITE.json_bytes(self.info["cli"]))
        self.old.mcp.write_bytes(b"release-fixture:" + SUITE.json_bytes(self.info["mcp"]))

    def dependencies(self, _binary, goos):
        if goos == "darwin":
            return {"kind": "mach-o", "libraries": ["/usr/lib/libSystem.B.dylib"], "minimum_os_from_binary": "15.0",
                    "notarization_claimed": False, "signature_details": "fixture only", "signature_inspection_exit": 0}
        if goos == "linux":
            return {"kind": "elf", "linkage": "dynamic", "ldd": "libc.so.6 => /lib/libc.so.6",
                    "glibc_versions": ["2.34", "2.39"], "required_glibc_from_binary": "2.39", "all_dependencies_resolved": True}
        return {"kind": "pe", "DLLs": ["KERNEL32.dll"], "system_DLLs_only": True}

    def smoke(self, *_):
        return {"status": "PASS", "system_PATH_only": True, "MCP_version": RELEASE_VERSION,
                "MCP_tools": 12, "MCP_protocol_only": True, "USB_access": False, "daemon_started": False,
                "CLI_version": {"version": RELEASE_VERSION, "build": {"revision": SOURCE, "modified": "false",
                    "os": self.target["GOOS"], "arch": self.target["GOARCH"], "cgo_enabled": True}}}

    def notices(self, root, modules, web=None, mcp=False):
        files = {"licenses/go/LICENSE": b"Go fixture license\n"}
        records = {"licenses/go/LICENSE": {"source": {"kind": "go-toolchain", "version": RELEASE.TOOLCHAIN},
                    "bytes": len(files["licenses/go/LICENSE"]), "sha256": SUITE.sha(files["licenses/go/LICENSE"])}}
        for module in modules:
            origin = ({"kind": "same-checkout", "module": module["Path"], "commit": SOURCE} if module.get("Replace") else
                      {"kind": "public-module", "module": module["Path"], "version": module["Version"], "sum": module["Sum"]})
            for name in (["LICENSE", "LICENSE-old"] if module["Path"] == "github.com/b1ug/gid" else ["LICENSE"]):
                path = RELEASE.notice_prefix(module, SOURCE) + name
                files[path] = (module["Path"] + name + "\n").encode()
                records[path] = {"source": origin, "bytes": len(files[path]), "sha256": SUITE.sha(files[path])}
        if web:
            for name in RELEASE.LICENSES:
                path = "licenses/" + name; files[path] = (name + "\n").encode()
                records[path] = {"source": {"kind": "embedded-web", "commit": web["source_commit"], "path": name},
                                 "bytes": len(files[path]), "sha256": SUITE.sha(files[path])}
        return files, records

    def stage(self):
        def info(_bytes, name, _root):
            return self.info["mcp" if name.startswith("nb1-mcp") else "cli"]
        with patch.object(RELEASE.BASE, "source_identity", return_value=SOURCE), \
                patch.object(RELEASE.BASE, "binary_build_text", side_effect=lambda data, name, root: info(data, name, root)), \
                patch.object(RELEASE.BASE, "build_info", side_effect=lambda value: value), \
                patch.object(RELEASE, "selected_modules", side_effect=lambda root, value, mcp=False: self.modules["mcp" if mcp else "cli"]), \
                patch.object(RELEASE, "web_inventory", return_value=self.old.web), \
                patch.object(RELEASE, "collect_notices", side_effect=self.notices), \
                patch.object(RELEASE, "runtime_dependencies", side_effect=self.dependencies), \
                patch.object(RELEASE, "native_smoke", side_effect=self.smoke):
            return RELEASE.stage(self.root, self.cli_input, self.old.mcp, self.out, RELEASE_VERSION, SOURCE, self.context)


def refresh_release_platform(directory, value):
    (directory / "platform.json").write_bytes(SUITE.json_bytes(value))
    files = {p.name: p.read_bytes() for p in directory.iterdir() if p.name != "SHA256SUMS"}
    (directory / "SHA256SUMS").write_bytes(SUITE.checksums(files))


class ReleaseSuiteTests(unittest.TestCase):
    def setUp(self):
        def inspect(data, name):
            if not data.startswith(b"release-fixture:"):
                raise ValueError("fixture binary has no Go build info")
            return SUITE.read_json(data.removeprefix(b"release-fixture:"))
        self.inspection = patch.object(RELEASE, "archived_build_info", side_effect=inspect)
        self.inspection.start()
        self.addCleanup(self.inspection.stop)

    def test_five_native_versioned_platforms_assemble_and_rehearsal_cannot_publish(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary); platforms = root / "platforms"; platforms.mkdir()
            for goos, arch in sorted(RELEASE.TARGETS):
                parent = root / (goos + arch); parent.mkdir()
                fixture = ReleaseFixture(parent, goos, arch)
                fixture.out = platforms / (goos + "-" + arch)
                value = fixture.stage()
                skill_record = value["components"]["skill"]
                self.assertEqual(set(skill_record["files"]), SUITE.SKILL_FILES | {"LICENSE"})
                skill = SUITE.read_archive((fixture.out / skill_record["archive"]).read_bytes(), RELEASE.SKILL_FILES, windows=True)
                self.assertEqual(skill["LICENSE"], (fixture.root / "LICENSE").read_bytes())
            output = root / "release"
            manifest = RELEASE.assemble(platforms, output, RELEASE_VERSION, SOURCE)
            self.assertEqual(len(manifest["platforms"]), 5)
            self.assertEqual(len(manifest["assets"]), 11)
            self.assertEqual(len(list(output.iterdir())), 13)
            self.assertTrue(RELEASE.verify_release(output, RELEASE_VERSION, SOURCE)["release"])
            with self.assertRaisesRegex(ValueError, "rehearsal cannot publish"):
                RELEASE.verify_release(output, RELEASE_VERSION, SOURCE, publish=True)
            downloaded = root / "downloaded"
            import shutil
            shutil.copytree(output, downloaded)
            RELEASE.verify_download(output, downloaded)
            (downloaded / "SHA256SUMS").write_bytes(b"wrong\n")
            with self.assertRaises(ValueError): RELEASE.verify_download(output, downloaded)

    def test_tag_context_requires_official_version_tag_source_and_master(self):
        def fake_git(_root, *args):
            return "" if args[0] == "status" else SOURCE
        env = {"GITHUB_EVENT_NAME": "push", "GITHUB_REF": "refs/tags/" + RELEASE_VERSION,
               "GITHUB_REPOSITORY": "b1ug/nb1"}
        with patch.object(RELEASE, "git", side_effect=fake_git), patch.object(RELEASE, "run") as process:
            self.assertTrue(RELEASE.context(Path("."), env)["publication_eligible"])
            self.assertIn("--is-ancestor", process.call_args.args[0])
            for change in [{"GITHUB_REPOSITORY": "someone/fork"}, {"GITHUB_REF": "refs/heads/master"},
                           {"GITHUB_REF": "refs/tags/v0.1.0\npublish=true"}]:
                with self.subTest(change=change), self.assertRaises(ValueError):
                    RELEASE.context(Path("."), {**env, **change})
            with patch.object(RELEASE, "git", side_effect=lambda root, *args: "2" * 40 if args[1:2] == ("refs/tags/" + RELEASE_VERSION + "^{commit}",) else fake_git(root, *args)), self.assertRaises(ValueError):
                RELEASE.context(Path("."), env)

    def test_dispatch_and_pull_request_are_readonly_rehearsals(self):
        with patch.object(RELEASE, "git", side_effect=lambda root, *args: "" if args[0] == "status" else SOURCE):
            for event in ("workflow_dispatch", "pull_request"):
                env = {"GITHUB_EVENT_NAME": event, "GITHUB_REF": "refs/heads/test", "INPUT_VERSION": RELEASE_VERSION, "INPUT_PUBLISH": "false"}
                self.assertFalse(RELEASE.context(Path("."), env)["publication_eligible"])
                with self.assertRaises(ValueError): RELEASE.context(Path("."), {**env, "INPUT_PUBLISH": "true"})

    def test_dev_wrong_source_cgo_toolchain_and_foundation_are_rejected(self):
        with tempfile.TemporaryDirectory() as temporary:
            fixture = ReleaseFixture(Path(temporary))
            original = fixture.info["cli"]
            for field, value in [("-ldflags", "-X github.com/b1ug/nb1/config.GitSummary=dev"),
                                 ("vcs.modified", "true"), ("vcs.revision", "2" * 40), ("CGO_ENABLED", "0")]:
                changed = copy.deepcopy(original); changed["settings"][field] = value
                with self.subTest(field=field), self.assertRaises(ValueError):
                    RELEASE.check_binary(changed, fixture.modules["cli"], RELEASE_VERSION, SOURCE, fixture.target)
            changed = copy.deepcopy(original); changed["go_version"] = "go1.27.3"
            with self.assertRaises(ValueError): RELEASE.check_binary(changed, fixture.modules["cli"], RELEASE_VERSION, SOURCE, fixture.target)
            modules = copy.deepcopy(fixture.modules["cli"]); modules[0]["Version"] = "v0.0.0-local"
            changed = copy.deepcopy(original); changed["dependencies"] = modules
            with self.assertRaises(ValueError): RELEASE.check_binary(changed, modules, RELEASE_VERSION, SOURCE, fixture.target)

    def test_trimpath_without_ldflags_still_requires_real_native_version_evidence(self):
        with tempfile.TemporaryDirectory() as temporary:
            fixture = ReleaseFixture(Path(temporary))
            for info in fixture.info.values(): info["settings"].pop("-ldflags")
            fixture.refresh_binary_bytes()
            value = fixture.stage()
            self.assertEqual(value["runtime"]["CLI_version"]["version"], RELEASE_VERSION)
            self.assertEqual(value["runtime"]["MCP_version"], RELEASE_VERSION)
            value["runtime"]["MCP_version"] = "dev"
            refresh_release_platform(fixture.out, value)
            with self.assertRaises(ValueError): RELEASE.verify_platform(fixture.out, RELEASE_VERSION, SOURCE)

    def test_candidate_flags_missing_files_wrong_runtime_and_extra_assets_rejected(self):
        with tempfile.TemporaryDirectory() as temporary:
            fixture = ReleaseFixture(Path(temporary)); value = fixture.stage()
            for mutate in [lambda v: v.update(release=False),
                           lambda v: v["runtime"].update(system_PATH_only=False),
                           lambda v: v["runtime"].update(MCP_version="1.0.0-dev"),
                           lambda v: v["runtime"].update(extracted_archives=False),
                           lambda v: v["runtime"]["CLI_version"]["build"].update(revision="2" * 40)]:
                changed = copy.deepcopy(value); mutate(changed); refresh_release_platform(fixture.out, changed)
                with self.assertRaises(ValueError): RELEASE.verify_platform(fixture.out, RELEASE_VERSION, SOURCE)
            refresh_release_platform(fixture.out, value)
            (fixture.out / "unexpected.txt").write_bytes(b"extra")
            with self.assertRaises(ValueError): RELEASE.verify_platform(fixture.out, RELEASE_VERSION, SOURCE)

    def test_offline_verifier_reinspects_rehashed_binary_bytes_and_rejects_identity_drift(self):
        with tempfile.TemporaryDirectory() as temporary:
            fixture = ReleaseFixture(Path(temporary)); original = fixture.stage()
            record = original["components"]["mcp"]
            content = SUITE.read_archive((fixture.out / record["archive"]).read_bytes(), set(record["files"]), record["binary"])
            alternatives = [b"harmless text without Go build information"]
            for field, wrong in [("vcs.revision", "2" * 40), ("GOARCH", "amd64")]:
                info = copy.deepcopy(fixture.info["mcp"]); info["settings"][field] = wrong
                alternatives.append(b"release-fixture:" + SUITE.json_bytes(info))
            for replacement in alternatives:
                with self.subTest(replacement=replacement[:50]):
                    value = copy.deepcopy(original); item = value["components"]["mcp"]
                    changed = dict(content); changed[item["binary"]] = replacement
                    changed["SHA256SUMS"] = SUITE.checksums({n: b for n, b in changed.items() if n != "SHA256SUMS"})
                    archive = SUITE.archive_bytes(changed, item["binary"])
                    (fixture.out / item["archive"]).write_bytes(archive)
                    item.update(sha256=SUITE.sha(archive), binary_sha256=SUITE.sha(replacement),
                                files={n: SUITE.sha(b) for n, b in changed.items()})
                    refresh_release_platform(fixture.out, value)
                    with self.assertRaisesRegex(ValueError, "Go build info|build info differs"):
                        RELEASE.verify_platform(fixture.out, RELEASE_VERSION, SOURCE)

    def test_offline_release_rejects_one_actual_target_repeated_under_five_keys(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary); fixture = ReleaseFixture(root); platform = fixture.stage()
            directory = root / "forged"; directory.mkdir()
            files = {record["archive"]: (fixture.out / record["archive"]).read_bytes()
                     for record in platform["components"].values()}
            manifest = {"schema_version": 1, "artifact_kind": "nb1-release-suite", "release": True,
                        "version": RELEASE_VERSION, "source": SOURCE, "build_context": platform["build_context"],
                        "embedded_web": platform["embedded_web"], "scope": RELEASE.SCOPE,
                        "platforms": {os_name + "-" + arch: copy.deepcopy(platform) for os_name, arch in RELEASE.TARGETS},
                        "assets": {name: {"sha256": SUITE.sha(data), "bytes": len(data)} for name, data in files.items()}}
            files["release.json"] = SUITE.json_bytes(manifest); files["SHA256SUMS"] = SUITE.checksums(files)
            for name, data in files.items(): (directory / name).write_bytes(data)
            with self.assertRaisesRegex(ValueError, "platform key or unique target"):
                RELEASE.verify_release(directory, RELEASE_VERSION, SOURCE)

    def test_archived_build_info_uses_static_exact_byte_inspection(self):
        self.inspection.stop()
        with tempfile.TemporaryDirectory() as temporary:
            fixture = ReleaseFixture(Path(temporary))
            text = build_text(SUITE.MCP, fixture.target, fixture.modules["mcp"])
            with patch.object(RELEASE.BASE, "binary_build_text", return_value=text) as reader:
                value = RELEASE.archived_build_info(b"foreign executable bytes", "nb1-mcp.exe")
            self.assertEqual(value["module"], SUITE.MCP)
            self.assertEqual(reader.call_args.args[:2], (b"foreign executable bytes", "nb1-mcp.exe"))

    def test_native_dependencies_distinguish_system_runtime_and_static_elf(self):
        valid = {"kind": "elf", "linkage": "static", "glibc_versions": [], "required_glibc_from_binary": None,
                 "all_dependencies_resolved": True}
        RELEASE.check_native_dependencies(valid, "linux")
        for goos, value in [("linux", {**valid, "required_glibc_from_binary": "2.39"}),
                            ("windows", {"kind": "pe", "DLLs": ["libwinpthread-1.dll"], "system_DLLs_only": True}),
                            ("darwin", {"kind": "mach-o", "libraries": ["@rpath/bad.dylib"], "minimum_os_from_binary": "15.0", "notarization_claimed": False})]:
            with self.subTest(goos=goos), self.assertRaises(ValueError): RELEASE.check_native_dependencies(value, goos)

    def test_original_linked_module_and_toolchain_notices_are_collected_and_bound(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary); directory = root / "gid"; directory.mkdir()
            goroot = root / "go"; goroot.mkdir(); (goroot / "LICENSE").write_bytes(b"Go license\n")
            (directory / "LICENSE").write_bytes(b"GID license\n")
            (directory / "LICENSE-old").write_bytes(b"Original native license\n")
            module = {"Path": "github.com/b1ug/gid", "Version": "v0.0.3", "Sum": "h1:public"}
            row = {**module, "Dir": str(directory)}
            def command(argv, cwd):
                return str(goroot) if argv[1:3] == ["env", "GOROOT"] else json.dumps(row)
            with patch.object(RELEASE.BASE, "command", side_effect=command), patch.object(RELEASE.BASE, "source_identity", return_value=SOURCE):
                files, records = RELEASE.collect_notices(root, [module])
            prefix = RELEASE.notice_prefix(module, SOURCE)
            self.assertEqual(files[prefix + "LICENSE-old"], b"Original native license\n")
            self.assertEqual(records[prefix + "LICENSE-old"]["source"]["sum"], module["Sum"])
            RELEASE.check_notices(records, [module], SOURCE, None)
            records.pop(prefix + "LICENSE-old")
            with self.assertRaisesRegex(ValueError, "GID original notices"):
                RELEASE.check_notices(records, [module], SOURCE, None)
            (directory / "LICENSE").unlink(); (directory / "LICENSE-old").unlink()
            with patch.object(RELEASE.BASE, "command", side_effect=command), patch.object(RELEASE.BASE, "source_identity", return_value=SOURCE), self.assertRaisesRegex(ValueError, "no bounded top-level notices"):
                RELEASE.collect_notices(root, [module])

    def test_assembly_refuses_missing_or_mixed_platform_contexts(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary); platforms = root / "platforms"; platforms.mkdir()
            for index, (goos, arch) in enumerate(sorted(RELEASE.TARGETS)):
                parent = root / (goos + arch); parent.mkdir()
                fixture = ReleaseFixture(parent, goos, arch, tagged=index == 0)
                fixture.out = platforms / (goos + "-" + arch); fixture.stage()
            with self.assertRaisesRegex(ValueError, "contexts differ"):
                RELEASE.assemble(platforms, root / "mixed", RELEASE_VERSION, SOURCE)
            import shutil
            shutil.rmtree(next(platforms.iterdir()))
            with self.assertRaisesRegex(ValueError, "five platform"):
                RELEASE.assemble(platforms, root / "missing", RELEASE_VERSION, SOURCE)


if __name__ == "__main__":
    unittest.main()
