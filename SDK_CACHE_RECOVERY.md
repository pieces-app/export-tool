# Optional recovery from SDK caches

The current OS can return summary identities, dates, and hierarchy enums while omitting body and graph relationships. Older SDK caches on this machine retain some of those links. `--sdk-cache` uses that historical evidence to attach current OS records. It is optional and never makes an incomplete OS projection complete.

## Run it

```sh
./pieces-export export --output ./recovered-export \
  --sdk-cache /path/to/pieces_client_sqlite.db

# Multiple explicitly selected caches; the latest valid field version wins.
./pieces-export export --output ./recovered-export \
  --sdk-cache /path/to/older/pieces_client_sqlite.db \
  --sdk-cache /path/to/another/pieces_client_sqlite.db
```

The flag is repeatable, including through the Bash/PowerShell installer's normal export-argument forwarding. Paths are validated before OS activation or Desktop closure. The preflight screen states how many caches were selected and that their links may be stale. Cache contents are not applied by `scan` or `export --dry-run`. `WORKSTREAM_SUMMARIES` must be selected for recovery; include the referenced material types to make their targets available. Ordinary export approval, privacy settings, native metadata, and output-directory rules still apply.

Cache paths vary by client and installation. There is no automatic scan of the user's home directory during export, and no mandatory cache dependency for ordinary OS export. A missing, unreadable, corrupt, or unsupported selected database stops the run with an actionable error. Cache paths and contents are not printed in errors or included in the manifest.

## Read and reconcile

1. Open an existing regular SQLite file with `mode=ro` and `query_only`. Require an actual `workstream_summaries` table with `json` and `expireAt` columns. Read a transactionally consistent view, including its WAL. Do not call SDK initialization, schema migration, cache cleanup, or deletion. Do not fall back to writable mode or `immutable=1`. SQLite's immutable option assumes a file cannot change, while a running client can write its WAL. See [SQLite URI modes](https://www.sqlite.org/uri.html) and [read-only WAL behavior](https://www.sqlite.org/wal.html#read_only_databases).
2. Skip expired, malformed, and oversized cached rows, recording aggregate counts. Match an exact summary ID already returned by the current OS. Require equal parsed creation times and valid update times; ignore a cache version newer than the captured OS record. Similar names, prose, timestamps, or embedding scores cannot create an identity match.
3. Consider only annotations, persons, pipelines, tags, events, sources, websites, ranges, hints, and summary references. Only an absent/null current OS field is eligible. Any present current field takes precedence, including an explicit empty collection or a malformed field that needs separate investigation.
4. Select the newest valid cached version **per field**. A newer explicit empty collection or tombstone suppresses older references. A missing field in a newer cache does not prove deletion. Equal-time versions with different active reference sets conflict; skip that field instead of merging them. Keep the chosen cache ordinal and both summary update times.
5. Resolve targets only against records already fetched during this export. Missing/out-of-selection targets receive no links and contribute to the coverage count. Do not fetch arbitrary cached content, create source records, or infer missing targets. Keep excluded/withheld targets in the private graph long enough for privacy propagation to suppress dependent content.
6. Apply the ordinary privacy, people-selection, canonical-path, Markdown/PDF, metadata, and final-audit passes. The canonical record JSON remains the current OS representation with the normal privacy transformations. Cached prose, embedded record copies, and credentials are never imported.

Limits: at most eight selected files, 200,000 summary rows per file, 8 MiB of JSON per row, two million inspected active references and 256 MiB of referenced-ID text across the selected files, and two minutes per cache transaction. Count/byte/deadline exhaustion stops the export before finalization. These are safety bounds, not proof that all clients retain a complete history.

## What the archive says

- `coverage.md` and `manifest.json` record rows, expiry/encoding failures, identity/update mismatches, conflicts, unavailable targets, added edges before privacy, and retained historical edges afterward. Edge totals include derived annotation inverses.
- `relationships.jsonl` marks recovered links as `historical_client_cache`, or `historical_client_cache_derived_inverse`, with `cache_evidence` containing the argument-order cache number and source/current summary update timestamps.
- Summary body attachments and related-record lists label historical evidence. The body itself comes from the current fetched annotation. Relationship siblings disclose that suggestions can use cached links. Relative local links still target included canonical files and undergo validation.
- These exports remain **partial, exit 2**. Cache recovery does not resolve missing current projections, unseen history, stale associations, or unavailable record types. Users without matching caches will have different recovery coverage.

The CLI embeds the CGO-free [modernc SQLite driver](https://pkg.go.dev/modernc.org/sqlite), pinned in `go.mod`. Users need no SQLite CLI, Python, SDK, or Go installation. Release packages include its license and required third-party notices. Cross-compilation remains distinct from native runtime acceptance.

## Evidence and remaining work

The Go reader was tested against four real caches and 11,732 retained summary JSON files from the running export, without extra HTTP requests or archive writes. It read 31,949 cache rows, matched 31,917 rows across the four caches, and found no invalid/expired rows, creation-time mismatches, future updates, or equal-time conflicts in that sample. It found candidate annotation links for 10,316 summaries (87.9%), referring to 21,277 distinct annotation IDs. There were no inline annotation bodies. Candidate person links covered 9,932 summaries but referenced only two persons. No pipeline links were found.

These are candidate counts against the staged summaries, not a reconciled live recovered archive. The full run must finish so referenced annotations/events/tags and privacy decisions can be checked. Recovery of 1,416 summaries without cached annotation links remains unresolved. The running `0.4.1-dev` process predates this option. Do not restart it just to change rendering; the implemented offline `rebuild` command can apply recovery after that archive is finalized, preserving exclusion decisions and source provenance. See [OFFLINE_REBUILD.md](OFFLINE_REBUILD.md). It has synthetic coverage; the current live archive is still pending.

Synthetic tests cover read-only WAL access with unchanged database/WAL bytes, blocked writes, wrong schemas/views, duplicate and missing cache paths, cancellation, reference budgets, current-field precedence, newer tombstones, conflicting equal-time versions, expired/invalid rows, source-time mismatches, missing targets, privacy propagation, canonical current annotation text, provenance, and moved Markdown/PDF links. Packaged-executable acceptance exercises the flag and its partial exit code. Record platform-specific execution evidence in [TODO.md](TODO.md).
