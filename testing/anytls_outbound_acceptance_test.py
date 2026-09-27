import unittest
from unittest import mock
import json
import os
from pathlib import Path
import signal
import socket
import subprocess
import sys
import tempfile
import time

import anytls_outbound_acceptance as acceptance

from anytls_outbound_acceptance import GROUPS, validate_events


FAKE_TOOL = r'''
import json, os, pathlib, signal, socket, subprocess, sys, time
state = pathlib.Path(os.environ["OWNED_STATE"])
if pathlib.Path(sys.argv[0]).name == "git":
    print("fixture-revision")
    sys.exit(0)
if sys.argv[1:] == ["version"]:
    print("go fixture")
    sys.exit(0)
worker = sys.argv[1] == "--worker"
depth = int(sys.argv[2]) if worker else 0
mode = os.environ["OWNED_MODE"]
clean = mode in ("clean", "parse", "malformed", "unicode")
if worker:
    signal.signal(signal.SIGTERM, signal.SIG_IGN)
s = socket.socket()
s.bind(("127.0.0.1", 0))
s.listen()
(state / (str(depth) + ".json")).write_text(json.dumps({"pid": os.getpid(), "port": s.getsockname()[1]}))
child = None
if depth < 2:
    child = subprocess.Popen([sys.executable, __file__, "--worker", str(depth + 1)])
if worker:
    if clean:
        if child: child.wait(timeout=5)
        sys.exit(0)
    while True: time.sleep(0.1)
deadline = time.monotonic() + 5
while not (state / "2.json").exists():
    if time.monotonic() > deadline: sys.exit(2)
    time.sleep(0.01)
(state / "evidence-dir").write_text(os.environ["ANYTLS_EVIDENCE_DIR"])
(state / "ready").touch()
if clean: child.wait(timeout=5)
if mode in ("interrupt", "term", "timeout"):
    while True: time.sleep(0.1)
if mode == "parse":
    print("[]", flush=True)
elif mode == "malformed":
    print("not-json", flush=True)
elif mode == "unicode":
    sys.stdout.buffer.write(b"\xff\n"); sys.stdout.buffer.flush()
else:
    print(json.dumps({"Action": "pass", "Test": "TestClientDefaultIdleTimer"}), flush=True)
    print(json.dumps({"Action": "pass", "Package": "fixture"}), flush=True)
'''


@unittest.skipUnless(os.name == "posix", "process-group integration requires POSIX")
class OwnedProcessTest(unittest.TestCase):
    def run_fixture(self, mode):
        with tempfile.TemporaryDirectory(prefix="anytls-owned-") as root:
            root = Path(root)
            tools = root / "bin"
            tools.mkdir()
            state = root / "state"
            state.mkdir()
            for name in ("go", "git"):
                path = tools / name
                path.write_text("#!" + sys.executable + "\n" + FAKE_TOOL)
                path.chmod(0o700)
            env = dict(os.environ, PATH=str(tools) + os.pathsep + os.environ.get("PATH", ""),
                       OWNED_STATE=str(state), OWNED_MODE=mode)
            evidence = root / "evidence"
            command = [sys.executable, str(Path(acceptance.__file__).resolve()),
                       "--group", "idle", "--output", str(evidence),
                       "--wall-timeout", "1" if mode == "timeout" else "10"]
            with (root / "runner.log").open("w+") as log:
                runner = subprocess.Popen(command, env=env, stdout=log, stderr=log, start_new_session=True)
                started = time.monotonic()
                try:
                    deadline = time.monotonic() + 8
                    while not (state / "ready").exists():
                        if runner.poll() is not None or time.monotonic() > deadline:
                            log.seek(0)
                            self.fail("fixture failed to start: " + log.read())
                        time.sleep(0.02)
                    if mode in ("interrupt", "term"):
                        os.kill(runner.pid, signal.SIGINT if mode == "interrupt" else signal.SIGTERM)
                    code = runner.wait(timeout=12)
                    self.assertLess(time.monotonic() - started, 12)
                    log.seek(0)
                    diagnostic = log.read()
                    report = json.loads((evidence / "report.json").read_text())
                    fixture_dir = report["groups"]["idle"]["fixtures_dir"]
                    self.assertEqual(fixture_dir, str((evidence / "idle-fixtures").resolve()))
                    self.assertTrue(Path(fixture_dir).is_absolute())
                    self.assertEqual((state / "evidence-dir").read_text(), fixture_dir)
                    if mode == "clean":
                        self.assertEqual(code, 0, diagnostic)
                        self.assertEqual(report["status"], "passed")
                    else:
                        self.assertNotEqual(code, 0, diagnostic)
                        self.assertEqual(report["status"], "failed")
                        self.assertTrue(report.get("error") or report["groups"]["idle"]["errors"])
                    if mode == "normal":
                        events = [json.loads(line) for line in (evidence / "idle.jsonl").read_text().splitlines()]
                        self.assertEqual(validate_events(events, ["TestClientDefaultIdleTimer"], 0), [])
                        self.assertIn("command exited 0", report["error"])
                        self.assertIn("owned process group", report["error"])
                    if mode == "timeout":
                        self.assertIn("TimeoutExpired", report["error"])
                    if mode == "interrupt":
                        self.assertIn("KeyboardInterrupt", report["error"])
                    if mode == "term":
                        self.assertIn("InterruptedError", report["error"])
                    if mode in ("parse", "malformed"):
                        self.assertTrue(any("invalid go test JSON" in error for error in report["groups"]["idle"]["errors"]))
                    self.assertTrue((evidence / "idle.jsonl").exists())
                    for path in sorted(state.glob("*.json")):
                        owned = json.loads(path.read_text())
                        # POSIX init may briefly retain a killed orphan as a
                        # zombie; neither a running process nor a socket owner.
                        deadline = time.monotonic() + 2
                        while True:
                            status = subprocess.run(["ps", "-o", "stat=", "-p", str(owned["pid"])],
                                                    capture_output=True, text=True, timeout=1).stdout.strip()
                            if not status or status.startswith("Z"):
                                break
                            if time.monotonic() > deadline:
                                self.fail(f"owned process survived: {owned} status={status}")
                            time.sleep(0.02)
                        with socket.socket() as sock:
                            sock.settimeout(0.2)
                            self.assertNotEqual(sock.connect_ex(("127.0.0.1", owned["port"])), 0,
                                                f"listener survived: {owned}")
                finally:
                    if runner.poll() is None:
                        runner.kill()
                        runner.wait(timeout=3)
                    # Only the isolated fixture's recorded PIDs: never touch
                    # actual Go tests or a concurrently running soak runner.
                    for path in state.glob("*.json"):
                        try:
                            os.kill(json.loads(path.read_text())["pid"], signal.SIGKILL)
                        except ProcessLookupError:
                            pass

    def test_normal_exit_cleans_term_ignoring_descendants(self):
        self.run_fixture("normal")

    def test_clean_process_group_exit_is_success(self):
        self.run_fixture("clean")

    def test_timeout_cleans_descendants_and_writes_failure(self):
        self.run_fixture("timeout")

    def test_keyboard_interrupt_cleans_descendants_and_writes_failure(self):
        self.run_fixture("interrupt")

    def test_sigterm_cleans_descendants_and_writes_failure(self):
        self.run_fixture("term")

    def test_parse_failure_cleans_descendants(self):
        for mode in ("parse", "malformed", "unicode"):
            with self.subTest(mode=mode):
                self.run_fixture(mode)


