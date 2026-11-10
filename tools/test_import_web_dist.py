"""Hermetic import boundary tests: temporary assets only, no frontend build."""
import copy
import importlib.util
import json
import os
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest
from unittest import mock

sys.dont_write_bytecode = True
SCRIPT = Path(__file__).with_name("import-web-dist.py")
spec = importlib.util.spec_from_file_location("nb1_web_import", SCRIPT)
importer = importlib.util.module_from_spec(spec)
sys.modules[spec.name] = importer
spec.loader.exec_module(importer)
COMMIT = "1" * 40


def fixture(path, patterns=False, bilingual=False):
    path.mkdir()
    assets = importer.BILINGUAL_ASSETS if bilingual else importer.PATTERN_ASSETS if patterns else importer.ASSETS
    contracts = importer.PATTERN_CONTRACTS if patterns or bilingual else importer.CONTRACTS
    for name in assets:
        (path / name).write_bytes(("fixture " + name + "\n").encode())
    manifest = {
        "schema_version": 1, "product": "nb1-web", "asset_base": "./",
        "source": {"repository": "local:nb1-web", "commit": COMMIT, "dirty": False, "tree_sha256": "2" * 64},
        "build": {"tool": "copy-static/v1", "node": "v24.0.0", "lockfile_sha256": "3" * 64},
        "api_contracts": sorted(contracts),
        "files": [{"path": name, "bytes": (path / name).stat().st_size, "sha256": importer.sha((path / name).read_bytes())} for name in sorted(assets)],
    }
    manifest["source"]["tree_sha256"] = importer.tree_digest({name: (path / name).read_bytes() for name in assets})
    write_manifest(path, manifest)
    return manifest


def write_manifest(path, value):
    (path / "manifest.json").write_text(json.dumps(value, sort_keys=True, separators=(",", ":")) + "\n")


def validate(path):
    return importer.validate_directory(path, COMMIT, importer.sha((path / "manifest.json").read_bytes()))


def initial_destination(path):
    path.mkdir()
    (path / "dist").mkdir()
    (path / "dist" / "PENDING").write_bytes(importer.PENDING)
    (path / "manifest.lock.json").write_bytes(b"{}\n")


def snapshot(path):
    return {str(p.relative_to(path)): p.read_bytes() for p in path.rglob("*") if p.is_file()}


