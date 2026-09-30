# Rebuild a completed export offline

`rebuild` regenerates Markdown, PDFs, indexes, graph links, and metadata from a finalized export. It makes **no Pieces OS requests**, does not launch OS, and does not close Desktop. It writes a separate folder and leaves its input unchanged. This is useful after a long export when only rendering, people selection, or historical cache-link recovery needs to change.

```sh
./pieces-export rebuild --source ./finished-export --output ./rebuilt --format both

# Narrow an all-people archive using its retained profile evidence.
./pieces-export rebuild --source ./finished-export --output ./profiles --people profiles

# Reuse exactly the original policy and domain list contents.
./pieces-export rebuild --source ./finished-export --output ./rebuilt --policy policy.json

# Recover historical links to annotation records already in the archive.
./pieces-export rebuild --source ./finished-export --output ./recovered \
  --sdk-cache /path/to/pieces_client_sqlite.db
```

On Windows use `.\pieces-export.exe` with the same arguments. The command shows the number of included source records, original privacy mode, original coverage-issue count, destination, and cache warning before the usual `[Y/n]` confirmation. `--yes`/`-y` supports unattended use; EOF declines without creating a folder. Progress reports local stages and record counts, with no invented OS request metrics or network ETA.

Privacy reconciliation reports examined records, throughput, and a phase estimate. It still scans every retained record, but avoids rewriting already-synced JSON when the sanitized value is exactly unchanged. Rendering similarly avoids rewriting JSON unless private references were removed. These checks reduce local disk work; they do not skip scanning, restore omitted data, or predict the duration of later stages.

## Input and output contract

Only finalized archive formats 4 and 5 are supported. A finalized archive whose coverage status is `partial` is a valid input; an active or interrupted `.partial` directory is not. Missing/unfinalized manifests are rejected. The destination and its staging sibling must be new and outside the source, including when a parent is a symlink or a filesystem alias. Reads are rooted under the source directory; symbolic links, nonregular inputs, unsafe graph paths, duplicate mappings, invalid record identities, and mismatched counts fail without finalizing the output.

Format, timezone, naming, relationship layout, and metadata settings inherit recorded source settings unless explicitly changed. Legacy inputs without those settings default to readable naming, both relationship layouts, and native metadata off; use explicit flags to choose another presentation.

The rebuild imports canonical `data/` records in filtered mode or `raw/` records in preservation mode. It does not import arbitrary generated Markdown as replacement source content. The retained `relationships.jsonl` graph is replayed with its original direct, inverse, or historical-cache provenance. New paths are assigned before rendering and links are validated again. PDFs are regenerated from the new Markdown, so local destinations match the new folder structure.

Archive format 5 adds `rebuild-state.jsonl`, bound to the manifest by SHA-256. It records included-record content digests, original core projection states and cache eligibility, per-record redaction counts, person-query evidence, and explicit user/person mapping evidence. Excluded, withheld, missing, and omitted decisions contain only a material, opaque record reference, and state. Graph and link-map digests bind identities to their original paths. These checks detect accidental alteration; they are not signatures or authentication of an arbitrary archive's author. The full archive remains sensitive even though omitted identities are opaque.

Format-5 archives through `0.8.6-dev` did not record signal projection states. The reader accepts a wholly missing map for a signal, reports all seven signal relationship fields as unknown, and keeps the result partial. It does not infer original emptiness from pruned JSON. Partial or invalid maps are rejected, and required evidence for other material types is unchanged. New exports/rebuilds persist signal states for subsequent reconstruction.

The reader streams collection entries and JSONL. Manifest reads are bounded to 16 MiB, canonical record reads to 128 MiB, JSONL rows to 1 MiB, and the link map to 1 GiB. IDs, path maps, and graph metadata remain in memory. Large-history rebuild throughput and memory still require live acceptance; this feature does not yet provide disk-backed graph storage or resume after interruption.

## Privacy and omission rules

Filtered input requires the **same policy hash and domain-list hashes**. Changing modes or relaxing/replacing the policy is rejected. The current scanner still scans retained records, reconciles known credentials, propagates new exclusions, and audits JSON/Markdown/PDF output. Updated detection can redact or withhold additional content; the rebuild cannot recover bytes already redacted or omitted. Preservation input remains preservation mode and can contain sensitive originals.

