# Obsidian export

The `0.20.0-dev` working build creates a **compact browsing vault alongside a complete archive**. Open the output's `vault/` subfolder in Obsidian. The parent is the full export, including canonical JSON, complete relationships and technical documents. The signed/notarized `0.18.0-rc2` email packages do not include this feature yet.

## Why the layout changed

The original vault exposed 86,051 Markdown files and 1,805,438 document links to Obsidian. Related-summary sidecars alone occupied 436 MB. Raw supporting records repeated huge incoming-link lists; the largest navigation page had 69,375 neighbors. Graph filters only change what is drawn, not what Obsidian indexes.

Obsidian's first renderer crashed after about 66 minutes of indexing. A restart with the existing cache crashed after 15.76 seconds. Both were `EXC_BREAKPOINT / SIGTRAP`; the reports do not prove the exact cause or an out-of-memory condition. Adding more connection pages in presentation version 2 improved routes but increased indexing volume, so that candidate was not adopted.

Presentation version 3 retains readable memories and actual entity connections inside `vault/`. It leaves exhaustive technical inverse lists, relationship sidecars, canonical JSON and technical records in the parent archive. Connection notes provide bounded reverse navigation to readable memories, and Obsidian computes live backlinks from ordinary note links. No data is deleted from the archive to achieve the reduction.

## Create and open a vault

For an existing finalized export, use the offline command:

```sh
./pieces-export obsidian --source ./finished-export --output ./Pieces-Obsidian --yes
```

For a new export from Pieces OS:

```sh
./pieces-export export --format obsidian --output ./Pieces-Obsidian
```

For a full offline reconstruction with ordinary integrity/privacy checks:

```sh
./pieces-export rebuild --source ./finished-export --output ./Pieces-Obsidian --format obsidian
```

Use the original `--policy` when rebuild requires it. Export/rebuild use the existing scope, people selection, privacy policy, timezone, progress and confirmation. Add `--yes` for headless operation. Interrupted source fetching supports `--work`, `--recovery-keys` and `resume`; the saved format is retained. Offline conversion itself has no resume checkpoint: use a fresh destination after an interruption, retaining the source and diagnostic `.partial` folder.

1. In Obsidian choose **Open folder as vault** and select **`Pieces-Obsidian/vault`**.
2. Open **Start Here.md**. It leads to Timeline, Personas, Single-click summaries, People, Topics, Sources, Websites, Pipelines and Signals when present.
3. Notes open in Reading view by default. Toggle editing with `Cmd+E` on macOS or `Ctrl+E` on Windows/Linux. Properties remain searchable but hidden above the document; readable line length is enabled.
4. Open a summary. Follow an embedded person/topic link, or a connection section below its body. That connection note has a **Connected memories** section with clickable links and, when needed, **Browse all**. The backlink count in the status bar also opens incoming references. Select another memory to continue browsing.
5. **Nearby summaries** offers up to four direct alternatives. Persona reports are links to separate retained versions, not repeated text inside each summary.
6. Open **Local graph** from the command palette. Start at depth 1; large sources and the user's own person may connect to thousands of notes. Hide sidebars or enlarge the graph pane when necessary.
7. Search an existing topic using `tag:topic/<normalized-label>` or use the Tags panel. Quick switcher recognizes readable names and original-title aliases.

Open only the child `vault/` folder. Opening the parent makes Obsidian index the full archive again and creates overlapping vaults. Keep the whole bundle for archival use; the child vault also works independently because its generated local links stay inside it. Do not rename files outside Obsidian after importing them. Keep an untouched source export for rebuilding after you start editing notes.

## Folder and naming contract

```text
Pieces-Obsidian/
  manifest.json
  index.md                         # full archive entry point
  data/                            # or raw/ for preserve mode
  relationships.jsonl
  link-map.json
  timeline/
  workstream_summaries/             # original numbered chronological files
  markdown/                        # full technical/supporting documents
  signals/                         # complete digest, when selected
  OBSIDIAN_START_HERE.md            # points the user to the child vault
  vault/                           # SELECT THIS FOLDER IN OBSIDIAN
    .obsidian/
    Start Here.md
    Archive.md                     # coverage as reference text
    index.md
    record-map.json                # retained identity -> browsing path
    workstream_summaries/
      timeline/
      personas/
        users/<person>/profile_summaries/
        related_persons/<person>/profile_summaries/
      single_click_summaries/<pipeline>/
    connections/
      people/
      topics/
      sources/
      websites/
      pipelines/
    signals/                       # individual signals, when included
      descriptions/                # retained SIGNAL_DESCRIPTION notes
```

