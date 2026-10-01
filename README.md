# Pieces Export

A Go CLI that exports local Pieces OS records as chronological Markdown, PDFs, JSON, and a linked graph. Source stays private; users receive a native executable with its secret detector and timezone database included. They do not need Go, Python, Flutter, or a separately installed scanner.

This development build has a real current-junction archive that passed independent record/body/graph/link checks: 11,559 retained summaries and 1,381 selected people, finalized in 51m23s with partial status. Performance is still too slow; missing references, withheld summaries and retained summaries without annotation bodies remain unresolved. Actual completed-source replay passed: 38m06s, 172,527 matching document/evidence files and zero new OS requests. A retained-input diagnostic traced 187 withheld summaries and 403 withheld annotations to dangling person links; the forward fix renders those unavailable destinations as plain labels. See [measured results](PERFORMANCE_INVESTIGATION.md). Native runtime coverage (including Windows), file-manager/PDF-viewer integration, and source coverage remain release gates. Signing and notarization are not required for this utility. Follow the detailed [execution checklist](TODO.md), [export specification](EXPORT_SPEC.md), and [folder layout and naming contract](EXPORT_LAYOUT.md). Archive consistency does not mean every byte ever stored in Pieces has been recovered.

## Run from this repository

Use Go 1.27.1 or newer (Go's toolchain support can download the required compiler):

```sh
go test ./...
go build -trimpath -buildvcs=false -o pieces-export ./cmd/pieces-export
./pieces-export doctor
./pieces-export export --output ./exports/my-export
```

Pieces OS must be installed. The CLI discovers loopback ports 39300–39333, verifies readiness/version/environment, and can launch the installed OS if absent. Use `--launch-os=false` for read-only discovery or `--base-url http://127.0.0.1:39300` for a specific instance. It refuses non-loopback connections, proxies, and redirects. `doctor` checks health/version; collection authorization and compatibility are checked during export.

Before export, the CLI scans counts and bounded samples, shows a rough duration range, offers Markdown/PDF output, and asks `Export now? [Y/n]`. EOF cancels; `--yes` explicitly approves unattended runs. After approval it gracefully closes Pieces Desktop if running; Pieces OS stays active. Use `--close-desktop=false` to keep Desktop open.

The default `filtered` mode removes detected secrets and basic financial identifiers. Open the resulting `index.md` and inspect `manifest.json` for exclusions, failed reads, inventory changes, and scope limitations.

```sh
./pieces-export policy init --output export-policy.json
./pieces-export export --policy export-policy.json --output ./exports/filtered

# Explicit preservation mode includes original sensitive content in raw/ JSON.
./pieces-export export --mode preserve --output ./exports/private-archive
```

Opt-in recovery in `0.15.0-dev` saves the completed-source boundary. After that checkpoint, local privacy/graph/document processing can restart without OS access:

```sh
./pieces-export export --output ./exports/my-export --work ./exports/private-work --recovery-keys ./exports/private-keys
./pieces-export resume --work ./exports/private-work --recovery-keys ./exports/private-keys --inspect
./pieces-export resume --work ./exports/private-work --recovery-keys ./exports/private-keys --output ./exports/recovered
```

Parents must exist; the workspace must be new. Workspace, keys and output must be separate and non-nested. Both private recovery directories are retained. This does not resume interrupted source fetching or reuse `.partial` folders, and it adds checkpoint I/O. See [the recovery contract and limitations](RECOVERY_DESIGN.md#cli-and-failure-ux).

`--output` is relative to your terminal working directory, or may be absolute. Without it, the CLI uses `pieces-export-<timestamp>` in that working directory. It prints the absolute destination before confirmation. Output must be a new directory. Work is staged in a sibling `.partial` directory and renamed after rendering and validation. An ordinary run has no recovery checkpoint. Opt-in `--work` supports offline replay only after source collection completes; every retry/replay needs a new output path. Exit codes: `0` = completed for the implemented scope, `1` = fatal error, `2` = archive produced with missing records or other completeness issues.

New exports write related-summary suggestions once in `.relationships_graph.md` siblings, linked from summary footers. Use `--relationships both` only when you want the lists duplicated inside summaries too. The ranked suggestions and full graph remain available in either layout.

## Summaries and personas by default

New builds default to `--scope summaries --people profiles` for summaries and persona/profile documents:

```sh
./pieces-export export --output ./exports/summaries
./pieces-export export --dry-run --launch-os=false
# Explicitly request all supported collections, including activity history.
./pieces-export export --scope all --output ./exports/all-data
```

Summary narrative text is stored in **annotations**, not in event bodies. Summaries scope inventories summaries, persons, and pipelines, then traverses current association endpoints for bodies, profiles and related labels. When all summary/person annotation reads reconcile, only referenced annotations are fetched; older servers fall back to the full annotation inventory and direct persona-history reads. Missing snapshots still have their indexed junctions checked, with missing records reported honestly. The explicit summary hierarchy is also retained. Current relationships take precedence over historical SDK caches. See [current traversal and verification status](JUNCTION_API.md). Local `0.17.4-dev` candidate packages include this traversal, bounded parallel audits, grouped association storage, missing-person link fallback and bounded navigation pages. A real partial archive is verified; full migration coverage, acceptable speed and publication remain pending.

Events are useful for raw activity history, event-derived source/website/person graph connections, connectivity evidence, and privacy propagation from excluded activity to dependent summaries. They are optional for a document-focused export. This scope skips event bodies, source-window history, hints, signals, conversations, and other unselected collections. It can use a bounded person/event-association count query; it does not download the underlying events. Preflight and benchmark also respect the selected scope.

Secret detection and domain rules still scan the exported content. If a website origin exists only in skipped activity, its domain cannot be used to exclude that summary. `withhold_unproven_generated_content: true` remains the conservative policy option when domain filtering is configured. Related-summary suggestions include only available evidence; missing destinations stay unlinked. `coverage.md` and the manifest distinguish intentional scope omissions from read failures. A successful summaries export is not an all-data migration.

Advanced `--materials TYPE,TYPE` still selects full inventories of those types; it cannot be combined with `--scope`. `--materials WORKSTREAM_SUMMARIES` alone does **not** select annotation bodies or profile context.

## Rebuild without fetching from OS again

```sh
./pieces-export rebuild --source ./exports/finished --output ./exports/rebuilt --format both
./pieces-export rebuild --source ./exports/finished --output ./exports/profiles --people profiles
```

This reads only a finalized archive and writes a new folder. It preserves the source policy, exclusions, graph provenance, and coverage gaps, and can optionally use historical `--sdk-cache` links to records already exported. It never connects to or launches OS. Legacy archives remain partial where reconstruction evidence is unavailable. Use the original `--policy` when one was configured. See [offline rebuilding](OFFLINE_REBUILD.md) for integrity checks, conservative cache handling, and limitations.

## Consolidated signals

Starting with `0.9.0-dev`, the CLI adds `--signals-digest split|single|off`. Split is the default, with `--signals-per-part 1000` and `--signals-max-part-mib 8`; canonical signal files remain available in every mode. The digest preserves approved attached descriptions, reports missing projections, and links to included records. It makes no extra OS requests. Oversized documents fail without truncation. Rebuild can change digest presentation offline; independent PDF limits still apply. See [layout and limits](EXPORT_LAYOUT.md#signals-digest).

## Measure performance and choose people

```sh
./pieces-export export --dry-run --launch-os=false --people profiles --people-report
./pieces-export benchmark --launch-os=false --benchmark-reads 40 --benchmark-duration 30s
./pieces-export export --people profiles --output "$HOME/Documents/Pieces-Export"
```

Dry run scans and calibrates without creating export files or closing Desktop. The optional people report reads persona/profile evidence, printing aggregates only. Profiles mode skips event-count queries; all/connected modes can include them. The bounded calibration reuses samples and can benefit from caches; it does not measure full-export throughput.

Adaptive pacing is on by default: one outstanding data request, batches starting at five and growing toward at most 50, latency/response-size backoff, short pauses for OS headroom, and a stop on persistent overload or transport failure. Progress shows phase counts, rate, phase ETA, last HTTP p95, retries, backoffs, cumulative file-write attempts, and flush time. New exports/rebuilds save aggregate phase/scan/read/write/flush measurements in `performance.json` and the manifest; failed runs attempt to leave diagnostics in staging. These reports are not resume checkpoints. See [measurement boundaries](EXPORT_SPEC.md#local-performance-diagnostics-0123-dev). `--performance conservative --batch-size 5` uses smaller reads and at least 100 ms pauses. OS uses a database write lock even for batch reads, so adding concurrent read workers is deliberately avoided. This cannot guarantee OS will never stall; see the control loop in [EXPORT_SPEC.md](EXPORT_SPEC.md#output-destination-adaptive-reads-and-terminal-progress).

`--file-workers 2` is the default for local record Markdown writes and final text-file audits in current candidates; use `1` for serial operation or up to `4` for more parallelism. Each file is still synced, large document writes and PDF audits run alone, and OS reads stay serialized. Existing `0.17.1-dev` packages still audit serially. [Bounds and failure behavior](EXPORT_SPEC.md#bounded-local-markdown-writes-0130-dev) apply to export and offline rebuild.

Final privacy auditing uses bounded read buffers and rejects truncated JSON. A later audit may reuse a successful content check only after rereading and matching the entire file's checksum under unchanged scanner inputs. Paths, new/changed content, PDFs and closing reports retain their checks. The private cache is bounded and discarded across processes; it is not a recovery checkpoint. See [the exact contract](EXPORT_SPEC.md#reusing-unchanged-final-audit-results-0162-dev) and [measured limits](PERFORMANCE_INVESTIGATION.md#reusing-unchanged-audit-results).

`--people profiles` is the default for summaries scope. Explicit `--scope all` and custom `--materials` default to `--people all`; an explicit `--people` always overrides the scope default. `profiles` selects persona/profile-bearing people and stored account identities; `connected` additionally keeps summary-linked/high-connectivity people. Missing evidence is retained conservatively. Selection does not remove summaries/events or merge names/emails. The finalized 2026-09-30 focused baseline retained **1,381 of 4,413 persons**, omitting **3,032 (68.7%)**; every retained person had profile history. That older export remains partial because its core relationship recovery relies on historical evidence. The current-junction archive retained **1,381 of 4,343 persons**, omitting **2,962 (68.2%)**, with current profile evidence for all retained people. Twelve earlier same-name candidate groups were identified for review; no automatic identity merges occurred.

## What this version implements

- Inventories 32 generic material types plus connector/observer snapshots. Unreadable types are reported. Reads records in batches of up to 50, with individual-read fallback where an endpoint exists.
- Splits large inventories into adaptive creation-time windows, deduplicates boundary IDs, then audits against an unfiltered inventory to capture undated records. A final ID comparison reports additions/deletions during the run. There is no cursor or offset to advance.
- Exports summaries and their annotation bodies, events, persons/profiles, retained persona histories, tags, conversations/transcripts, individual signals, and other accessible records. Resolves typed references and rewrites supported Pieces Markdown links to local paths, with backlinks and a graph file.
- Sorts records by creation timestamp into `timeline/records.jsonl` and daily Markdown indexes; undated records have a separate index. Use `--timezone America/New_York` or another IANA zone to select daily boundaries.
- Embeds Gitleaks 8.30.1 and supplements it with credential-field removal, a second pass for exact copies of known credentials, Luhn-valid payment card numbers, checksum-valid IBANs, formatted US SSNs, and optional email redaction. Unsupported binary/data-URL representations are withheld in filtered mode. The export process does not send content to a remote scanner or validate credentials online.
- Applies normalized domain allow/deny rules and optional local category lists. Explicit deny rules win; explicit allows can override a category-list match. A denied URL anywhere in a record excludes the whole record. Known dependent summaries, signals, and their attached descriptions are withheld, and strict derived-content mode can withhold all generated content when source filtering is active. A final local text scan checks generated output before publication to the requested folder.

Website categories are **off by default**. The CLI can download public UT1 category lists as a separate setup command; the subsequent export uses local files only:

```sh
./pieces-export lists fetch --output lists --categories adult,bank
```

Add these entries to the policy JSON, with paths relative to the policy file:

```json
"domain_lists": [
  {"path": "lists/adult.domains", "category": "adult"},
  {"path": "lists/bank.domains", "category": "bank"}
]
```

Every configured list is a deny list; `category` is its report label. The importer uses domain entries, not URL/path-specific entries. It normalizes public-feed hostnames and records invalid-entry omissions and original/imported checksums in `source.json`; manually supplied lists are validated strictly. Domain lists are imperfect and do not identify adult prose, images, or banking information without a matching domain. See [the actual JSON policy example](policy.example.json) and [download-user instructions](DOWNLOAD_README.md) for strict mode and supported flags.

## Documents, ranking, and metadata

```sh
./pieces-export scan --environment staging --launch-os=false
./pieces-export export --environment production --format both --output ./exports/documents
./pieces-export export --environment staging --os-path '/Applications/Pieces OS (Local Staging).app' --output ./exports/staging
./pieces-export export --related-order relevance --related-limit 25 --related-since 2026-01-01 --output ./exports/ranked
```

Open [EXPORT_LAYOUT.md](EXPORT_LAYOUT.md) for the complete archive-format-5/6 tree. `workstream_summaries/` contains `timeline/`, `personas/users/`, `personas/related_persons/`, and `single_click_summaries/daily_standups/` plus the other descriptor-based folders. Person folders have `profile.md`, `profile_summaries/`, and `related_workstream_summaries/index.md`. Exact user-to-person endpoint mappings establish user folders. Other explicit hierarchy types have `hierarchical_summaries/<type>/` folders. Shared documents keep one canonical file with links from each relevant index.

Summary filenames are `000000.safe_title.YYYY-MM-DD.uuid.md`, starting with the newest creation timestamp globally across summary folders. Gaps within an individual folder are expected. Six-digit minimum padding keeps ascending filename order chronological from newest to oldest. Titles are sanitized before naming; `--naming opaque` hides titles in filenames. Relationship siblings use the same basename plus `.relationships_graph.md`; `--relationships inline|sidecar|both` controls placement.

Related lists have Tags, Source, Person, and Website sections. Default `--related-order relevance` counts distinct shared dimensions (1–4), then uses recency and ID to break ties. `recent` sorts by creation time. `--related-limit` defaults to 50 per section; `--related-since` applies only to suggestions. Complete group indexes retain older/overflow matches. All selected records still export regardless of suggestion cutoff.

Supported Pieces narrative links become local links only when the target is included. Missing/private/unsupported targets and original local/empty links retain their labels without navigation. Code samples remain code. Permitted external web/mail links stay external; their online availability is not checked. Every generated local Markdown/PDF target is validated, including after a moved-folder fixture test.

`--format markdown` is the default. `pdf` and `both` currently produce PDFs **and** retain canonical Markdown companions. PDF document links open the corresponding PDFs. Markdown retains the complete navigation, including JSON records. PDF labels remain plain text for non-PDF destinations or filenames unsupported by Preview's legacy encoding; the latter produce a partial result. Viewer permissions can still restrict local-file navigation; see [native viewer findings](NATIVE_GUI_ACCEPTANCE.md). PDF rendering is embedded, with no browser, remote image fetching, or external converter. Unsupported glyphs produce a partial result; `--pdf-font /path/to/font.ttf` can select a font with broader coverage. Multi-font fallback and complex-script shaping remain future work.

PDF rendering has configurable per-document input/page/output limits (16 MiB / 1,000 pages / 64 MiB by default) and cancellation checks within long blocks. Exceeding a limit fails with the partial folder retained, never silent truncation. Finish Markdown first and use offline `rebuild --format both` for large histories. Automatic splitting and strict memory/CPU isolation remain pending; see [PDF limits](EXPORT_SPEC.md#pdf-resource-limits-and-cancellation).

Descriptions, tags, normalized source tags, persons, and website hosts appear in documents and portable `.metadata.json` sidecars. `--metadata auto` also attempts macOS Finder xattrs, Linux XDG xattrs, or Windows writable Shell properties and records readback outcomes. Actual file-manager visibility depends on the platform, filesystem, indexing, and property handlers. `--metadata off` keeps portable metadata only. Sidecars survive ZIP/cross-platform copying where native attributes may be lost.

## Historical SDK-cache recovery

`--sdk-cache /path/to/pieces_client_sqlite.db` optionally recovers historical summary, annotation, person, and signal relationships to records fetched from the current OS. Repeat the flag for multiple caches. Current fields take precedence, cached text is never imported, conflicting/tombstoned links are handled conservatively, and the archive remains partial. [SDK_CACHE_RECOVERY.md](SDK_CACHE_RECOVERY.md) documents selection, bounds, privacy, provenance, and the 87.9% candidate body-link coverage measured on this machine. Candidate coverage is not a completed live recovery.

## Build binary-only downloads

```sh
go run ./cmd/release --version 0.17.0-dev --output dist/0.17.0-dev
```

This produces six ZIPs and `SHA256SUMS.txt` under ignored `dist/0.17.0-dev/`: macOS, Linux, and Windows, each for AMD64 and ARM64. Each ZIP contains only the executable, download instructions, proprietary license, and third-party notices. No application source is packaged or published. Builds use `CGO_ENABLED=0`, trimmed build paths, disabled VCS stamping, and stripped debug symbols. Notices are gathered from dependency modules compiled into the requested platforms and from the Go runtime; packaging stops if a module has no root license/notice file.

Go is a better fit here than Python because it supports native cross-compilation through `GOOS`/`GOARCH`, and this implementation needs no C runtime integration. Python packaging is possible, but PyInstaller bundles a Python interpreter and builds distributions specific to the build OS. Neither approach prevents reverse engineering. See [Go build documentation](https://pkg.go.dev/cmd/go#hdr-Compile_packages_and_dependencies), [Go platform configuration](https://go.dev/doc/install/source), and [PyInstaller's operating model](https://pyinstaller.org/en/stable/operating-mode.html).

The packager does not sign, notarize, upload, or publish anything. Distribution is intentionally unsigned and closed source under [LICENSE.txt](LICENSE.txt). See [DISTRIBUTION.md](DISTRIBUTION.md) for the Bash/PowerShell installers, GCP object layout, Gist publication, cleanup UX, and native CI matrix. Production distribution still needs complete migration acceptance, native platform evidence, and an actual download destination.

## Known limits

Live hierarchy verification recovered **1,708 direct edges**, using two global identifier reads plus 20 parent reads, with 1,004 distinct children and matching returned inventories. OS remained healthy. This avoids an extra snapshot request for every summary. It does not prove internal database completeness because server helpers may suppress errors.

An evenly spaced sample of 250 summary snapshots omitted annotation/person/pipeline fields. Current ObjectBox stores those relationships in separate junctions; refreshed source and live probes identify their read APIs. The new summary/profile traversal is integrated and under verification. **A finalized live export using it, complete body/membership reconciliation, and acceptable performance remain release blockers.** Missing fields alone no longer establish that these relationships are inaccessible. Full-history export has not been verified.


- Person-to-summary and pipeline membership indexes depend on available typed associations; projected snapshots can omit them. New exports disclose absent/malformed core projections in `coverage.md` and the manifest, and return partial status (exit 2) even if material counts reconcile. The exporter queries persona/profile histories directly but cannot promise every summary describing a person. The consolidated signals digest reports missing descriptions explicitly; complete live description recovery remains pending.
- Association metadata is exported for observed typed pairs and paginated selected event/person history by default (`--associations linked`); `--associations off` skips its additional reads. Missing relationship projections can hide pairs, so this is not complete association enumeration. Supplementary settings/analysis views, fingerprint audio downloads, and separate binary attachment extraction are not implemented. Preservation JSON retains encoded representations returned by the server; filtered mode withholds unsupported binary forms. Inaccessible internal records are reported.
- The current timeline uses **record creation time**, not summary activity ranges or scheduled calendar occurrence times. Those source fields remain in record JSON for a later activity timeline. The CLI exports full history; it does not yet offer a user-selected date range.
- There is no atomic server snapshot. The final inventory catches ID changes, not in-place updates to existing records. Deleted history and unavailable/cloud-only data cannot be recovered. Failed reads result in a partial manifest and nonzero exit status.
- Privacy filtering is conservative but cannot guarantee anonymity or recognize every secret. It does not include Presidio/NLP, arbitrary bank-account-number detection, semantic adult-content classification, or image/audio inspection. Strict derived-content filtering intentionally removes all generated content when source filtering is active because complete provenance cannot be proved.
- Records are staged on disk, but IDs, graph metadata, and category lists remain in memory. Large unfiltered inventories must fit the configured response limit (`--max-response-mib`, default 64). Disk-backed graph storage, resumable exports, and large-history benchmarks remain future work.

## Research and design

Latest read-only validation on 2026-09-29: the inventory grew to 1,902,907 records, including 11,730 summaries and 4,291 persons. Final calibration read 750 record samples in 40 batches in 454 ms (repeated, possibly cached samples). The complete people preview took about 35 seconds, backed off for two ~0.9-second requests, and finished with OS responsive, recent p95 ~2 ms, and zero retries. It did not create an archive or close Desktop.

Earlier validation on 2026-09-29: fixture tests cover inventory reconciliation, filtering, graph ranking/cutoffs, missing-link flattening, readable names, PDF semantic audits, and moved-folder links. The live staging scan found 1,902,150 retained records, including 11,729 summaries and 4,265 persons; its uncalibrated full-inventory estimate was roughly 1–8 hours for Markdown. A bounded export fetched all 12 selected assets and 12 formats; its partial result correctly reported out-of-selection references and unsupported PDF glyphs. It did not establish full-history completeness. Synthetic macOS metadata readback, Finder tags/name sorting, and Spotlight lookup passed. Finder's editable Comments box remained empty; PDF viewer access restrictions are documented separately. Detailed commands and final release-check results are in [TODO.md](TODO.md).

`GOTOOLCHAIN=go1.27.1 go run golang.org/x/vuln/cmd/govulncheck@v1.8.0 ./...` reports zero reachable vulnerabilities on this macOS host. It still reports advisories in imported packages/dependency modules that the analyzer does not find called; this is not a claim of a vulnerability-free dependency graph. Re-run this check when preparing a release.

[Endpoint and traversal guide](EXPORT_GUIDE.md): source-backed endpoint inventory, pagination semantics, graph model, SDK loading patterns, personas/profiles, and remaining coverage work.

[Privacy filtering research](PRIVACY_FILTERING.md): alternatives, category sources, provenance issues, and a broader future policy design. Its proposed YAML is not the CLI's JSON configuration schema.
