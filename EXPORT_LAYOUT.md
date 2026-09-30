# Export folders, naming, and navigation

Updated 2026-09-30. This is the implemented **archive format 5** contract. See [EXPORT_SPEC.md](EXPORT_SPEC.md) for behavior, [TODO.md](TODO.md) for release acceptance, and [EXPORT_GUIDE.md](EXPORT_GUIDE.md#summary-classification-and-relationship-coverage) for schema and traversal evidence. The signals digest is included starting with `0.9.0-dev`; live completeness still requires acceptance.

## Where the export goes

`--output` chooses a **new folder**. Relative paths resolve from the terminal's current working directory. The CLI prints its absolute destination before `Export now? [Y/n]` and again on completion.

```sh
./pieces-export export --people profiles --output "$HOME/Documents/Pieces-Export"
```

In PowerShell:

```powershell
.\pieces-export.exe export --people profiles --output "$HOME\Documents\Pieces-Export"
```

Without `--output`, the destination is `pieces-export-<timestamp>` under the current directory. The exporter stages work in `<destination>.partial` and renames it after validation. Existing output/staging directories are never overwritten. An interrupted run can leave a partial folder; resume is not implemented. `export --dry-run` creates neither folder and does not close Desktop. Add `--launch-os=false` to prevent activation of an absent OS.

`performance.json` is produced at finalization (or best effort after a failure), starting with `0.12.3-dev`. It contains aggregate diagnostics only and cannot certify completeness or resume a partial archive. See [measurement boundaries](EXPORT_SPEC.md#local-performance-diagnostics-0123-dev).

`--file-workers` changes local rendering throughput without changing file names, document content or relative links. The default is two; one keeps writes serial. Every file is still synced, and all writers settle before validation/finalization.

PDF limits and failure behavior are described in the [specification](EXPORT_SPEC.md#pdf-resource-limits-and-cancellation). Oversized documents are not truncated or automatically split; the entire PDF export stays unfinished on a limit failure. A finalized Markdown archive can be rebuilt repeatedly into separate destinations without rereading OS.

## Optional event history

`--scope summaries` keeps this summary/persona/pipeline folder structure while omitting event bodies and other activity collections. Summary bodies are annotation records. Summaries, annotations, persons, and pipeline definitions are inventoried; supporting tags, websites, source applications, applications, ranges, and anchors are fetched only when referenced. New exports default to `--scope summaries --people profiles`. Use `--scope all` to select all supported collections, with all people by default. Explicit `--people` overrides either scope default; custom material selections retain all people unless overridden.

`manifest.json` and `coverage.md` record scope and intentional omissions. Local links point only to included files. Suggestions can be less connected without event-derived source/person/website evidence. Domain rules still inspect exported URLs, but cannot recognize origins available only in skipped activity. See [the scope contract](EXPORT_SPEC.md#optional-event-history-and-summaries-scope).

## Folder map

Names and IDs below are illustrative. Only included, successfully read records appear. In filtered mode, records and folder labels must pass privacy processing first. Empty categories can contain an index without record files.

```text
Pieces-Export/
  index.md                                  # start here
  index.pdf                                 # when PDF requested
  manifest.json                             # counts, issues, hierarchy checks, performance
  performance.json                          # aggregate local phase/read/scan/write/flush measurements
  rebuild-state.jsonl                       # hashed included evidence; opaque omission decisions
  coverage.md                               # aggregate record/projection gaps; partial status
  link-map.json                             # canonical paths keyed by material/ID hash
  relationships.jsonl                       # typed edges; derived inverses marked

  workstream_summaries/
    index.md                                # every included workstream summary
    timeline/
      index.md
      000000.title.2026-09-29.<uuid>.md
      000000.title.2026-09-29.<uuid>.relationships_graph.md
      000000.title.2026-09-29.<uuid>.metadata.json
    personas/
      index.md                              # category links and duplicate-review candidates
      users/
        index.md                            # verified user-to-person mappings
        Alex_Example.<group-key>/
          profile.md                        # latest persona/profile text and all version links
          profile_summaries/
            index.md
            000000.hierarchical_profile_summary.2026-09-29.<id>.md
            000001.profile_description.2026-08-15.<id>.md
          related_workstream_summaries/
            index.md                        # direct associations and profile-context summaries
      related_persons/
        index.md
        Casey_Example.<group-key>/
          profile.md
          profile_summaries/...
          related_workstream_summaries/index.md
    single_click_summaries/
      index.md
      daily_standups/
        index.md
        000001.title.2026-09-29.<uuid>.md
        000001.title.2026-09-29.<uuid>.relationships_graph.md
        000001.title.2026-09-29.<uuid>.metadata.json
      morning_briefs/...
      end_of_day_recaps/...
      week_recaps/...
      todays_headlines/...
      time_tracker/...
      collaboration_patterns/...
      ai_habits/...
      top_of_mind/...
      meeting_prep/...
      custom_summaries/...
      temporal/...
      persona/...                           # descriptor alone does not establish a person owner
      <safe-custom-descriptor>.<key>/...
      unclassified/...                      # SPECIFIC type with no descriptor
    hierarchical_summaries/
      index.md
      deep_study_hierarchical_summary/...
      query_driven_hierarchical_summary/...
      generic_hierarchical_summary/...
      conversationally_generated_hierarchical_summary/...
    pipeline_associations/
      index.md                              # secondary indexes of known explicit memberships
      <pipeline-name>.<id>.md                # links to canonical outputs and pipeline record

  signals/                                  # selected SIGNALS only
    index.md                                # counts, missing descriptions, links to parts
    parts/000000.signals.<newest>.<oldest>.md # default: at most 1,000 entries / 8 MiB
    all-signals.md                          # alternative with --signals-digest single
  timeline/
    index.md                                # chronological navigation across ALL material types
    records.jsonl                           # all included dated records, creation-time ascending
  markdown/
    persons/                                # separate canonical identity records
    annotations/                            # ordinary annotations and shared profile versions
    pipelines/                              # pipeline definitions
    signals/                                # individual signals, currently implemented
    associations/<family>/<opaque-key>.md   # source metadata linked to both canonical endpoints
    events/...
    tags/...
    websites/...
    <other-material>/...
    days/YYYY-MM-DD.md                       # all-record daily indexes
    undated.md
    relationships/<dimension>/...           # complete shared-group indexes
  data/<material>/<opaque-key>.json          # filtered records
  raw/<material>/<opaque-key>.json           # used instead of data/ in preservation mode
  pdf/                                      # when PDF requested
    workstream_summaries/...                 # mirrors nested document layout
    timeline/index.pdf
    persons/...                             # generic markdown/ prefix is omitted
    annotations/...
    <other-material>/...
```

`pdf` and `both` both retain canonical Markdown companions. PDF document links target their mirrored PDFs, at the first page. Markdown retains JSON and other non-PDF navigation. Keep the whole export together; PDF viewers may require permission to open each local destination. See [native viewer findings and filename limits](NATIVE_GUI_ACCEPTANCE.md). Portable metadata sidecars accompany summary Markdown and PDF files in their own trees. A run uses only one of `data/` and `raw/`.

## How summary placement is determined

Placement happens after privacy processing and people selection. Every source record has one canonical document in `link-map.json`. Indexes provide extra ways to reach that file, without copying its body into every related folder.

| Source field | Canonical placement |
| --- | --- |
| `parentHierarchicalType = SPECIFIC_HIERARCHICAL_SUMMARY` | `workstream_summaries/single_click_summaries/<descriptor-folder>/` |
| `parentHierarchicalType = TEMPORAL_DAY/WEEK/MONTH/QUARTER/YEAR_HIERARCHICAL_SUMMARY` | `workstream_summaries/timeline/` |
| Absent or `UNKNOWN` parent type | `workstream_summaries/timeline/`; preserve the unknown classification, without claiming automatic generation |
| Other explicit parent type | `workstream_summaries/hierarchical_summaries/<type>/` |
| Annotation type `HIERARCHICAL_PROFILE_SUMMARY` or `PROFILE_DESCRIPTION` with one included owner group | That person's `profile_summaries/` |
| Shared persona/profile annotation with multiple included owner groups | One file under `markdown/annotations/`, linked from every owner index |

The hierarchy enum is the broad category. **`parentHierarchicalTypeDescriptor` is the finer pipeline-style key** used by the client UI. The [SDK generation enum](../unified_monorepo/frontend/pieces_platform_client_sdk/packages/pieces_dart_client_sdk/lib/src/utils/enums/workstream_summary_generation_type.dart) defines exact built-in keys. The exporter maps `standup` to `daily_standups`, `morning_brief` to `morning_briefs`, `end_of_day_recap` to `end_of_day_recaps`, `week_recap` to `week_recaps`, and `custom_summary` to `custom_summaries`. The other built-in folder names match their descriptors.

Unknown custom descriptors retain a safe label plus a deterministic hash suffix. Two different descriptors that normalize to the same label remain separate. Opaque naming replaces custom descriptor labels with stable keys. Missing descriptors go to `unclassified`. Summary titles, dates, matching names, and shared tags do not establish pipeline ownership.

The server emits `custom_pipeline_<pipeline-uuid>` for custom pipelines. This classifies an output even when `summary.pipelines` is absent. When that exact pipeline is included, its approved display name supplies the folder label, followed by the descriptor's stable hash suffix. Same-name pipelines remain distinct. Missing or excluded pipelines retain the descriptor fallback; their names are never read into the output. This display lookup does not fabricate a typed pipeline edge. Known `summary.pipelines` and `pipeline.summaries` associations remain available through `pipeline_associations/`. Multiple pipeline indexes can link to the same summary without moving it away from its type/descriptor folder.

The SDK also defines a `persona` pipeline descriptor. It is not the same as the annotation enum `HIERARCHICAL_PROFILE_SUMMARY`, and alone cannot prove which person owns a workstream summary. Such outputs stay visibly classified under the single-click tree until an explicit relationship establishes ownership/context.

## Persons, profiles, and retained histories

`workstream_summaries/personas/<users|related_persons>/<name>.<group-key>/profile.md` displays the newest retained persona text, newest factual profile description, links to all known retained versions, and related summary navigation. These types remain distinct:

- `HIERARCHICAL_PROFILE_SUMMARY`: generated persona text.
- `PROFILE_DESCRIPTION`: factual bio/about-me text.
- Workstream-summary hierarchy enums: categories of summaries, not persona identity or ownership proof.

The exporter queries projected persons directly with `POST /person/<person>/annotations`, filtering by those two annotation types. History pages use overlapping creation-time boundaries and ID deduplication. If a full page is saturated at one timestamp and cannot progress, it reports unresolved history rather than skipping records. Independently inventoried annotations remain exported regardless of graph reachability.

`profile_summaries/` contains full canonical Markdown for versions owned by exactly one included group. Its index also links shared versions stored elsewhere. Each group uses a separate newest-first annotation rank. Names use the approved display name, then email if no usable name is present, then `person`; a stable group key prevents same-name collisions.

`users/` is populated only through **`GET /user/<user-id>/person`**. The exporter checks the current `GET /user` identity and, when selected, retained `USERS` records. It never assumes User ID equals Person ID or that a platform/name/email match proves the mapping. An unresolved mapping leaves the person under `related_persons/`. This does not discard their profile history. Other equal explicit platform identities can share a navigation folder while keeping separate source identity records; names and emails are review candidates only.

`related_workstream_summaries/index.md` includes all known direct person-to-summary associations and summaries attached to that person's persona/profile annotations. The profile page distinguishes these two paths. **A profile-context or person association does not prove that a summary is exclusively about that person.** Summary creation can attach the author's latest persona as context. Typed links and the hierarchy remain navigable; narrative name mentions are not promoted to ownership.

### Choosing how many people to retain

| Option | Selection |
| --- | --- |
| `--people all` (default for all/custom scope) | Every readable, privacy-approved person |
| `--people profiles` (default for summaries scope) | Verified user mappings, explicit platform identities, retained persona/profile evidence, and conservatively retained unknown annotation evidence |
| `--people connected` | Profile selection plus known summary associations, sufficient content connections, or unknown connectivity evidence |

`--min-person-connections 10` controls the connected threshold. Counts use distinct included content IDs where available, supplemented by source event-association totals for projected persons. Source totals precede privacy filtering and do not mean every associated event survives. Ghost persons do not qualify solely by the connection threshold. `--people profiles` is a person selection option; it does not restrict all other material exports to personas.

### Measured reduction

Refreshed read-only preview, 2026-09-30: **4,406 people → 1,373 retained, 3,033 omitted (68.8%)**, with six account identities already included. It completed in about 21 seconds with zero retries and one pacing backoff. All projected-person event counts were deliberately unqueried in profiles mode; unknown connectivity is reported explicitly. These are pre-privacy eligibility counts, not the final export outcome or identity merges.

Historical comparison:

Read-only preview, 2026-09-29, staging `12.6.29-staging`:

| Measure | Count |
| --- | ---: |
| Persons evaluated | 4,291 |
| Persons with persona annotations | 1,373 |
| Persons with separate profile descriptions | 0 |
| Stored platform identities, already within the retained group | 6 |
| Retained with `profiles` | **1,373** |
| Intentionally omitted with `profiles` | **2,918 (68.0%)** |
| Source event connectivity of at least 10 | 3,720 |
| Person-side summary connectivity not projected | 4,291 |
| Retained with conservative `connected` selection | 4,291 |

This is a meaningful reduction in person documents, measured before export privacy filtering. It is selection, not verified identity deduplication or a completed full export. Twelve shared-full-name groups were review candidates; zero shared platform-ID or explicit-email groups were found in the evaluated fields. No source records were merged or deleted. The newer exact user-mapping logic still needs full selection reconciliation on the live dataset.

## Hierarchy and relationship coverage

The exporter inventories all summaries independently, then reads the global parent/child identifier sets and each parent's immediate children. It stores both navigation directions without recursive body duplication. Multiple parents are preserved. It checks the returned child inventory against recovered children and reports malformed/missing/inconsistent endpoints as partial coverage. `manifest.summary_hierarchy` records the returned candidate counts, parents read, edges read, and inventory match result.

**Live check of the implemented traversal:** 11,733 summary IDs; 20 global parent IDs; 1,004 global child IDs; 20 parent reads; **1,708 direct edges**, with the returned inventories matching and zero coverage issues. The read-only probe took about 0.10 seconds on this host; 25 measured requests before the final health checks, recent p95 about 14 ms, no retries/backoffs. This replaces approximately one extra snapshot request per summary with two global reads plus one read per parent. It is not a full export timing estimate. The OS remained healthy.

The server's hierarchy helpers can suppress internal association errors; matching their returned inventories is not proof that every database edge exists in the response. Complete database-level reconciliation remains a release acceptance requirement.

Body and membership coverage is separate. An evenly spaced sample of 250 summary snapshots exposed their enums/descriptors, but none exposed annotation/person/pipeline relationships. Sampled persona annotations from both direct person queries and annotation batches likewise omitted their `summaries` projection. The schema supports these relationships; the installed build is omitting them from the inspected responses. Missing fields are unknown coverage, not empty collections.

The exporter now handles both `summary.annotations` and inverse `annotation.summaries` attachments, marks derived inverses, and traverses persona annotation → workstream summary where that link is present. It cannot reconstruct a link omitted on both sides by comparing text, titles, or timestamps. Summary-body attachment, full person/pipeline membership, and full-history migration remain release blockers. The generic `RELATIONSHIPS` collection cannot substitute: its node enum covers assets/formats/tags/websites rather than the full memory graph.

## File naming and ordering

Summary basename: `<zero-padded-global-rank>.<safe-title>.<creation-date>.<source-uuid>`.

- Rank starts at `000000` for the newest included summary across all workstream-summary folders. Width is at least six digits. Gaps within one folder are expected.
- Sort by creation timestamp descending, then ID ascending. Undated summaries follow dated ones and use `undated`. Activity ranges remain separate from creation time; selected timezone controls display dates.
- Privacy processing precedes naming and ranking. Titles are Unicode-normalized and limited to 90 UTF-8 bytes; ordinary person/pipeline labels use 48. Separators, controls, and reserved punctuation are removed. UUIDs or stable opaque ID fallbacks prevent collisions.
- `.md`, `.relationships_graph.md`, and `.metadata.json` share the summary basename. PDF copies use the same basename under `pdf/`.
- A re-export can change ranks/titles/paths. Source identity remains in the record and path map; no persistent-path guarantee is made between separate exports.
- `--naming opaque` removes personal/custom names from record and group filenames. Fixed category names remain. It does not anonymize document contents.
- Total Windows path length, long root folders, non-Latin names, and cross-filesystem copies remain native platform acceptance tests.

## Valid links and related summaries

Every renderer uses final canonical paths and relative paths with forward slashes. Markdown uses URI escaping; PDF local-file actions use file specifications with a Unicode filename and a legacy MacRoman filename. The whole folder can be moved. PDF document links target mirrored PDFs at their first page; heading fragments do not become PDF page anchors. Non-PDF destinations remain plain labels in PDF. Destinations outside the legacy filename encoding also remain plain labels, with a warning and partial status; Markdown preserves those links. Supported embedded Pieces links become local links only when the target exists and is included; missing/private/unsupported/original-filesystem/empty destinations become plain visible labels. No empty `[]()` or placeholder `#` links are generated. Permitted external web/mail links stay external and are not contacted to check availability.

Related-summary sections use Tags, Source, Person, and Website. Default relevance scores one point per distinct shared dimension, then sorts by recency and ID. `--related-order recent`, `--related-limit`, and `--related-since` change suggestions only. Complete group/person/pipeline indexes preserve all known included memberships beyond suggestion limits. Markdown and PDF local targets are checked before finalization and tested after moving the archive.

Descriptions, tags, normalized source/website tags, and approved person labels appear in documents and portable metadata sidecars. Optional native attributes use the platform-specific mechanisms documented in [EXPORT_SPEC.md](EXPORT_SPEC.md#native-metadata-and-portability); filesystem copying can remove those attributes, so sidecars remain authoritative.

## Coverage report

The root `coverage.md` links to the manifest and reports per-material counts alongside the core summary/person/pipeline projection counts. Absent or malformed relationship fields make an export partial, even if all returned IDs reconcile. Explicitly empty fields are counted separately. Inverse body recovery and hierarchy traversal preserve supported links without certifying the omitted projections. Counts concern included records; private record labels and identifiers do not appear in this report. The root index links to it, and PDF mode adds `pdf/coverage.pdf` with links to companion PDFs.

With explicit `--sdk-cache` inputs, this report also reconciles historical candidate/recovered edges. Body attachments and graph JSONL identify cache provenance while retaining the same canonical folder tree and filenames. No duplicate cached record tree is emitted. See [SDK_CACHE_RECOVERY.md](SDK_CACHE_RECOVERY.md).

## Signals digest

Individual records remain in `markdown/signals/`. Version `0.9.0-dev` adds a consolidated view without new OS requests or signal generation. It renders from approved staged records after privacy filtering and final path assignment. See TODO for package/platform acceptance; full live-archive recovery remains unverified.

| Option | Result |
| --- | --- |
| `--signals-digest split` (default) | `signals/index.md` plus numbered `parts/` documents with previous/next navigation. |
| `--signals-digest single` | The same index plus `signals/all-signals.md`, subject to the document byte budget. |
| `--signals-digest off` | Canonical signal records only; no consolidated folder. |
| `--signals-per-part 1000` | Split after this many entries; allowed range 1–10,000. |
| `--signals-max-part-mib 8` | Maximum Markdown bytes per digest document; allowed range 1–128 MiB. |

When signals are outside the selected scope, no digest is generated. An explicitly selected empty inventory gets an index explaining zero entries, with no nonexistent part link. Ordering is creation time descending, source ID ascending for ties, then invalid/missing dates last. Occurrence ranges are displayed separately. Part basenames contain their zero-based part number and newest/oldest entry creation dates in the export timezone; `undated` is explicit. Entry ranks are global across parts.

Entries include identity, origin/category, creation/update times, exposed occurrence ranges, and links to included canonical signal/person/pipeline/summary/event/website/range/annotation records. Only attached nonempty `SIGNAL_DESCRIPTION` annotations supply description prose. Every approved version is retained with its timestamp and attachment provenance; multiple descriptions are reported, without choosing an authoritative current version. Either endpoint can expose a supported signal relationship; derived inverse edges are identified explicitly. Missing/private/unselected targets remain unlinked. Unsupported embedded Pieces links become visible labels, using the same rules as other documents.

The index and `manifest.signals_digest` reconcile entries to included signals and report description presence/multiplicity, unavailable target occurrences, undated signals, unknown projections, part counts, and bytes. Excluded, withheld, and intentionally omitted records are counted separately. Disabled/unselected digests do not compute description statistics. Current staged data contains 6,716 signals with all seven forward relationship projections absent; this does not prove descriptions are absent. See [the source contract](EXPORT_GUIDE.md#signals-and-their-description-annotations).

A separate read-only SDK-cache probe found historical description candidates for 4,126 of those signals (61.4%). The wrapped-row importer is packaged in `0.10.0-dev`, with synthetic export/offline/PDF tests and actual-package acceptance on available macOS/Linux runtimes. This remains a candidate count, not finalized digest coverage. Recovered descriptions use this same layout with explicit historical provenance. Older archives without original field eligibility require fresh source evidence before these attachments can be recovered. See [cache evidence and remaining work](SDK_CACHE_RECOVERY.md#wrapped-annotation-person-and-signal-caches).

Planning renders one bounded entry at a time and reports approved entry bytes before writing. A second pass checks content hashes against the plan. Each document reserves 16 KiB for headings/navigation; canonical input reads are capped at 128 MiB per record. Filtered exports also enforce the existing 2 MiB text-field scanner limit before rendering. An oversized individual entry, single document, or index fails without truncation or archive finalization. Choose smaller split parts, a larger allowed byte budget, or `off` in a new export/rebuild destination. These limits do not cap total process memory or disk usage, and interrupted `.partial` directories cannot be resumed.

PDF mode mirrors these documents under `pdf/signals/`, with the independent PDF input/page/output limits. Markdown splitting does not automatically satisfy a PDF page limit or split an oversized PDF further. For large histories, finish Markdown first, then rebuild the finalized archive offline with fewer entries per part if PDF conversion needs it. Rebuild inherits a recorded digest mode (otherwise `split`); count/byte budgets use current defaults unless explicitly supplied. Synthetic checks cover 6,716 entries, chronology, Unicode, filtering, missing/shared links, moved archives, cancellation, and size failures. Live recovery and native viewer acceptance remain open in [TODO.md](TODO.md).

## Regenerating an archive

`rebuild --source <finalized-folder> --output <new-folder>` regenerates this layout offline and can add PDFs or narrow an all-people archive. Source files remain unchanged; original coverage gaps and privacy omissions persist. New format 5 state/graph/link-map checksums allow original user/projection evidence to survive rendering. Legacy format 4 imports remain partial. See [OFFLINE_REBUILD.md](OFFLINE_REBUILD.md).

The `0.10.1-dev` cache reader also accepts canonical annotations embedded in the SDK's summary-body and description views. Their explicit OS fields use the same annotation paths and historical provenance as other recovered records. Provider keys never create file links. Candidate recovery and unresolved UI-only bindings are detailed in [SDK_CACHE_RECOVERY.md](SDK_CACHE_RECOVERY.md#canonical-annotation-records-in-sdk-views).

Association metadata starts with `0.11.0-dev`: observed typed pairs are looked up by default (`--associations linked`), with `--associations off` available to skip those extra reads. Canonical records use `data/associations/<family>/` or `raw/associations/<family>/`, with Markdown under `markdown/associations/<family>/` and mirrored PDFs under `pdf/associations/<family>/`. They appear in all-record chronology and backlink sections. Both endpoints must survive selection and privacy processing. Coverage is limited to observed pairs, not all possible associations; absent projection fields and failed lookups remain explicit gaps. See [association export behavior](EXPORT_SPEC.md#typed-association-metadata-export).

In `0.12.0-dev`, selecting both event and person history also pages their association collection by person. Associations discovered without ordinary endpoint projections use the same folders above. New direct event/person graph edges cite the retained canonical association record through `association_ref`; the JSON snapshots are unchanged. Coverage records pagination checks and remaining uncertainty. The graph links and their source evidence survive a compatible offline rebuild; use `0.12.0-dev` or newer for these archives.
