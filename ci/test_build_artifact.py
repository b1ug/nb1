import importlib.util
import json
import tempfile
import unittest
from pathlib import Path
from unittest.mock import patch

SPEC = importlib.util.spec_from_file_location("build_artifact", Path(__file__).with_name("check-build-artifact.py"))
ARTIFACT = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(ARTIFACT)
SOURCE = "1" * 40
MODULES = [{"Path": path, "Version": "v0.0.1", "Sum": "h1:public"} for path in ARTIFACT.MODULES]


def build_text():
    lines = ["candidate: go1.27.3", "\tpath\tgithub.com/b1ug/nb1"]
    lines += ["\tdep\t{Path}\t{Version}\t{Sum}".format(**item) for item in MODULES]
    lines += ["\tbuild\t" + pair for pair in ["CGO_ENABLED=1", "GOOS=darwin", "GOARCH=arm64",
                                                   "vcs.revision=" + SOURCE, "vcs.modified=false"]]
    return "\n".join(lines) + "\n"


def help_evidence():
    return {"schema_version": 1, "validation": "cli_help_only", "checks": [
        {"arguments": arguments, "stdout": "\n".join(markers)}
        for arguments, markers in ARTIFACT.HELP_CHECKS]}


class CandidateTests(unittest.TestCase):
    def test_build_identity_accepts_public_native_source(self):
        result = ARTIFACT.check_build_info(build_text(), SOURCE, "darwin", "arm64", MODULES)
        self.assertEqual(result["CGO_ENABLED"], "1")
        self.assertEqual(result["vcs.revision"], SOURCE)

    def test_stub_wrong_source_target_dirty_and_dependency_are_rejected(self):
        for before, after in [("CGO_ENABLED=1", "CGO_ENABLED=0"), ("GOARCH=arm64", "GOARCH=amd64"),
                              ("vcs.revision=" + SOURCE, "vcs.revision=" + "2" * 40),
                              ("vcs.modified=false", "vcs.modified=true"), ("h1:public", "h1:other"),
                              ("github.com/b1ug/nb1", "example.com/other")]:
            with self.subTest(after=after), self.assertRaises(ValueError):
                ARTIFACT.check_build_info(build_text().replace(before, after), SOURCE, "darwin", "arm64", MODULES)
        with self.assertRaises(ValueError):
            ARTIFACT.check_build_info(build_text() + "\t=>\t../local (devel)\n", SOURCE, "darwin", "arm64", MODULES)

    def test_public_module_documents_reject_workspace_or_incomplete_selection(self):
        self.assertEqual(ARTIFACT.module_documents("\n".join(map(json.dumps, MODULES))), MODULES)
        for broken in [MODULES[:1], MODULES + MODULES[:1], [MODULES[0], MODULES[0]],
                       [dict(MODULES[0], Replace={"Dir": "/local"}), MODULES[1]],
                       [dict(MODULES[0], Main=True), MODULES[1]],
                       [dict(MODULES[0], Sum=""), MODULES[1]]]:
            with self.subTest(broken=broken), self.assertRaises(ValueError):
                ARTIFACT.module_documents("\n".join(map(json.dumps, broken)))

    def test_smoke_rejects_missing_commands_and_runtime_claims(self):
        ARTIFACT.check_smoke(help_evidence())
        for mutate in [lambda x: x.update(job={"status": "queued"}),
                       lambda x: x.update(validation="preview_compiled"),
                       lambda x: x.update(schema_version=True),
                       lambda x: x["checks"].pop(),
                       lambda x: x["checks"][1].update(arguments=["control", "--mode", "direct"]),
                       lambda x: x["checks"][2].update(stdout="old CLI help")]:
            value = help_evidence(); mutate(value)
            with self.assertRaises(ValueError):
                ARTIFACT.check_smoke(value)

    def test_smoke_runs_only_help_with_isolated_config(self):
        scratch_paths = []
        calls = []
        def fake_command(args, cwd):
            self.assertEqual(args[2], cwd / "setting.yaml")
            self.assertEqual(args[-1], "--help")
            arguments, markers = ARTIFACT.HELP_CHECKS[len(calls)]
            self.assertEqual(args[3:], arguments)
            self.assertEqual((cwd / "setting.yaml").read_text(), "{}\n")
            scratch_paths.append(cwd)
            calls.append(arguments)
            return "\n".join(markers)
        with patch.object(ARTIFACT, "command", fake_command):
            ARTIFACT.smoke(Path("/not-executed/nb1"))
        self.assertEqual(len(calls), 3)
        self.assertFalse(scratch_paths[0].exists())

    def test_verify_fails_on_tampering_missing_extra_or_symlink(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            (root / "nb1").write_bytes(b"candidate")
            (root / "LICENSE").write_bytes(b"license")
            ARTIFACT.write_json(root / "build.json", {"target": {"GOOS": "darwin"}})
            ARTIFACT.write_json(root / "cli-help.json", help_evidence())
            names = ["LICENSE", "build.json", "nb1", "cli-help.json"]
            checksum = "".join(ARTIFACT.digest(root / n) + "  " + n + "\n" for n in sorted(names))
            (root / "SHA256SUMS").write_text(checksum)
            ARTIFACT.verify(root)
            (root / "nb1").write_bytes(b"tampered")
            with self.assertRaises(ValueError): ARTIFACT.verify(root)
            (root / "nb1").write_bytes(b"candidate")
            (root / "extra").write_bytes(b"unexpected")
            with self.assertRaises(ValueError): ARTIFACT.verify(root)
            (root / "extra").unlink(); (root / "nb1").unlink()
            with self.assertRaises(ValueError): ARTIFACT.verify(root)
            # Symlink creation can require Windows privileges; checksum/missing
            # paths above still run on Windows without skipping the whole case.
            try: (root / "nb1").symlink_to(root / "LICENSE")
            except OSError: return
            with self.assertRaises(ValueError): ARTIFACT.verify(root)


if __name__ == "__main__":
    unittest.main()
