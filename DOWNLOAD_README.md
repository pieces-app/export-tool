# Pieces Export CLI

This download contains a native executable and third-party notices. No Python, Go installation, separate secret scanner, or source checkout is needed to run it. Pieces OS must already be installed and able to serve the user's local data. The CLI can launch it if absent.

On macOS/Linux, open a terminal in the extracted directory:

```sh
./pieces-export doctor
./pieces-export export --output ./my-pieces-export
```

On Windows, use PowerShell:

```powershell
.\pieces-export.exe doctor
.\pieces-export.exe export --output .\my-pieces-export
```

The CLI scans inventory, shows a rough duration estimate, offers Markdown/PDF output, then asks `Export now? [Y/n]`. After approval, Pieces Desktop closes gracefully if running; Pieces OS stays running. Use `--close-desktop=false` to keep Desktop open, `--launch-os=false` to disable OS activation, or `--yes` for automation. EOF cancels.

`--output` resolves from the terminal working directory; the CLI prints the absolute destination. With no option, it creates `pieces-export-<timestamp>` there. Open `my-pieces-export/index.md` after completion, or `index.pdf` when PDF was requested. Read `manifest.json` for included, excluded, withheld, and missing records and current coverage limits. Existing output directories are never overwritten. Exit code 2 means a partial export that needs review; exit code 1 means failure. Interrupted/failed runs can leave a `.partial` directory; it is not a completed export, even if it contains a provisional manifest. Finalization refuses an existing destination. Filesystems lacking that safe rename operation leave the partial folder intact and require a new output on a supported filesystem. This version does not resume partial directories: choose a new output path for a retry.

For a smaller export centered on summary documents and personas, use:

```sh
./pieces-export export --scope summaries --people profiles --output ./my-summaries
```

On Windows, use `.\pieces-export.exe` with the same flags. Summary text comes from annotations; fetching all events is not required. This scope inventories summaries, annotations, persons, and pipelines, then reads referenced tags/websites/sources/applications/ranges/anchors only. It skips event bodies, hints, source-window history, signals, conversations, and other unselected collections. Person connectivity may use an association-count query without reading event bodies. Missing body attachments remain reported as coverage gaps.

`--scope all` preserves the default all-data export. Events add activity history and some source/person/website graph evidence. Omitting them reduces website-origin filtering coverage: rules still inspect exported URLs, but cannot discover a domain present only in skipped activity. Review the intentional omissions in `coverage.md`. The folder structure and validated local links are the same for both scopes. `scan`, `benchmark`, and `export --dry-run` accept `--scope summaries` too. Advanced `--materials` cannot be combined with `--scope`.

To regenerate documents/PDFs from an already completed export without reading OS again:

```sh
./pieces-export rebuild --source ./my-pieces-export --output ./rebuilt-export --format both
```

Use the original `--policy` if configured. The input stays unchanged, exclusions remain excluded, and incomplete coverage remains partial. `--people profiles` can narrow an all-people archive; missing records cannot be restored offline. Optional historical `--sdk-cache` links may attach already-exported annotation bodies, subject to conservative privacy checks. Active `.partial` folders are rejected. Format 5 archives include reconstruction checksums and evidence; older format 4 archives are accepted with explicitly incomplete evidence and partial status.

To preview without creating an archive or closing Desktop:

```sh
./pieces-export export --dry-run --launch-os=false --people profiles --people-report
```

Reads adapt automatically: one outstanding data request, batches start at five and grow up to 50, slow/large responses reduce batch size and add pauses, and repeated overload or a transport failure stops further reads. The terminal shows phase progress, elapsed time, rate, phase ETA, last HTTP latency, retries, backoffs, cumulative file writes, and flush time. New exports and rebuilds save aggregate timings and counts to `performance.json` and the manifest at finalization; ordinary failures attempt to leave diagnostics in staging. Write time includes flush time, so do not add those timings together. Reports contain no record bodies or paths and are not resume checkpoints or proof of completeness. `--performance conservative --batch-size 5` leaves longer pauses. A bounded calibration is not a whole-export speed guarantee; samples can be cached and local filtering/PDF/disk work adds cost.

