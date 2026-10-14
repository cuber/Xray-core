#!/usr/bin/env python3
"""Reproduce the fixed-ref review inventory without fetching or changing refs."""

import argparse
import collections
import hashlib
import json
import pathlib
import subprocess


BASE = "b4f08981becb71eaa995fa98ed2098ade92566bb"
FORK = "2074c22344fdd6f020fd0111d9c35b1f60c839a7"
UPSTREAM = "3519dfecbd65022ba71d9bc73e94063d0cbc8636"
REVIEWED = {
    "8f15190c": ("P0", "missing-security", "Malformed QUIC sniffer panic; narrow backport"),
    "a2cec2e5": ("P1", "missing-correctness", "DNS oversized-name panic and error propagation"),
    "0bafca94": ("P1", "missing-concurrency", "Atomic stats registration; preserve DomainTraffic"),
    "6ce8dc53": ("P1", "missing-correctness", "Buffered writer partial-capacity handling"),
    "598bde74": ("P1", "missing-concurrency", "Route snapshot publication; adapt fork ListRule/weights"),
    "d878fc83": ("P1", "partial-overlap", "Post-auth SS bridge lifecycle; not a handshake fix"),
    "56bb6366": ("P1", "missing-memory", "Weak shared matcher cache; with registry cleanup"),
    "be8009c6": ("P1", "missing-memory", "Weak matcher registry; also changes domain case handling"),
    "2776ea6d": ("KEEP", "equivalent-in-fork", "SS outbound defer Close already present and regression-covered"),
    "eef6e63b": ("KEEP", "equivalent-in-fork", "WaitReadCloser protected by mutex/Once in fork"),
    "ee2b2c5a": ("KEEP", "equivalent-in-fork", "leastLoad tolerance already implemented in fork"),
    "d43a808e": ("KEEP", "equivalent-in-fork", "Fork has its own formatting gate"),
    "9d9eaf39": ("P2", "missing-conditional", "XHTTP request body closure still missing; production usage gate"),
    "77f98eba": ("P2", "partial-overlap", "WaitReadCloser covered; client.closed race still needs audit"),
    "1e036ce1": ("P2", "missing-conditional", "XHTTP/3 underlying QUIC/UDP closure"),
    "f124daf5": ("P2", "missing-conditional", "Normal Observatory empty-selector CPU loop; not burst"),
    "ca4b156b": ("P2", "partial-overlap", "Fork closes probes already; DO NOT import 10-second minimum"),
    "c4dfcd4c": ("P2", "partial-overlap", "Retain fork initial-check and recovery semantics"),
    "756a2d13": ("P2", "dependency-review", "Hy2 sendThrough crosses dialer/proto/platform files"),
    "3dc8bf3d": ("P2", "dependency-review", "Dynamic Hy2 UUID behavior; authentication mapping"),
    "d562d894": ("P2", "dependency-review", "ChromeParrot UDP truncation depends on Hy2 upgrade"),
    "de2caf3c": ("P2", "missing-conditional", "Hy2 masquerade UDS, NOT Freedom sidecar UDS redirect"),
    "4e87f596": ("BLOCK", "breaking-config", "ipsBlocked ceases enforcing old policy; finalRules migration required"),
    "958eb9ea": ("BLOCK", "breaking-config", "finalRules adds delayed blocking defaults"),
    "906d49a2": ("BLOCK", "breaking-config", "finalRules IPv4 default behavior"),
    "3e2f040c": ("BLOCK", "breaking-config", "Freedom compatibility after finalRules introduction"),
    "c26d2eda": ("BLOCK", "breaking-config", "dialerProxy skips finalRules/resolution; validate intended policy"),
    "95e98162": ("BLOCK", "breaking-contract", "SS UoT removal/dependency changes; AnyTLS dependency must stay"),
    "d7fa2076": ("BLOCK", "breaking-config", "Plaintext public VLESS/Trojan ban; distinguish native encryption"),
    "55956f8d": ("BLOCK", "breaking-config", "TLS deprecated fields removed; scan actual consumers"),
    "1fc6850d": ("BLOCK", "breaking-config", "ECH query behavior/field removal"),
    "fd2ca748": ("BLOCK", "toolchain", "Go 1.27 requirement; no automatic dependency bump"),
    "8267cf95": ("BLOCK", "breaking-architecture", "Finalmask dialer/listener rework collides with fork UDS/listeners"),
    "ada99a4e": ("BLOCK", "breaking-architecture", "Hy2 v2.12.2 transport/auth lifecycle migration"),
    "1d62941b": ("BLOCK", "dependency-review", "Hy2 v2.8.2 predecessor, not an isolated memory fix"),
    "c42deab5": ("P2", "alias-retained", "clients remains accepted; no mandatory users rename"),
    "fb548f54": ("P2", "alias-retained", "network remains accepted beside method"),
    "1dbafe62": ("P2", "alias-review", "Tunnel/DNS aliases must be checked before full upgrade"),
    "af7eb680": ("P2", "compatibility-review", "REALITY minClientVer default; interoperability gate"),
}


