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

The flag is repeatable, including through the Bash/PowerShell installer's normal export-argument forwarding. Paths are validated before OS activation or Desktop closure. The preflight screen states how many caches were selected and that their links may be stale. Cache contents are not applied by `scan` or `export --dry-run`. At least one of `WORKSTREAM_SUMMARIES`, `ANNOTATIONS`, `PERSONS`, or `SIGNALS` must be selected for recovery; include the referenced material types to make their targets available. Ordinary export approval, privacy settings, native metadata, and output-directory rules still apply.

Cache paths vary by client and installation. There is no automatic scan of the user's home directory during export, and no mandatory cache dependency for ordinary OS export. A missing, unreadable, corrupt, or unsupported selected database stops the run with an actionable error. Cache paths and contents are not printed in errors or included in the manifest.

## Read and reconcile

1. Open an existing regular SQLite file with `mode=ro` and `query_only`. Require at least one supported table (`workstream_summaries`, `annotations`, `persons`, `signals`, `summaries_annotation_summary`, or `summaries_annotation_description`) with `json` and `expireAt` columns. Reject views or incompatible schemas under those names. Summary rows contain the OS object directly; all other supported tables must contain it under `os`. The two summary-view tables are additional sources of canonical annotation records; their provider keys never supply relationships. Outer UI fields never supply identities, timestamps, relationships, or prose. Read a transactionally consistent view, including its WAL. Do not call SDK initialization, schema migration, cache cleanup, or deletion. Do not fall back to writable mode or `immutable=1`. SQLite's immutable option assumes a file cannot change, while a running client can write its WAL. See [SQLite URI modes](https://www.sqlite.org/uri.html) and [read-only WAL behavior](https://www.sqlite.org/wal.html#read_only_databases).
2. Skip expired, malformed, and oversized cached rows, recording aggregate counts. Match an exact material type and record ID already returned by the current OS. Require equal parsed creation times and valid update times; ignore a cache version newer than the captured OS record. Similar names, prose, timestamps, or embedding scores cannot create an identity match.
3. Use a fixed typed field allowlist: summary annotations/persons/pipelines/tags/events/sources/websites/ranges/hints/summaries; annotation summaries/persons/signals; person annotations/summaries; and signal annotations/persons/pipelines/summaries/workstream_events/websites/ranges. Only an absent/null current OS field is eligible. Any present current field takes precedence, including an explicit empty collection or a malformed field that needs separate investigation. Where original inverse-field eligibility is recorded for a summary/person/annotation/signal attachment, a present current inverse also supersedes a historical link. Current positive attachments still supply their own inverse. Blocked endpoints remain available privately for conservative withholding; this precedence rule cannot bypass their privacy dependencies.
4. Select the newest valid cached version **per field**. A newer explicit empty collection or tombstone suppresses older references. A missing field in a newer cache does not prove deletion. Equal-time versions with different active reference sets conflict; skip that field instead of merging them. Keep the chosen cache ordinal and both record update times. Wrapped-row evidence also records the material and an opaque source-record reference so inverse edges retain the correct owner.
5. Resolve targets only against records already fetched during this export. Missing/out-of-selection targets receive no links and contribute to the coverage count. Do not fetch arbitrary cached content, create source records, or infer missing targets. Keep excluded/withheld targets in the private graph long enough for privacy propagation to suppress dependent content.
6. Apply the ordinary privacy, people-selection, canonical-path, Markdown/PDF, metadata, and final-audit passes. The canonical record JSON remains the current OS representation with the normal privacy transformations. Cached prose, embedded record copies, and credentials are never imported.

Limits: at most eight selected files, 200,000 rows per file shared across all supported tables, 8 MiB of JSON per row, two million inspected active references and 256 MiB of referenced-ID text across the selected files, and two minutes per cache transaction. Count/byte/deadline exhaustion stops the export before finalization. These are safety bounds, not proof that all clients retain a complete history.

## What the archive says

- `coverage.md` and `manifest.json` record rows, expiry/encoding failures, identity/update mismatches, conflicts, unavailable targets, added edges before privacy, and retained historical edges afterward. Edge totals include derived annotation inverses.
- `relationships.jsonl` marks recovered links as `historical_client_cache`, or `historical_client_cache_derived_inverse`, with `cache_evidence` containing the argument-order cache number and source/current update timestamps. Legacy summary evidence retains `cached_summary_updated`/`os_summary_updated`; wrapped rows use `record_material`, opaque `record_ref`, `cached_record_updated`, and `os_record_updated`. Starting with `0.10.1-dev`, `record_table` identifies the fixed source table for wrapped evidence; older typed evidence without it remains readable. Rebuilding validates the typed owner and its current timestamp.
- Summary body attachments, signal descriptions, and related-record lists label historical evidence. Persona profiles disclose that associations can include historical links and link to the detailed provenance. The body itself comes from the current fetched annotation. Relationship siblings disclose that suggestions can use cached links. Relative local links still target included canonical files and undergo validation.
- These exports remain **partial, exit 2**. Cache recovery does not resolve missing current projections, unseen history, stale associations, or unavailable record types. Users without matching caches will have different recovery coverage.