`--file-workers 2` is the default for local record Markdown writes. Use `--file-workers 1` for serial writes or up to `4` for more parallelism. Every file still receives a disk sync. The bounded pool drains before auditing/finalization; documents larger than 4 MiB run alone. This applies to export and offline rebuild and does not increase OS request concurrency. Cumulative flush time can exceed elapsed time when writes overlap. Full-history speed still needs validation; this does not add resume support.

`--people all` is the default. `--people profiles` keeps people with retained persona/profile annotations and stored account identities. `--people connected --min-person-connections 10` also keeps known summary-associated/high-connectivity people. Unknown evidence is retained conservatively. These options reduce person records, not selected summaries/events/other materials, and do not merge names/emails. Current OS projections can omit person-to-summary associations, making connected mode less selective. `--people-report` shows an aggregate pre-privacy estimate with its own bounded read budget.

Browse `workstream_summaries/index.md`. Its `timeline/` contains chronological summaries, `single_click_summaries/` contains folders such as `daily_standups/` and `morning_briefs/`, and `personas/users/` and `personas/related_persons/` hold profile folders. Each person folder has `profile.md`, `profile_summaries/`, and `related_workstream_summaries/index.md`. User folders require a verified user-to-person mapping. Shared versions and summaries use one canonical file with links from the relevant indexes. Other hierarchy types are under `hierarchical_summaries/`. Known pipeline memberships have secondary indexes in `pipeline_associations/`.

Summary hierarchy is read separately and reconciled against the returned identifier sets. Some OS builds omit summary-body/person/pipeline associations from their responses. The exporter reports those coverage limits; a descriptor can classify a summary without supplying its missing body attachment. Person and persona-context associations can mean author or participant rather than exclusive subject matter. Full-history migration is not yet verified. Signals retain individual canonical files and a consolidated newest-first view at `signals/index.md`. Use `--signals-digest split|single|off` to choose numbered parts, one document, or canonical files only. Split defaults to 1,000 entries and 8 MiB per document (`--signals-per-part`, `--signals-max-part-mib`). Attached descriptions are preserved with their provenance; missing attachments are reported rather than guessed. Oversized entries/documents fail without truncation or finalization. Independent PDF limits still apply; finish Markdown first and rebuild offline with smaller parts when needed. These options are available starting with `0.9.0-dev`.

Filtered mode is the default. It embeds Gitleaks plus explicit credential-field handling, basic payment-card/IBAN/formatted-US-SSN checks, and optional email masking. It applies the policy to JSON, Markdown, generated PDFs, filenames, and document metadata, rescans earlier records against credentials found later, withholds binary/data-URL content, and audits final output. Final text auditing reuses a bounded buffer within each traversal; every file starts with fresh overlap state, and JSON/PDF retain their specialized checks. No scan is skipped by this allocation optimization. It is not a guarantee that every sensitive fact is detected. Generic bank-account detection, NLP PII detection, and image/audio/imported-PDF sanitization are not included.

Website category filtering is **not enabled by default**. Create a policy and, optionally, explicitly download local category domain lists:

```sh
./pieces-export policy init --output policy.json
./pieces-export lists fetch --output lists --categories adult,bank
```

The second command downloads public UT1 datasets; it does not send user data. It normalizes hostnames, omits malformed entries with counts in `lists/source.json`, and saves attribution and checksums. Add these entries to the policy's `domain_lists` array (paths are relative to the policy file):

```json
[
  {"path": "lists/adult.domains", "category": "adult"},
  {"path": "lists/bank.domains", "category": "bank"}
]
```

Then run:

```sh
./pieces-export export --output ./filtered-export --policy policy.json
```

All listed domain files are deny lists, regardless of their descriptive `category` label. Entries match the domain and its subdomains. Explicit `allow` entries override list membership; explicit `deny` entries win over everything. Secret redaction still runs on allowed sources. This first version excludes an entire record when it contains a denied URL, including a URL quoted in prose. It withholds summaries/annotations with known excluded dependencies. Set `withhold_unproven_generated_content` to `true` to withhold all generated summaries, generated annotation types, and conversation messages when source filtering is active; this conservative mode can remove substantial useful content. Domain lists cannot classify adult images or infer banking content with no known URL.

For a private preservation archive containing originals, explicitly choose:

