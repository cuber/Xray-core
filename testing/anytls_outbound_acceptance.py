#!/usr/bin/env python3
"""Run strict opt-in AnyTLS gates and retain machine-checkable evidence."""

import argparse
import hashlib
import json
import os
from pathlib import Path
import re
import signal
import subprocess
import sys
import tempfile
import time


ROOT = Path(__file__).resolve().parents[1]
GROUPS = {
    "sidecar": (".", (
        "TestAnyTLSOutboundEgressUDS", "TestAnyTLSOutboundRuntimeDisplay",
        "TestAnyTLSSidecarTrafficStatisticsSwitches",
        "TestAnyTLSSidecarTrafficSnapshotAndCoreRestart",
    )),
    "processes": ("./proxy/anytls", (
        "TestAnyTLSOutboundSeparateProcesses/direct",
        *(f"TestAnyTLSOutboundSeparateProcesses/{entrance}/{protocol}"
          for protocol in ("anytls", "socks", "vless", "hysteria")
          for entrance in ("migratedProxySettings", "dialerProxy")),
    )),
    "chains": ("./proxy/anytls", tuple(
        f"TestAnyTLSExternalServersChained/{entrance}/{protocol}/{kind}"
        for entrance in ("migratedProxySettings", "dialerProxy")
        for protocol in ("socks", "vless", "anytls", "hysteria")
        for kind in ("SINGBOX", "MIHOMO")
    )),
    "external": ("./proxy/anytls", (
        "TestExternalClients/SINGBOX", "TestExternalClients/MIHOMO",
        "TestAnyTLSExternalServers/SINGBOX", "TestAnyTLSExternalServers/MIHOMO",
        "TestAnyTLSExternalControlPairs/SINGBOX_to_MIHOMO",
        "TestAnyTLSExternalControlPairs/MIHOMO_to_SINGBOX",
        "TestAnyTLSExternalClientsThroughNativeOutbound/SINGBOX",
        "TestAnyTLSExternalClientsThroughNativeOutbound/MIHOMO",
    )),
    "address": ("./proxy/anytls", (
        "TestAnyTLSOutboundAddressIPv6", "TestAnyTLSOutboundAddressDNSLocality",
        "TestAnyTLSOutboundAddressStaticBinding",
    )),
    "idle": ("./proxy/anytls/internal/engine", ("TestClientDefaultIdleTimer",)),
    "soak": ("./proxy/anytls", ("TestAnyTLSOutboundSoak",)),
}


def validate_events(events, required, returncode):
    """A successful process alone is not evidence that required tests ran."""
    passed = set()
    errors = []
    package_pass = False
    for event in events:
        action, name = event.get("Action"), event.get("Test")
        if action in ("skip", "fail"):
            errors.append(f"{action}: {name or event.get('Package', '<package>')}")
        if action == "pass":
            if name:
                passed.add(name)
            else:
                package_pass = True
    errors.extend(f"required test not passed: {name}" for name in required if name not in passed)
    if not package_pass:
        errors.append("missing successful package result")
    if returncode != 0:
        errors.append(f"go test exit={returncode}")
    return errors


def cleanup_group(process, grace=1.0):
    """Reap the leader and kill its group, including TERM-resistant descendants."""
    def send(sig):
        try:
            os.killpg(process.pid, sig)
            return True
        except ProcessLookupError:
            return False

    remaining = send(signal.SIGTERM)
    if remaining:
        deadline = time.monotonic() + grace
        while time.monotonic() < deadline:
            process.poll()
            if not send(0):
                break
            time.sleep(0.02)
        # The leader may already have exited while a child still holds sockets.
        send(signal.SIGKILL)
    process.wait(timeout=grace + 1)
    return remaining


