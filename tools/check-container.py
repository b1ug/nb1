#!/usr/bin/env python3
"""Validate the USB-free Linux image contract; never publish an image."""
import argparse
import datetime
import json
import pathlib
import subprocess


def run(args, timeout=60):
    result = subprocess.run(args, text=True, capture_output=True, timeout=timeout)
    if result.returncode:
        raise RuntimeError(f"command failed ({result.returncode}): {args[0:3]}\n{result.stderr[-4000:]}")
    return result.stdout


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--image", required=True)
    parser.add_argument("--output", type=pathlib.Path, required=True)
    args = parser.parse_args()
    receipt = {"schema_version": 1, "status": "FAIL", "checked_at": datetime.datetime.now(datetime.timezone.utc).isoformat(), "source": run(["git", "rev-parse", "HEAD"]).strip(), "scope": "Linux image build and USB-free runtime only", "usb_tested": False, "daemon_lifecycle_tested": False, "checks": {}}
    checks = receipt["checks"]
    try:
        info = json.loads(run(["docker", "image", "inspect", args.image]))[0]
        config = info["Config"]
        receipt.update(image_id=info["Id"], os=info["Os"], architecture=info["Architecture"])
        checks["linux"] = info["Os"] == "linux"
        checks["nonroot_user"] = config["User"] == "65532:65532"
        checks["direct_entrypoint"] = config["Entrypoint"] == ["/usr/local/bin/nb1"]
        checks["default_help"] = config["Cmd"] == ["--help"]
        checks["no_exposed_ports"] = not config.get("ExposedPorts")
        options = ["docker", "run", "--rm", "--network=none", "--read-only", "--cap-drop=ALL", "--security-opt=no-new-privileges", "--tmpfs", "/tmp:rw,nosuid,nodev,size=16m", "--tmpfs", "/var/lib/nb1:rw,nosuid,nodev,uid=65532,gid=65532,mode=0700,size=16m"]
        help_text = run(options + [args.image])
        checks["offline_default_help_runs"] = "Usage:" in help_text and "Available Commands:" in help_text
        probe = r'''set -eu
printf 'uid=%s\n' "$(id -u)"
printf 'gid=%s\n' "$(id -g)"
printf 'home=%s\n' "$HOME"
awk '/^CapEff:/ {print "cap_eff=" $2} /^NoNewPrivs:/ {print "no_new_privs=" $2}' /proc/self/status
awk '$2 == "/" {n=split($4, opts, ","); for (i=1; i<=n; i++) if (opts[i]=="ro") print "root_mount_readonly=yes"}' /proc/mounts
printf 'home_mode=%s\n' "$(stat -c %a "$HOME")"
printf 'ok' > "$HOME/probe"
printf 'ok' > /tmp/nb1-probe
rm "$HOME/probe" /tmp/nb1-probe
printf 'private_writable=yes\n'
if (printf 'no' > /etc/nb1-probe) 2>/dev/null; then exit 71; fi
printf 'root_write_rejected=yes\n'
for tool in go gcc g++; do if command -v "$tool" >/dev/null 2>&1; then exit 72; fi; done
printf 'build_tools_absent=yes\n'
ldd /usr/local/bin/nb1
'''
        output = run(options + ["--entrypoint", "/bin/sh", args.image, "-c", probe])
        fields = dict(line.split("=", 1) for line in output.splitlines() if "=" in line and "=>" not in line)
        checks.update(runtime_uid=fields.get("uid") == "65532", runtime_gid=fields.get("gid") == "65532", private_home=fields.get("home") == "/var/lib/nb1", private_home_mode=fields.get("home_mode") == "700", no_effective_capabilities=fields.get("cap_eff") == "0000000000000000", no_new_privileges=fields.get("no_new_privs") == "1", private_writable=fields.get("private_writable") == "yes", root_mount_readonly=fields.get("root_mount_readonly") == "yes", root_write_rejected=fields.get("root_write_rejected") == "yes", build_tools_absent=fields.get("build_tools_absent") == "yes", dynamic_libraries_resolved="libc.so" in output and "not found" not in output)
        receipt["probe"] = output
        if not all(checks.values()):
            raise RuntimeError("failed checks: " + ", ".join(k for k, v in checks.items() if not v))
        receipt["status"] = "PASS"
    except Exception as exc:
        receipt["failure"] = str(exc)
        raise
    finally:
        args.output.parent.mkdir(parents=True, exist_ok=True)
        args.output.write_text(json.dumps(receipt, indent=2) + "\n")
        print(json.dumps({"status": receipt["status"], "checks": checks, "output": str(args.output)}))


if __name__ == "__main__":
    main()