Shared/ownerless profile reports retain their existing canonical annotation folder inside the vault. The folder structure follows the archive's resolved ownership and pipeline classification; no missing association is invented.

Generated browsing titles remove Markdown heading/emphasis syntax, which otherwise displayed literally in profile titles. If a report already begins with a heading, its body supplies that heading once; the exporter does not prepend a duplicate. Full report text remains intact.

The parent retains all original paths and newest-first numbered summary filenames. Inside the vault, filenames use readable sanitized titles with spaces, bounded to 80 UTF-8 bytes before disambiguation. Titles that collide within a folder (including case/punctuation variants) receive a local date and a 12-character identity digest. Reserved names and secondary collisions are guarded. Unique titles have no unnecessary UUID suffix. This matters because Obsidian's graph and backlink headings use filenames, not aliases. Opaque naming retains opaque filenames. Folder indexes list summaries/profile histories newest first and display localized dates. Summary previews paginate at 50 entries or 64 KiB; other indexes retain the 250-entry/64-KiB limit.

### Timeline and summary previews

Each summary in a folder index has a title link, its recorded activity period and a short description. This applies to Timeline, single-click pipeline summaries and persona summary folders. Dates use the archive timezone, including the correct daylight-saving offset; offline conversion keeps the source timezone. A typical entry reads:

```text
Summary title → opens the full note

Activity: September 30, 2026 · 9:00 AM – 10:00 AM EDT (UTC-04:00)

A short description from the retained summary metadata.
```

Activity dates come from retained summary → range relationships and the range's absolute `from`/`to` values. Creation time is not treated as coverage. Missing or invalid bounds produce `Created: … · Activity range unavailable.` Multiple ranges show their outer **Activity span**, with a range count; that span does not imply continuous activity between ranges. Crossing midnight or an offset change shows both full dates/offsets. Historical relationship evidence remains labeled.

Descriptions use approved summary metadata (falling back to the canonical description when no sidecar exists), flatten Markdown into plain text and show at most 360 characters plus an ellipsis. Embedded description links do not create extra graph edges. Missing descriptions are labeled explicitly. The full note and canonical text remain unchanged; no generated summary or copied persona report supplies the preview.

Page links and page headings show **Summary dates**, the range of creation dates on that page. This matches the newest-created-first ordering, while each entry separately shows the actual activity coverage. Pages contain at most 50 previews and retain the byte bound to keep scrolling and indexing manageable. Exact machine-readable source timestamps remain in the canonical archive.

All existing people/tag/source/website/pipeline identities get named connection notes. A person note links its report history; histories with more than eight versions show the latest three and a paginated complete list. Every retained profile report appears once in the browsing vault. Source records and full relationship evidence remain in the archive parent.

### Clickable connected memories

Every source, topic, person, website and pipeline note has **Connected memories** in its document body. Up to eight linked memories appear directly. For larger lists, the note shows the five newest and **Browse all N connected memories**, opening `<connection-name>.memories/index.md`. The complete list paginates at 250 entries or 64 KiB; helper pages are tagged `pieces/navigation` and excluded from the default semantic graph. Entries have readable titles, a memory type and localized creation time. They do not copy summary/profile bodies or descriptions, keeping large hubs manageable.

Membership comes from actual rendered links from retained summaries, profile reports, signals and signal descriptions, including links embedded inside their prose. Multiple references from one memory count once. Code examples, frontmatter, external URLs, missing targets and navigation pages do not count. Lists use newest-created-first order, put undated memories last and preserve distinct record identities. Zero-reference records state that no retained memory notes link to them; a person's separately linked profile history can still be useful.

The **Backlinks** sidebar is a live Obsidian view, whereas these document links are generated at export time. Its count can include folder indexes, newly added browsing pages and links you add after export, so it need not match the connected-memory count. No plugin or expanded sidebar is required to follow the generated links. Existing user edits after export are not synchronized into these generated lists automatically.

## Links, tags and graph meaning

Standard relative Markdown links create automatic Obsidian backlinks. Bounded connected-memory indexes make the same retained memories reachable from the document body. There are no community plugins, ambiguous basename-only wiki links or hidden external-file links. Existing Pieces URIs are rewritten to the retained browsing destination. Missing, filtered or technical targets without a browsing note become plain labels. Literal `[[...]]` and `![[...]]` in source prose are escaped to prevent accidental phantom links or transclusions; code examples are preserved. Ordinary web links remain external and their remote availability is not checked.