The CLI embeds the CGO-free [modernc SQLite driver](https://pkg.go.dev/modernc.org/sqlite), pinned in `go.mod`. Users need no SQLite CLI, Python, SDK, or Go installation. Release packages include its license and required third-party notices. Cross-compilation remains distinct from native runtime acceptance.

## Evidence and remaining work

The Go reader was tested against four real caches and 11,732 retained summary JSON files from the running export, without extra HTTP requests or archive writes. It read 31,949 cache rows, matched 31,917 rows across the four caches, and found no invalid/expired rows, creation-time mismatches, future updates, or equal-time conflicts in that sample. It found candidate annotation links for 10,316 summaries (87.9%), referring to 21,277 distinct annotation IDs. There were no inline annotation bodies. Candidate person links covered 9,932 summaries but referenced only two persons. No pipeline links were found.

These are candidate counts against the staged summaries, not a reconciled live recovered archive. The full run must finish so referenced annotations/events/tags and privacy decisions can be checked. Recovery of 1,416 summaries without cached annotation links remains unresolved. The running `0.4.1-dev` process predates this option. Do not restart it just to change rendering; the implemented offline `rebuild` command can apply recovery after that archive is finalized, preserving exclusion decisions and source provenance. See [OFFLINE_REBUILD.md](OFFLINE_REBUILD.md). It has synthetic coverage; the current live archive is still pending.

Synthetic tests cover read-only WAL access with unchanged database/WAL bytes, blocked writes, wrong schemas/views, duplicate and missing cache paths, cancellation, reference budgets, current-field precedence, newer tombstones, conflicting equal-time versions, expired/invalid rows, source-time mismatches, missing targets, privacy propagation, canonical current annotation text, provenance, and moved Markdown/PDF links. Packaged-executable acceptance exercises the flag and its partial exit code. Record platform-specific execution evidence in [TODO.md](TODO.md).

### Annotation-stage reconciliation

After the running export completed its annotation fetch phase, a read-only local probe used the production cache-candidate selection logic and checked the candidate IDs against staged annotation files. No source HTTP calls or archive/cache modifications occurred. The opt-in test configuration accepts `reconcile_completed_annotations: true`; use it only after that collection's fetch phase finishes. It emits aggregate counts, never IDs, bodies, or cache paths.

- Four caches then contained 31,951 rows, with the same 31,917 matched rows and no candidate conflicts.
- Of 21,277 distinct candidate annotation targets, 21,274 retained files matched identity and contained nonempty exportable text. Three files were absent; no identity mismatches were observed.
- All cached annotation targets were present for 10,313 summaries. At least one text-bearing annotation was present for all 10,316 summaries with attachment candidates.
- 10,295 staged summaries had a retained, nonempty `SUMMARY`-type annotation through the cached IDs: **87.7% of the 11,732 staged summaries**. This is a potential historical body-attachment recovery count, not a finalized recovery result or proof of complete body content.
- All three absent target IDs matched the older payment-card heuristic inside a complete UUID. That is evidence consistent with the previously fixed false positive, not proof of the omission reason. Compare the final manifest's decisions before considering source rereads with the corrected scanner.

Late privacy propagation, final identity reconciliation, and actual offline rebuilding remain outstanding. The 1,416 summaries without cached attachment candidates still lack that recovery route. Cache prose must not fill missing annotation files or override privacy exclusions. These counts do not measure person reduction: the cached person edges reference only two identities, and persona selection requires the completed person/history evidence.

### Wrapped annotation, person, and signal caches

A further read-only probe on 2026-09-30 found a different row shape in the `annotations`, `persons`, and `signals` tables: the canonical OS record is nested under `os`. The summary table stores its OS object directly. Looking only for relationships on the outer wrapper therefore misses useful historical evidence. The annotation and person SDK serializers confirm the wrapper; signal wrapping was observed in the selected databases. Outer `pfd` identities, text, and UI timestamps are not canonical OS evidence and must not be imported.

The bounded probe matched nested IDs and exact creation times against staged canonical files, rejected cache updates newer than captured OS updates, considered only absent/null current fields, and used newest-per-field selection with tombstone and conflict handling. It checked the retained current annotation's type and text presence; it did not import cached prose. There were no source HTTP calls, cache/archive writes, or private values in the aggregate report. The active archive can still change during privacy reconciliation, so this is provisional evidence rather than a finalized recovery test.

| Candidate measure | Count |
| --- | ---: |
| Summaries with a retained `SUMMARY` body through summary-cache links | 10,295 |
| Summaries with a retained `SUMMARY` body through inverse annotation-cache links | 1,215 |
| Additional summaries supplied by the inverse route | 17 |
| Combined distinct summaries with body candidates | **10,312 / 11,732 (87.9%)** |
| Signals with retained `SIGNAL_DESCRIPTION` candidates | **4,126 / 6,716 (61.4%)** |
| Distinct retained signal-description annotation targets | 4,129 |
| Referenced signal-description targets absent from staging | 3 |

The 1,215 inverse-route candidates overlap heavily with the summary-cache candidates; they must not be added as if disjoint. The combined result leaves 1,420 summaries and 2,590 signals without a validated body/description candidate from these routes. These denominators describe this staged dataset, not coverage guaranteed for another installation. Text presence does not establish complete content, and historical links do not establish current membership. The earlier 10,316-summary figure counts any annotation attachment candidate, whereas 10,312 counts a staged nonempty annotation of type `SUMMARY` through either route.

The probe also found historical signal-to-event and signal-to-summary dependencies, annotation-to-person edges, and person-to-annotation edges. Those dependencies must participate in privacy propagation before any recovered text is rendered. They do not establish new identity merges or a final person-reduction count.

**The wrapped-table importer is packaged and tested in `0.10.0-dev`.** `0.9.1-dev` packages still read summary rows only. Tests cover typed provenance, original-field eligibility, bounds shared across tables, excluded/shared dependencies, strict and preservation modes, scope omissions, repeated offline rebuilding, and moved Markdown/PDF links. Actual packaged acceptance passed on macOS ARM64, Rosetta AMD64, and isolated Linux ARM64; native Windows remains unverified. The actual older `0.9.1-dev` writer also passed the conservative eligibility compatibility test. Detailed evidence is recorded in TODO. Final retained live recovery remains unmeasured; do not apply recovery to the running `.partial` directory.

New exports persist absent/null versus present eligibility for each supported field before sanitization. Older archives did not record this for annotation/person/signal fields; their core projection states alone are insufficient because an earlier rebuilder may have synthesized unknown states. New wrapped-field recovery therefore skips unknown eligibility, counts it in `unknown_original_field_eligibility`, and preserves that decision across repeated rebuilding. Legacy summary recovery keeps its existing separately documented behavior. This means the current `0.4.1-dev` live archive cannot automatically realize the new wrapped-cache candidate counts offline. After it finishes, obtain original-field evidence through a new bounded source capture before claiming those associations recovered. Missing cached summary/signal owners withhold linked retained annotation bodies; missing cached annotations also withhold linked retained summaries/signals in filtered offline recovery.

### Canonical annotation records in SDK views

The SDK stores `LocalAnnotation` views in `summaries_annotation_summary` and `summaries_annotation_description`. The generated Riverpod keys bind a view to a summary, but they are UI evidence rather than fields of the canonical OS record. The body view selects `SUMMARY` or `DEEP_STUDY_HIERARCHICAL_SUMMARY`; the description view selects `DESCRIPTION`. Sources: [body-view notifier](../unified_monorepo/frontend/pieces_platform_client_sdk/packages/pieces_dart_client_sdk/lib/src/notifiers/annotations/summary_rollup_annotation_notifier.dart), [generated key](../unified_monorepo/frontend/pieces_platform_client_sdk/packages/pieces_dart_client_sdk/lib/src/notifiers/annotations/summary_rollup_annotation_notifier.g.dart), and [description-view notifier](../unified_monorepo/frontend/pieces_platform_client_sdk/packages/pieces_dart_client_sdk/lib/src/notifiers/annotations/summary_description_annotation_notifier.dart).

A bounded comparison against the production summary-cache candidates found:

| View | Rows | Distinct validated pairs | Pairs already in summary cache | Novel pairs | Novel pairs with explicit `os.summaries` evidence | Novel pairs with only a UI binding |
| --- | ---: | ---: | ---: | ---: | ---: | ---: |
| Summary body | 730 | 697 | 471 | 226 | 194 | 32 |
| Description | 4,604 | 3,986 | 3,432 | 554 | 546 | 8 |

All 226 novel body-view pairs had staged `SUMMARY` text for summaries without a body through the summary-cache route. They have not been reconciled against the 17 additional inverse-annotation candidates, newest/conflicting versions across every table, or final privacy decisions. Do not add these counts to the earlier combined coverage. An initial independent probe also observed five deep-study body-view rows, already among the non-novel pairs. This is candidate evidence, not a finalized recovered archive.

`0.10.1-dev` reads only the nested canonical annotation from these two tables. It does not select or parse the provider key. Only explicit OS relationships can become candidates under the existing identity, timestamp, original-field, cross-table version/conflict, privacy, and byte/row/time rules. A null view or a null `os` record increments `empty_provider_views`; it does not delete an older canonical relationship. The 32 body and eight description UI-only bindings do not become edges. Graph evidence records the table name without cache paths or UI keys.

The opt-in local probe supports `compare_provider_views: true` together with `reconcile_completed_annotations: true`. It compares current annotation identity/type/timestamps and emits aggregate counts only. The summary run with all new table validation passed in 10.824 seconds; the provider comparison passed in 3.058 seconds on warm local files. These timings are probes, not export benchmarks. No source HTTP requests or archive/cache writes occurred.