```sh
./pieces-export export --output ./private-originals --mode preserve
```

Preservation mode bypasses content filtering and can retain credentials and private content. Keep it separate from filtered exports. Exposed binary fields remain in original JSON; this version does not extract separate attachment files or download fingerprint audio.

Useful options:

If you have an older native Pieces client cache, `--sdk-cache /path/to/pieces_client_sqlite.db` can recover historical summary, annotation, person, and signal links to current records. Repeat it for multiple explicitly selected files. It reads caches without modifying their records, imports no cached prose, labels recovered links with their provenance, and keeps the archive partial because cached links may be stale. Current OS fields take precedence; missing/excluded targets are not linked. Version `0.10.1-dev` also reads canonical annotation records in the SDK summary-body/description views; UI keys and cached text never create attachments. This is optional and cannot promise complete recovery. Wrapped annotation/person/signal recovery requires original-field eligibility recorded by `0.10.0-dev` or newer; older archives skip unknown fields rather than infer them from filtered JSON.

```sh
./pieces-export export --help
./pieces-export export --output ./export --base-url http://127.0.0.1:39300
./pieces-export export --output ./export --timezone America/New_York
./pieces-export scan --environment staging --launch-os=false
./pieces-export export --output ./documents --format both --metadata auto
./pieces-export export --output ./ranked --related-order relevance --related-limit 25 --related-since 2026-01-01
./pieces-export export --output ./summaries --materials WORKSTREAM_SUMMARIES,ANNOTATIONS,PERSONS,TAGS
./pieces-export materials
```

All timestamps are retained in JSON. Daily Markdown indexes sort by record creation time; summary covered ranges and calendar scheduled times are preserved in context, not treated as identical to creation time. Undated records have their own index. Summary names are `000000.safe_title.YYYY-MM-DD.uuid.md`: newest first when sorting ascending by filename. The global rank starts at zero across all summary folders; gaps within a folder are expected. Ranks and paths can change on re-export. Use `--naming opaque` for names without titles. Related summaries appear by Tags, Source, Person, and Website, inline and in matching `.relationships_graph.md` siblings (`--relationships inline|sidecar|both`).

Related lists default to strongest overlap: one point per distinct shared dimension (maximum four), followed by recency. Ten shared tags still count as one dimension. `--related-order recent` selects newest first. The default limit is 50 per section; the optional `--related-since` cutoff filters suggestions only. Complete group indexes keep older/overflow matches navigable. No export records are removed by these list settings.

Embedded Pieces links point to included local documents or become plain labels. Empty, missing, unsupported, and original filesystem links are flattened; code examples stay code. Permitted external HTTP/HTTPS/email links remain external and are not checked online. Generated local links are checked before completion. In PDFs, a standalone link label is directly clickable; links within surrounding prose get separate labeled link lines so unlinked text cannot open a different record.

`--format markdown` is the default. `pdf` and `both` produce PDFs with retained Markdown companions; PDF document links open the mirrored PDFs at their first page. Markdown retains the complete graph, including JSON links. Non-PDF destinations remain plain text in PDF. Filenames outside the legacy MacRoman encoding also remain unlinked in PDF, with a warning and partial status; their Markdown links remain intact. Keep the entire folder together. Viewer access restrictions still apply: in Preview on macOS 15.7.3, a moved summary/index round trip worked after both documents were opened directly, but first-use links to unopened files could produce permission alerts. No viewer security settings or filesystem permissions are relaxed by the exporter. PDFs use an embedded font, text/list/code layout, and a readable table fallback. Unsupported glyphs make the result partial while preserving full text in Markdown. Use `--pdf-font /path/to/font.ttf` (maximum 32 MiB) for broader font coverage; there is no automatic font downloading or multi-font fallback.

Summary descriptions/tags/sources/persons/websites are in the files and portable `.metadata.json` sidecars. `--metadata auto` attempts native attributes and records readback results. macOS uses Finder metadata xattrs; Linux uses XDG xattrs where supported; Windows requires an installed writable property handler. Markdown often lacks such a Windows handler. Sidecars remain available on every platform. `--metadata off` disables native attributes. File-manager display/indexing and attribute retention when copying vary by OS/filesystem.