Primary notes link directly to their retained people, tags, sources, websites and pipelines. Summary hierarchy links remain. Nearby summaries are suggestions drawn from the archive's already-bounded related-summary lists: at most **four**, ordered by shared-dimension count then recency/identity, or newest-first when the archive uses `--related-order recent`. No new similarity inference, identity merging or global deduplication runs. A graph edge means a document link, which may be a source association, a hierarchy edge, a navigation link or a clearly labeled suggestion.

The default global graph filter is:

```text
tag:pieces/connection OR tag:pieces/summary OR tag:pieces/profile OR tag:pieces/signal
```

For narrower views, use `tag:entity/person OR tag:pieces/profile` or `tag:entity/tag OR tag:pieces/summary`. The full global graph remains large; a local graph and nearby-summary links are the primary browsing routes. Broad hubs retain their true connectivity. Independent people/profile groups and source records are not joined by invented edges.

| Property/tag | Meaning |
| --- | --- |
| `topic/<label>` | Approved existing Pieces tag labels; never LLM-generated topics. |
| `source/<label>`, `person/<label>`, `website/<label>` | Existing document metadata facets; these are search aids, not merged identities. |
| `pieces/summary`, `pieces/profile`, `pieces/signal`, `pieces/connection`, `pieces/navigation` | Exporter navigation categories. |
| `entity/person`, `entity/tag`, `entity/source`, `entity/website`, `entity/pipeline` | Type of the connection record. |
| `pieces_tags` | Original approved labels where document metadata supplies them. |
| `aliases` | Retained title as plain text, with Markdown emphasis/heading syntax removed for safe display. |
| `pieces_type`, `pieces_id` | The source identity; group/index pages have no invented identity. |
| `created`, `updated` | Exact available source times using the archive timezone's offset; prose dates are human readable. |

Tags normalize whitespace/punctuation and case; long labels receive a digest suffix. Labels that normalize alike can share a search tag while retaining distinct source records. Properties use JSON-compatible YAML escaping. Not every profile/signal has every metadata facet. The module does not infer missing tags or timestamps.

## Signals without exporting every event

Offline conversion only includes signals already in its source. The current real summary archive has **zero signals**; live signal completeness has not been established by converting it. Synthetic tests cover retained descriptions, multiple versions, unknown projections, denied origins, secret redaction and valid links.

To request summaries and signals without event bodies:

```sh
./pieces-export export --format obsidian \
  --materials WORKSTREAM_SUMMARIES,ANNOTATIONS,PERSONS,PIPELINES,SIGNALS,TAGS,WEBSITES,WORKSTREAM_PATTERN_ENGINE_SOURCES,APPLICATIONS,RANGES,ANCHORS \
  --output ./Pieces-Obsidian-With-Signals
```

