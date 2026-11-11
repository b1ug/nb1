#!/usr/bin/env python3
"""Import one explicitly pinned, local nb1-web dist. No build or network access."""
import argparse
from dataclasses import dataclass
import hashlib
import json
import os
from pathlib import Path
import re
import stat
import sys
import tempfile

MAX_BYTES = 256 * 1024
MAX_MANIFEST_BYTES = 16 * 1024
ASSETS = frozenset((
    "index.html", "demo.css", "demo.js", "orbit.html", "orbit.css", "orbit.js", "LICENSE.txt",
    "patterns.html", "patterns.js", "patterns.css", "i18n.js", "locales.js",
))
CONTRACTS = frozenset(("nb1-api/v1",))
PENDING = b"Waiting for verified nb1-web dist.\n"


class InvalidBundle(ValueError):
    pass


@dataclass(frozen=True)
class Bundle:
    files: dict
    manifest: dict
    manifest_sha256: str

    @property
    def total_bytes(self):
        return sum(len(value) for value in self.files.values())


def sha(data):
    return hashlib.sha256(data).hexdigest()


def tree_digest(files):
    # All closed asset profiles use ASCII paths. The manifest is not itself
    # part of the source tree hash. Match the frontend builder byte for byte.
    entries = "".join(name + "\0" + sha(files[name]) + "\n" for name in sorted(files) if name != "manifest.json")
    return sha(entries.encode("utf-8"))


def hex_value(value, length):
    return isinstance(value, str) and re.fullmatch("[0-9a-f]{%d}" % length, value) is not None


def require(condition, reason):
    if not condition:
        raise InvalidBundle(reason)


def object_keys(value, keys, label):
    require(type(value) is dict and set(value) == set(keys), label + " fields must match the contract")


def strict_json(data):
    def pairs(values):
        result = {}
        for key, value in values:
            require(key not in result, "duplicate JSON field: " + key)
            result[key] = value
        return result

    def constant(_):
        raise InvalidBundle("non-finite JSON numbers are not accepted")

    def strings(value):
        if value is None:
            raise InvalidBundle("null is not accepted")
        if isinstance(value, str):
            value.encode("utf-8", "strict")
        elif isinstance(value, dict):
            for key, item in value.items():
                strings(key)
                strings(item)
        elif isinstance(value, list):
            for item in value:
                strings(item)

    try:
        result = json.loads(data.decode("utf-8", "strict"), object_pairs_hook=pairs, parse_constant=constant)
        strings(result)
        return result
    except (UnicodeError, json.JSONDecodeError, RecursionError) as error:
        raise InvalidBundle("invalid or lossy JSON: " + str(error)) from error


def plain_info(path, directory=False):
    info = path.lstat()
    reparse = getattr(info, "st_file_attributes", 0) & getattr(stat, "FILE_ATTRIBUTE_REPARSE_POINT", 0x400)
    require(not reparse and not stat.S_ISLNK(info.st_mode), "links and reparse points are not accepted: " + path.name)
    require(stat.S_ISDIR(info.st_mode) if directory else stat.S_ISREG(info.st_mode), "unexpected file type: " + path.name)
    if not directory:
        require(info.st_nlink == 1, "hardlinked files are not accepted: " + path.name)
    return info


def identity(info):
    return info.st_dev, info.st_ino, info.st_mode, info.st_size, info.st_nlink


def read_plain(path, limit):
    before = plain_info(path)
    require(before.st_size <= limit, "file exceeds its byte limit: " + path.name)
    flags = os.O_RDONLY | getattr(os, "O_BINARY", 0) | getattr(os, "O_NOFOLLOW", 0)
    descriptor = os.open(str(path), flags)
    with os.fdopen(descriptor, "rb") as stream:
        require(identity(os.fstat(stream.fileno())) == identity(before), "file changed before read: " + path.name)
        data = stream.read(limit + 1)
        require(identity(os.fstat(stream.fileno())) == identity(before), "file changed during read: " + path.name)
    require(identity(plain_info(path)) == identity(before), "file path changed during read: " + path.name)
    require(len(data) == before.st_size and len(data) <= limit, "file length changed: " + path.name)
    return data


def names(directory):
    plain_info(directory, directory=True)
    result = set()
    with os.scandir(str(directory)) as entries:
        for entry in entries:
            require(len(result) < len(ASSETS) + 1, "too many dist entries")
            result.add(entry.name)
    return result