def git(repo, *args):
    return subprocess.check_output(["git", "-C", str(repo), *args])


def classify_path(name):
    if name.startswith(("spec/", "draft/")) or name.endswith(".md"):
        return "documentation"
    if name.endswith((".pb.go", ".pb.go.gz")):
        return "generated-protobuf"
    if name.endswith("_test.go") or name.startswith("testing/"):
        return "tests"
    if name.startswith(".github/") or name in {"go.mod", "go.sum", "build.sh", ".gitignore"}:
        return "build-dependencies-ci"
    return "runtime-or-schema"


def file_stats(repo, left, right):
    rows = []
    # Rename detection is disabled so every path has one deterministic row.
    for line in git(repo, "diff", "--no-renames", "--numstat", left, right).decode().splitlines():
        added, removed, name = line.split("\t", 2)
        rows.append({"path": name, "added": added, "removed": removed, "kind": classify_path(name)})
    return rows


def commit_inventory(repo):
    result = []
    for line in git(repo, "log", "--reverse", "--format=%H%x09%cI%x09%s", f"{BASE}..{UPSTREAM}").decode().splitlines():
        sha, date, title = line.split("\t", 2)
        priority, disposition, reason = REVIEWED.get(sha[:8], ("REVIEW", "not-individually-audited", "Inventory only; title is not acceptance evidence"))
        paths = git(repo, "diff-tree", "--no-commit-id", "--name-only", "-r", sha).decode().splitlines()
        result.append({"sha": sha, "date": date, "title": title, "priority": priority,
                       "disposition": disposition, "reason": reason, "paths": paths})
    return result