People mode inherits the source setting. An `all` archive can be narrowed to `profiles` or `connected`. A previously narrowed archive cannot be broadened or switched to another selection: those people are unavailable. Original people statistics remain in `manifest.rebuild.source_people`; the current people report describes the retained input evaluated during rebuilding. Coverage keeps previous exclusions/withholding/omissions and adds new decisions without recreating their records.

Original coverage issues, unsupported data limitations, source partial status, hierarchy evidence, source performance, and source read timestamps remain visible. Repeated rebuilds retain the original OS read interval. Current rebuild performance reports zero OS requests; original performance is explicitly labeled as source evidence. Initial/inventoried/fetched/final collection counts refer to the original source read, not a fresh reconciliation. Scope omissions are inherited from that source read.

## Historical cache recovery offline

The normal [SDK cache rules](SDK_CACHE_RECOVERY.md) still apply: explicit local paths, read-only SQLite/WAL, bounded reads, exact current archived summary identity/creation time, non-future cached update time, current-field precedence, latest valid cache field, and conflicting versions skipped. Cached prose and embedded records are never copied into the archive.

Only already-included canonical records can supply document content. For privacy propagation, a selected-but-unavailable cached target is represented by a private blocking placeholder; it cannot acquire a file or link. If a cached summary is absent from the archived inclusion set, it may have been excluded: any already-included annotation bodies explicitly attached to it in the cache are conservatively withheld. This may remove additional content, including links from older cache history; the manifest reports the aggregate effect. Unselected types remain intentional scope omissions.

Existing historical edge provenance survives rebuilding without re-reading caches. Adding a different cache set to an archive that already used cache recovery is rejected; rebuild its original pre-recovery archive to compare cache sets. This avoids silently blending incompatible versions or cache ordinal meanings. Historical recovery remains **partial, exit 2**.

## Legacy format 4

The currently running long export uses an older executable and will produce format 4. It can be rebuilt once finalized. That format lacks reconstruction digests and original per-record evidence, so rebuilding reports `legacy_reconstruction_evidence_incomplete` and remains partial. Original private identities are not reconstructed from names or emails. Existing user membership is replayed only from explicit local person links in its existing `personas/users/*/profile.md` navigation. Missing original projection/selection evidence stays unknown, and an original nonzero per-record redaction count can become unavailable (-1).

A format 4 all-people report can sometimes recover annotation-selection evidence. The manifest must explicitly contain total, selected, omitted, and unknown counts and an issues array; the report must account for every retained person with no selection omissions. The distinct retained-person `persona_history_unresolved` issues must exactly match the reported unknown-person count. Only then can those identified people stay unknown while the others regain their completed annotation-selection evidence. Missing fields are not interpreted as zero; inconsistent counts, unkeyed issues, or a previously narrowed people mode disable this recovery. Issues for privacy-excluded people do not count toward the retained population. The manifest and coverage report state whether reconciliation succeeded and how many people remain unknown.

This does not recover per-person event-connection counts or prove complete person-to-summary projections. Those remain unknown, and legacy status remains partial. A source report that cannot be reconciled retains people conservatively. Compatibility has been exercised against the actual older 0.4.x executable on a synthetic server, not only against a relabeled new fixture.

An offline rebuild cannot repair missing canonical records, restore the summaries omitted by an older scanner, verify that a cached attachment is still current, or create attachments absent from every source. Those need separate source recovery/verification. This command also cannot turn an incomplete migration into a complete one merely by rendering every available file.

## Verification

Synthetic tests exercise source immutability, input checksums, swapped path mappings, graph/state corruption, unsafe paths and symlinks, destination aliases, policy mismatch, existing outputs, cancellation, legacy fallback, verified user folders, person narrowing without restoration, privacy propagation from unavailable cached dependencies, historical provenance across repeated rebuilds, PDFs, and local links after moving the result. Native packaged acceptance runs with its fixture OS already shut down. See [TODO.md](TODO.md) for executed platform evidence and remaining live acceptance.