def validate_directory(directory, expected_commit, expected_manifest_sha):
    require(hex_value(expected_commit, 40), "expected source commit must be 40 lowercase hex characters")
    require(hex_value(expected_manifest_sha, 64), "expected manifest SHA-256 must be 64 lowercase hex characters")
    directory = Path(directory)
    present = names(directory)
    require(present == ASSETS | {"manifest.json"}, "dist must match the complete 13-file v1 asset profile")
    assets, required_contracts = ASSETS, CONTRACTS
    wanted = assets | {"manifest.json"}
    data = read_plain(directory / "manifest.json", MAX_MANIFEST_BYTES)
    require(sha(data) == expected_manifest_sha, "manifest SHA-256 differs from the explicit pin")
    value = strict_json(data)
    object_keys(value, ("schema_version", "product", "asset_base", "source", "build", "api_contracts", "files"), "manifest")
    require(type(value["schema_version"]) is int and value["schema_version"] == 1 and value["product"] == "nb1-web" and value["asset_base"] == "./", "unsupported manifest identity or asset base")
    source = value["source"]
    object_keys(source, ("repository", "commit", "dirty", "tree_sha256"), "source")
    require(source["repository"] == "local:nb1-web" and source["commit"] == expected_commit and source["dirty"] is False and hex_value(source["tree_sha256"], 64), "source must be the pinned clean nb1-web commit")
    build = value["build"]
    object_keys(build, ("tool", "node", "lockfile_sha256"), "build")
    node = build["node"]
    require(build["tool"] == "copy-static/v1" and type(node) is str and 0 < len(node) <= 128 and node.strip() == node and all(32 <= ord(ch) < 127 for ch in node) and hex_value(build["lockfile_sha256"], 64), "invalid build metadata")
    contracts = value["api_contracts"]
    require(type(contracts) is list and len(contracts) == len(required_contracts) and all(type(c) is str for c in contracts) and set(contracts) == required_contracts, "unsupported, incomplete or duplicate API contracts")
    require(type(value["files"]) is list and len(value["files"]) == len(assets), "invalid asset declaration count")
    result = {"manifest.json": data}
    total = len(data)
    for item in value["files"]:
        object_keys(item, ("path", "bytes", "sha256"), "asset")
        name, size = item["path"], item["bytes"]
        require(type(name) is str and name in assets and name not in result, "unknown, duplicate or unsafe asset path")
        require(type(size) is int and 0 < size <= MAX_BYTES and hex_value(item["sha256"], 64), "invalid asset byte count or digest")
        total += size
        require(total <= MAX_BYTES, "dist exceeds 256 KiB including manifest")
        content = read_plain(directory / name, size)
        require(len(content) == size and sha(content) == item["sha256"], "asset differs from its manifest: " + name)
        result[name] = content
    require(set(result) == wanted and names(directory) == wanted, "dist file set changed")
    require(source["tree_sha256"] == tree_digest(result), "source tree SHA-256 differs from verified assets")
    return Bundle(result, value, expected_manifest_sha)


def lock_bytes(bundle):
    return (json.dumps({"schema_version": 1, "product": "nb1-web", "source_commit": bundle.manifest["source"]["commit"], "manifest_sha256": bundle.manifest_sha256}, sort_keys=True, indent=2) + "\n").encode("utf-8")


def snapshot_destination(base):
    plain_info(base.parent, directory=True)
    plain_info(base, directory=True)
    directory, lock = base / "dist", base / "manifest.lock.json"
    entries = names(directory)
    old_lock = read_plain(lock, 4096)
    if entries == {"PENDING"} and read_plain(directory / "PENDING", 128) == PENDING and old_lock.strip() == b"{}":
        return {"PENDING": PENDING}, old_lock
    value = strict_json(old_lock)
    object_keys(value, ("schema_version", "product", "source_commit", "manifest_sha256"), "lock")
    require(value["schema_version"] == 1 and type(value["schema_version"]) is int and value["product"] == "nb1-web", "invalid existing bundle lock")
    old = validate_directory(directory, value["source_commit"], value["manifest_sha256"])
    return old.files, old_lock


def write_new(path, data):
    with path.open("xb") as stream:
        stream.write(data)
        stream.flush()
        os.fsync(stream.fileno())


def remove_exact(directory, contents):
    require(names(directory) == set(contents), "cleanup refused changed directory: " + directory.name)
    for name, data in contents.items():
        require(read_plain(directory / name, MAX_BYTES) == data, "cleanup refused changed file: " + name)
    for name in contents:
        (directory / name).unlink()
    directory.rmdir()


def remove_known_subset(directory, contents):
    present = names(directory)
    require(present.issubset(contents), "cleanup refused unknown staged entries")
    remove_exact(directory, {name: contents[name] for name in present})


