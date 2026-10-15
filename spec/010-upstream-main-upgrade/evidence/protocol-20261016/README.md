# Protocol Verification Events

Nine small gzip JSONL files retain the final Darwin matrix, whole defaults and
independent domain test events for ordinary, race and checkptr2. The authoritative
execution/source mapping and exact commands are in [manifest.json](manifest.json);
the [ledger](../../protocol-chain-darwin-20261016.md) explains results and limits.

These files are **filtered structured events, not complete original logs or
stdout**. The field allowlist is `Time`, `Action`, `Package`, `Test`, `Elapsed`;
the action allowlist is `start`, `run`, `pass`, `skip`, `fail`. Package start is
Go's actual `start` action. Missing optional fields remain absent. Every Output,
OutputType and output action is omitted, including skip reasons and stacks.
No certificate, configuration body, production credential or raw stdout is copied.

The manifest records each original full JSONL/stderr path and SHA256, filtered
gzip SHA256/counts, actual command argv/exit/timestamps, source commit/tree/file
hashes, compilation artifacts and scenario-child instrumentation evidence.
Original raw files remain temporary: their hashes identify exact bytes but do
not preserve those bytes or permit reconstruction from this archive.

The original 292 matrix and defaults ran on clean Core76. Only the separate
domain runs include the additive domain test file. All three matrix PASS sets
match, as do all three domain PASS sets; defaults differ only by the declared
race-only child negative control. Default manifest package detail is limited to
slow packages; all structured Test/Package results remain in the gzip files.

Equivalent event projection (gzip header/compressor differences can change the
compressed SHA256):

```sh
jq -c 'select(.Action as $a | ["start","run","pass","skip","fail"] | index($a)) | {Time,Action,Package,Test,Elapsed} | with_entries(select(.value != null))' raw.jsonl | gzip -n -9 > filtered.events.jsonl.gz
```

The retained historical-red entries anchor the original Core44 failure and
bounded debugger/lsof evidence without importing their complete output.
52242 remains owner-unknown; 55590 is coexistence corroboration only. No old
failed or interrupted matrix is represented as completed PASS.

`SHA256SUMS` freezes the scoped source, reports and evidence files, with paths
relative to the Core root. Verify the checksum, gzip integrity and every event
field/action/count there:

```sh
shasum -a 256 -c spec/010-upstream-main-upgrade/evidence/protocol-20261016/SHA256SUMS
gzip -t spec/010-upstream-main-upgrade/evidence/protocol-20261016/*.events.jsonl.gz
python3 - <<'PY'
import collections, gzip, hashlib, json
from pathlib import Path
root = Path('spec/010-upstream-main-upgrade/evidence/protocol-20261016')
manifest = json.loads((root / 'manifest.json').read_text())
total = 0
for run in manifest['runs']:
    path = root / run['filtered_events']['file']
    assert hashlib.sha256(path.read_bytes()).hexdigest() == run['filtered_events']['sha256']
    counts = collections.Counter()
    with gzip.open(path, 'rt') as events:
        for line in events:
            event = json.loads(line)
            assert set(event) <= set(manifest['field_allowlist'])
            assert event['Action'] in manifest['action_allowlist']
            scope = 'test' if event.get('Test') is not None else 'package'
            counts[scope + '_' + event['Action']] += 1
            total += 1
    assert dict(counts) == run['event_counts']
print(json.dumps({'archives': len(manifest['runs']), 'events': total, 'mismatches': 0}))
PY
```

The repository ignores `*.gz`; the coordinator's later checkpoint must explicitly
force-add only these nine reviewed `*.events.jsonl.gz` files. No raw log should
be force-added. This agent did not stage or commit the archive.