Use `--environment production` or `--environment staging` to choose an OS build. Auto accepts a unique existing instance; if none is running, auto launch selects production. Staging shares the production URL scheme: supply an explicit `--os-path` executable/bundle (macOS defaults to `/Applications/Pieces OS (Local Staging).app`). The selected environment is verified after launch. An explicit `--base-url` connects only to that instance and does not launch it if unreachable.

The tool only reads loopback Pieces OS APIs. It does not modify capture settings or create/delete/regenerate user content. Export-time privacy processing makes no external network requests. Only the explicit `lists fetch` command accesses the public category provider.

Current OS versions may also omit summary annotation/person/pipeline links. Direct persona queries recover persona histories; complete summary body attachment and person/pipeline grouping remain pending API coverage work. Projection warnings appear in the manifest.

Association metadata (`0.11.0-dev`): `--associations linked` is the default. It reads metadata for typed relationship pairs whose two records are already selected and included, stores role/confidence/evidence and other approved fields, and links both records. These extra lookups are not included in the initial inventory estimate. Use `--associations off` to skip them. Missing projections can hide pairs; a 404 is reported as unknown availability, not proof that a relationship never existed. Offline rebuild retains the fetched metadata without contacting OS and removes associations to newly omitted people.

Event/person history (`0.12.1-dev`): when both events and persons are selected, the exporter also pages association records by person. This can recover explicit graph links absent from ordinary record snapshots. It checks pagination metadata, repeated records, totals and start/end changes, and labels incomplete reads. Previously measured small collections are queried first; a response without pagination metadata stops further page queries, with observed-pair lookups retained as a fallback. It does not provide an atomic snapshot. Summaries scope continues to skip event history. Rebuilding archives containing the new association-derived graph edges requires `0.12.0-dev` or newer.

Local graph construction (`0.12.2-dev`) reads summary descriptions and website hosts as needed instead of retaining all annotation/website JSON in memory. Unrelated canonical records still export in full. Terminal progress separates summary relationships from graph/chronology indexing. This reduces one local processing cost; full-history throughput remains under validation.

Current limits: no atomic snapshot, historical/deleted-data recovery, complete association-object export, supplementary settings/analysis views, resume, separate binary extraction, or guaranteed semantic privacy. Capture/editing during a run can change records; the manifest reports identity-set drift but cannot detect every in-place edit. Large exports keep IDs/graph metadata and configured domain lists in memory. Review coverage before treating the result as a complete migration.

Signal descriptions live in linked `SIGNAL_DESCRIPTION` annotations. Missing signal relationships are reported as unknown coverage and make the archive partial, even when every signal ID was fetched. Signal batch responses also omit embeddings; empty vectors do not reproduce the original vector database. Rebuilding an older archive without signal projection evidence keeps those relationships unknown.

This utility is intentionally unsigned and is not notarized. Its application source remains private; use of the downloaded binary is covered by `LICENSE.txt`. Keep `THIRD_PARTY_NOTICES.txt` with the executable. Platform runtime testing and complete migration acceptance are still required before a production-ready claim.

PDF limits apply per document: `--pdf-max-input-mib 16`, `--pdf-max-pages 1000`, and `--pdf-max-output-mib 64`. They can be raised within the CLI's documented ranges, but cannot be disabled. A limit failure leaves an unfinished `.partial` folder and returns exit 1; it does not silently cut off content. For large archives, finish Markdown first and use `rebuild --format both` into a new folder, so a PDF retry needs no additional OS reads. Automatic splitting is not yet implemented. These limits bound document inputs/pages/output, not total process memory or time spent inside font/PDF libraries.

Domain filtering now propagates through known signal/event/summary/description dependencies, and strict derived-content filtering includes signals and `SIGNAL_DESCRIPTION` annotations. Shared description attachments are withheld conservatively. Older archives lack this capability marker (`signal_privacy_version: 1`); if they used domain/source rules, offline rebuild withholds their signals/descriptions and reports partial status because pruned dependency links cannot be recovered offline. A fresh source export is needed to recover safe descriptions in that situation. Missing OS projections remain a separate coverage limitation.

Final JSON/JSONL audits scan decoded values with bounded read buffering and independent file state; unfinished containers fail validation.
