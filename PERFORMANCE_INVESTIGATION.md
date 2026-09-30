# Live export performance investigation

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

## Evidence from the active process

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
2. **Events/history are optional.** `--scope summaries --people profiles` omits event bodies, hints, signals and other unselected activity collections. It still inventories annotations to preserve summary/profile bodies. This mode is fixture-tested; its real runtime/reduction must be measured after the current run finishes.
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

## Latest status and recovery boundary (2026-09-30)

At about 25 h 32 min total, the original export had written 978,243/1,907,456 Markdown records (51.3%), averaging 71.5 records/second. Its rendering-only estimate was another 3 h 37 min, excluding later metadata/link/audit work. HTTP counts stayed unchanged; RSS was about 4.92 GiB and the recent capacity check found approximately 195 GiB free. The run remains active and unfinalized. This is unacceptable summary-export performance, explained by excessive default scope and local file-processing costs; increasing OS request concurrency cannot accelerate this phase.

The private recovery adapter now checkpoints actual records and scanner/graph evidence after source collection, and can replay all local phases into a new destination with zero OS requests. Synthetic tests cover privacy and graph equivalence, cancellation, incomplete capture rejection and abrupt process exit on available macOS/Linux runtimes. It remains outside the CLI and current download packages. It adds storage work beside existing writes, so it is not counted as a performance optimization. See [the exact boundary and outstanding work](RECOVERY_DESIGN.md#completed-source-adapter-and-cli).

The product acceptance priority remains the actual newer summaries/profile export: final bodies, links, exclusions and wall time, including the annotation inventory and referenced records. The live preview and cached request samples cannot establish that result. Batched persistence, fewer unnecessary generated files and resume integration are implementation work; none retroactively accelerates the original executable.

The decision to defer every new archive writer was narrowed after checking that the old run is local-only and about 195 GiB remained free: a focused export can proceed with serialized adaptive OS reads, while explicitly reporting shared-disk contention. Its first attempt on 2026-09-30 ended during discovery in 0.22 seconds without creating an archive. Neither the previous port nor ports 39300–39333 responded, and no OS process was present. Docker Desktop later also became unavailable. No application was relaunched; the staging relaunch preference is pending. This failed startup provides no export-throughput measurement.

Completed-source CLI recovery is now packaged in `0.15.0-dev` with explicit workspace/key paths, authenticated inspection and new-output replay. Full race and available Mac/package/installer checks passed. It retains per-file sync and adds checkpoint writes, so it is still not a speed optimization. Default key locations, cleanup, interrupted source reads, artifact skipping and actual large-history acceptance remain open.