class EvidenceTest(unittest.TestCase):
    def test_non_posix_explicitly_rejected_without_spawning(self):
        with mock.patch.object(acceptance.os, "name", "nt"), mock.patch.object(acceptance.subprocess, "Popen") as popen:
            with self.assertRaisesRegex(RuntimeError, "requires POSIX"):
                acceptance.run_owned(["unused"], stdout=None, stderr=None, timeout=1)
            popen.assert_not_called()

    def test_empty_selection_is_not_success(self):
        self.assertTrue(validate_events([{"Action": "pass", "Package": "fixture"}], ["TestRequired"], 0))

    def test_required_pass_and_package_exit(self):
        events = [{"Action": "pass", "Test": "TestRequired"}, {"Action": "pass", "Package": "fixture"}]
        self.assertEqual(validate_events(events, ["TestRequired"], 0), [])
        self.assertTrue(validate_events(events, ["TestRequired"], 1))
        self.assertTrue(validate_events(events[:1], ["TestRequired"], 0))

    def test_subtest_skip_or_failure_is_not_success(self):
        events = [{"Action": "pass", "Test": "TestRequired"}, {"Action": "pass", "Package": "fixture"}]
        for action in ("skip", "fail"):
            with self.subTest(action=action):
                self.assertTrue(validate_events(events + [{"Action": action, "Test": "TestRequired/client"}], ["TestRequired"], 0))

    def test_both_external_clients_required(self):
        events = [{"Action": "pass", "Test": "TestExternal/SINGBOX"}, {"Action": "pass", "Package": "fixture"}]
        self.assertTrue(validate_events(events, ["TestExternal/SINGBOX", "TestExternal/MIHOMO"], 0))

    def test_every_external_chain_required(self):
        _, required = GROUPS["chains"]
        self.assertEqual(len(set(required)), 16)
        events = [{"Action": "pass", "Test": name} for name in required]
        events.append({"Action": "pass", "Package": "fixture"})
        self.assertEqual(validate_events(events, required, 0), [])
        for index in range(len(required)):
            with self.subTest(missing=required[index]):
                self.assertTrue(validate_events(events[:index] + events[index + 1:], required, 0))

    def test_every_native_process_chain_required(self):
        _, required = GROUPS["processes"]
        self.assertEqual(len(set(required)), 9)
        self.assertIn("TestAnyTLSOutboundSeparateProcesses/direct", required)
        for protocol in ("anytls", "socks", "vless", "hysteria"):
            for entrance in ("proxySettings", "dialerProxy"):
                self.assertIn(f"TestAnyTLSOutboundSeparateProcesses/{entrance}/{protocol}", required)
        events = [{"Action": "pass", "Test": name} for name in required]
        events.append({"Action": "pass", "Package": "fixture"})
        self.assertEqual(validate_events(events, required, 0), [])
        for index in range(len(required)):
            with self.subTest(missing=required[index]):
                self.assertTrue(validate_events(events[:index] + events[index + 1:], required, 0))

    def test_native_outbound_reverse_clients_required(self):
        _, required = GROUPS["external"]
        for kind in ("SINGBOX", "MIHOMO"):
            name = f"TestAnyTLSExternalClientsThroughNativeOutbound/{kind}"
            self.assertIn(name, required)
            events = [{"Action": "pass", "Test": item} for item in required if item != name]
            events.append({"Action": "pass", "Package": "fixture"})
            self.assertTrue(validate_events(events, required, 0))


if __name__ == "__main__":
    unittest.main()