def run_owned(command, *, stdout, stderr, timeout, cwd=ROOT, env=None):
    """POSIX only; descendants must retain the inherited process group."""
    if os.name != "posix":
        raise RuntimeError("strict process-tree cleanup requires POSIX; unsupported platform")
    process = subprocess.Popen(command, cwd=cwd, env=env, stdout=stdout,
                               stderr=stderr, start_new_session=True)
    code = None
    remaining = False
    try:
        code = process.wait(timeout=timeout)
    finally:
        original_error = sys.exc_info()[1]
        # A second Ctrl-C must not interrupt cleanup halfway through.
        previous = signal.signal(signal.SIGINT, signal.SIG_IGN)
        previous_term = signal.signal(signal.SIGTERM, signal.SIG_IGN)
        try:
            try:
                remaining = cleanup_group(process)
            except Exception as cleanup_error:
                if original_error is None:
                    raise
                print(f"cleanup error (original failure retained): {cleanup_error}", file=sys.stderr)
        finally:
            signal.signal(signal.SIGTERM, previous_term)
            signal.signal(signal.SIGINT, previous)
    if remaining:
        raise RuntimeError(f"command exited {code} but owned process group {process.pid} remained; cleaned up")
    return code


def output(command, timeout=30, cwd=ROOT):
    with tempfile.TemporaryFile(mode="w+") as stream:
        code = run_owned(command, stdout=stream, stderr=stream, timeout=timeout, cwd=cwd)
        stream.seek(0)
        result = stream.read()
    if code:
        raise subprocess.CalledProcessError(code, command, output=result)
    return result.strip()


def binary_metadata(path, version_args):
    path = Path(path).expanduser().resolve(strict=True)
    digest = hashlib.sha256()
    with path.open("rb") as stream:
        for block in iter(lambda: stream.read(1024 * 1024), b""):
            digest.update(block)
    version = output([str(path), *version_args], timeout=10)
    return {"path": str(path), "sha256": digest.hexdigest(), "version": version.strip()}