def config_inventory(root):
    protocol_counts = collections.Counter()
    objects = []
    files = sorted((root / "xray/nodes").glob("*/config-reality-raw.json"))
    for path in files:
        config = json.loads(path.read_text())
        node = path.parent.name
        for side in ("inbounds", "outbounds"):
            for item in config.get(side, []):
                protocol_counts[f"{side}:{item.get('protocol', 'unknown')}"] += 1
        def visit(value, trail):
            if isinstance(value, dict):
                for key, child in value.items():
                    if key in {"ipsBlocked", "finalRules", "uot", "UoT", "udpOverTcp", "echForceQuery",
                               "allowInsecure", "disableSystemRoot", "enableSessionResumption", "rejectUnknownSni",
                               "pinnedPeerCertificateChainSha256", "verifyPeerCertInNames",
                               "sendThrough", "bindToDevice", "interface"}:
                        objects.append({"node": node, "path": trail + "." + key, "field": key,
                                        "type": type(child).__name__, "items": len(child) if isinstance(child, (list, dict)) else None})
                    visit(child, trail + "." + key)
            elif isinstance(value, list):
                for index, child in enumerate(value):
                    visit(child, f"{trail}[{index}]")
        visit(config, "$config")
    return {"scope": "current local worktree, including local generated config; NOT deployed-state proof",
            "files": len(files), "protocol_objects": dict(sorted(protocol_counts.items())),
            "field_counts": dict(sorted(collections.Counter(x["field"] for x in objects).items())),
            "field_nodes": {field: sorted({x["node"] for x in objects if x["field"] == field})
                            for field in sorted({x["field"] for x in objects})}, "objects": objects}


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--repo", type=pathlib.Path, required=True)
    parser.add_argument("--config", type=pathlib.Path, required=True)
    parser.add_argument("--evidence", type=pathlib.Path, required=True)
    parser.add_argument("--diffs", type=pathlib.Path, required=True)
    parser.add_argument("--check", action="store_true")
    args = parser.parse_args()
    for ref in (BASE, FORK, UPSTREAM):
        git(args.repo, "cat-file", "-e", f"{ref}^{{commit}}")
    assert git(args.repo, "merge-base", FORK, UPSTREAM).decode().strip() == BASE
    args.evidence.mkdir(parents=True, exist_ok=True)
    args.diffs.mkdir(parents=True, exist_ok=True)
    fork = file_stats(args.repo, BASE, FORK)
    upstream = file_stats(args.repo, BASE, UPSTREAM)
    endpoint = file_stats(args.repo, FORK, UPSTREAM)
    overlap = sorted({x["path"] for x in fork} & {x["path"] for x in upstream})
    commits = commit_inventory(args.repo)
    assert len(commits) == 203
    assert len(set(x["sha"] for x in commits)) == len(commits)
    products = {
        "upstream-commits.json": json.dumps(commits, ensure_ascii=True, indent=2) + "\n",
        "config-usage.json": json.dumps(config_inventory(args.config), ensure_ascii=True, indent=2) + "\n",
        "overlap-files.txt": "\n".join(overlap) + "\n",
    }
    for name, rows in (("fork-files.tsv", fork), ("upstream-files.tsv", upstream), ("endpoint-files.tsv", endpoint)):
        products[name] = "path\tadded\tremoved\tkind\n" + "".join(f"{x['path']}\t{x['added']}\t{x['removed']}\t{x['kind']}\n" for x in rows)
    products["upstream-commits.md"] = "# Cached upstream commits\n\n203 commits through `3519dfec`; not the September 30 web snapshot.\n\n" + \
        "| SHA | Date | Priority | Disposition | Subject |\n|---|---|---|---|---|\n" + \
        "".join(f"| [{x['sha'][:8]}](https://github.com/XTLS/Xray-core/commit/{x['sha']}) | {x['date'][:10]} | {x['priority']} | {x['disposition']} | {x['title'].replace('|', '/')} |\n" for x in commits)
    hashes = {}
    for name, left, right in (("base-to-fork.diff", BASE, FORK), ("base-to-upstream-cache.diff", BASE, UPSTREAM), ("fork-to-upstream-cache.diff", FORK, UPSTREAM)):
        data = git(args.repo, "diff", "--no-ext-diff", "--no-renames", "--binary", left, right)
        hashes[name] = {"sha256": hashlib.sha256(data).hexdigest(), "bytes": len(data), "from": left, "to": right}
        if args.check:
            assert (args.diffs / name).read_bytes() == data, name
        else:
            (args.diffs / name).write_bytes(data)
    baseline = {"review_date": "2026-10-04", "base": BASE, "fork": FORK, "upstream_cached": UPSTREAM,
                "upstream_web_snapshot": "b26a91d (v26.9.30; short SHA, not fetched)",
                "freshness": "Git network refresh blocked; web evidence newer than local cached ref",
                "fork_commits": int(git(args.repo, "rev-list", "--count", f"{BASE}..{FORK}")),
                "cached_upstream_commits": len(commits), "fork_paths": len(fork), "cached_upstream_paths": len(upstream),
                "endpoint_paths": len(endpoint), "overlap_paths": len(overlap),
                "path_kinds": {name: dict(collections.Counter(x["kind"] for x in rows))
                               for name, rows in (("fork", fork), ("cached_upstream", upstream), ("endpoint", endpoint))},
                "diffs": hashes,
                "config_head": git(args.config, "rev-parse", "HEAD").decode().strip(),
                "config_status_sha256": hashlib.sha256(git(args.config, "status", "--porcelain")).hexdigest()}
    products["baselines.json"] = json.dumps(baseline, ensure_ascii=True, indent=2) + "\n"
    for name, data in products.items():
        if args.check:
            assert (args.evidence / name).read_text() == data, name
        else:
            (args.evidence / name).write_text(data)
    print(json.dumps({"checked": args.check, "commits": len(commits), "fork_paths": len(fork),
                      "upstream_paths": len(upstream), "endpoint_paths": len(endpoint),
                      "overlap_paths": len(overlap), "diff_bytes": sum(x["bytes"] for x in hashes.values())}))


if __name__ == "__main__":
    main()
