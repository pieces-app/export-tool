# Current association API and summary traversal

Investigated 2026-09-30. **This supersedes the earlier claim that summary/person association enumeration is unavailable.** That claim described older local checkouts. Refreshed source exposes 87 read servers and the current SDK uses them. The installed staging OS must still pass capability and full-history checks; source availability alone is not live coverage. No source repository working tree was switched or edited; `origin/main` was fetched for read-only inspection.

## Missing owners and annotation fallback — 2026-10-01

The corrected real `0.17.1-dev` export finalized with partial status and passed independent archive consistency checks. All **102,753 owner-side traversals** reconciled; 28,359 referenced annotations were fetched without a full-inventory fallback. Included output has current annotation evidence for all 11,559 retained summaries and 1,381 retained persons, current summary evidence for those persons, and 31 memberships across four pipelines. The reader verified 263,020 core relationship edges against canonical association endpoints, with no historical cache evidence. Missing records, withheld dependency chains and 375 retained summaries without annotation edges remain coverage gaps; successful traversal does not create absent source text. See [complete measurements and limits](PERFORMANCE_INVESTIGATION.md#finalized-corrected-summariesprofile-export).

The pinned SDK selector was rechecked after that result: `getSummaryAnnotation()` enumerates current summary-to-annotation IDs, fetches each annotation and returns the first `SUMMARY` or `DEEP_STUDY_HIERARCHICAL_SUMMARY`, otherwise `null`. It has no second body endpoint in this selector. The exporter retains all linked annotation text, including the one observed summary whose nonempty annotation is a hierarchical profile. Empty current associations do not establish whether older text remains elsewhere; no regeneration or silent historical-cache substitution was performed.

The live `0.17.0-dev` run encountered 43 referenced persons whose snapshots were unavailable. It skipped their junction reads, then interpreted the missing annotation coverage as requiring a full annotation inventory. This expanded the initially referenced 28,359 annotation set to a full 74,118-ID fallback. Initial linked-set reduction was therefore not an achieved export reduction.

At the recorded server revision, `lib/utils/associations_read_server_base.dart` forwards count/list calls directly to the indexed association facade using the owner ID. It does not fetch the owner's core snapshot first. At the recorded database-facade revision, `lib/objectbox/facades/common/association_facade.dart` likewise delegates indexed `countBySideA`/`countBySideB` reads. A missing snapshot is not evidence of absent associations.

The forward fix includes missing owners in the usual closure traversal. Successful empty/positive counts must reconcile just as for present or excluded owners. Available targets remain exportable, the unavailable owner remains missing with no invented path/body, and indexed evidence survives completed-source replay and rebuild. Only that verified annotation evidence permits referenced-only retrieval; actual count/list failures remain failures. Tests include missing persons with no annotations, retained profile annotations attached to missing persons, missing summaries, invalid counts, compiled CLI export and offline replay.

The internal facade also exposes batch-count methods, but the inspected HTTP association server does not expose a bulk-count route. Internal methods are not callable API contracts for this tool.

## Source evidence

| Repository | Inspected revision | Finding |
| --- | --- | --- |
| isomorphic_server | `d4e9d488a071e509ea5e7eda1fd4a0a38c17d15d` | 87 generated association servers share bounded side listings, indexed counts and bulk reads. |
| database_facade | `9131ceda1bb1c59209f08ac4a6244c128181359f` | Summary snapshots read ObjectBox models; relationships live in separate junction tables. |
| generated_runtime | `bef51d8a172c9cc059eb784f76c7c7a086fa0666` | Exact association JSON field names; the summary adapter does not reconstruct embedded relationship maps. |
| os_server | `bb1366b776d888d4fa2587113a6fac00c650b158` | Summary/annotation association server is instantiated and its count/bulk routes appear in middleware. |
| pieces_platform_client_sdk | `e0e2ccf1a1875a4f6715911a9cdfee8dd3ea93dd` | Riverpod providers fetch junction peers by pages or bulk; the summary body selector reads those IDs. |

The active OS's bounded native stack sample loaded `libobjectbox.dylib`; no Couchbase library appeared in that sample. This supports investigating the ObjectBox path, but is not an exact build-to-commit attestation. The refreshed SDK's summary-body notifier explicitly explains that migration left the summary's embedded annotation map null. It now watches the junction, fetches its annotation IDs, and selects `SUMMARY` or `DEEP_STUDY_HIERARCHICAL_SUMMARY`. We should reproduce the read traversal, not its UI cache or first-visible-body selection.

Primary source: [shared HTTP server](https://github.com/pieces-app/isomorphic_server/blob/d4e9d488a071e509ea5e7eda1fd4a0a38c17d15d/lib/utils/associations_read_server_base.dart), [summary database facade](https://github.com/pieces-app/database_facade/blob/9131ceda1bb1c59209f08ac4a6244c128181359f/lib/facades/workstreamSummaries/workstream_summary_facade.dart), [ObjectBox summary adapter](https://github.com/open-runtime/generated_runtime/blob/bef51d8a172c9cc059eb784f76c7c7a086fa0666/sdk/objectbox/dart/common/lib/adaptors/workstream_summary_adaptor.dart), [SDK summary body selector](https://github.com/pieces-app/pieces_platform_client_sdk/blob/e0e2ccf1a1875a4f6715911a9cdfee8dd3ea93dd/packages/pieces_dart_client_sdk/lib/src/notifiers/annotations/summary_rollup_annotation_notifier.dart), [SDK junction providers](https://github.com/pieces-app/pieces_platform_client_sdk/blob/e0e2ccf1a1875a4f6715911a9cdfee8dd3ea93dd/packages/pieces_dart_client_sdk/lib/src/generated/notifiers/associations/workstream_summary_to_annotation_associations_providers.dart).

## Read contract

For every row in the inventory below, let `F` be the family and `A`/`B` its route segments. Routes are literal; JSON fields sometimes differ from route names.

| Method and path | Input and response | Export behavior |
| --- | --- | --- |
| `GET /F/A/{a}/B/{b}` | One association joining a known pair. | Never brute-force all possible pairs. |
| `POST /F/batch/fetch` | `{"associations":{"iterable":[{"id":"association-id"}]}}`; output `associations` plus flattened `notFound`. | Read only association IDs already observed; reconcile returned/missing identities. |
| `GET /F/A/{id}?limit=50&offset=0&transferables=false` | Direct collection with `iterable` and `indices`. Equivalent B-side route. | Default limit 50, server cap 500. Generic pages have **no** `total`, `offset`, `limit` or cursor metadata. Do not require the older event/person pagination envelope. |
| `GET /F/A/{id}/count` | `{"id":"requested-id","count":123}`. Equivalent B-side route. | Indexed count; validate owner and nonnegative integer. This is not a snapshot token. |
| `POST /F/A/bulk` | `{"iterable":["owner-id-1","owner-id-2"]}`. Equivalent B-side route. | Direct flat association collection. Server drops unique input IDs beyond 200 and sets `truncated:true`; exporter must reject truncation, never silently accept it. |

Bulk returns **all rows for the accepted owners**, with no row cap. Bounding owner count alone does not bound server work. The new reader requires observed owner counts, at most 50 owners and at most 5,000 expected rows; larger owners use pages. It validates every association ID, exact side bindings, timestamps, active indices and per-owner result counts. Unknown fields and exact JSON numbers survive. Count/page/bulk availability must be tested separately; a 404 is an unsupported/unavailable read, not an empty relationship.

Listing order is association `created DESC` in the inspected ObjectBox provider. Offset reads are not atomic during concurrent edits and have no unique tie-break contract. Record IDs must be deduplicated, and repeated/non-progressing pages, changed counts or mismatched boundaries must produce explicit drift/incomplete coverage. Start/end count equality alone cannot detect equal-count substitutions. Bulk avoids offset movement for small groups but does not freeze all families or later record reads.

Malformed bulk input can return an empty HTTP 200, so request shape is part of the acceptance contract. Send strings, not `{id:...}` objects, in the bulk `iterable`. Do not copy the batch-fetch body into this endpoint.

## Installed-OS evidence

After the focused export entered local-only privacy reconciliation, three sequential bounded probes used the real staging OS at version `12.6.29-staging`. The final probe took 0.15 seconds for 54 requests, zero retries/backoffs, recent HTTP p95 2.50 ms and peak 5.55 ms. These are warm, small reads, not an export-throughput benchmark.

- Three sampled summaries had nine summary-to-annotation associations and three summary-to-person associations, with owner counts matching bulk results and bounded offset pages.
- The nine annotation edges resolved to seven distinct current annotation records; **all three summaries had nonempty current `SUMMARY`/deep-study body text**. No cached text or cache-derived link was used by this probe.
- The three sampled summaries had no pipeline associations. A separate bounded inventory returned 13 pipelines; four had nonzero summary association counts. Counted bulk reads for three nonempty pipelines verified five rows with stable final counts.
- A final OS health/version read succeeded. The probe emitted only aggregates and did not change source data, caches or either export. Ignored evidence: `exports/junction-live-capability.log`, `exports/junction-live-bodies.log`, `exports/junction-live-pipelines.log`.

This verifies these installed read capabilities and sampled bindings. It does not yet prove every summary has a body, full graph/history coverage, source quiescence or an export using these readers. The current baseline executable still follows the older path.

## Exporter integration (0.16.1-dev candidate, 2026-09-30)

The export command now traverses nine families through eleven owner sides: summary→annotation/person/tag/website/source/anchor/range, person→summary/annotation, pipeline→summary, and summary→pipeline. It queries excluded owners too, so shared narrative text inherits privacy denials. Both endpoints and raw association metadata are retained, with canonical association-record provenance. Only selected material types are hydrated; summaries scope still omits events.

Each owner is counted first. Up to 50 owners with at most 5,000 counted rows share a bulk request; larger owners use 50-row pages with duplicate detection, a terminal read and head recheck. Final per-owner counts must match. A missing/unsupported first count selects the older-schema fallback. After a side is supported, failed reads, contradictory original core projections, changed counts/records or unstable pages stop the capture before finalization. Equal-count substitutions remain possible: this is not a database snapshot. Traversal is bounded to eight reference-closure passes and two million returned rows per side, two million distinct retained association records globally, and a 256 MiB identity-key budget (including a fixed allowance per key). This is not a hard process RSS bound.

Successful reads are recorded separately from original JSON field shapes, including empty sets. They resolve the corresponding core coverage gap and override historical cache attachments in either direction. Reconstruction evidence and completed-source recovery preserve these decisions; offline graph proofs validate canonical association bindings. The encrypted recovery compatibility binding is now v2; v1 development workspaces require their original executable. Final archives carrying current junction evidence use reconstruction-state version 2, so older rebuilders refuse them instead of dropping current-empty evidence. Original field shapes and ordinary archive format 5 remain intact; current rebuilding supports reconstruction-state versions 1 and 2.

In summaries scope, the full annotation inventory is deferred. If every inventoried summary and person has reconciled current annotation relationships, only referenced annotations are fetched. Otherwise the full annotation fallback remains. All-data/custom scope still inventories annotations, and `--associations off` retains the older behavior. The manifest records the actual inventory/reference choice. Preflight's four-collection estimate is conservative and can overestimate this path. No reduction percentage or live speedup is established yet.

Focused tests exercise body placement, profile selection, pipeline navigation, link validation, repeated offline rebuilding, encrypted replay, stale-cache precedence, denied shared bodies, pagination instability and older-server fallback. Six binary-only `0.16.1-dev` candidate ZIPs and their hashes were checked. Actual macOS ARM64 default/current-junction/projected/rebuild/recovery acceptance passed (25.201 s with race checks), as did current-junction acceptance under Rosetta AMD64 (2.285 s). These are synthetic tests, not live export timings. The integration passed the full race suite (exporter 534.994 s). The final reconstruction-version compatibility change then passed focused export/rebuild/capture/archive checks (141.306 s with race checks), as did vet. Current-junction live export acceptance remains pending. Running `0.15.0-dev` binaries remain unchanged.

The direct person-history endpoint was also traced through `PersonDatabaseFacade.annotationsByPersonIdentifier` to `lib/objectbox/databases/common/core_entity_bespoke_queries.dart` at the recorded database-facade revision. It selects annotation IDs from the same `PersonToAnnotationAssociationEntity.personRefUid` junction before applying type/time filters. Thus profiles mode can avoid repeating this filtered history query after every current person→annotation association has already been enumerated and hydrated. Unsupported current junctions retain the dedicated history fallback. This source equivalence does not prove an exact installed build or complete database migration.

## Remaining integration and acceptance

- [x] Refresh the source evidence and extract the exact 87 family/route/JSON bindings; retain the historical 23-family contract separately.
- [x] Implement and fixture-test bounded count, side-page and counted-bulk readers without source writes, regeneration or pairwise brute force.
- [x] Probe installed staging OS for summary→annotation, summary→person and pipeline→summary after the focused run finished source reads. Counts, bulk and bounded offset pages matched for the sampled owners; current annotation bodies and nonempty pipeline-side memberships were verified. Full-history traversal remains open.
- [x] Integrate summary/profile junction traversal before body/privacy/person/path processing, including excluded owners and canonical association provenance. Preserve raw metadata and validate graph proofs during offline rebuilding.
- [x] Traverse summary annotation/person/pipeline/tag/website/source/range/anchor edges, plus profile ownership/history. Resolve only selected peer material types in summaries scope; events remain optional. Live full-history acceptance remains below.
- [x] Persist explicit reconciled junction reads as separate coverage evidence, retaining original projection shapes and unknown older schemas.
- [x] Prefer current junction evidence over historical SDK cache edges; preserve precedence in offline rebuilding and encrypted recovery.
- [x] Avoid the full annotation inventory only after all summary/person annotation reads reconcile. Preserve older-schema fallback and all-data inventories. Synthetic unrelated annotations are omitted; real reduction remains unmeasured.
- [ ] Handle person selection after source associations/profile annotations and privacy filtering. Reconcile all linked profile versions and direct summary links; preserve intentional omissions separately from exclusions and unknown evidence.
- [ ] Extend optional all-data association capture across all selected families and both surviving endpoint inventories. A per-side API cannot prove recovery of associations whose two endpoints are both absent; retain that limitation.
- [ ] Verify source drift, deleted endpoints, denied domains, shared annotations, embedded Markdown, graph links, relocated archives, recovery and native packages with the real installed OS.

`junction_export.go` wires the selected summary/profile plans into export; the full 87-family registry remains broader than automatic traversal. The finalized focused baseline used the older binary and historical cache recovery. That baseline passed consistency checks with partial status; it did not recover current junction associations.

## Live cardinality check before another export

A subsequent read-only sizing probe made 320 requests in 0.88 seconds with no retries/backoffs (recent HTTP p95 about 4 ms). It inspected only IDs and indexed counts: 30 summary owners, 30 persons and all 13 pipelines. The summary sample had 66 annotation rows, 29 person rows, 99 tag rows, 101 source rows and 29 range rows. All pipelines together reported 44 summary associations at that interval. No bodies or export artifacts were read or written.

This supports the diagnosis that indexed source reads are responsive, but **does not establish complete-export speed**. A naive projection from this small sample suggests approximately 128,000 association rows across the nine families. Distribution is skewed (the sampled persons missed summary associations entirely), so that number is an order-of-magnitude warning, not a forecast or inventory. Writing separate JSON and Markdown for every association could offset the annotation savings. Consolidated evidence/output persistence must be addressed before treating another full export as a performance proof. Ignored evidence: `exports/junction-live-sizing.log`.

## Complete family inventory

Each family below supports the shared route templates above in the inspected source. Endpoint types and JSON keys come from generated schemas, not English pluralization. Self-relations have deliberately different route segments for their two sides.

| Family (`F`) | A route → JSON field | B route → JSON field |
| --- | --- | --- |
| `anchor_to_anchor_point_associations` | `anchor` → `anchor` | `anchor_point` → `anchorPoint` |
| `anchor_to_asset_associations` | `anchor` → `anchor` | `asset` → `asset` |
| `anchor_to_workstream_pattern_engine_source_associations` | `anchor` → `anchor` | `source` → `workstreamPatternEngineSource` |
| `annotation_to_anchor_associations` | `annotation` → `annotation` | `anchor` → `anchor` |
| `annotation_to_asset_associations` | `annotation` → `annotation` | `asset` → `asset` |
| `connector_to_annotation_associations` | `connector` → `connector` | `annotation` → `annotation` |
| `connector_to_conversation_associations` | `connector` → `connector` | `conversation` → `conversation` |
| `connector_to_conversation_message_associations` | `connector` → `connector` | `message` → `conversationMessage` |
| `connector_to_person_associations` | `connector` → `connector` | `person` → `person` |
| `connector_to_website_associations` | `connector` → `connector` | `website` → `website` |
| `connector_to_workstream_event_associations` | `connector` → `connector` | `workstream_event` → `workstreamEvent` |
| `connector_to_workstream_summary_associations` | `connector` → `connector` | `workstream_summary` → `workstreamSummary` |
| `conversation_message_to_anchor_associations` | `message` → `conversationMessage` | `anchor` → `anchor` |
| `conversation_message_to_annotation_associations` | `message` → `conversationMessage` | `annotation` → `annotation` |
| `conversation_message_to_asset_associations` | `message` → `conversationMessage` | `asset` → `asset` |
| `conversation_message_to_conversation_message_associations` | `message` → `x` | `additional_message` → `y` |
| `conversation_message_to_hint_associations` | `message` → `conversationMessage` | `hint` → `hint` |
| `conversation_message_to_range_associations` | `message` → `conversationMessage` | `range` → `range` |
| `conversation_message_to_website_associations` | `message` → `conversationMessage` | `website` → `website` |
| `conversation_message_to_workstream_pattern_engine_source_associations` | `message` → `conversationMessage` | `source` → `source_` |
| `conversation_to_anchor_associations` | `conversation` → `conversation` | `anchor` → `anchor` |
| `conversation_to_annotation_associations` | `conversation` → `conversation` | `annotation` → `annotation` |
| `conversation_to_asset_associations` | `conversation` → `conversation` | `asset` → `asset` |
| `conversation_to_conversation_message_associations` | `conversation` → `conversation` | `message` → `conversationMessage` |
| `conversation_to_range_associations` | `conversation` → `conversation` | `range` → `range` |
| `conversation_to_tag_associations` | `conversation` → `conversation` | `tag` → `tag` |
| `conversation_to_website_associations` | `conversation` → `conversation` | `website` → `website` |
| `conversation_to_workstream_pattern_engine_source_associations` | `conversation` → `conversation` | `source` → `workstreamPatternEngineSource` |
| `entity_to_subscription_associations` | `entity` → `entity` | `subscription` → `subscription` |
| `entity_to_user_associations` | `entity` → `entity` | `user` → `user` |
| `fingerprint_to_person_associations` | `fingerprint` → `fingerprint` | `person` → `person` |
| `fingerprint_to_workstream_event_associations` | `fingerprint` → `fingerprint` | `workstream_event` → `workstream_event` |
| `hint_to_asset_associations` | `hint` → `hint` | `asset` → `asset` |
| `person_to_anchor_associations` | `person` → `person` | `anchor` → `anchor` |
| `person_to_annotation_associations` | `person` → `person` | `annotation` → `annotation` |
| `person_to_asset_associations` | `person` → `person` | `asset` → `asset` |
| `person_to_conversation_message_associations` | `person` → `person` | `message` → `conversationMessage` |
| `person_to_person_associations` | `person` → `x` | `additional_person` → `y` |
| `person_to_tag_associations` | `person` → `person` | `tag` → `tag` |
| `person_to_website_associations` | `person` → `person` | `website` → `website` |
| `person_to_workstream_pattern_engine_source_associations` | `person` → `person` | `source` → `source_` |
| `pipeline_to_connector_associations` | `pipeline` → `pipeline` | `connector` → `connector` |
| `pipeline_to_range_associations` | `pipeline` → `pipeline` | `range` → `range` |
| `pipeline_to_schedule_associations` | `pipeline` → `pipeline` | `schedule` → `schedule` |
| `pipeline_to_signal_associations` | `pipeline` → `pipeline` | `signal` → `signal` |
| `pipeline_to_website_associations` | `pipeline` → `pipeline` | `website` → `website` |
| `pipeline_to_workstream_pattern_engine_source_associations` | `pipeline` → `pipeline` | `source` → `source` |
| `pipeline_to_workstream_summary_associations` | `pipeline` → `pipeline` | `workstream_summary` → `workstreamSummary` |
| `signal_to_annotation_associations` | `signal` → `signal` | `annotation` → `annotation` |
| `signal_to_person_associations` | `signal` → `signal` | `person` → `person` |
| `signal_to_range_associations` | `signal` → `signal` | `range` → `range` |
| `signal_to_website_associations` | `signal` → `signal` | `website` → `website` |
| `signal_to_workstream_event_associations` | `signal` → `signal` | `workstream_event` → `workstream_event` |
| `signal_to_workstream_summary_associations` | `signal` → `signal` | `workstream_summary` → `workstream_summary` |
| `subscription_to_user_associations` | `subscription` → `subscription` | `user` → `user` |
| `tag_to_anchor_associations` | `tag` → `tag` | `anchor` → `anchor` |
| `tag_to_annotation_associations` | `tag` → `tag` | `annotation` → `annotation` |
| `tag_to_asset_associations` | `tag` → `tag` | `asset` → `asset` |
| `tag_to_conversation_message_associations` | `tag` → `tag` | `message` → `conversationMessage` |
| `tag_to_website_associations` | `tag` → `tag` | `website` → `website` |
| `tag_to_workstream_event_associations` | `tag` → `tag` | `workstream_event` → `workstreamEvent` |
| `tag_to_workstream_pattern_engine_source_window_associations` | `tag` → `tag` | `source_window` → `sourceWindow` |
| `tag_to_workstream_summary_associations` | `tag` → `tag` | `workstream_summary` → `workstreamSummary` |
| `website_to_annotation_associations` | `website` → `website` | `annotation` → `annotation` |
| `website_to_asset_associations` | `website` → `website` | `asset` → `asset` |
| `website_to_workstream_pattern_engine_source_associations` | `website` → `website` | `source` → `source_` |
| `website_to_workstream_pattern_engine_source_window_associations` | `website` → `website` | `source_window` → `sourceWindow` |
| `workstream_event_to_anchor_associations` | `workstream_event` → `workstreamEvent` | `anchor` → `anchor` |
| `workstream_event_to_annotation_associations` | `workstream_event` → `workstreamEvent` | `annotation` → `annotation` |
| `workstream_event_to_conversation_message_associations` | `workstream_event` → `workstreamEvent` | `message` → `conversationMessage` |
| `workstream_event_to_hint_associations` | `workstream_event` → `workstreamEvent` | `hint` → `hint` |
| `workstream_event_to_person_associations` | `workstream_event` → `workstream_event` | `person` → `person` |
| `workstream_event_to_website_associations` | `workstream_event` → `workstreamEvent` | `website` → `website` |
| `workstream_event_to_workstream_pattern_engine_source_associations` | `workstream_event` → `workstreamEvent` | `source` → `source_` |
| `workstream_event_to_workstream_pattern_engine_source_window_associations` | `workstream_event` → `workstreamEvent` | `source_window` → `sourceWindow` |
| `workstream_summary_to_anchor_associations` | `workstream_summary` → `workstreamSummary` | `anchor` → `anchor` |
| `workstream_summary_to_annotation_associations` | `workstream_summary` → `workstreamSummary` | `annotation` → `annotation` |
| `workstream_summary_to_asset_associations` | `workstream_summary` → `workstreamSummary` | `asset` → `asset` |
| `workstream_summary_to_conversation_associations` | `workstream_summary` → `workstreamSummary` | `conversation` → `conversation` |
| `workstream_summary_to_conversation_message_associations` | `workstream_summary` → `workstreamSummary` | `message` → `conversationMessage` |
| `workstream_summary_to_hint_associations` | `workstream_summary` → `workstreamSummary` | `hint` → `hint` |
| `workstream_summary_to_person_associations` | `workstream_summary` → `workstreamSummary` | `person` → `person` |
| `workstream_summary_to_range_associations` | `workstream_summary` → `workstreamSummary` | `range` → `range` |
| `workstream_summary_to_website_associations` | `workstream_summary` → `workstreamSummary` | `website` → `website` |
| `workstream_summary_to_workstream_event_associations` | `workstream_summary` → `workstreamSummary` | `workstream_event` → `workstreamEvent` |
| `workstream_summary_to_workstream_pattern_engine_source_associations` | `workstream_summary` → `workstreamSummary` | `source` → `source_` |
| `workstream_summary_to_workstream_summaries_associations` | `workstream_summary` → `x` | `additional_workstream_summary` → `y` |

## Request-volume cost observed during the format-6 live run

The 2026-10-01 summaries/profile run passed 98,000 HTTP requests while OS responses remained around 2 ms p95 and no retries occurred. Most calls in these phases are per-owner counts and final reconciliation; grouped transaction staging kept public file writes unchanged during association collection. With eight summary-owner plans, two person-owner plans and one pipeline-owner plan, the initial owner inventory implies roughly 205,000 count requests before bulk reads/reference closure. This is an estimate from the traversal shape, not a finalized request total or a throughput guarantee.

At inspected isomorphic_server revision `d4e9d488a071e509ea5e7eda1fd4a0a38c17d15d`, `lib/utils/material_handlers/data_handlers/material_data_handler_registry.dart` registers core material collections but no association families. `/materials/metrics` and `/materials/identifiers` therefore cannot simply replace these owner reads with a generic association inventory. `lib/utils/associations_read_server_base.dart` exposes the eight reads described above; no bulk-count endpoint is present in that contract. No invalid capability probes were sent alongside the active exporter.

Next optimization work should measure count latency versus association filtering/transaction cost, then test bounded count concurrency or a reconciled bulk strategy against the same graph/body evidence. Retain the server-work bound: bulk owner count alone does not limit returned rows. Any replacement must still reject truncated/partial/changing collections, preserve empty-owner evidence, stop on overload and prove no lost privacy dependency. Do not remove reconciliation just to lower a request counter.
