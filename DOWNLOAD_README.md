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

Reads adapt automatically: one outstanding data request, batches start at five and grow up to 50, slow/large responses reduce batch size and add pauses, and repeated overload or a transport failure stops further reads. The terminal shows phase progress, elapsed time, rate, phase ETA, recent latency, retries, and backoffs. `--performance conservative --batch-size 5` leaves longer pauses. A bounded calibration is not a whole-export speed guarantee; samples can be cached and local filtering/PDF/disk work adds cost.

`--people all` is the default. `--people profiles` keeps people with retained persona/profile annotations and stored account identities. `--people connected --min-person-connections 10` also keeps known summary-associated/high-connectivity people. Unknown evidence is retained conservatively. These options reduce person records, not selected summaries/events/other materials, and do not merge names/emails. Current OS projections can omit person-to-summary associations, making connected mode less selective. `--people-report` shows an aggregate pre-privacy estimate with its own bounded read budget.

Browse `workstream_summaries/index.md`. Its `timeline/` contains chronological summaries, `single_click_summaries/` contains folders such as `daily_standups/` and `morning_briefs/`, and `personas/users/` and `personas/related_persons/` hold profile folders. Each person folder has `profile.md`, `profile_summaries/`, and `related_workstream_summaries/index.md`. User folders require a verified user-to-person mapping. Shared versions and summaries use one canonical file with links from the relevant indexes. Other hierarchy types are under `hierarchical_summaries/`. Known pipeline memberships have secondary indexes in `pipeline_associations/`.

Summary hierarchy is read separately and reconciled against the returned identifier sets. Some OS builds omit summary-body/person/pipeline associations from their responses. The exporter reports those coverage limits; a descriptor can classify a summary without supplying its missing body attachment. Person and persona-context associations can mean author or participant rather than exclusive subject matter. Full-history migration is not yet verified. Individual signals export today; a consolidated signals document is planned.

Filtered mode is the default. It embeds Gitleaks plus explicit credential-field handling, basic payment-card/IBAN/formatted-US-SSN checks, and optional email masking. It applies the policy to JSON, Markdown, generated PDFs, filenames, and document metadata, rescans earlier records against credentials found later, withholds binary/data-URL content, and audits final output. It is not a guarantee that every sensitive fact is detected. Generic bank-account detection, NLP PII detection, and image/audio/imported-PDF sanitization are not included.

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

If you have an older native Pieces client cache, `--sdk-cache /path/to/pieces_client_sqlite.db` can recover historical summary links to current records. Repeat it for multiple explicitly selected files. It reads caches without modifying their records, imports no cached prose, labels recovered links with their provenance, and keeps the archive partial because cached links may be stale. Current OS fields take precedence; missing/excluded targets are not linked. This is optional and cannot promise complete recovery.

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

Embedded Pieces links point to included local documents or become plain labels. Empty, missing, unsupported, and original filesystem links are flattened; code examples stay code. Permitted external HTTP/HTTPS/email links remain external and are not checked online. Generated local links are checked before completion.

`--format markdown` is the default. `pdf` and `both` produce PDFs with retained Markdown companions; PDF document links open the mirrored PDFs at their first page. Markdown retains the complete graph, including JSON links. Non-PDF destinations remain plain text in PDF. Filenames outside the legacy MacRoman encoding also remain unlinked in PDF, with a warning and partial status; their Markdown links remain intact. Keep the entire folder together. Viewer access restrictions still apply: in Preview on macOS 15.7.3, a moved summary/index round trip worked after both documents were opened directly, but first-use links to unopened files could produce permission alerts. No viewer security settings or filesystem permissions are relaxed by the exporter. PDFs use an embedded font, text/list/code layout, and a readable table fallback. Unsupported glyphs make the result partial while preserving full text in Markdown. Use `--pdf-font /path/to/font.ttf` (maximum 32 MiB) for broader font coverage; there is no automatic font downloading or multi-font fallback.

Summary descriptions/tags/sources/persons/websites are in the files and portable `.metadata.json` sidecars. `--metadata auto` attempts native attributes and records readback results. macOS uses Finder metadata xattrs; Linux uses XDG xattrs where supported; Windows requires an installed writable property handler. Markdown often lacks such a Windows handler. Sidecars remain available on every platform. `--metadata off` disables native attributes. File-manager display/indexing and attribute retention when copying vary by OS/filesystem.

Use `--environment production` or `--environment staging` to choose an OS build. Auto accepts a unique existing instance; if none is running, auto launch selects production. Staging shares the production URL scheme: supply an explicit `--os-path` executable/bundle (macOS defaults to `/Applications/Pieces OS (Local Staging).app`). The selected environment is verified after launch. An explicit `--base-url` connects only to that instance and does not launch it if unreachable.

The tool only reads loopback Pieces OS APIs. It does not modify capture settings or create/delete/regenerate user content. Export-time privacy processing makes no external network requests. Only the explicit `lists fetch` command accesses the public category provider.

Current OS versions may also omit summary annotation/person/pipeline links. Direct persona queries recover persona histories; complete summary body attachment and person/pipeline grouping remain pending API coverage work. Projection warnings appear in the manifest.

Current limits: no atomic snapshot, historical/deleted-data recovery, complete association-object export, supplementary settings/analysis views, resume, separate binary extraction, or guaranteed semantic privacy. Capture/editing during a run can change records; the manifest reports identity-set drift but cannot detect every in-place edit. Large exports keep IDs/graph metadata and configured domain lists in memory. Review coverage before treating the result as a complete migration.

This utility is intentionally unsigned and is not notarized. Its application source remains private; use of the downloaded binary is covered by `LICENSE.txt`. Keep `THIRD_PARTY_NOTICES.txt` with the executable. Platform runtime testing and complete migration acceptance are still required before a production-ready claim.
