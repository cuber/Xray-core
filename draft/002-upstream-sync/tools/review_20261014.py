#!/usr/bin/env python3
"""Generate redacted, fixed-ref audit evidence; never change refs or worktrees."""

import argparse
import collections
import json
import pathlib
import subprocess

import inventory


BASE = "b4f08981becb71eaa995fa98ed2098ade92566bb"
FORK = "baf3672e0c45bdcb290169ea510c1c24a5625961"
UPSTREAM = "0888beb38a9ba45440557175e71ce21cf8ba3b13"


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--repo", type=pathlib.Path, required=True)
    parser.add_argument("--config", type=pathlib.Path, required=True)
    parser.add_argument("--output", type=pathlib.Path, required=True)
    parser.add_argument("--check", action="store_true")
    args = parser.parse_args()

    def git(*items):
        return inventory.git(args.repo, *items).decode().strip()

    assert git("merge-base", FORK, UPSTREAM) == BASE
    fork = inventory.file_stats(args.repo, BASE, FORK)
    upstream = inventory.file_stats(args.repo, BASE, UPSTREAM)
    endpoint = inventory.file_stats(args.repo, FORK, UPSTREAM)
    merge = subprocess.run(
        ["git", "-C", str(args.repo), "merge-tree", "--write-tree", "--name-only", FORK, UPSTREAM],
        capture_output=True, text=True, check=False,
    )
    if merge.returncode not in (0, 1):
        raise RuntimeError(merge.stderr)
    # The first section is tree ID followed by conflicted paths, not diagnostics.
    conflicts = merge.stdout.split("\n\n", 1)[0].splitlines()[1:]
    commits = []
    for line in git("log", "--reverse", "--format=%H%x09%cI%x09%s", f"{BASE}..{UPSTREAM}").splitlines():
        sha, date, subject = line.split("\t", 2)
        commits.append({"sha": sha, "date": date, "subject": subject})
    usage = inventory.config_inventory(args.config)
    extra = []
    empty_ips = 0
    for path in sorted((args.config / "xray/nodes").glob("*/config-reality-raw.json")):
        config = json.loads(path.read_text())
        for index, outbound in enumerate(config.get("outbounds", [])):
            if "proxySettings" in outbound:
                extra.append({"node": path.parent.name, "outbound_index": index})
            if outbound.get("settings", {}).get("ipsBlocked") == []:
                empty_ips += 1
    report = {
        "date": "2026-10-14", "base": BASE, "fork": FORK, "upstream": UPSTREAM,
        "scope": "Static inventory and merge-tree preview, NOT candidate runtime acceptance",
        "fork_commits": int(git("rev-list", "--count", f"{BASE}..{FORK}")),
        "upstream_commits": commits,
        "files": {"fork": fork, "upstream": upstream, "endpoint": endpoint},
        "path_kinds": {name: dict(collections.Counter(row["kind"] for row in rows))
                       for name, rows in (("fork", fork), ("upstream", upstream))},
        "overlap": sorted({row["path"] for row in fork} & {row["path"] for row in upstream}),
        "merge_conflicts": conflicts,
        "config_head": inventory.git(args.config, "rev-parse", "HEAD").decode().strip(),
        "config_dirty": bool(inventory.git(args.config, "status", "--porcelain")),
        "config_usage": usage, "proxy_settings": extra, "empty_ips_blocked": empty_ips,
    }
    data = json.dumps(report, indent=2, ensure_ascii=True) + "\n"
    if args.check:
        if args.output.read_text() != data:
            raise SystemExit("audit evidence differs; inspect refs/config before regenerating")
    else:
        args.output.parent.mkdir(parents=True, exist_ok=True)
        args.output.write_text(data)
    print(json.dumps({"checked": args.check, "upstream_commits": len(commits),
                      "overlap": len(report["overlap"]), "conflicts": len(conflicts),
                      "config_files": usage["files"]}))


if __name__ == "__main__":
    main()
