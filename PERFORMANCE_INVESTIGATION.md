# Live export performance investigation

**Current result, 2026-10-01:** the corrected `0.17.1-dev` summaries/profile export finalized in **51m23s**, exit **2 / partial**, and passed independent archive consistency/body/link acceptance. It fetched all 11,746 summary snapshots in **1m36s**; supporting traversal, file output and privacy auditing dominate the complete runtime. This is still unacceptable default-export performance. Complete migration and production readiness are not established.

Both prior processes—the original all-data export and `0.17.0-dev` current-junction attempt—exited with errors and left unfinished folders. Neither was stopped or replaced. The latter failed after **46m31s** while scanning a 20 MB generated index. The **65m45s historical-cache baseline** is an earlier partial archive; the new archive uses current junction evidence without historical caches. Different captured graphs, source inventories and host contention prevent treating this as a controlled speed comparison. [The failed run and forward fixes](#failed-current-junction-run-and-forward-fixes) are documented below. Older process checkpoints are historical.

## Finalized corrected summaries/profile export

The actual `0.17.1-dev` macOS ARM64 executable ran from 15:54:22 to 16:45:45 UTC, **3,082.832 s**, with default summaries/profile selection, Markdown, automatic metadata and retained encrypted recovery. The finalized archive is `exports/live-summaries-20261001-recovery`. No historical SDK caches were supplied. Both earlier failed processes had exited, so they were no longer competing for disk writes. Some bounded package tests and read-only diagnostics ran on the same development machine; this is not an isolated benchmark.

Source collection reached **200,672 captured records**, including the supporting graph, after fetching 11,746 summaries and **28,359 referenced annotations**. Unlike the prior attempt, the 43 unavailable person snapshots did not trigger a full annotation inventory. Referenced annotation fetching was **61.7% smaller than the 74,121-annotation preflight inventory**; this is a selection comparison, not a whole-run speedup.

| Measured phase | Wall time |
| --- | ---: |
| Fetch 11,746 summary snapshots | 1m36s |
| Fetch 4,343 person snapshots | 28s |
| Current-junction owner traversals combined | 10m52s |
| Fetch 28,359 referenced annotations | 3m35s |
| Retained source checkpoint | 1m48s |
| Privacy reconciliation | 1m22s |
| Group approved association evidence | 22s |
| Render Markdown | 7m19s |
| Write graph and navigation indexes | 36s |
| Both output audits combined | 17m39s |
| Native and portable metadata | 2m27s |
| Validate local links | 41s |
| Write reconstruction evidence | 15s |

The table omits smaller reference/inventory stages and preflight; the full diagnostic remains in the archive. Final HTTP totals are **208,770 requests**, zero retries and four adaptive backoffs, with **4m15s pacing waits**, recent p95 2.24 ms and peak request 8.00 s. All **102,753 owner-side traversals** reconciled. Local totals are **176,080 public writes/syncs**, **19m25s summed sync time**, about **1.95 GB logical writes**, and **2,778 encrypted transactions / 1m18s summed transaction time**. Operation durations overlap and must not be added to wall time. Peak RSS was **1,718,419,456 bytes (1.60 GiB)**. Before metadata/finalization, a directory count found 61,910 JSON files, 2,636 JSONL files and 84,862 Markdown files; record counts and file counts are different.

A five-second passive sample of the first audit, symbolicated using the running binary's Go PC table, shows Gitleaks detection and regular-expression matching. Observed CPU was approximately one core. This confirms a CPU scanning bottleneck during that sample, distinct from durable-write cost. The closing audit reused **149,408 approvals only after hashing their full bytes**; new metadata/reconstruction files still underwent content scanning. No privacy validation was bypassed.

Independent archive acceptance passed in **68.452 s** including test startup. It reconciled **193,419 included records**, 43 missing records, 2,962 intentionally omitted people and 4,248 withheld records. Included output contains **11,559 summaries**, **1,381 persons**, **5,142 profile-history documents**, **531,712 graph edges**, **263,020 verified canonical association edges**, and **31 summary/pipeline memberships across four pipelines**. Every included summary/person has current annotation evidence; all included persons also have current summary-association evidence. The reader verified 26,784 attached summary-annotation body occurrences, 5,142 profile-history documents and all local Markdown targets. Historical graph/body evidence count is zero.

Person selection retained **1,381 of 4,343** inventoried people and omitted **2,962 (68.2%)**; this is meaningful selection, not identity merging. Read-only macOS verification independently decoded both stored attributes for **all 23,118 metadata-bearing documents** and matched their portable sidecars. This verifies stored tags/comments, not Finder indexing or viewer behavior.

Remaining content gaps must stay visible: **187 summaries** and **403 annotations** were withheld during dependency/privacy processing. The manifest's explicit issues are 43 unavailable person references; the precise withholding chains still need analysis from retained capture. Among included summaries, **11,183** have a nonempty standard summary-type body and **11,184** have a nonempty annotation of any type. Of the other standard-body cases, **375 have no retained annotation edges**, and one has a nonempty hierarchical profile annotation. All 376 have summary kind `UNKNOWN`. A valid archive does not prove absent source text never existed or establish full-history migration.

Authenticated recovery inspection passed in **7.380 s**, confirming a complete 200,672-record capture. Actual packaged replay started at **16:47:04 UTC** into `exports/live-summaries-20261001-recovery-replayed`; it is still running. After finalization it must independently pass archive acceptance, preserve coverage/privacy decisions and match document/evidence bytes except execution diagnostics, with zero current OS requests. Source fetching is not repeated to test local processing. Ignored aggregate logs, reports and the passive sample remain under `exports/`.

## Forward bounded text-audit candidate

The forward **`0.17.2-dev` candidate** adds parallel text-file auditing under the existing `--file-workers 1–4` setting (default two). Each worker owns its detector and buffers; immutable policy/known-credential inputs remain identical. PDF semantic checks run alone, and every failure/cancellation joins active jobs and invalidates the traversal's reuse evidence. Source pacing, per-file sync, output bytes and privacy rules are unchanged. The running `0.17.1-dev` replay still uses its serial auditor.

A bounded synthetic comparison scanned the same 256 distinct Markdown/JSON files, about 6.49 MB, with a fresh audit cache each time. Three one-iteration samples per worker count on the development Mac gave median **1.155 s / 0.591 s / 0.302 s** for **1 / 2 / 4** workers. Allocation totals rose from about **109.5 MB to 113.8 MB to 122.2 MB** per traversal; these are allocations, not peak RSS. Fixture creation and original scanner initialization were excluded, and the host also ran local recovery. This demonstrates phase parallelism, not a whole-export speedup or a relaxed scan deadline.

Focused audit race checks passed (13.551 s), including worker bounds, PDF barriers, failure/cancellation, full-byte cache checks, late credentials and malformed/private content. The full combined race suite passed (exporter 448.489 s), and vet passed. All current packaged Mac ARM64 CLI/installer cases passed (109.026 s with race checks), and Rosetta worker/CLI checks passed. Six candidate binary-only ZIP layouts, checksums and embedded files match. An initial whole-tree invocation encountered duplicate `main` functions in ignored profiling helpers; both helpers now have standalone build tags, and the clean full command passed. Real retained-capture performance validation remains pending; the candidate has not been uploaded. Current Linux runtime testing remains unavailable because Docker is stopped, and Actions remains on the billing hold.

## Failed current-junction run and forward fixes

The actual packaged `0.17.0-dev` default summaries/profile export ran from 14:44:59 to 15:31:30 UTC on 2026-10-01, **2,790.759 s**, exit **1**. No SDK caches or retained recovery workspace were used. Its original all-data counterpart shared the filesystem. The failed folder is `exports/live-summaries-20261001-current-junctions.partial`; no final archive or independently accepted counts exist.

| Measured completed/failed phase | Wall time |
| --- | ---: |
| Fetch 11,746 summary records, including persistence | 2m56s |
| Fetch 4,338 person records | 54s |
| Eleven current-junction owner traversals combined | 13m26s |
| Fetch initial 28,359 referenced annotations | 3m38s |
| Full annotation fallback, covering 74,118 IDs | 5m42s |
| Privacy reconciliation | 1m59s |
| Group 131,510 approved association records | 20s |
| Record rendering plus graph/index generation | 12m21s |
| Output audit before failure | 2m47s |

The diagnostic reports **243,174 public writes/syncs**, about **2.07 GB logical bytes written**, **30m24s summed public sync time**, and **2,778 encrypted transactions / 4m07s summed transaction time**. These operation times overlap and must not be added to wall time. The source client made **210,162 requests**, zero retries and 15 adaptive backoffs; final recent HTTP p95 was about 7 ms. Peak RSS was **1,966,899,200 bytes (1.83 GiB)**. Native metadata, closing audit and finalization did not run. Grouping association files alone does not establish acceptable performance.

Two specific defects were isolated:

1. **Missing owners caused unnecessary annotation expansion.** The initial referenced set had 28,359 annotations, but 43 unavailable person snapshots caused the exporter to inventory all 74,118 annotations. Any earlier percentage based on the initial set is not the achieved reduction. Current indexed junction routes do not require the owner snapshot. The forward fix enumerates those owner IDs, retains available linked annotations and leaves the owner marked missing. Actual reconciliation is still required before skipping unrelated annotations; failed/unsupported reads do not become empty evidence. Focused source/replay/rebuild tests passed (94.094 s and 14.899 s), and compiled CLI/current-junction/recovery checks passed (7.229 s).
2. **Unbounded navigation produced a pathological scan input.** The audit completed 110,303 files and then failed on `index.md` (20,017,932 bytes). A read-only single-file reproduction failed after **56.213 s** with the same scanner deadline error. CPU profiling attributed approximately **97%** of sampled CPU to regular-expression matching; the detector checks cancellation between rules, so an individual regex can run beyond its nominal deadline. The forward fix generates compact landing navigation and pages large generated lists at 250 entries / 64 KiB, preserving every destination. It does not weaken or bypass the scanner.

Re-rendering the real failed index's **110,745 list entries** into disposable private pages retained all entries in **447 files**, maximum **57,084 bytes**. Their default-policy content scans passed in **15.124 s**, with **17.89 s** for the full diagnostic test. The failed source folder was not modified. This is a navigation diagnostic only: it does not reproduce the original process's learned credentials, copy target documents, validate the entire archive, or establish a new whole-export time. Focused pagination/source/rebuild/graph tests passed in **20.293 s** with race checks, including 62,501-entry navigation, byte bounds, Unicode, relocation, cancellation, PDF links and post-audit secret injection.

The original `0.4.1-dev` process also exited **1** after its record-render counter reached 1,907,456/1,907,456. Its terminal error was not retained in the aggregate checkpoint, so its exact failure is not established. Its failed root index is **282,558,289 bytes**, demonstrating the same unbounded navigation design, but that observation alone does not establish its exit cause. Both partial folders remain intact; neither supports completed-source CLI recovery because those runs did not enable a retained checkpoint.

Candidate `0.17.1-dev` contains both fixes. The full source-package race suite passed (exporter **489.702 s**). Actual Mac ARM64 CLI checks passed (**33.645 s**, with race checks), as did the Rosetta executable (**33.963 s**): missing-owner export/recovery and 253 linked summary bodies through paginated export, relocation and offline rebuild. All six binary-only ZIP checksums, exact four-file layouts and embedded bytes match the checked build inputs. Vet/diff checks pass. A final read-only doctor check found staging OS ready. Current native Windows/Linux execution and publication remain unverified; no Actions or upload occurred.

Next acceptance requires the combined fixes in an actual full live run, final counts/bodies/links/privacy reconciliation and complete phase/resource measurements. Remaining ordinary file sync volume, junction count traffic, profile/native metadata cost and source-fetch recovery are separate work. No new accepted end-to-end speed claim is made.

Observed 2026-09-30 against the original running `0.4.1-dev` export. The user requested that this run finish; it was not stopped, restarted, signalled, or replaced. The investigation used one five-second native stack sample, read-only process/resource counters, existing source/history, and filesystem-capacity checks. No OS HTTP reader or source mutation was added. Stack/resource artifacts are ignored local files under `exports/`; no record bodies, names or identifiers are included in this document.

## What is taking time

The export selected the wrong default workload for a summary-focused product, then multiplied it with inefficient persistence. The current bottleneck is local record processing, with repeated durable file rewrites a major cost. This is not an acceptable production-throughput result or an inherent requirement of exporting summaries.

“Render Markdown” means generating document text, relative links, relationship pages, and navigation indexes and writing them to disk. It is not a UI renderer. The original run creates a Markdown document for every included material record, including events, hints, and tags. It requested Markdown only, so PDF conversion does not explain its rendering time.

On 2026-09-30 the development CLI default changed to `--scope summaries --people profiles`. All-data export now requires explicit selection. Supporting annotations still require inventory because the installed OS omits some summary-body associations; this is more work than 11,732 summary JSON reads. It does not require event/hint history. End-to-end speed on the real database remains unmeasured. The native compiled CLI passed synthetic default scan/dry-run/export, retained body/profile, privacy and moved Markdown/PDF-link checks. The correction is now packaged in `0.14.0-dev`; actual ZIP/default-command and installer acceptance passed on available macOS/Linux runtimes. The original running binary and older release ZIPs retain their previous defaults.

- At the checkpoint the process had run about **21 h 40 min**, including more than **4 h 15 min** in privacy reconciliation. Collection fetching/writing and final identity reads occupied roughly the preceding **17 h 24 min**. That earlier interval includes local work; it is not 17 hours of measured server latency.
- Initial preflight counted approximately **1,906,002 records**, not just summary documents. Staged counts included 689,546 events, 681,479 hints, 284,287 tags and 11,732 summaries. These are pre-final-privacy counts, not accepted archive totals.
- OS request count remains **54,121**, with zero retries and 381 adaptive backoffs. Privacy reconciliation is local. The displayed 11 ms HTTP p95 is the last read-stage value, not a current health measurement.
- The older reconciliation loop reads, sanitizes and rewrites every retained JSON record, even if sanitization leaves it unchanged. `rewriteJSON` writes a temporary file, calls the ordinary synced writer, then renames it over the record. Older Markdown rendering also repeats a canonical rewrite after reference pruning even when no references changed.
- Go 1.27.1's macOS `os.File.Sync` goes through `internal/poll.FD.Fsync`, which uses `fcntl(F_FULLFSYNC)` with an `fsync` fallback for unsupported filesystems. Each ordinary successful file write requests durable storage. Multiplying small flush latency by millions of files is expensive: **10 ms × 1.9 million files is approximately 5.3 hours for one pass**, before scanning/rendering costs. This is arithmetic illustrating the scale, not a measured whole-run attribution or ETA.

## Historical evidence from the original process

| Observation | Result | Interpretation and limit |
| --- | --- | --- |
| Native stack sample, five seconds at 10 ms intervals | 456 sample ticks per thread; 233 `__fcntl`, 23 `__open`, and 11 `__rename` stack occurrences | Consistent with the synchronous rewrite loop. Counts pool thread samples and are not a whole-run CPU or wall-time breakdown. |
| Go symbols recovered from the running executable's own PC table | `rescanKnownCredentials`, `Scanner.Sanitize`, `Scanner.walk`, `Scanner.cleanString`, Gitleaks detection and regex routines | Confirms privacy scanning is active. Stripped native stacks do not unwind every Go call across a blocked syscall. |
| Separate 10.010-second process-counter sample | 1.370 CPU seconds from `ps` (about 0.137 core equivalents); 6,586,368 physical read bytes and 5,820,416 physical write bytes | The process is not saturating a CPU in this sample; substantial time is spent waiting. This does not measure all host disk activity. |
| Process memory | About 4.03 GiB resident; host has 128 GiB RAM and reported low memory pressure | No evidence from this check that host memory exhaustion is the immediate cause. In-memory graph scaling still needs work. |
| Available filesystem space | About 205 GiB | This check did not find an out-of-space condition. It does not guarantee enough space for all later generated files. |
| Existing output/session | Active process and `.partial` folder, no finalized archive | Repeated progress lines alone do not prove completion. Rendering, metadata, link checks and final privacy validation remain. |

The raw `proc_pid_rusage` timing fields were not treated as seconds; the reported CPU delta above comes from two `ps` cumulative CPU-time readings. Byte counters came from the installed SDK's `rusage_info_v4` layout. Idle runtime helper threads were not counted as evidence that the export itself is deadlocked.

## Later observation: rendering is active

At about 21 h 53 min total elapsed, the original process had finished privacy reconciliation and was rendering Markdown: roughly 38,700 of 1,907,456 records, at about 80.5 records/second. Its rendering-only estimate was about 6 h 27 min more. This is a phase estimate, not a completion promise; metadata, link checks and final audits remain afterward. The all-data run renders supporting event/tag/hint records as well as summaries. The original run remains intact.

At the later 22 h 08 min checkpoint, rendering had reached 102,547/1,907,456 records (5.4%), averaging about 73 records/second, with a rendering-only estimate of 6 h 52 min more. The changing rate illustrates why this is not a reliable whole-run completion estimate. Client RSS was about 4.92 GiB.

## Improvements already available in the newer build

1. **Unchanged files stay unchanged.** Since `0.8.8-dev`, privacy reconciliation still performs its full scan but compares original/sanitized values and rewrites only changed records. Rendering likewise rewrites canonical JSON only when reference pruning actually deletes something. Late-credential, privacy-deletion, and file-identity regression tests cover this behavior.
2. **Events/history are optional.** `--scope summaries --people profiles` omits event bodies, hints, signals and other unselected activity collections. It still inventories annotations to preserve summary/profile bodies. This mode is fixture-tested; its real runtime/reduction must be measured when OS is available. The old run is now local-only, so it does not prevent a separate source reader; shared-disk contention still affects timings.
3. **Ranking and graph preparation cost less.** Related-summary ranking retains complete scoring with bounded displayed results. `0.12.2-dev` loads needed descriptions/hosts rather than all supporting JSON and sorts only summary identities. The isolated graph benchmark improved from 84.02 ms / 172.57 MB allocated to 0.92 ms / 0.149 MB for its 2,000-unrelated-body fixture. Allocation totals are not RSS or whole-export speedup.
4. **Newer local phases report counts.** Privacy reconciliation, summary relationships and graph/chronology indexing show completed/total records. The original executable cannot gain these counters in place.

These changes do **not** remove the per-file flush from first writes or every generated Markdown/metadata document. They therefore do not yet prove fast full-history export. None changes the currently running executable.

## Next work and acceptance

- [ ] Finish and validate the existing run; do not treat its staged files as a resumable or finalized archive.
- [x] Make summaries/profile documents the default, with all supported collections explicit. Verify the actual compiled CLI skips event bodies and full supporting inventories, retains bodies/persona history, and preserves valid moved Markdown/PDF links. Explicit people overrides and custom selections remain supported.
- [x] Add aggregate local diagnostics for new exports/rebuilds: phase timing/counts, canonical JSON reads, scans, writes, syncs and unchanged-rewrite skips. Terminal counters distinguish last HTTP latency from current local work. Reports at finalization or ordinary failure contain no record-derived values. See [measurement boundaries and exclusions](EXPORT_SPEC.md#local-performance-diagnostics-0123-dev); operation timing overlaps and is not an exclusive CPU/I/O breakdown.
- [ ] Measure the newer summaries-focused path and a representative complete-history path with the actual OS, without concurrent readers. Compare final eligible record/body/graph counts as well as time and resources.
- [ ] After measuring summary scope, reduce unrelated annotation/supporting-document work only where authoritative associations prove it safe. Keep canonical data, coverage and local link destinations consistent; do not drop files while leaving graph links to them. Evaluate consolidated/indexed presentation of optional all-data history separately from summary documents.
- [ ] Replace excessive per-file flush work with a reviewed checkpoint/durability design. Keep cancellation, disk-full handling and exclusive finalization; validate crash recovery before weakening any existing persistence guarantee. Do not merely disable `Sync` to obtain a benchmark win.
- [ ] Add resumable, checksummed staging and disk-backed inventory/graph storage. The current old run has no supported resume path; switching binaries would require a new run and separate validation.
- [ ] Profile the later graph/rendering/audit stages at actual scale. No reliable remaining-time estimate exists for the old run, and the active rendering phase is not the last phase.

See [the execution checklist](TODO.md) for the independent data-coverage, native platform, viewer and distribution gates. Attachment/audio implementation is still open; its source inspection was paused to investigate this performance issue.

## Diagnostics implementation verification (`0.12.3-dev`)

New export/rebuild reports and terminal counters passed focused persistence, cancellation, exclusive-file collision, phase aggregation, and offline-provenance checks. Full `go test -race ./...` passed (exporter 477.812 seconds), as did vet, actionlint and ZIP/hash validation. Actual packages passed on macOS ARM64, Rosetta AMD64 and isolated Linux ARM64; native Windows remains pending. These timings are test-suite durations, not live export benchmarks. Measurements do not remove file sync, bypass privacy, or add resume support.

## Bounded local writers (`0.13.0-dev`)

A bounded, synthetic filesystem experiment kept exclusive creation and every `File.Sync`, comparing 128 files at one/two/four writers. It used temporary directories and no source OS requests. The original export remained active on the same filesystem, so these are observed development-machine samples, not isolated storage benchmarks or full-history predictions. After implementing a bounded pool, the actual renderer was also measured on 128 staged synthetic annotation records with local diagnostics enabled. Three one-iteration samples per case on Apple M4 Max produced these median results:

| Workload | One writer | Two writers | Four writers |
| --- | ---: | ---: | ---: |
| 4 KiB artifact writes | 112.73 files/s | 162.31 files/s | 206.50 files/s |
| 64 KiB artifact writes | 93.74 files/s | 137.93 files/s | 202.55 files/s |
| Actual staged Markdown renderer with diagnostics | 82.54 records/s | 112.32 records/s | 137.05 records/s |

The instrumented renderer's median wall time was 1.551 s serial, 1.140 s with two writers, and 0.934 s with four. Two writers gave about **36% more records/second (27% less stage time)** in this fixture; four had a wider 0.821–1.677 s range. Diagnostics recorded exactly **144 file-sync calls in every case**, including supporting indexes. Summed sync time can exceed wall time with parallel writers; it is not an exclusive elapsed-time breakdown. The earlier run without diagnostics had medians of 91.01 / 129.57 / 161.38 records/s; differing concurrent filesystem activity prevents attributing the difference solely to instrumentation. The default is two, with `--file-workers 1–4` on export/rebuild. This is bounded manual parallelism for local record documents, not more OS readers or a calibrated universal optimum. First canonical writes, privacy and final audits were excluded from the renderer benchmark; they still cost time. Pool payloads are bounded, oversized documents run alone, and barriers preserve existing error/finalization behavior. See [the exact contract](EXPORT_SPEC.md#bounded-local-markdown-writes-0130-dev).

Reproduce the bounded experiment without auto-calibrating to a large disk workload:

```sh
go test ./internal/exporter -run '^$' \
  -bench '^Benchmark(DurableArtifactWrites|StagedMarkdownWrites)$' \
  -benchtime=1x -count=3 -benchmem
```

Focused race checks passed for serial/parallel content equivalence, file-sync accounting, bounded workers, oversized documents, cancellation, persistence failures and report settings. Actual packaged parallel Markdown exhaustion on a dedicated Linux tmpfs also passed: exit 1, incomplete staging retained, no PDF phase, no success/final directory. Full release evidence is recorded in TODO. No change affects the running original executable.

Release verification completed for the bounded-writer change: full race suite passed (exporter 488.442 seconds), along with static checks, six ZIP/hash checks, macOS ARM64/Rosetta/Linux ARM64 packaged acceptance and the actual Linux exhaustion cases. Windows binaries/test executables compile; native Windows is still unverified. At the final live checkpoint, the original export was still active after about 22 h 29 min, rendering 189,211/1,907,456 records (9.9%) at about 70.3 records/second. Its rendering-only ETA was about 6 h 48 min, excluding later phases; no archive completion is claimed.

## Final privacy-audit allocations (`0.13.1-dev`)

The previous plain-text audit allocated a new 1 MiB read buffer for each file. A bounded fixture of small Markdown documents measured the following medians over three runs of three audit iterations each, before and after reusing one lazy buffer per traversal:

| Fixture | Before allocation per audit | After allocation per audit | Before time | After time |
| --- | ---: | ---: | ---: | ---: |
| 64 text documents | 68.90 MB | 2.77 MB | 20.98 ms | 19.96 ms |
| 512 text documents | 551.18 MB | 14.75 MB | 168.15 ms | 159.11 ms |

The 512-document case reduced allocated bytes by about **97.3%**, while elapsed time improved only about **5.4%**. Allocation totals are not peak resident memory. Repeated warm synthetic documents on the development Mac do not establish cold-disk or complete-history performance; the original export was active on the same filesystem. Fixture creation, scanner initialization and cleanup are excluded; pathname/content checks and directory traversal are included. All privacy checks still execute.

The implementation keeps 1 MiB reads and a 4 KiB overlap with fresh state for every file. It scans only the bytes actually returned by each read, including after a longer or rejected file. JSON and JSONL still scan decoded strings/numbers; PDFs still receive semantic validation. Empty/directory-only traversals now observe cancellation. Regression tests cover chunk-boundary credentials, email and denied hosts, separate fragments in adjacent files, stale buffer bytes, empty files, escaped JSON credentials, numeric financial values, later JSONL records, malformed JSON/PDF, PDF text leakage, private filenames and symlinks.

```sh
go test ./internal/exporter -run '^$' \
  -bench '^BenchmarkFinalAuditSmallDocuments$' -benchtime=3x -count=3 -benchmem
```

Source privacy rules, disk sync, output layout and resume support are unchanged. Remaining scanner, decoded-token, directory-walk and graph costs need separate measurements; this is not a promise that the original running export will finish sooner.

The decoded-JSON path now reuses a separate 32 KiB buffered reader with a fresh decoder for each file. A file-backed token-only fixture reduced underlying read calls, including EOF probes, from 4 to 2 for one row and from 24 to 3 for 512 rows. This isolates reader behavior, not policy scanning. The 512-record full JSON audit stayed around 76–78 ms; no material full-audit speedup is established. The production reader buffer is reused across files; the isolated comparison allocates one for each iteration.

New reset/boundary tests also exposed an existing integrity bug: `Decoder.Token` can return EOF inside an unfinished container. The audit now rejects EOF with an open object/array. Regressions cover truncated keys/values/containers, malformed trailing input, incomplete later JSONL values, complete multi-value streams, duplicate-key secrets, fresh reader/decoder state after rejection, and secrets crossing the 32 KiB refill boundary. These changes retain all decoded-token privacy checks.

Package verification for `0.13.1-dev` passed on macOS ARM64 (66.855 seconds including installers), Rosetta AMD64 (47.485 seconds), and isolated Linux ARM64, which also passed the new audit regressions and actual disk-exhaustion tests. All six ZIP checksums and four-file layouts were checked. Windows binaries and test executables compile; native Windows and real-history measurements remain open. Full combined-source `go test -race ./...` passed (exporter 477.861 seconds), along with vet, actionlint and zero reachable vulnerability findings. No binary upload or Actions job occurred.

Latest live checkpoint: after about 24 h 11 min, the original executable was rendering 624,884/1,907,456 records (32.8%), at about 71.1 records/second. The rendering-only estimate was 5 h 01 min; later phases are excluded. Its roughly 4.92 GiB RSS and unchanged HTTP counters are observations, not source health or whole-run resource guarantees. The earlier 23 h 49 min filesystem check found approximately 199 GiB free. The final directory is absent and staging remains active.

## Transactional recovery storage experiment

[RECOVERY_DESIGN.md](RECOVERY_DESIGN.md) records the next persistence design and a bounded storage comparison: 128 roughly 4 KiB synthetic records took a median 1.188 s with current per-file sync, versus 0.142 s in encrypted SQLite transactions of up to 50 with EXTRA/fullfsync enabled. Single-record SQLite commits were slower (3.493 s). This establishes batching as a useful candidate, not a whole-export speedup or a deployed durability change. The actual archive still needs its ordinary files; materialization costs cannot be omitted from acceptance.

Abrupt synthetic process-exit tests on macOS/Linux ARM64 kept records and accumulated scanner/cursor state atomic, including a spilled uncommitted transaction. Tests and the full remaining implementation checklist are in the design. Production files, the running binary, source OS and release ZIPs are unchanged.

The internal transactional store is now implemented on the persistent workspace/key layer. Its current 128-record benchmark measured median 3.715 / 0.309 / 0.120 seconds for commits of 1 / 16 / up to 50 records, including encrypted envelopes and authenticated change history. macOS/Rosetta/Linux tests passed, including abrupt exits, corruption rejection, preserved SQLite locks and actual Linux filesystem exhaustion. Native Windows and exporter integration remain pending. Initialization, reopen verification and ordinary archive materialization are outside the benchmark; adding this store alongside existing file writes would add work, so no whole-export speedup or durability change is claimed. See [the store contract and evidence](RECOVERY_DESIGN.md#transactional-store-implemented-not-connected-to-export).

## Live summaries preflight and persona-query costs (2026-09-30)

The old export was confirmed in local rendering, with no open TCP connection and unchanged HTTP counters. Its render/final-validation path contains no further source reads. The earlier blanket deferral of live probes was narrowed: bounded sequential read-only probes can run while this old process does local work; a second large archive writer remains deferred to avoid storage contention. The original process was never stopped or modified.

- The actual `0.14.0-dev` CLI, with default scope and a 32-batch/15-second calibration budget, counted 11,744 summaries, 74,086 annotations, 4,405 persons and 13 pipelines: **90,248 core records** before referenced-record/history expansion. It read 532 records in repeated samples across 32 batches in 162 ms; repeated/cache-warm samples are not unique full-history throughput. The 4m27s–26m52s printed estimate is an uncalibrated model, not an accepted export time. The post-calibration health check passed; no new archive or Desktop closure occurred.
- The profile-read candidate's bounded preview read 4,406 people and retained 1,373, omitting 3,033 (68.8%) before privacy processing. It completed in about 21 seconds: 9,008 requests including its preflight, zero retries, one backoff following a 935 ms response, and final displayed p95 3 ms. Counts differ from the earlier scan because the source continues changing. All 4,406 projected-person event-count lookups were skipped. Missing person-to-summary projections remain unknown.
- Full profile-history export combines both types in one query, with separate-stream fallback for incompatible responses or tied timestamp boundaries. For short histories this reduces three former per-person requests to one in profiles mode. A bounded current-account comparison matched 427 IDs/types/timestamps/texts, all with nonempty bodies, using nine combined versus ten single-type history requests. The final comparison and health probes took 25 requests, zero retries/backoffs, p95 8.17 ms and peak 12.41 ms; it was a warm local sample, not the complete export.

Logs are ignored local aggregate reports: `exports/summary-default-live-dry-run.log`, `exports/profile-reads-live-preview.log`, and `exports/profile-history-live-comparison.log`. These measurements support a smaller, less wasteful source-read path. They do not prove full narrative attachment coverage, final privacy omissions, archive speed, or production readiness.

## Historical status and recovery boundary (2026-09-30)

At about 25 h 32 min total, the original export had written 978,243/1,907,456 Markdown records (51.3%), averaging 71.5 records/second. Its rendering-only estimate was another 3 h 37 min, excluding later metadata/link/audit work. HTTP counts stayed unchanged; RSS was about 4.92 GiB and the recent capacity check found approximately 195 GiB free. The run remains active and unfinalized. This is unacceptable summary-export performance, explained by excessive default scope and local file-processing costs; increasing OS request concurrency cannot accelerate this phase.

The private recovery adapter now checkpoints actual records and scanner/graph evidence after source collection, and can replay all local phases into a new destination with zero OS requests. Synthetic tests cover privacy and graph equivalence, cancellation, incomplete capture rejection and abrupt process exit on available macOS/Linux runtimes. It remains outside the CLI and current download packages. It adds storage work beside existing writes, so it is not counted as a performance optimization. See [the exact boundary and outstanding work](RECOVERY_DESIGN.md#completed-source-adapter-and-cli).

The product acceptance priority remains the actual newer summaries/profile export: final bodies, links, exclusions and wall time, including the annotation inventory and referenced records. The live preview and cached request samples cannot establish that result. Batched persistence, fewer unnecessary generated files and resume integration are implementation work; none retroactively accelerates the original executable.

The decision to defer every new archive writer was narrowed after checking that the old run is local-only and about 195 GiB remained free: a focused export can proceed with serialized adaptive OS reads, while explicitly reporting shared-disk contention. Its first attempt on 2026-09-30 ended during discovery in 0.22 seconds without creating an archive. Neither the previous port nor ports 39300–39333 responded, and no OS process was present. Docker Desktop later also became unavailable. No application was relaunched; the staging relaunch preference is pending. This failed startup provides no export-throughput measurement.

Completed-source CLI recovery is now packaged in `0.15.0-dev` with explicit workspace/key paths, authenticated inspection and new-output replay. Full race and available Mac/package/installer checks passed. It retains per-file sync and adds checkpoint writes, so it is still not a speed optimization. Default key locations, cleanup, interrupted source reads, artifact skipping and actual large-history acceptance remain open.

## Focused live run and corrected relationship source (2026-09-30)

The prior staging-launch question was a precaution rather than a new authorization requirement. The existing user instruction already authorized launching OS when unavailable. The explicit installed staging bundle was launched without closing Desktop or the original exporter; doctor verified `12.6.29-staging` on port 39301. Actual `0.15.0-dev` then started a separate default summaries/profile export with four explicit read-only SDK caches, Markdown and automatic metadata. Its ignored log is `exports/live-summaries-20260930-attempt2.log`. The original export remains active on the same filesystem, so this is a shared-load measurement.

Preflight counted 11,745 summaries, 74,093 annotations, 4,413 persons and 13 pipelines: 90,264 core records before referenced/history expansion. Summary fetching completed in roughly three minutes. At 17m12s in annotation fetching, 70,709/74,093 annotations had been processed, with cumulative file-sync time of 11m19.731s across source-stage writes. Recent HTTP p95 was 9 ms, but several outlier reads took seconds and triggered 22 adaptive backoffs; zero retries had occurred. These observations exclude later graph/render/validation work and are not final archive counts.

A three-second native sample of staging OS showed ObjectBox loaded. Refreshed repository refs then revealed the missing current read contract: 87 association servers with per-side count/list/bulk APIs. The September SDK already uses these junctions to retrieve summary bodies, because migration leaves embedded relationship maps absent. This is a source-research correction, not a completed exporter optimization: the running baseline still uses the earlier embedded/cache route. The exact contracts, bounds, current source revisions and integration gates are in [JUNCTION_API.md](JUNCTION_API.md). After verifying current junction closure, summaries scope can avoid unrelated annotation inventory; that change must preserve profiles, all attached bodies, privacy dependencies and graph links.

## Linked annotation retrieval — earlier implementation checkpoint

The current junction API investigation exposed the reason a summaries-focused export still inventoried roughly 74,000 annotations: the older traversal could not identify all body/profile attachments from snapshots. The new exporter traverses current summary/person association endpoints and fetches only referenced annotations once both owner sets reconcile. Older servers retain the full fallback. Nine association families also supply summary labels/origins and person/pipeline memberships. No event bodies are introduced. See [the source and implementation contract](JUNCTION_API.md).

Synthetic acceptance retains both required body/profile annotations while omitting 20 unrelated annotations, with complete current core relationship evidence, valid links and repeated offline rebuilding/recovery. This is a behavioral check, not a live reduction estimate. Canonical association records add their own writes, so fewer annotation records alone cannot establish a net runtime win. First-write sync, generated Markdown/metadata files, privacy audit volume and final archive checks remain separate bottlenecks.

The older `0.15.0-dev` focused baseline completed Markdown and the first output audit, then entered native/portable metadata. It had about 195,000 measured writes before metadata, compared with about 91,000 staged material records. It still uses historical cache recovery and the full annotation inventory. Its shared disk load includes the original all-data export; neither run has been replaced or interrupted. Final timings and acceptance remain pending.

A bounded live cardinality probe subsequently completed 320 ID/count requests in **0.88 s**, zero retries/backoffs, recent p95 roughly **4 ms**. Its 30-summary sample implies substantial supporting association volume; extrapolation is unreliable but sufficient to reject the assumption that adding one JSON plus one Markdown file per association is free. The candidate has correct graph traversal in synthetic package acceptance, but has not proved a faster live export. Consolidated association evidence and a better persistence strategy remain immediate performance work.

## Finalized summaries-focused baseline — 2026-09-30

The actual `0.15.0-dev` executable finished in **3,944.609 seconds (65m45s)**, exit **2**, with a finalized **partial** archive at `exports/live-summaries-20260930-attempt2`. No partial sibling remains. The original all-data exporter was still writing to the same disk; timings are not an isolated benchmark. The optional encrypted recovery workspace was disabled, so checkpoint duplication does not explain this run.

| Measured phase | Wall time |
| --- | ---: |
| Fetch summary records (including persistence) | 2m49s |
| Fetch annotations (including persistence) | 18m01s |
| Markdown rendering | 14m00s |
| Both filtered-output audits combined | 20m43s |
| Native and portable metadata | 5m40s |
| Privacy reconciliation | 1m26s |
| Link validation | 1m05s |
| Reconstruction evidence | 32s |

Other source, cache and graph phases account for the remaining time. The report measured **218,956 writes and 218,956 syncs**, about **964 MB** of logical output, and **36m39s summed sync time**. Sync time overlaps writes and concurrent writers and must not be added to wall time. There were 232,405 canonical JSON reads (about 488 MB); 91,071 privacy rewrites and 87,998 rendering rewrites were correctly skipped as unchanged. Peak RSS was **980,172,800 bytes (about 935 MiB)**. The final manifest timestamp precedes its closing audit; the wrapper elapsed time and phase report measure the longer actual run.

Independent finalized-archive acceptance passed in **79.85 s**: 87,998 included records reconcile with all stored decisions/hashes; Markdown local links resolve; 24,992 attached annotation bodies match rendered text. Retained summaries: **11,742**; with nonempty summary-type bodies: **10,515**; with any nonempty annotation: **10,867**. Every summary-type body link in this baseline came from historical cache evidence. The graph has 92,188 edges, including 75,279 historical edges. Consistency does not establish complete current source coverage.

Person selection retained **1,381 of 4,413**, a **68.7% reduction** (3,032 intentional omissions). All retained persons have profile history, with 5,171 history links. This is selection, not deduplication/merging. Three summaries and 39 annotations were withheld; 43 person references were unavailable; the annotation inventory grew by 13 during the read interval. Historical links and absent core projections also keep this archive partial. The original all-data export is still running and has not acquired these changes.

The next acceptance target is a current-junction export with fewer supporting artifacts, improved persistence and less repeated audit work. The junction integration passed the full race suite (exporter 534.994 s). Candidate `0.16.1-dev` adds a reconstruction-state compatibility guard, verified by focused export/rebuild/capture/archive checks (141.306 s), vet, six ZIP/hash checks and available Mac package tests, but per-file sync and full audits remain. **Do not claim this candidate is already a fast or complete live migration.**

## Reusing unchanged audit results

Candidate `0.16.2-dev` avoids repeating expensive content scanning when the second traversal rereads identical bytes under identical scanner inputs. It still reads/hashes full files, checks every pathname, fully scans changed/new content, and repeats PDF semantic checks. Learned credentials and policy/domain changes invalidate the cache; no audit cache is persisted. Details and limits are in [EXPORT_SPEC.md](EXPORT_SPEC.md#reusing-unchanged-final-audit-results-0162-dev).

A 512-file synthetic mixed JSON/Markdown comparison measured median **181.23 ms** for a full scan versus **11.22 ms** for checksum-verified reuse (about **93.8% less time for that repeated phase**). Allocated bytes were approximately 22.54 MB versus 1.41 MB. Three samples of three iterations each on Apple M4 Max; fixture setup and prerequisite scanning are excluded, and these are warm local files. Existing full-scan benchmarks now explicitly clear reuse state between iterations to preserve their meaning. This does not establish whole-export speedup.

The read-only real-archive comparison **passed** against the finalized focused baseline: **215,882 files / 961,039,685 bytes**. The full scan took **683.411 s (11m23s)**; the repeated checksum-verified pass took **61.377 s (1m01s)**, about **91.0% less time** for that pass. All 215,882 second-pass file contents reused successful checks only after rereading their full bytes; no content scan was necessary because this reader modified nothing. The private cache retained 22,045,228 pathname bytes plus entries/digests/map overhead. This is not a measured peak-RSS bound.

Full content scanning accounted for 630.541 s of the first pass; checksum reads accounted for 36.593 s of the second. Other wall time includes pathname scanning, opening/statting files, directory traversal and accounting. The measurement includes no OS request, output write or native metadata change. It uses the default policy but cannot reconstruct credentials learned by the original process, so this is a phase comparison, not privacy recertification or source completeness acceptance. The original all-data exporter and regression work shared the host/filesystem. A real export also introduces new sidecars/reconstruction/manifest files between passes; those still need full scans, so the measured savings cannot simply be subtracted from the 65m45s baseline. The ignored aggregate log is `exports/live-audit-reuse-comparison.log`.

Candidate `0.16.2-dev` is packaged locally for all six targets. Full race regression passed (exporter 508.055 s); focused audit/performance tests passed in 31.593 s. Actual packaged Mac ARM64 export/default/projected/current-junction/rebuild/recovery checks passed in 31.132 s with race checks, and Rosetta CLI/default/current-junction checks passed in 8.689 s. Final ZIP contents/hashes/instructions and executable bytes were verified. Current Linux/Windows runtime acceptance is still pending. The new private cache does not change record sync calls or accelerate an already running older executable.

```sh
go test ./internal/exporter -run '^$' \
  -bench '^BenchmarkAuditUnchangedOutput$' -benchtime=3x -count=3 -benchmem
PIECES_EXPORT_LIVE_AUDIT_ARCHIVE=/absolute/finalized/archive \
  go test ./internal/exporter -run '^TestLiveAuditUnchangedArchive$' -count=1 -v -timeout 26m
```

## Canonical transaction adapter — 2026-10-01

The source/privacy/render/recovery code now opens records through a shared access boundary. This removes the direct filename assumptions that prevented grouped storage. An internal encrypted transaction backend supports bounded pending writes, indexed current-record reads, replacement and explicit tombstones; it passes fixture export/capture/replay and privacy checks. It is not selected by the CLI until public grouping/navigation and workspace lifecycle are complete. The released/local `0.16.2-dev` packages and original live exporter are unchanged.

The actual capture adapters wrote and reread 128 roughly 4 KiB records in a median **1.822 s** with individual synced files versus **0.128 s** with three encrypted transactions, about **93.0% less time for this capture/read workload**. Three samples of three iterations each on the development Mac; allocations rose from roughly 1.69 MB to 5.66 MB. File-helper sync counters exclude SQLite VFS flushes; SQLite's durability settings remain enabled. Initialization, key creation, public file materialization, filtering, graph work, metadata, audits and cleanup are outside this comparison. Individual public files would still incur their own writes, so grouping remains necessary before another whole-export speed claim. Full details and reproduction are in [RECOVERY_DESIGN.md](RECOVERY_DESIGN.md#canonical-record-access-and-transaction-backend-2026-10-01).

The first focused access-layer regression run hit the existing scanner deadline during a large host time jump. The unchanged isolated retry passed in 8.554 s; no scanning deadline was loosened. The access-layer full race suite then passed (exporter 538.312 s). This interruption is separate from the new adapter's correctness/performance results.

## Grouped public association output — 2026-10-01

The CLI now uses encrypted transactions for association staging and materializes approved records into bounded JSONL chunks with matching navigation pages. Summaries/profile histories keep their individual files. Format 6 separates record references from document paths, records exact canonical row offsets/lengths/hashes, and verifies them during offline import. This is connected to export/rebuild/completed-source replay; it is no longer an adapter-only experiment. Ordinary source records still incur their existing per-file writes/syncs.

A 123-association fixture now creates **three JSONL files and three Markdown pages**, versus 246 individual canonical/Markdown files: **97.6% fewer files for that association family**. All records, numeric metadata, two endpoint edges per association, summary bodies and profile history survive export, completed-source replay, repeated offline rebuilding and moving the archive. This is a subset artifact-count result, not a whole-export timing claim. Other supporting documents and graph/index files remain.

The full source-package race suite passed (exporter **647.125 s**); final boundary tests passed (**26.854 s**), including byte-limited groups, a larger single row, encrypted staging checks, normal cleanup, cancellation, closed-store/publication errors, legacy upgrades, missing/duplicate/truncated rows, unsafe files and contradictory graph identities even after checksums are updated. Mac ARM64 actual-package checks passed (**55.541 s**, with race checks), Rosetta package checks passed (**20.176 s**), and a final current-source Mac package smoke check passed (**1.136 s**). Vet/diff checks pass. Six binary-only `0.17.0-dev` ZIPs were verified locally; current native Windows/Linux runtime execution, publication and crash-orphan cleanup remain pending.

A measured default summaries/profiles export is now running against the already-running staging OS, with Markdown, automatic metadata and no historical SDK caches. Output is `exports/live-summaries-20261001-current-junctions`; aggregate logs and wrapper timing are ignored alongside it. Preflight counted **11,746 summaries, 74,118 total annotations, 4,335 persons and 13 pipelines**. That annotation count is the available inventory, not the count this linked-only traversal promises to fetch. Early summary fetching ran around 72 records/s including persistence, with two adaptive backoffs. These are startup observations, not finalized coverage or a duration result.

The original all-data exporter remains untouched and shares the filesystem. Its latest checkpoint was **1,862,542/1,907,456 Markdown records (97.6%)**, phase elapsed 7h47m, render-only ETA 11m; later phases remain. Source request counters were unchanged. Approximately **169 GiB** remained free when the new run began. Final live source counts, bodies, omissions, graph/link validity, output file volume, wall time and peak memory remain required before a speed or completeness claim.
