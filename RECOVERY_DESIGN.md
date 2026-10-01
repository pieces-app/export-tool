# Resumable export and durable staging

Updated 2026-10-01. **Completed-source recovery is available through opt-in CLI commands in `0.15.0-dev`; interrupted-fetch recovery remains pending.** Explicit workspace/key directories are required; automatic key locations and cleanup are unfinished. The current CLI still rejects an existing `.partial` destination, and the active `0.4.1-dev` export cannot acquire checkpoints in place. Keep its process and files intact. This work must preserve the full export scope, privacy checks, graph evidence, filenames, and finalization rules in [EXPORT_SPEC.md](EXPORT_SPEC.md).

## Current association integration — 2026-10-01

The transaction adapter is now selected by export/rebuild/replay for association records. Ordinary records keep their file backend. This is a transient staging optimization, separate from the explicit completed-source recovery workspace. Its final output uses format-6 grouped JSONL and Markdown pages, so encrypted staging does not merely add a second write before the old per-association files. The complete bounds, lifecycle, graph identity and importer contract are in [EXPORT_SPEC.md](EXPORT_SPEC.md#grouped-association-storage--format-6).

Finalized archives contain neither keys nor the staging database. Success and ordinary error/cancellation clean up the owned transient sibling; abrupt process exit can leave it. It is deliberately not advertised as a source-resume checkpoint. Interrupted-fetch state, automatic cleanup of crash leftovers, default retained-recovery key locations and reuse of completed artifacts remain open. Historical sections below describe the earlier implementation sequence; their “not selected by CLI” status is superseded for the association backend by this integration.

## Decision and implementation order

Pursue a private, encrypted, batched SQLite workspace as the authoritative recovery input. SQLite is already a compiled dependency for SDK-cache recovery; users would not install another runtime. Keep the final archive as ordinary Markdown/PDF/JSON files. A database workspace is internal recovery state, not a replacement for the requested files and folders.

The internal store/key/ownership layer passes the available local acceptance checks described below. The adapter persists actual exporter state after final source reconciliation and replays local processing into a fresh destination. The CLI now creates and opens these workspaces with exclusive ownership and explicit private key locations. Default platform locations and safe automatic cleanup remain next steps. Interrupted source-fetch recovery additionally needs verified source identity and persisted inventory boundaries. These are implementation milestones toward full resume, not a change to the release objective.

The current file writer still calls `File.Sync`. Keep that behavior until a separate replay and durability test proves a replacement. Adding a checkpoint database beside every unchanged file write will add overhead; it is not itself a performance fix. The eventual capture adapter must avoid duplicating authoritative persistence, then materialize ordinary output files from committed records. Measure both phases together.

## Why existing files are insufficient

### Next performance gate: association storage and presentation

The current junction reader improves source completeness but also feeds association records through ordinary `run.store` and `render`. Before format 6, each included association acquired its own synced canonical JSON and Markdown file. This is the wrong cost model for internal graph evidence at the observed cardinality. A faster reader alone can make local output work larger. The live sizing evidence and limits are in [JUNCTION_API.md](JUNCTION_API.md#live-cardinality-check-before-another-export).

The next implementation must preserve complete association payloads and typed proof identities while consolidating their storage and presentation. Keep summaries and person/profile documents as ordinary navigable files. An association need not have an individual human-readable document when its source/target links and metadata are available in a bounded family index and canonical record stream. This is implemented for associations in format 6; older archives retain individual association files. Complete live speed and abrupt-failure/platform acceptance are separate gates.

- [x] Introduce an explicit canonical-record access layer for write/read/replace/remove/hash operations. Source capture, late-credential rescanning, pruning, body rendering, signals, recovery capture/replay and offline rebuilding now use it. Deferred-file and encrypted transaction fixtures verify the complete local path; current public output remains per-file.
- [x] Batch association staging in the encrypted transactional store without duplicating individual association JSON writes. Bound transactions by count/bytes and stop on failure. Exclusion decisions and learned credentials remain in the live exporter and optional completed-source checkpoint; transient transactions do not provide interrupted-fetch recovery.
- [x] Design a versioned public canonical stream/index for associations, with bounded chunks, exact record locations/hashes, declared counts, and rejection of missing, duplicated, altered or unaccounted rows. Compact late privacy changes before final publication; excluded payloads cannot remain in an earlier chunk or a temporary sibling.
- [x] Separate record identity/coverage from document identity. `archiveLinkMap` accepts shared association pages in format 6; reconstruction, graph provenance and final-archive acceptance use explicit record references and verified row locations. Older readers must refuse the new format rather than silently lose proofs.
- [x] Render bounded association-family indexes with working source/target document links. Keep related-summary sections linked directly to summaries/persons. If section fragments are used, validate their existence in Markdown and PDF; never invent links to omitted documents.
- [x] Rebuild and replay completed-source checkpoints into the consolidated format, retaining current-junction empty decisions, historical-cache precedence, privacy withholding and complete canonical association metadata. Keep readers for existing per-file archives. Interrupted-fetch continuation remains unfinished.
- [ ] Test abrupt capture/materialization exits, disk exhaustion, cancellation, checksum/index tampering, privacy changes affecting one row of a chunk, moved-folder links and exclusive finalization. Keep durability guarantees explicit; buffering a write is not an acknowledged recoverable checkpoint.
- [ ] Benchmark capture **plus** all final files, metadata, privacy audits and reconstruction. Compare artifact/write/sync counts, body/graph coverage, wall time and peak memory on the actual summaries/profile workload before another full live performance claim.

This change is independent of checksum-based audit reuse. It must not mutate the original running export, reinterpret its `.partial` files as checkpoints, or remove requested summary/profile files to improve a benchmark.

### Canonical record access and transaction backend (2026-10-01)

`canonicalRecordStore` now centralizes current-record opening, creation/replacement, removal and flush boundaries. Consumers no longer assume that a canonical record can be opened directly at `Meta.DataPath`; that remains its public navigation location. Canonical raw capture, exact-byte hashing and decoded JSON reads use the same abstraction. Current file reads retain confined, symlink-rejecting archive access; exact numeric JSON values survive decoding. The existing file backend keeps exclusive creation, synced temporary replacement and missing-file removal behavior. Capture flushes pending storage before collecting evidence; finalization flushes after all rendering/pruning and before its first output audit. A failed flush prevents a completed archive.

The private `transactionalCanonicalRecords` backend is implemented and tested but **is not selected by the CLI yet**. It uses the existing owned encrypted store, with at most 50 pending identities and a 7 MiB ordinary payload budget. A larger record commits alone, under a 128 MiB-minus-64 KiB envelope budget. Updating a pending identity replaces its pending bytes; exceeding a count/byte bound drains the prior batch first. Successful commits clear retained payload buffers. Open readers own their bytes and survive later replacement/flush; closing those readers clears their retained payloads. These are queue/payload bounds, not process-RSS or forensic-erasure guarantees. Identity indexes and the export graph still scale with record counts.

`recovery.Store.Get` supplies bounded indexed access to one current authenticated payload, validating its latest change generation, ciphertext digest and encrypted binding. It returns caller-owned bytes; missing records remain distinct from exclusions. Whole-store history/schema verification still occurs at open, and `Visit` retains its full traversal checks. A selected lookup is not whole-store revalidation. The transaction adapter requires a fresh store, records removals as encrypted tombstones, and stops further reads/writes after commit failure. Its checkpoint label explicitly says it is temporary canonical staging; it lacks inventory cursors, complete scanner state and graph evidence and **cannot resume interrupted source reads**. The existing completed-source capture adapter remains responsible for real recovery evidence.

Tests move actual fixture records off their original filenames, then run privacy rescanning, association/person selection, Markdown/PDF rendering, checksums and archive acceptance through the new access path. Additional tests run a real encrypted transaction backend through export, completed-source capture and offline replay, verify late-credential removal and profile bodies, and check that private record text does not appear in the staging database. The test-only final materializer still writes individual public files, so it is not the production consolidation solution. Grouped public records, graph identities/navigation, CLI workspace ownership/cleanup, and full live acceptance remain required before enabling the backend by default.

The actual adapter's capture benchmark writes and rereads 128 roughly 4 KiB synthetic records. Across three samples of three iterations, median time was **1.822 s** for individual synced files versus **0.128 s** for **three encrypted transactions** (about 93.0% less capture/read time). File-helper sync counts were 128 versus zero; the latter counter excludes SQLite's own durable VFS work, which retains EXTRA synchronization and fullfsync settings. Allocations were about 1.69 MB versus 5.66 MB per iteration. Initialization/key creation, public materialization, filtering, graph work, audits and cleanup are excluded. Shared host/disk load varied. This does not establish whole-export speedup.

Verification: the access-layer full race run passed (exporter 538.312 s). Final combined canonical/lookup race tests passed (exporter 19.764 s, recovery 3.274 s); actual compiled Mac default/current-junction/rebuild/recovery checks passed in 21.519 s. Rosetta canonical/export/capture/replay cases also passed. Windows AMD64/ARM64 and Linux ARM64 test executables compile; current native Windows/Linux execution is unverified. Vet and diff checks passed. No new download package, upload or Actions run was made for this intermediate storage integration.

```sh
go test -race ./internal/exporter ./internal/recovery -run '^(TestCanonical|TestStoreGet)' -count=1
go test ./internal/exporter -run '^$' -bench '^BenchmarkCanonicalRecordCapture$' -benchtime=3x -count=3 -benchmem
```

Current source evidence:

| State | Current location | Required recovery behavior |
| --- | --- | --- |
| Canonical records and record decisions | `run.store` in `internal/exporter/export.go` | Commit a record, its included/excluded/withheld decision, counters and source progress together. A missing file cannot be interpreted as intentional exclusion. |
| Original projection shapes and cache eligibility | `Meta.ProjectionStates`, `SupplementableFields`, `RelationshipProjectionUnknown`, `PersonProjection` | Preserve before sanitization or reference pruning. An absent projection differs from a known empty relationship. |
| Relationships and their provenance | `Meta.Edges`, `AssociationEndpoints`, `run.derivedEdges`, `associationEdges`, `cachedEdges` | Preserve typed endpoints and proofs, including dependencies needed to withhold derived content. Do not infer missing associations from names or timestamps. |
| Person selection and account ownership | `PersonEvidence`, `run.people`, `userPersonIDs` | Preserve unknown evidence and verified user mappings. Recompute counts deterministically without merging identities. |
| Credentials discovered during scanning | `Scanner.known`, `ObserveCredentials`, `remember` in `internal/exporter/policy.go` | Restore the complete accumulated matcher state before any replay, scan, render or final audit. Sanitized JSON no longer contains all values needed to reconstruct it. |
| Inventory and association pagination | `run.inventory`, per-material windows and association-page state | Persist acknowledged work, stable boundaries and outstanding IDs. Replaying an unfinished batch must not double-count or skip records. |
| Output paths and metadata | `assignPaths`, `run.documentMetadata` | Freeze naming only after privacy/person selection. Track dependent documents, sidecars and indexes together. |
| Archive reconstruction evidence | `writeArchiveState` and `rebuild-state.jsonl` | This is produced near finalization and lacks the full live scanner/cursor state. It is not an interrupted-run checkpoint. |

In particular, `store` can retain the original title and edges in `Meta` for an excluded/withheld record. Dumping the in-memory `run` or `Meta` structs as plaintext would expose information intentionally absent from the final filtered archive. The public rebuild evidence already handles excluded identities as opaque references; it must stay that way.

## Workspace, keys, and ownership

Planned layout, separate from the shareable archive:

```text
chosen-parent/
  Pieces-Export.work/          # private recovery workspace; encrypted payloads
    state.sqlite
    state.sqlite-journal      # present only as required by SQLite
    owner.lock                # per-directory operating-system lock
    header.json               # version, random ID, config digest and size bound
    proof.bin                 # authenticated ownership/configuration proof
  Pieces-Export.partial/       # generated output, still unfinished
  Pieces-Export/               # appears only after final validation/rename

per-user persistent state/
  pieces-export/keys/<random-workspace-id>.key
  pieces-export/keys/<random-workspace-id>.lock # common lease for copied workspaces
```

The key must not be inside `.work`, `.partial`, the completed archive, a log, a manifest, a ZIP, or an environment variable. Create a random per-workspace key with exclusive creation in a private persistent user-state directory. Use restrictive permissions on POSIX and verified user-only ACLs on Windows. Resolve the platform state directory deliberately; a temporary/cache directory that the OS may purge is unsuitable for the only recovery key. Filesystem confinement, symlinks/reparse points, key-file replacement and ownership checks are implementation gates.

Encrypt record payloads, scanner state, private IDs/relationships, configuration containing private paths, and metadata before passing them to SQLite. Journals and temporary tables must therefore receive ciphertext. Authenticate workspace ID, schema version, item kind, opaque identity and generation as associated data. Use a fresh nonce for every encryption with the standard library's authenticated-encryption API. Bound decoded and encrypted sizes before allocation. The storage experiment uses AES-GCM with a random 32-byte key; it does not implement production key management.

This protects against exposing plaintext through a copied workspace alone. It does not protect against a process running as the same user that can read both the key and workspace, nor promise forensic erasure from backups, swap or storage snapshots. Missing/wrong keys and authentication failures stop recovery without modifying the workspace. Never generate a replacement key for existing encrypted state.

Hold an OS-backed exclusive lock for the entire resume/export operation. A PID, timestamp, stale file, or elapsed timeout is not sufficient evidence that another writer stopped. Verify lock release after an abrupt process exit on each platform. Resume must refuse active ownership; it must never stop the owner. Relative paths must resolve inside their intended roots, and destination creation/final rename remain exclusive.

## Transaction contract

Use one controlled database connection initially. A capture transaction contains:

1. Included canonical payloads or explicit non-inclusion decisions for the batch.
2. Original relationship/projection evidence and privacy dependencies.
3. Newly discovered credentials plus an authenticated complete scanner generation, with no plaintext credential columns.
4. Inventory/page cursor, exact completed IDs and counter changes.
5. The new checkpoint generation and its phase prerequisites.

Commit before reporting records as durably captured. A failed/uncommitted transaction advances none of these items. An acknowledged transaction cannot leave the scanner or cursor behind its records. Deduplicate by stable material/ID within the workspace, and retain the distinction between initial fetch, replacement, reference hydration and omitted records.

Bound transactions by both record count and encoded bytes; an HTTP batch of 50 can contain a very large record. Start performance experiments at 16 and 50 ordinary records, with large records committed alone under explicit size limits. Flush a partial batch on ordinary cancellation only when it is internally complete; otherwise roll it back. Never respond to a storage failure by fetching more source batches.

The experiment uses `journal_mode=DELETE`, `synchronous=EXTRA`, `fullfsync=ON`, and verifies the values by reading them back. SQLite documents that EXTRA adds a directory sync after rollback-journal deletion, while `fullfsync` requests the macOS full-flush operation; unknown PRAGMAs can otherwise be silently ignored. The installed driver's Darwin implementation calls `fcntl(F_FULLFSYNC)` with an `fsync` fallback. These settings are a candidate baseline, not proof of power-loss durability on every filesystem. [SQLite PRAGMA reference](https://www.sqlite.org/pragma.html#pragma_synchronous), [fullfsync reference](https://www.sqlite.org/pragma.html#pragma_fullfsync).

A process-exit test cannot certify hardware or OS power-loss behavior. Keep separate acceptance for disk-full, commit I/O errors, directory durability, unsupported filesystems and actual platform flush behavior. [SQLite's atomic-commit assumptions](https://www.sqlite.org/atomiccommit.html#hardware_assumptions).

## Phase transitions and replay

| Phase | Conditions for a committed boundary | Resume behavior |
| --- | --- | --- |
| Source capture | Exact initial inventory/window/page work, record decisions, scanner state and evidence committed together | Retry only unacknowledged work after verifying source continuity. Final source reconciliation remains required. |
| Capture complete | Hierarchy, account/person evidence, reference closure, cache provenance, selected associations and final ID reads completed or explicitly reported unavailable | Later local phases make no new OS reads. Missing source evidence remains a coverage limitation. |
| Privacy reconciliation | Full captured credential set frozen; every retained record evaluated for that generation | Restart or resume a proven per-record scan for the same generation. A newer credential generation invalidates earlier scan completion. |
| Graph/person selection | Dependency propagation, association filtering and person choices complete | Restore all decisions; replay must not turn unknown evidence into an empty relationship or revive excluded nodes. |
| Path assignment | Included identity set and naming/ranking settings frozen | Reuse verified paths; never renumber summaries partway through replay. |
| Markdown, PDF, metadata | Each artifact group records expected bytes/hash, dependencies and completed persistence | Verify completed files before skipping. Recreate only owned, incomplete artifacts; never overwrite unrelated files. Native metadata needs its own readback/reapply policy. |
| Validation | Full link and privacy audits run against the current final artifact generation | Re-run validation after any replayed/changed artifact. A previous audit marker alone is insufficient. |
| Finalization | Final manifest, reconstruction evidence and destination checks pass | Preserve exclusive rename. A crash after rename must be distinguished from an unfinished archive before cleanup. |

SQLite commits cannot atomically commit arbitrary output files. Artifact persistence therefore precedes the transaction marking those files complete. A crash between these operations may leave an unacknowledged file: verify its expected hash and ownership before adopting it, or regenerate it safely. A completion row without a matching file/hash is not success. Initial integration keeps existing file sync barriers. Reducing those barriers is a later, separately tested durability change.

Authenticate and reconcile the expected item set/counts as well as individual blobs; authenticated ciphertext alone does not detect a deleted row or certify whole-workspace completeness. Detect mixed generations and conflicting output ownership. State rollback/replay must be explicit; encryption does not make a checkpoint an atomic snapshot of Pieces OS.

During source-fetch recovery, a localhost port is not an identity. Establish a supported stable source/database identity and environment binding before enabling automatic continuation. Account IDs or a matching version alone do not prove the same database. If source continuity cannot be established, refuse automatic fetch resume; retain the checkpoint for diagnosis instead of silently combining databases. Even with verified identity, report source additions/deletions and the limits on detecting in-place edits across the interruption.

Freeze scope, materials, policy/category hashes, privacy capabilities, SDK-cache evidence, people selection, timezone, naming, related-summary settings, digest/PDF options and schema compatibility. A resume is continuation of the same export. Changed selection/layout belongs in a later offline rebuild of a finalized archive. Define explicitly tested compatibility IDs; do not trust a generic development version string as schema compatibility.

## CLI and failure UX

Available in `0.15.0-dev`:

```text
pieces-export export --output <archive> --work <new-workspace> --recovery-keys <private-keys>
pieces-export resume --work <workspace> --recovery-keys <private-keys> --inspect
pieces-export resume --work <workspace> --recovery-keys <private-keys> --output <new-archive>
```

`--work` is opt-in and requires `--recovery-keys`; both parents must already exist. Export requires a new workspace. The key directory may be reused only when its ownership/permissions pass validation. Workspace, keys, output and partial output must be separate and non-nested. Checks compare existing filesystem identities, symlinked parents and conservatively folded unresolved path components. Symlinks supplied as workspace/key roots are rejected. These options are rejected on scan/dry run before discovery.

Inspection authenticates the workspace, acquires exclusive workspace/key ownership, reports phase/generation/item counts and, after a complete capture, material-record decisions and frozen settings. It makes no OS calls or archive writes; SQLite may roll back an interrupted transaction in the private workspace while opening it. A `fetching` or `writing` checkpoint is inspectable but not replayable. Unsupported compatibility IDs, missing/mismatched keys and active owners fail before output creation. The explicit compatibility contract must change when capture, privacy or rendering semantics become incompatible; a development version string alone is not the contract.

Compatible navigation bug fixes may change later dependency propagation/rendering from the complete captured inputs. The missing-person embedded-link correction retains already-scanned narratives and flattens unavailable destinations, while preserving captured missing/excluded/withheld decisions, policy and learned credentials. It does not restore payloads excluded before capture or relax strict derived-content rules. Cross-version verification must distinguish intentional restored narratives from unexpected changes; byte equality with an older over-withholding renderer is not the acceptance requirement for this correction.

Resume holds ownership through review and replay. It validates a fresh output outside the original archive/partial folder, shows frozen scope/people/privacy/format and original timestamps, then asks for confirmation unless `--yes` is supplied. EOF/No cancels without creating output. Selection, policy, cache, naming and format overrides are not accepted; use a later offline rebuild for layout changes. Current tool version and zero current OS requests are separate from the original capture tool version/request totals. The terminal reports local replay elapsed time without calling time since capture stopped CPU time or promising a whole-run ETA.

**Current retention behavior:** success, ordinary failure and cancellation retain the private workspace and external keys. The export prints an inspect command without claiming capture completed. Interrupted source fetching must restart in a new workspace/output. Neither resume nor installer cleanup should delete recovery inputs. Keep workspace and keys outside the shareable archive and installer temporary files; losing the keys prevents replay. Automatic platform key paths, verified cleanup/discard and source-fetch continuation remain release work. Native Windows recovery runtime and power-loss acceptance remain unverified.

## Internal workspace layer

`internal/recovery` provides `Create`, `Open`, `Close`, `Seal` and `Unseal`, plus the transactional store described next. The opt-in exporter/CLI adapter now uses it with explicit paths. Default platform key-directory resolution and automatic workspace cleanup remain unfinished. The original live process is unchanged.

The caller supplies separate workspace/key directories with existing parents and a nonzero immutable-configuration digest. Directory paths are checked lexically and by filesystem identity to reject nesting and aliases. A workspace is created exclusively. Keys are random 32-byte values, created exclusively outside the workspace; existing keys are never regenerated. Header/proof/key reads are bounded. The canonical header rejects unknown/duplicate fields, unsupported versions, changed settings and invalid identifiers. Open performs no content writes. A failed initialization may retain an incomplete private directory/key; cleanup and interruption during initialization still need integration.

The ownership proof binds the key to a random workspace ID, format, configuration digest and payload limit. Envelopes also authenticate their item kind, opaque record reference and generation. Every envelope derives its own AES key using HKDF-SHA256 with a fresh 32-byte random salt, then uses the standard library's AES-GCM random-nonce API. This avoids sharing one GCM nonce budget across an entire migration and retries. A payload defaults to at most 128 MiB, with smaller caller-selected bounds; encrypted lengths are checked before decryption. Clearing the retained master key on `Close` is not a guarantee of forensic memory erasure. Whole-workspace completeness, generation ordering and database row integrity belong to the next transactional layer.

Two kernel locks are held: one in the workspace and one beside its external key. The second prevents a copied workspace using the same key directory from acquiring independent ownership through a copied `owner.lock`. Neither stale timestamps nor PID files authorize recovery. Tests prove rejection of both active original/copy opens and release after an abrupt synthetic child exit. This does not defend against a same-user process deliberately bypassing locks or duplicating the entire key directory.

Platform behavior:

- **macOS:** require current-user ownership, private mode bits, regular singly-linked files and no ACL entries. Newly created directories lose inherited ACLs before key creation via the system `chmod` operating on a held descriptor. ACL verification uses `fgetattrlist` directly on that descriptor, with a fixed-size extended-security reference. A regression fixture exposed that `ls` on `/dev/fd` does not report the underlying ACL; that approach was removed. The native ABI/ACL fixtures pass on ARM64 and Rosetta AMD64. Unsupported ACL queries fail closed.
- **Linux:** require current-user ownership, private mode bits and regular singly-linked files. POSIX ACL named-user/group access is limited by the mask reflected in group-class mode bits. The fixture accepts a fully masked named-user entry, then rejects it after its read permission becomes effective. [Linux ACL permission mapping](https://man7.org/linux/man-pages/man5/acl.5.html).
- **Windows:** directory creation supplies a protected user/SYSTEM ACL immediately. New empty files receive the actual user's ownership before content is written; existing ownership/ACLs are checked rather than repaired. Checks reject reparse points, multiple links and additional ACL principals. Locks use nonblocking `LockFileEx`; extended-length local/UNC directory paths are supported in the implementation. Windows AMD64/ARM64 tests compile, including an extra-principal ACL regression, but **none of this is native Windows acceptance yet**. [CreateDirectory security attributes](https://learn.microsoft.com/en-us/windows/win32/api/fileapi/nf-fileapi-createdirectoryw), [LockFileEx](https://learn.microsoft.com/en-us/windows/win32/api/fileapi/nf-fileapi-lockfileex).

Verification: macOS ARM64 focused race suite passed (3.022 seconds); actual Rosetta AMD64 test executable passed; Linux ARM64 passed as unprivileged UID 65534 in an isolated container. The Linux tmpfs skipped the ACL-specific fixture because it lacked ACL support; a separate run on the container's writable filesystem passed that fixture (0.06 seconds). No private source/cache/export directories were mounted. Vet and diff checks passed. Covered cases include encrypted-state persistence, wrong/missing/oversized keys, changed header/configuration, truncated/tampered/misbound envelopes, payload limits, cancellation, close/encryption races, directory moves, symlinks/hardlinks, permissions, active copies and abrupt lock-owner exit.

Key/header/proof file writes currently call `File.Sync`; crash/power-loss guarantees for directory entries and initial key provisioning remain unverified. No persistence barrier in the current exporter changed. The earlier storage benchmark used the simpler test-only cipher and does not measure this layer's per-envelope derivation, permission checks or future whole-export integration. The export adapter must additionally keep keys/workspaces outside every output/archive tree, define and freeze the actual configuration digest, select persistent per-user locations, and manage safe finalization/cleanup.

```sh
go test -race ./internal/recovery -count=1 -v
```

## Transactional store

`CreateStore`, `OpenStore`, `Commit`, `Snapshot`, `Visit` and `Close` now use the workspace's persistent key and ownership locks. Initialization commits generation zero. Every later commit atomically replaces a bounded batch of encrypted records, extends the change history and stores the complete caller-encoded checkpoint state. The caller supplies the expected previous generation; stale concurrent callers cannot overwrite newer progress. Excluded/withheld records remain explicit encrypted decisions rather than disappearing from storage.

The store treats record payloads and checkpoint state as opaque bytes. **It does not yet serialize `Meta`, restore the real `Scanner.known`, validate phase prerequisites, or persist the exporter's inventory/graph state.** Its synthetic fixtures model cumulative credentials and cursor advancement; those tests do not prove replay of the actual exporter. Internal record counts describe stored items, not accepted OS-export totals. Large inventories and graph state will need bounded fragments in the adapter, rather than one unbounded checkpoint blob.

Ordinary batches accept at most 50 records and 8 MiB of combined plaintext including checkpoint framing/state. One larger record may commit alone, subject to the per-payload bound and a 128 MiB combined bound; state-only checkpoints are supported. SQLite row/SQL/attachment limits and bounded blob queries are enforced. These are input/allocation limits, not a total process-memory guarantee. A storage error stops further writes on that handle and requires close/reopen to resolve commit uncertainty. Validation/conflict errors do not advance the generation.

The database has three tables and one supporting index:

| Storage | Contents |
| --- | --- |
| `items` | Opaque reference, current generation, authenticated encrypted record payload |
| `changes` | Ordered sequence, opaque reference, generation and ciphertext digest for every committed replacement |
| `checkpoint` | One authenticated encrypted state containing generation, current-item/change counts, change-chain head and caller state |
| `changes_ref` | Latest change lookup for each opaque reference |

Each change extends a SHA-256 chain covering its previous head, sequence, generation, reference and ciphertext digest. The chain head and counts are inside the authenticated checkpoint envelope. Open verifies the exact schema/version, SQLite integrity, complete chain/counts and the current record set against each reference's latest committed change. Deleting rows, adding rows, substituting a stale valid record, truncating history, changing ciphertext or replaying only an older checkpoint fails verification. Visits decrypt one bounded current record at a time; callers must copy callback payloads they retain. History grows with replacements; compaction is not implemented.

This authenticates an internally consistent stored generation. It cannot detect restoration of an **entire older valid workspace** without an independent trusted anti-rollback anchor, and does not establish source-database identity or source completeness. A same-user process that deliberately bypasses ownership and reads the key remains outside the encryption threat model. Full verification at open is proportional to stored history/payload volume and still needs large-history timing.

One pinned connection uses DELETE journaling, EXTRA synchronization, fullfsync, a 4 MiB page-cache target, memory temporary storage and disabled mmap/trusted schema; settings are read back. Database and journal files must remain regular/private, and unexpected WAL/SHM files are rejected before opening SQLite. Moving/replacing the active directory fails before creating a journal at the new path. The main database's held descriptor is checked without opening/closing another descriptor while SQLite is active: POSIX close can release that process's SQLite locks. A second-process writer test verifies the lock survives the checks. See [SQLite's advisory-lock warning](https://www.sqlite.org/howtocorrupt.html) and [PRAGMA settings](https://www.sqlite.org/pragma.html).

Verification on 2026-09-30:

- macOS ARM64 complete recovery-package race suite passed (8.842 seconds); actual Rosetta AMD64 and isolated unprivileged Linux ARM64 tests passed. Windows AMD64/ARM64 executables compile; native Windows remains unverified. Vet and diff checks passed.
- Abrupt synthetic child exit before/after the second commit recovered exactly 16/66 records with matching accumulated credentials and progress. The uncommitted case left a real spilled hot journal. Wrong configuration/key attempts left files unchanged before SQLite recovery. Workspace/journal checks found no synthetic plaintext credential/title markers.
- Real filesystem exhaustion on a dedicated 16 MiB Linux tmpfs passed both before transaction and during commit. After reclaiming only the fixture filler, reopening retained the previous record/scanner/cursor generation. This is separate from the additional `max_page_count`/SQLITE_FULL test. No production filesystem was filled.
- Cancellation after transaction writes, stale concurrent generations, replacements, empty payloads, large-record bounds, malformed/truncated state, unexpected schema, symlinks/hardlinks, unsafe journal permissions and directory moves are covered. The Linux tmpfs skips the separate ACL-mask fixture; that unchanged fixture was verified on the container's writable filesystem in the workspace-layer acceptance above.

These tests do not certify hardware power loss, initial key/directory provisioning durability, native Windows flush behavior, exporter interruption/replay equivalence or CLI cleanup.

The actual store's bounded benchmark includes envelope key derivation, permission checks, record encryption, change-log writes and checkpoint commits. It stores 128 roughly 4 KiB synthetic records; medians of three one-iteration samples on the development Mac were:

| Records per transaction | Median time | Median records/second |
| --- | ---: | ---: |
| 1 | 3.715 s | 34.45 |
| 16 | 0.309 s | 414.2 |
| Up to 50 | 0.120 s | 1,070 |

Initialization, key provisioning, verification on reopen, source reads, privacy scanning, graph work, ordinary archive-file materialization, Markdown/PDF and final audits are excluded. The original live export was active on the same filesystem. This supports batched capture, **not a complete-export speedup**. The earlier experiment used different cache/cipher machinery; do not attribute differences between the two benchmark runs to one code change. The existing exporter's file-sync behavior is unchanged.

```sh
go test ./internal/recovery -run '^$' -bench '^BenchmarkTransactionalStore$' \
  -benchtime=1x -count=3 -benchmem
```

## Storage experiment and evidence

`internal/exporter/recovery_storage_test.go` is test-only. The child receives an ephemeral key through stdin, commits 16 encrypted synthetic records, then writes another 512 records and updated cumulative scanner/cursor state in one transaction. A 64 KiB SQLite page cache forces the larger transaction to spill pages. The child calls `os.Exit` before or after commit, bypassing rollback/close/deferred cleanup. The parent checks the actual hot journal before reopening, database integrity, recovered row counts, matching scanner/cursor generation, payload authentication, wrong-key rejection and record-binding/tamper rejection. It inspects database/journal bytes for the synthetic credential markers and key. This is evidence for these fixtures, not a universal absence-of-plaintext or recovery guarantee.

On macOS ARM64, focused race tests passed (3.696 seconds). The isolated Linux ARM64 executable also passed both cases (0.04 seconds): no network, source, OS cache, or real export mounted. Process-crash recovery kept 16 records before the second commit, and all 528 after it. Windows AMD64/ARM64 test executables compile, and vet/diff checks pass. Production checkpoint integration and native Windows acceptance remain open.

The bounded benchmark stores 128 synthetic payloads of roughly 4 KiB, with three one-iteration samples on the development Mac while the original export remains active:

| Storage path | Median time | Median records/second | Transactions per iteration |
| --- | ---: | ---: | ---: |
| Existing exclusive per-file write and sync | 1.188 s | 107.7 | Not applicable |
| Encrypted SQLite, one record per commit | 3.493 s | 36.65 | 128 |
| Encrypted SQLite, 16 records per commit | 0.364 s | 352.1 | 8 |
| Encrypted SQLite, up to 50 records per commit | 0.142 s | 900.5 | 3 |

Batching is essential: the one-record transactional case is slower. The 50-record case is about 8.4 times the isolated record-storage throughput, with synchronization enabled. It includes encryption, payload construction and scanner/cursor updates; setup and cleanup are excluded. It excludes HTTP reads, actual privacy scanning, full graph state, archive materialization, Markdown/PDF, metadata and validation. It is **not** an 8.4-times faster export claim or a reason to weaken file persistence.

```sh
go test -race ./internal/exporter -run '^TestRecoveryStorageAbruptExit$' -count=1 -v
go test ./internal/exporter -run '^$' -bench '^BenchmarkRecoveryStorage$' \
  -benchtime=1x -count=3 -benchmem
```

## Completed-source adapter and CLI

`internal/exporter/capture_checkpoint.go` captures the boundary after hierarchy, account/profile evidence, reference closure, optional SDK-cache recovery, selected association reads and final inventory reconciliation. The capture-complete marker means these read attempts ended; missing records and projection gaps remain explicit. It does not certify complete source coverage.

The encrypted store contains one canonical body at a time plus all `Meta` states, including excluded/withheld identities and missing-record inverse edges. It also contains inventory order, original projection/eligibility maps, verified account mappings, person-selection evidence, derived/association/cache provenance, issue order, normalized options, policy and category hashes, normalized category domains, and the scanner's accumulated credentials. Arbitrary credential bytes use base64 inside encryption so invalid UTF-8 cannot silently change a matcher. Equal-length overlapping known secrets now use a stable replacement order.

Ordinary transactions contain at most 50 items and roughly 7 MiB of encoded payload; a bounded oversized item is committed alone. Inventories/domains/issues/edges use 128-element fragments, and credential fragments also have a byte target. Every intermediate commit remains in `writing`; only a final state-only commit records `capture-complete`. A partial save cannot be replayed. Bodies are validated individually and are not retained as an additional in-memory dataset, although existing graph/ID memory costs remain.

Replay authenticates the store and validates the adapter version, item counts/identities, configuration, canonical IDs, material paths, fragment continuity and original decisions before creating output. Frozen domain lists are restored without reading their original files. SDK caches and OS are not read again. An optional custom PDF font must still exist and have the captured digest; fonts are not embedded in the workspace. Adapter-version compatibility must be maintained explicitly when schema or processing behavior changes; a generic development version string is not a compatibility guarantee.

The output must be a new destination with no existing `.partial` sibling. Replay restores canonical records, then reruns privacy reconciliation, dependency filtering, person selection, path assignment, Markdown/PDF, metadata, link checks and final audits. It leaves the interrupted output untouched and does not skip supposedly completed artifacts. The manifest's `capture_replay` records capture/replay times and the original source-request performance separately from zero current OS requests. Capture does not improve missing source evidence or turn unknown person connectivity into zero.

Synthetic checks compare canonical files, Markdown, graph/link maps and reconstruction evidence with an uninterrupted export, excluding run diagnostics and PDF creation timestamps. They also verify moved Markdown/PDF navigation, late-discovered secrets, deleted domain-list files, unavailable SDK caches, association numeric precision, preserve mode, missing/withheld records, more than one transaction/fragment, invalid authenticated state, existing output conflicts, cancellation and incomplete capture rejection. A subprocess test exits immediately after the completion commit without closing the store, then reopens and completes offline. Process exit is not proof of hardware power-loss durability.

The final focused capture suite passed with the race detector on macOS ARM64 in 48.582 seconds, under Rosetta AMD64, and in an unprivileged, network-isolated Linux ARM64 container with a read-only root and 512 MiB memory limit. This is bounded synthetic acceptance, not a large-history RSS bound. Both Windows architectures compile; native execution remains pending. The initial abrupt-exit assertion incorrectly expected request totals from a fixture client without performance collection enabled. The fixture now enables collection and checks positive original request totals separately from zero replay requests.

The concurrent full `go test -race ./...` sweep compiled before that fixture correction and finished with that one failure (exporter 524.197 seconds); its other tests/packages passed. The corrected entire capture suite above passed afterward. This is not recorded as a successful single full-suite invocation for this revision. `go vet ./...` and diff checks passed. No release ZIP, original process, Actions job or hosted asset was changed.

This is **completed-source CLI recovery, not interrupted-fetch recovery or a speed improvement**. The hook adds capture I/O beside existing synchronized canonical writes. Further integration must avoid duplicate authoritative persistence, add default platform key locations and verified cleanup, and verify actual platform behavior. Interrupted reads, per-artifact skipping and eliminating ordinary per-file flushes remain unimplemented.

```sh
go test -race ./internal/exporter -run '^(TestCapture|TestKnownCredentialOverlap)' -count=1
```

## Acceptance checklist

- [x] Inventory in-memory recovery state and its privacy hazards from current source.
- [x] Measure batched encrypted storage with synchronization enabled, including a one-record control.
- [x] Test abrupt child-process exit with real spilled transactions, atomic record/scanner/cursor recovery, and ciphertext rejection on macOS and Linux ARM64.
- [x] Implement versioned internal transactional schema, bounded reads, encrypted fields and authenticated checkpoint/current-item-set evidence; test available local runtimes and actual Linux filesystem exhaustion.
- [x] Integrate the completed-source schema and completion marker with opt-in export, authenticated inspect, confirmation and offline CLI replay into a fresh output. Preserve old partial files and private keys/workspace. Artifact skipping and interrupted-fetch resume remain separate work.
- [x] Add a private completed-source capture/replay adapter with actual canonical records, privacy state, relationship evidence and fresh-output replay. Validate document/graph equivalence, missing/withheld decisions, frozen domain lists, chunk boundaries and invalid checkpoint rejection. Source-fetch continuation and reusing completed artifacts remain separate work.
- [x] Implement and test the internal key/envelope/ownership layer on macOS and Linux, including copied-workspace locks and actual ACL fixtures.
- [ ] Connect persistent platform key locations, archive-root exclusion, initialization durability and safe cleanup; execute native Windows ACL/ownership/long-path acceptance.
- [ ] Add round-trip fixtures for every `Meta`, projection, association, person, cache and omission state; test credentials discovered before and after a checkpoint.
- [ ] Integrate capture commits with counters/cursors and eliminate duplicate authoritative persistence; benchmark combined capture/materialization rather than the database alone.
- [ ] Integrate privacy, graph, path and artifact phases; verify final content/decisions against an uninterrupted export after interruption at every boundary.
- [ ] Establish stable source identity and interrupted pagination semantics before enabling source-fetch resume.
- [ ] Test multiple resume attempts, active ownership, moved/tampered workspaces, missing keys, corrupted/truncated rows, disk-full/commit failures, cancellation and crash after final rename.
- [ ] Validate crash/power-loss durability and any proposed file-sync reduction independently on supported filesystems.
- [ ] Exercise the actual CLI and installer retention/cleanup on macOS/Linux/Windows; measure large-history CPU, memory, disk and whole-export time.
- [ ] Validate a new real export/resume against the user's OS after the existing run finishes. Never convert the running old `.partial` folder by guessing missing evidence.

## CLI release verification (`0.15.0-dev`)

Full `go test -race ./...` passed (exporter 521.642 s, CLI 3.621 s), as did vet and diff checks. The integrated capture/public API/CLI suite passed in 57.842 s (CLI 1.568 s). Actual native packages passed default/projected-summary/rebuild/recovery checks on macOS ARM64 (26.137 s with race checks) and Rosetta AMD64. The executable is killed after its completion commit, its fixture server is closed, and replay verifies source counters, preserved gaps, profiles, privacy and links. Both complete and partial exit statuses are checked.

Actual-ZIP installer checks passed in 41.399 s for Bash HTTPS and macOS PowerShell 7 with substituted file transport. A new scenario removes the downloaded utility after a recovery-enabled export, then opens the retained external workspace/key pair and verifies it is replayable. No installer script changed. All six release ZIPs and extracted payloads match their hashes and exact four-file layouts. Linux runtime could not start because Docker Desktop is stopped; Windows binaries compile but have no native runtime result. Prior Linux internal-store/adapter evidence does not close this revision's CLI gate.

The first new live summaries attempt failed discovery before creating an archive: OS is absent from the previous port and all discovery ports, and no OS process was found. Relaunch preference is pending. The original all-data process continues its local rendering; no app was launched, stopped or mutated. These release checks do not establish real-history runtime, body coverage or production readiness.

## Current junction compatibility (0.16.1-dev candidate)

Completed-source capture retains per-record current junction coverage, canonical association proofs and current-over-cache precedence. The compatibility binding is `pieces-export/completed-source-replay/2026-09-30/v2-current-junctions`. Earlier v1 development workspaces require their original executable; the candidate refuses an incompatible binding. Candidate package fixtures verify offline replay, including the conditional referenced-only annotation scope, without source requests. Interrupted source fetching and reduced output flush costs remain unimplemented.

Final shareable archives with retained current-junction evidence use reconstruction-state version 2. Older rebuilders reject that version; the current reader accepts versions 1 and 2 and rejects junction fields mislabeled as version 1. This is separate from the encrypted workspace compatibility binding. Focused export/rebuild/capture/archive tests passed after this guard (141.306 s with race checks), including repeated offline reconstruction.
