import hashlib
import json
import os
import shutil
import subprocess
import sys
import tempfile
import unittest
from pathlib import Path

from render_formula import DIRECTORY, read_input, render, validate


def fixture():
    # Synthetic inputs exist only in test memory. There is no published release
    # or archive at this test URL. CLI write mode must reject fixture_only=true.
    version = "123.456.789-fixture.1"
    return {"schema_version": 1, "fixture_only": True, "version": version,
            "source_url": "https://github.com/b1ug/nb1/archive/refs/tags/v" + version + ".tar.gz",
            "source_sha256": hashlib.sha256(b"local synthetic fixture, not a remote archive").hexdigest(),
            "source_commit": hashlib.sha256(b"synthetic fixture, not an upstream commit").hexdigest()[:40],
            "min_macos": "sequoia", "components": "nb1-only"}


class FormulaDraftTests(unittest.TestCase):
    def test_synthetic_render_is_memory_only_and_checks_current_help(self):
        data = fixture(); text = render(data, allow_fixture=True)
        self.assertNotIn("@@", text)
        self.assertIn('ENV["CGO_ENABLED"] = "1"', text)
        self.assertIn('ENV["GOWORK"] = "off"', text)
        self.assertIn('system "go", "mod", "download"', text)
        self.assertIn('"-mod=readonly"', text)
        self.assertIn('"--config", testpath/"setting.yaml"', text)
        self.assertIn('[["control", "--help"], "nb1 control [command]"]', text)
        self.assertIn('[["control", "run", "--help"], "nb1 control run SERIAL FILE"]', text)
        self.assertIn('[["service", "render", "--platform", platform', text)
        self.assertIn('on_macos do\n    depends_on macos:', text)
        self.assertNotIn('depends_on :macos', text)
        self.assertNotIn('"--mode"', text)
        self.assertNotIn("service do", text)
        self.assertNotIn("post_install", text)
        with self.assertRaises(ValueError): render(data)

    @unittest.skipUnless(os.environ.get("NB1_HOMEBREW_TEST_BINARY") and shutil.which("ruby"),
                         "set NB1_HOMEBREW_TEST_BINARY to a current nb1 executable and provide Ruby")
    def test_rendered_formula_help_calls_use_current_binary(self):
        # Evaluate the actual in-memory test block. The DSL shim does not fetch,
        # build, install or run Homebrew; only Open3's help calls are executed.
        harness = r'''
require "pathname"
module OS
  def self.mac?; true; end
end
class Formula
  class << self
    attr_reader :test_block
    def desc(*args); end
    def homepage(*args); end
    def url(*args); end
    def version(*args); end
    def sha256(*args); end
    def license(*args); end
    def depends_on(*args); end
    def on_macos(&block); instance_eval(&block); end
    def deny_network_access!; end
    def test(&block); @test_block = block; end
  end
  def bin; Pathname.new(ENV.fetch("NB1_HOMEBREW_TEST_BINARY")).dirname; end
  def testpath; Pathname.new(ENV.fetch("NB1_FORMULA_TESTPATH")); end
  def assert(value, message = nil); raise(message || "assertion failed") unless value; end
  def assert_match(expected, actual); assert(actual.include?(expected), actual); end
  def assert_equal(expected, actual); assert(expected == actual, actual); end
end
'''
        binary = Path(os.environ["NB1_HOMEBREW_TEST_BINARY"]).resolve(strict=True)
        self.assertEqual(binary.name, "nb1", "the macOS Formula installs a binary named nb1")
        text = render(fixture(), allow_fixture=True)
        with tempfile.TemporaryDirectory(prefix="nb1-formula-help-") as temporary:
            result = subprocess.run(["ruby", "-e", harness + text + "\nNb1.new.instance_eval(&Nb1.test_block)\n"],
                                    env=dict(os.environ, NB1_HOMEBREW_TEST_BINARY=str(binary),
                                             NB1_FORMULA_TESTPATH=temporary),
                                    cwd=temporary, capture_output=True, text=True, timeout=30)
            self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
            self.assertEqual(sorted(p.name for p in Path(temporary).iterdir()), ["setting.yaml"])

    def test_required_real_release_values_are_not_available(self):
        data = read_input(DIRECTORY / "release-input.required.json")
        with self.assertRaises(ValueError): validate(data)

    def test_invalid_versions_moving_urls_hashes_and_bundle_are_rejected(self):
        cases = {"version": ["v1.2.3", "1.02.3", "0.0.1", "1.2.3-01", "1.2.3+build", "1.2.3\n"],
                 "source_url": ["http://github.com/b1ug/nb1/archive/main.tar.gz", "https://github.com/b1ug/nb1/actions/artifacts/1", "https://github.com/b1ug/nb1/releases/latest/download/nb1.tar.gz"],
                 "source_sha256": [None, "", "f" * 64, "ABCDEF" * 10, "../../bad"],
                 "source_commit": ["1" * 40, "abc", "#{system('bad')}"] ,
                 "min_macos": [None, "", "linux", "sequoia); system('bad')"],
                 "components": [None, "nb1-and-mcp", "mcp-only"], "fixture_only": [None, 1, "false"]}
        for key, values in cases.items():
            for value in values:
                data = fixture(); data[key] = value
                with self.subTest(key=key, value=value), self.assertRaises(ValueError): validate(data)
        data = fixture(); data["unknown"] = True
        with self.assertRaises(ValueError): validate(data)

    def test_duplicate_null_unknown_and_bounded_input_are_rejected(self):
        with tempfile.TemporaryDirectory() as temporary:
            path = Path(temporary) / "input.json"
            for text in ['{"version":"1.2.3","version":"2.0.0"}', "{" + " " * 65536 + "}", "\ufeff{}"]:
                path.write_text(text, encoding="utf-8")
                with self.assertRaises((ValueError, UnicodeError)): read_input(path)
        for value in [None, [], {}, dict(fixture(), schema_version=True)]:
            with self.assertRaises(ValueError): validate(value)

    def test_cli_does_not_write_formula_for_missing_or_synthetic_release(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary); destination = root / "nb1.rb"
            for source in [DIRECTORY / "release-input.required.json", root / "fixture.json"]:
                if source.name == "fixture.json": source.write_text(json.dumps(fixture()), encoding="utf-8")
                result = subprocess.run([sys.executable, str(DIRECTORY / "render_formula.py"), "--input", str(source), "--out", str(destination)], capture_output=True, text=True)
                self.assertNotEqual(result.returncode, 0)
                self.assertFalse(destination.exists())
            result = subprocess.run([sys.executable, str(DIRECTORY / "render_formula.py"), "--input", str(root / "fixture.json"), "--check-only"], capture_output=True, text=True)
            self.assertEqual(result.returncode, 0, result.stderr)
            status = json.loads(result.stdout)
            self.assertTrue(status["fixture_only"])
            self.assertFalse(status["formula_written"])
            self.assertFalse(status["remote_url_and_source_identity_verified"])
            self.assertEqual(sorted(p.name for p in root.iterdir()), ["fixture.json"])


if __name__ == "__main__":
    unittest.main()