Do not combine `--scope` with a custom `--materials` selection. This inventories the named supporting collections and can read more than default summaries scope. Default summaries scope omits signals; `--scope all` includes events and other collections and is substantially larger. Signal descriptions are separate linked `SIGNAL_DESCRIPTION` notes. The parent archive retains the complete signal digest and attachment provenance. Unavailable projections remain unknown, and skipping event bodies limits event-derived origin filtering. See [signal coverage](EXPORT_LAYOUT.md#signals-digest).

## Preservation and safety boundaries

Offline conversion makes no Pieces OS calls and never edits its source. It copies the full visible archive, preserves canonical hashes and coverage, creates the compact view, validates every generated local Markdown target and atomically publishes the destination. It trusts existing privacy decisions and does not claim to re-scan a manually modified source; use `rebuild` for a new audit. Preserve mode remains preserve mode. Native attributes are not copied; portable sidecars report `native_status: off`.

Conversion supports finalized archive formats 4–6 with valid timeline/timezone evidence. Hidden settings are not copied, symlinks are rejected, destinations must be new, and a reserved pre-existing `vault/` folder is rejected unless it belongs to this module. Re-converting a compact archive creates a fresh browsing view in a new output and keeps the canonical parent. It is not an edit-merge tool. Individual reads are bounded; source copies stream through a fixed buffer. Cancellation/failure leaves the staged output, never replacing the source.

A partial source stays partial. Exit **2** means a completed output with documented gaps; **1** means failure; **0** means complete for the implemented scope (or declined/EOF with no output). Progress is on stderr. Sync and Publish are disabled in the generated vault, and no community plugins are installed.

## Measured acceptance — October 3, 2026

The first compact candidate completed in **260.80 seconds** at **228 MiB peak RSS**, including the complete archival copy. This was not a controlled benchmark against the earlier conversion. Obsidian 1.13.7 completed its first index within the roughly four-minute observation interval. Start Here, timeline pages, a summary, a topic, backlinks and a local graph opened. The graph review motivated the shorter browsing filenames described above; the final file checks and remaining GUI boundary are recorded below.

| Indexed data | Original full vault | Compact candidate |
| --- | ---: | ---: |
| Markdown notes | 86,051 | 30,455 |
| Markdown bytes | roughly 1 GB | 115.3 MB |
| Directed document links | 1,805,438 | 187,294 |
| Notes over 256 KiB | present, including multi-MB files | 0 |
| Summaries with direct summary neighbors | 1,012 | 11,749 of 11,751 |
| Missing local Markdown destinations | 0 | 0 |

All **11,751 summaries**, **5,170 profile reports**, **1,380 people**, **8,755 tags**, **109 sources**, **268 websites** and **13 pipelines** remain in the compact view. The parent retains **197,790 records and 547,897 canonical edges**. Independent checks verify all profile bodies, 23,198 non-profile summary attachment bodies, 4,162 profile links and the full archive's checksums/association evidence. There are 44 summary memberships across four pipelines. The existing 43 unavailable people and 375 summaries without nonempty annotation text remain; source status is partial.

The focused graph has 27,446 nodes and 153,317 directed links. Two summaries have no eligible semantic neighbors. Some people/sources still have over 11,000 neighbors, and 1,250 components reflect separate retained groups. These measurements do not promise that every global graph or broad local graph will be responsive.

Aggregate reports and test logs: `exports/obsidian-compact-20261003/`. Previous crash/candidate evidence: `exports/obsidian-20261003/`. Local development executable: `dist/obsidian-0.20.0-dev/pieces-export`. No replacement release has been signed, notarized or published.

## Corrected vault on this computer

Open **[Start Here](<~/Documents/Pieces Obsidian/vault/Start Here.md>)** in `~/Documents/Pieces Obsidian/vault`. In Obsidian's vault manager, choose **Open folder as vault** and select that exact folder. The earlier open `Pieces Export Compact/vault` remains an evaluation candidate with older titles/settings. Both candidates have the summary navigation refresh described below; their memory notes were preserved. The original `Pieces Memories` vault is also preserved.

The corrected conversion completed in **192.53 seconds** at **243 MiB peak RSS**, with **30,455 notes and 113,524,765 Markdown bytes**. It retains the same 187,294 document links and zero missing local Markdown destinations. All 5,170 profile bodies, 23,198 summary attachment bodies and 4,162 report links pass the independent reader. Canonical data, source omissions and partial status are unchanged. The full suite, vet, focused race tests and actual compiled offline/interrupted-fetch recovery checks pass; all six targets compile and Intel's version command runs through Rosetta.

**GUI acceptance boundary:** the earlier compact candidate completed indexing and successfully displayed a summary, a topic, backlinks and a local graph. The latest readable-name/heading fix has passed file/body/link checks but has not completed a fresh GUI/index/restart check. The user was actively browsing the earlier candidate; its memory notes and settings were preserved without interrupting that session. Do not infer final GUI or native Windows/Linux acceptance from compilation. No new package has been signed/notarized/published.

Evidence: `format-conversion.log`, `format-view-acceptance.log`, `format-graph.json`, `format-copy.json`, `format-full-tests.log`, `format-cli-race.log`, `format-vet.log` and `format-builds.json` in `exports/obsidian-compact-20261003/`. The intermediate unopened readable-name candidate is retained at `exports/obsidian-compact-20261003/before-title-fix/` for comparison.

### Summary navigation refresh — October 3

The exporter now writes the previews described above. The existing `Pieces Export Compact/vault` and `Pieces Obsidian/vault` were also refreshed in place, using their own record maps so all existing note filenames and links remain valid. Each received 257 generated navigation files across summary folders, adding 186 small pages. The other 30,385 non-settings files in each vault were hash-checked unchanged. Prior navigation files and manifests are backed up outside the vault in `exports/obsidian-timeline-20261003/backups/`.

All 11,751 summary entries are retained: **11,068** have usable activity ranges, **683** explicitly lack them, and **429** lack a retained description. Every one of the 12,450 local links on each refreshed set of pages resolves. The largest refreshed page is under 30 KiB. The corrected vault now has **30,641 notes and 117,580,666 Markdown bytes**; the earlier candidate has the same note count and 119,975,490 bytes. The parent archive's canonical records and coverage are unchanged. This refresh adds roughly 4 MB to the corrected vault, rather than copying full summary bodies into the indexes.

Open the [current candidate's first timeline page](<~/Documents/Pieces Export Compact/vault/workstream_summaries/timeline/index.pages/page-000001.md>) or the [corrected vault's timeline](<~/Documents/Pieces Obsidian/vault/workstream_summaries/timeline/index.md>). The full source test suite, vet and rebuilt native CLI offline/recovery checks passed after the change. A complete corrected-vault link audit found 187,852 document links, zero missing Markdown targets and no notes over 256 KiB. Aggregate staging, preservation, graph and compiled-CLI results are in `exports/obsidian-timeline-20261003/`; the full-suite log is `exports/obsidian-compact-20261003/timeline-full-tests.log`. No Obsidian window navigation or settings changes were needed for the refresh.

### Connection navigation refresh — October 3

Both local candidates now include **Connected memories** in all 10,525 connection notes. The real vault contains **96,454 distinct memory-to-connection references**; 808 connections need a complete browsing index, and nine have no incoming retained memory references. QuickTime Player has **77 summary references**: the original sidebar's 78th backlink was navigation. Its note now links the five newest and [all 77 connected memories](<~/Documents/Pieces Export Compact/vault/connections/sources/QuickTime_Player.65c17d84fade.memories/index.md>).

Existing summary/profile/signal notes, timeline pages, record maps and settings were preserved. All 20,117 other non-settings files per vault were hash-checked unchanged. Prior connection documents and manifests are backed up in `exports/obsidian-connections-20261003/backups/`. The corrected vault now has **31,691 notes, 140,805,107 Markdown bytes and 288,470 document links**, with zero missing Markdown destinations and no notes over 256 KiB. The earlier candidate has 31,693 notes and 147,332,212 bytes; its longer paths require two additional pages. Connection browsing pages stay within 64 KiB including properties. A tight real-data page exposed the previous post-pagination property-header overhead; pagination now budgets those properties directly.

The full source suite, vet, focused navigation/Obsidian tests and actual compiled offline/recovery tests pass. Reports and logs are in `exports/obsidian-connections-20261003/`. Both complete vault link audits found zero missing Markdown destinations. These file checks do not establish fresh-vault indexing/restart performance. The open app retained a cached copy of the QuickTime note after the filesystem refresh. **View → Force Reload** left its window blank; the window was closed, but automation could not reopen it. Reopen Obsidian from the Dock and verify the refreshed connection note before counting GUI acceptance as complete. No cache or vault files were deleted during this recovery attempt.

## Implementation and checks

`internal/exporter/obsidian_compact.go` selects and renders the browsing view from approved canonical records and relationship evidence. `obsidian.go` handles properties, settings and offline copying; `obsidian_connections.go` handles existing identities and ranked shortcuts; `obsidian_mentions.go` collects rendered memory links and builds bounded reverse navigation. `internal/cli/obsidian.go` exposes offline conversion. Export/rebuild run presentation before final auditing. `manifest.obsidian` version 3 records layout, vault path, preserved parent presentation, byte/note counts and source manifest digest for offline conversion. The archive schema itself remains unchanged.

Tests cover export/copy/rebuild/relocation/reconversion, source hashes, cleaned titles without duplicated report headings, Unicode and filename collisions, localized dates, actual Markdown/YAML links, report bodies, bounded peer ranking, undated records, signals/privacy, cancellation/overwrite/symlink refusal and compiled interrupted-fetch recovery. The compact acceptance reader independently checks selected identities and bodies against canonical data; deliberately losing a summary body causes it to fail. GUI indexing/search/navigation/restart checks are separate from file validation.

Official behavior references: [vault storage](https://obsidian.md/help/Files%2Band%2Bfolders/How%2BObsidian%2Bstores%2Bdata), [automatic backlinks](https://obsidian.md/help/Plugins/Backlinks), and [graph controls](https://obsidian.md/help/Plugins/Graph%2Bview).