class ImportBoundaryTests(unittest.TestCase):
    def test_bilingual_upgrade_rollback_and_explicit_legacy_restore(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            old = root / "old"; fixture(old, patterns=True)
            localized = root / "bilingual"; fixture(localized, bilingual=True)
            old_bundle, new_bundle = validate(old), validate(localized)
            self.assertEqual(len(new_bundle.files), 13)
            target = root / "webui"; initial_destination(target)
            importer.install(old_bundle, target)
            before = snapshot(target)
            real_replace = os.replace
            failed = [False]
            def fail_new_lock(src, dst):
                if not failed[0] and Path(src).parent.name.startswith(".webui-stage-") and Path(dst).name == "manifest.lock.json":
                    failed[0] = True
                    raise OSError("injected bilingual lock publication failure")
                return real_replace(src, dst)
            with mock.patch.object(importer.os, "replace", side_effect=fail_new_lock):
                with self.assertRaises(OSError): importer.install(new_bundle, target)
            self.assertTrue(failed[0])
            self.assertEqual(snapshot(target), before)
            importer.install(new_bundle, target)
            self.assertEqual(importer.snapshot_destination(target)[0], new_bundle.files)
            importer.install(old_bundle, target)
            self.assertEqual(snapshot(target), before)

    def test_bilingual_profile_rejects_partial_substituted_or_unpinned_assets(self):
        modes = ["missing-i18n", "missing-locales", "missing-pattern", "base-contracts", "unknown-contract", "extra", "tampered", "substituted-pattern", "base-plus-locales"]
        for mode in modes:
            with self.subTest(mode=mode), tempfile.TemporaryDirectory() as temporary:
                path = Path(temporary) / "dist"
                manifest = fixture(path, bilingual=True)
                if mode.startswith("missing-"):
                    name = {"missing-i18n": "i18n.js", "missing-locales": "locales.js", "missing-pattern": "patterns.js"}[mode]
                    (path / name).unlink()
                    manifest["files"] = [v for v in manifest["files"] if v["path"] != name]
                elif mode == "base-contracts": manifest["api_contracts"] = sorted(importer.CONTRACTS)
                elif mode == "unknown-contract": manifest["api_contracts"].append("unknown/v1")
                elif mode == "extra": (path / "extra.js").write_bytes(b"extra")
                elif mode == "tampered": (path / "locales.js").write_bytes(b"tampered")
                elif mode == "substituted-pattern":
                    for name in ["patterns.js", "patterns.css"]: (path / name).unlink()
                    manifest["files"] = [v for v in manifest["files"] if (path / v["path"]).exists()]
                elif mode == "base-plus-locales":
                    for name in ["patterns.html", "patterns.js", "patterns.css"]: (path / name).unlink()
                    manifest["files"] = [v for v in manifest["files"] if (path / v["path"]).exists()]
                    manifest["api_contracts"] = sorted(importer.CONTRACTS)
                write_manifest(path, manifest)
                before = snapshot(path)
                with self.assertRaises(importer.InvalidBundle): validate(path)
                self.assertEqual(snapshot(path), before)

    def test_pattern_profile_upgrade_rollback_and_explicit_legacy_restore(self):
        with tempfile.TemporaryDirectory() as temporary:
            root=Path(temporary)
            old=root/'old';fixture(old)
            extended=root/'patterns';fixture(extended, patterns=True)
            old_bundle,new_bundle=validate(old),validate(extended)
            self.assertEqual(len(old_bundle.files),8)
            self.assertEqual(len(new_bundle.files),11)
            target=root/'webui';initial_destination(target)
            importer.install(old_bundle,target)
            before=snapshot(target)
            real_replace=os.replace
            failed=[False]
            def fail_new_lock(src,dst):
                if not failed[0] and Path(src).parent.name.startswith('.webui-stage-') and Path(dst).name=='manifest.lock.json':
                    failed[0]=True
                    raise OSError('injected extended profile lock publication failure')
                return real_replace(src,dst)
            with mock.patch.object(importer.os,'replace',side_effect=fail_new_lock):
                with self.assertRaises(OSError):importer.install(new_bundle,target)
            self.assertTrue(failed[0]);self.assertEqual(snapshot(target),before)
            importer.install(new_bundle,target)
            self.assertEqual(snapshot(target/'dist'),new_bundle.files)
            self.assertEqual(importer.snapshot_destination(target)[0],new_bundle.files)
            importer.install(old_bundle,target)
            self.assertEqual(snapshot(target),before)

    def test_pattern_profile_is_closed_and_all_or_nothing(self):
        for mode in ['missing-html','missing-js','missing-css','missing-pattern-contract','base-contracts','extra-contract','substituted-base-asset']:
            with self.subTest(mode=mode),tempfile.TemporaryDirectory() as temporary:
                path=Path(temporary)/'dist';manifest=fixture(path,patterns=True)
                if mode.startswith('missing-') and mode.split('-')[1] in ['html','js','css']:
                    (path/('patterns.'+mode.split('-')[1])).unlink()
                elif mode=='missing-pattern-contract':manifest['api_contracts'].remove('nb1-pattern-record/experimental-v2')
                elif mode=='base-contracts':manifest['api_contracts']=sorted(importer.CONTRACTS)
                elif mode=='extra-contract':manifest['api_contracts'].append('unknown/v1')
                elif mode=='substituted-base-asset':
                    # Seven assets with a known optional file cannot masquerade
                    # as the complete seven-asset legacy profile.
                    for name in ['demo.js','patterns.js','patterns.css']:(path/name).unlink()
                    manifest['files']=[v for v in manifest['files'] if (path/v['path']).exists()]
                    manifest['api_contracts']=sorted(importer.CONTRACTS)
                write_manifest(path,manifest)
                before=snapshot(path)
                with self.assertRaises(importer.InvalidBundle):validate(path)
                self.assertEqual(snapshot(path),before)

    def test_check_and_dry_run_do_not_write(self):
        with tempfile.TemporaryDirectory() as temporary:
            source = Path(temporary) / "dist"
            fixture(source)
            before = snapshot(source)
            bundle = validate(source)
            self.assertEqual(bundle.files, before)
            for flag in ["--check", "--dry-run"]:
                result = subprocess.run([sys.executable, str(SCRIPT), str(source), "--source-commit", COMMIT,
                                         "--manifest-sha256", importer.sha(before["manifest.json"]), flag],
                                        capture_output=True, text=True, env=dict(os.environ, PYTHONDONTWRITEBYTECODE="1"))
                self.assertEqual(result.returncode, 0, result.stderr)
                self.assertEqual(json.loads(result.stdout)["status"], "checked" if flag == "--check" else "dry_run")
                self.assertEqual(snapshot(source), before)

    def test_closed_manifest_and_bounds(self):
        variants = [
            ("dirty", lambda m: m["source"].update(dirty=True)),
            ("missing-dirty", lambda m: m["source"].pop("dirty")),
            ("null", lambda m: m["source"].update(dirty=None)),
            ("wrong-commit", lambda m: m["source"].update(commit="4" * 40)),
            ("unknown", lambda m: m.update(environment="no")),
            ("boolean-version", lambda m: m.update(schema_version=True)),
            ("wrong-product", lambda m: m.update(product="other")),
            ("absolute-base", lambda m: m.update(asset_base="/")),
            ("empty-node", lambda m: m["build"].update(node="")),
            ("control-node", lambda m: m["build"].update(node="node\n24")),
            ("bad-tree", lambda m: m["source"].update(tree_sha256="bad")),
            ("false-tree-with-matching-manifest-pin", lambda m: m["source"].update(tree_sha256="4" * 64)),
            ("unknown-contract", lambda m: m["api_contracts"].append("other")),
            ("duplicate-contract", lambda m: m.update(api_contracts=[m["api_contracts"][0]] * 2)),
            ("missing-file", lambda m: m["files"].pop()),
            ("duplicate-file", lambda m: m["files"].__setitem__(0, copy.deepcopy(m["files"][1]))),
            ("traversal", lambda m: m["files"][0].update(path="../LICENSE.txt")),
            ("absolute", lambda m: m["files"][0].update(path="/LICENSE.txt")),
            ("unknown-file", lambda m: m["files"][0].update(path="secret.env")),
            ("missing-bytes", lambda m: m["files"][0].pop("bytes")),
            ("boolean-bytes", lambda m: m["files"][0].update(bytes=True)),
            ("negative-bytes", lambda m: m["files"][0].update(bytes=-1)),
            ("large-file", lambda m: m["files"][0].update(bytes=importer.MAX_BYTES + 1)),
        ]
        for name, mutate in variants:
            with self.subTest(name=name), tempfile.TemporaryDirectory() as temporary:
                path = Path(temporary) / "dist"
                manifest = fixture(path)
                mutate(manifest)
                write_manifest(path, manifest)
                before = snapshot(path)
                with self.assertRaises(importer.InvalidBundle):
                    validate(path)
                self.assertEqual(snapshot(path), before)

    def test_raw_json_and_explicit_pins(self):
        changes = [
            lambda b: b.replace(b'"product":"nb1-web"', b'"product":"nb1-web","product":"nb1-web"'),
            lambda b: b + b"{}",
            lambda b: b.replace(b'"v24.0.0"', b'"\\ud800"'),
            lambda b: b.replace(b'"v24.0.0"', b'"\xff"'),
            lambda b: b.replace(b'"schema_version":1', b'"schema_version":NaN'),
        ]
        for change in changes:
            with self.subTest(change=change), tempfile.TemporaryDirectory() as temporary:
                path = Path(temporary) / "dist"
                fixture(path)
                file = path / "manifest.json"
                file.write_bytes(change(file.read_bytes()))
                with self.assertRaises(importer.InvalidBundle):
                    validate(path)
        with tempfile.TemporaryDirectory() as temporary:
            path = Path(temporary) / "dist"
            fixture(path)
            with self.assertRaises(importer.InvalidBundle):
                importer.validate_directory(path, COMMIT, "0" * 64)
            with self.assertRaises(importer.InvalidBundle):
                importer.validate_directory(path, "../wrong", importer.sha((path / "manifest.json").read_bytes()))

    def test_file_identity_and_total_budget(self):
        for mode in ["extra", "directory", "tampered", "symlink", "hardlink", "root-link", "total"]:
            with self.subTest(mode=mode), tempfile.TemporaryDirectory() as temporary:
                root = Path(temporary)
                path = root / "dist"
                manifest = fixture(path)
                asset = path / "demo.js"
                if mode == "extra": (path / ".env").write_text("fixture")
                elif mode == "directory":
                    asset.unlink(); asset.mkdir()
                elif mode == "tampered": asset.write_text("changed")
                elif mode in ["symlink", "hardlink"]:
                    other = root / "outside"
                    other.write_bytes(asset.read_bytes()); asset.unlink()
                    (os.symlink if mode == "symlink" else os.link)(other, asset)
                elif mode == "root-link":
                    alias = root / "alias"; alias.symlink_to(path, target_is_directory=True); path = alias
                elif mode == "total":
                    for name in ["demo.js", "orbit.js", "demo.css"]:
                        content = b"x" * 100000; (path / name).write_bytes(content)
                        item = next(v for v in manifest["files"] if v["path"] == name)
                        item.update(bytes=len(content), sha256=importer.sha(content))
                    write_manifest(path, manifest)
                with self.assertRaises((importer.InvalidBundle, OSError)):
                    validate(path)

    def test_install_replace_and_rollback(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary); source = root / "source"; fixture(source)
            target = root / "webui"; initial_destination(target)
            bundle = validate(source)
            self.assertEqual(importer.install(bundle, target), [])
            self.assertEqual(snapshot(target / "dist"), bundle.files)
            self.assertEqual((target / "manifest.lock.json").read_bytes(), importer.lock_bytes(bundle))
            before = snapshot(target)
            real_replace = os.replace
            failed = [False]
            def fail_final_lock(src, dst):
                if not failed[0] and Path(src).parent.name.startswith(".webui-stage-") and Path(dst).name == "manifest.lock.json":
                    failed[0] = True
                    raise OSError("injected final lock rename failure")
                return real_replace(src, dst)
            with mock.patch.object(importer.os, "replace", side_effect=fail_final_lock):
                with self.assertRaises(OSError): importer.install(bundle, target)
            self.assertTrue(failed[0])
            self.assertEqual(snapshot(target), before)
            self.assertEqual(set(p.name for p in target.iterdir()), {"dist", "manifest.lock.json"})

    def test_total_budget_includes_manifest_and_is_inclusive(self):
        with tempfile.TemporaryDirectory() as temporary:
            path = Path(temporary) / "dist"
            manifest = fixture(path)
            item = next(v for v in manifest["files"] if v["path"] == "demo.js")
            for _ in range(8):
                other = sum((path / n).stat().st_size for n in importer.ASSETS if n != "demo.js")
                size = importer.MAX_BYTES - other - (path / "manifest.json").stat().st_size
                data = b"x" * size; (path / "demo.js").write_bytes(data)
                item.update(bytes=size, sha256=importer.sha(data))
                manifest["source"]["tree_sha256"] = importer.tree_digest({name: (path / name).read_bytes() for name in importer.ASSETS})
                write_manifest(path, manifest)
                if sum(p.stat().st_size for p in path.iterdir()) == importer.MAX_BYTES:
                    break
            self.assertEqual(validate(path).total_bytes, importer.MAX_BYTES)
            data = (path / "demo.js").read_bytes() + b"x"
            (path / "demo.js").write_bytes(data); item.update(bytes=len(data), sha256=importer.sha(data)); write_manifest(path, manifest)
            with self.assertRaises(importer.InvalidBundle): validate(path)

    def test_destination_refuses_unknown_or_concurrent_state(self):
        for mode in ["unknown", "held", "linked-lock", "linked-parent"]:
            with self.subTest(mode=mode), tempfile.TemporaryDirectory() as temporary:
                root=Path(temporary); source=root/"source";fixture(source)
                target=root/"webui"; initial_destination(target)
                if mode == "unknown": (target/"dist"/"unknown").write_text("keep")
                elif mode == "held": (target/".import.lock").write_text("owner")
                elif mode == "linked-lock": os.link(target/"manifest.lock.json",root/"outside-lock")
                elif mode == "linked-parent":
                    alias=root/"alias";alias.symlink_to(root,target_is_directory=True);target=alias/"webui"
                before=snapshot(target)
                with self.assertRaises((importer.InvalidBundle,OSError)):importer.install(validate(source),target)
                self.assertEqual(snapshot(target),before)


if __name__ == "__main__":
    unittest.main()
