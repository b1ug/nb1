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


if __name__ == "__main__":
    unittest.main()