def candidate_roots(core_root, sidecar_root=None):
    core_root = Path(core_root).expanduser().resolve(strict=True)
    if core_root != ROOT or not (core_root / "go.mod").is_file():
        raise ValueError("--core-root must be the checkout containing this runner")
    if sidecar_root is None:
        return core_root, None
    sidecar_root = Path(sidecar_root).expanduser().resolve(strict=True)
    if sidecar_root == core_root or not (sidecar_root / "go.mod").is_file():
        raise ValueError("--sidecar-root must be a distinct Go checkout")
    module = json.loads(output(["go", "list", "-m", "-json", "github.com/xtls/xray-core"],
                               cwd=sidecar_root))
    replacement = module.get("Replace", {}).get("Dir")
    if not replacement or Path(replacement).resolve() != core_root:
        raise ValueError("Sidecar must replace Core with the explicit candidate --core-root")
    return core_root, sidecar_root


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--group", action="append", choices=GROUPS, required=True)
    parser.add_argument("--core-root", type=Path, required=True,
                        help="explicit candidate Core checkout containing this runner")
    parser.add_argument("--sidecar-root", type=Path,
                        help="explicit candidate Sidecar; required for the sidecar group")
    parser.add_argument("--output", type=Path, required=True, help="new evidence directory outside the Core worktree")
    parser.add_argument("--wall-timeout", type=float, default=900,
                        help="wall-clock seconds per group, including build (default: 900)")
    args = parser.parse_args()
    if "sidecar" in args.group and args.sidecar_root is None:
        parser.error("--group sidecar requires --sidecar-root")
    if not 0 < args.wall_timeout < float("inf"):
        parser.error("--wall-timeout must be finite and positive")
    directory = args.output.expanduser().resolve()
    if directory == ROOT or ROOT in directory.parents:
        parser.error("evidence must be outside the Core worktree")
    if args.sidecar_root is not None:
        sidecar_path = args.sidecar_root.expanduser().resolve()
        if directory == sidecar_path or sidecar_path in directory.parents:
            parser.error("evidence must be outside the Sidecar worktree")
    directory.mkdir(parents=True, exist_ok=False)
    report = {"groups": {}, "binaries": {}, "status": "failed"}
    env = os.environ.copy()
    env["ANYTLS_STRICT"] = "1"
    env["ANYTLS_SOAK_STRICT"] = "1"
    env["ANYTLS_SOAK_DURATION"] = "10m"
    def interrupted(signum, frame):
        raise InterruptedError(f"runner received signal {signum}")

    old_term = signal.signal(signal.SIGTERM, interrupted)
    try:
        if os.name != "posix":
            raise RuntimeError("strict process-tree cleanup requires POSIX; unsupported platform")
        core_root, sidecar_root = candidate_roots(args.core_root, args.sidecar_root)
        report["core_root"] = str(core_root)
        report["sidecar_root"] = str(sidecar_root) if sidecar_root else None
        if sidecar_root:
            report["sidecar"] = {
                "revision": output(["git", "rev-parse", "HEAD"], cwd=sidecar_root),
                "worktree": output(["git", "status", "--porcelain"], cwd=sidecar_root),
            }
        report.update(revision=output(["git", "rev-parse", "HEAD"]),
                      worktree=output(["git", "status", "--porcelain"]),
                      go=output(["go", "version"]))
        if "external" in args.group or "chains" in args.group:
            for key, version_args in (("ANYTLS_SINGBOX", ["version"]), ("ANYTLS_MIHOMO", ["-v"])):
                if not env.get(key):
                    raise ValueError(f"required executable: {key}")
                report["binaries"][key] = binary_metadata(env[key], version_args)
                env[key] = report["binaries"][key]["path"]
        for group in dict.fromkeys(args.group):
            package, required = GROUPS[group]
            group_root = sidecar_root if group == "sidecar" else core_root
            roots = sorted({name.split("/")[0] for name in required})
            pattern = "^(" + "|".join(re.escape(name) for name in roots) + ")$"
            command = ["go", "test", "-json", "-race", package, "-run", pattern, "-count=1", "-timeout=12m"]
            print(f"Running {group}; evidence: {directory}", flush=True)
            events, parse_errors = [], []
            fixtures = str(directory / f"{group}-fixtures")
            group_env = dict(env, ANYTLS_EVIDENCE_DIR=fixtures)
            report["groups"][group] = {"command": command, "exit": None,
                                       "cwd": str(group_root),
                                       "fixtures_dir": fixtures,
                                       "errors": ["group did not complete"]}
            with (directory / f"{group}.jsonl").open("w") as log, (directory / f"{group}.stderr").open("w") as stderr:
                code = run_owned(command, stdout=log, stderr=stderr,
                                 cwd=group_root, env=group_env, timeout=args.wall_timeout)
            with (directory / f"{group}.jsonl").open() as log:
                for line in log:
                    try:
                        event = json.loads(line)
                        if not isinstance(event, dict):
                            raise ValueError("go test JSON event must be an object")
                        events.append(event)
                    except (ValueError, UnicodeError) as exc:
                        parse_errors.append(f"invalid go test JSON output: {exc}")
            errors = parse_errors + validate_events(events, required, code)
            report["groups"][group] = {"command": command, "exit": code,
                                       "cwd": str(group_root),
                                       "fixtures_dir": fixtures, "errors": errors}
            print(f"{group}: {'FAIL' if errors else 'PASS'}", flush=True)
        if not any(item["errors"] for item in report["groups"].values()):
            report["status"] = "passed"
    except (Exception, KeyboardInterrupt) as exc:
        report["error"] = f"{type(exc).__name__}: {exc}"
    finally:
        # Preserve the original failure through report persistence, even if a
        # second termination request arrives during shutdown.
        old_int = signal.signal(signal.SIGINT, signal.SIG_IGN)
        signal.signal(signal.SIGTERM, signal.SIG_IGN)
        try:
            (directory / "report.json").write_text(json.dumps(report, indent=2) + "\n")
        finally:
            signal.signal(signal.SIGINT, old_int)
            signal.signal(signal.SIGTERM, old_term)
    if report["status"] != "passed":
        print(json.dumps(report, indent=2), file=sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    sys.exit(main())