def install(bundle, base):
    """Replace the generated pair; normal exceptions restore the previous pair.

    Concurrent builds/imports and crash-atomic directory transactions are not
    promised. A lock excludes another importer; inconsistent bytes fail closed
    in Go, and interrupted backup directories are preserved for inspection.
    """
    base = Path(base)
    plain_info(base.parent, directory=True)
    plain_info(base, directory=True)
    marker = base / ".import.lock"
    descriptor = os.open(str(marker), os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
    marker_identity = identity(os.fstat(descriptor))
    stage = backup = None
    old_files = old_lock = None
    moved_dist = moved_lock = new_dist = new_lock = False
    new_lock_data = lock_bytes(bundle)
    failure = None
    rollback_failed = False
    warnings = []
    try:
        old_files, old_lock = snapshot_destination(base)
        stage = Path(tempfile.mkdtemp(prefix=".webui-stage-", dir=str(base)))
        backup = Path(tempfile.mkdtemp(prefix=".webui-backup-", dir=str(base)))
        (stage / "dist").mkdir()
        for name, data in bundle.files.items():
            write_new(stage / "dist" / name, data)
        write_new(stage / "manifest.lock.json", new_lock_data)
        validate_directory(stage / "dist", bundle.manifest["source"]["commit"], bundle.manifest_sha256)
        os.replace(str(base / "dist"), str(backup / "dist"))
        moved_dist = True
        os.replace(str(base / "manifest.lock.json"), str(backup / "manifest.lock.json"))
        moved_lock = True
        os.replace(str(stage / "dist"), str(base / "dist"))
        new_dist = True
        os.replace(str(stage / "manifest.lock.json"), str(base / "manifest.lock.json"))
        new_lock = True
        require(snapshot_destination(base) == (bundle.files, new_lock_data), "installed bundle differs from staged bytes")
    except BaseException as error:
        failure = error
        try:
            if new_dist:
                remove_exact(base / "dist", bundle.files)
            if new_lock:
                require(read_plain(base / "manifest.lock.json", 4096) == new_lock_data, "rollback refused changed lock")
                (base / "manifest.lock.json").unlink()
            if moved_dist:
                os.replace(str(backup / "dist"), str(base / "dist"))
            if moved_lock:
                os.replace(str(backup / "manifest.lock.json"), str(base / "manifest.lock.json"))
        except BaseException as rollback_error:
            rollback_failed = True
            failure = InvalidBundle("import and rollback failed; inspect retained backup %s: %s; %s" % (backup, error, rollback_error))
    finally:
        # Only delete known, unchanged staging/backup contents. Unknown remnants
        # remain visible instead of being recursively removed.
        try:
            if stage is not None:
                try:
                    if (stage / "dist").exists():
                        remove_known_subset(stage / "dist", bundle.files)
                    if (stage / "manifest.lock.json").exists():
                        require(read_plain(stage / "manifest.lock.json", 4096) == new_lock_data, "changed staged lock")
                        (stage / "manifest.lock.json").unlink()
                    stage.rmdir()
                except (OSError, InvalidBundle) as error:
                    warnings.append("retained stage %s: %s" % (stage, error))
            if backup is not None and not rollback_failed:
                try:
                    if (backup / "dist").exists():
                        remove_exact(backup / "dist", old_files)
                    if (backup / "manifest.lock.json").exists():
                        require(read_plain(backup / "manifest.lock.json", 4096) == old_lock, "changed backup lock")
                        (backup / "manifest.lock.json").unlink()
                    backup.rmdir()
                except (OSError, InvalidBundle) as error:
                    warnings.append("retained backup %s: %s" % (backup, error))
        finally:
            os.close(descriptor)
            try:
                require(identity(plain_info(marker)) == marker_identity, "import lock identity changed")
                marker.unlink()
            except (OSError, InvalidBundle) as error:
                warnings.append("retained import lock: " + str(error))
    if failure is not None:
        if warnings:
            raise InvalidBundle(str(failure) + "; " + "; ".join(warnings)) from failure
        raise failure
    return warnings


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("dist", type=Path)
    parser.add_argument("--source-commit", required=True)
    parser.add_argument("--manifest-sha256", required=True)
    mode = parser.add_mutually_exclusive_group()
    mode.add_argument("--check", action="store_true", help="validate the input only; write nothing")
    mode.add_argument("--dry-run", action="store_true", help="also validate the destination; write nothing")
    args = parser.parse_args()
    try:
        bundle = validate_directory(args.dist, args.source_commit, args.manifest_sha256)
        base = Path(__file__).resolve().parents[1] / "internal" / "webui"
        warnings = []
        if args.dry_run:
            snapshot_destination(base)
        elif not args.check:
            warnings = install(bundle, base)
        print(json.dumps({"status": "checked" if args.check else "dry_run" if args.dry_run else "imported", "source_commit": args.source_commit, "manifest_sha256": args.manifest_sha256, "files": sorted(bundle.files), "total_bytes": bundle.total_bytes, "cleanup_warnings": warnings}, sort_keys=True))
        if warnings:
            return 1
    except (OSError, InvalidBundle) as error:
        print("web dist import refused: " + str(error), file=sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    sys.exit(main())
