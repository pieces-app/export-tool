# Live export performance investigation

Observed 2026-09-30 against the original running `0.4.1-dev` export. The user requested that this run finish; it was not stopped, restarted, signalled, or replaced. The investigation used one five-second native stack sample, read-only process/resource counters, existing source/history, and filesystem-capacity checks. No OS HTTP reader or source mutation was added. Stack/resource artifacts are ignored local files under `exports/`; no record bodies, names or identifiers are included in this document.

## What is taking time

The current bottleneck is local record processing, with repeated durable file rewrites a major cost. This is not an acceptable production-throughput result.

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

## Improvements already available in the newer build

1. **Unchanged files stay unchanged.** Since `0.8.8-dev`, privacy reconciliation still performs its full scan but compares original/sanitized values and rewrites only changed records. Rendering likewise rewrites canonical JSON only when reference pruning actually deletes something. Late-credential, privacy-deletion, and file-identity regression tests cover this behavior.
2. **Events/history are optional.** `--scope summaries --people profiles` omits event bodies, hints, signals and other unselected activity collections. It still inventories annotations to preserve summary/profile bodies. This mode is fixture-tested; its real runtime/reduction must be measured after the current run finishes.
3. **Ranking and graph preparation cost less.** Related-summary ranking retains complete scoring with bounded displayed results. `0.12.2-dev` loads needed descriptions/hosts rather than all supporting JSON and sorts only summary identities. The isolated graph benchmark improved from 84.02 ms / 172.57 MB allocated to 0.92 ms / 0.149 MB for its 2,000-unrelated-body fixture. Allocation totals are not RSS or whole-export speedup.
4. **Newer local phases report counts.** Privacy reconciliation, summary relationships and graph/chronology indexing show completed/total records. The original executable cannot gain these counters in place.

These changes do **not** remove the per-file flush from first writes or every generated Markdown/metadata document. They therefore do not yet prove fast full-history export. None changes the currently running executable.

## Next work and acceptance

- [ ] Finish and validate the existing run; do not treat its staged files as a resumable or finalized archive.
- [ ] Record per-stage elapsed time, file/read/write/flush counts and durations in an aggregate performance report for new runs. Separate source wait, scanner work, local persistence, graph preparation, rendering and validation. Avoid record-derived values in diagnostics.
- [ ] Measure the newer summaries-focused path and a representative complete-history path with the actual OS, without concurrent readers. Compare final eligible record/body/graph counts as well as time and resources.
- [ ] Replace excessive per-file flush work with a reviewed checkpoint/durability design. Keep cancellation, disk-full handling and exclusive finalization; validate crash recovery before weakening any existing persistence guarantee. Do not merely disable `Sync` to obtain a benchmark win.
- [ ] Add resumable, checksummed staging and disk-backed inventory/graph storage. The current old run has no supported resume path; switching binaries would require a new run and separate validation.
- [ ] Profile the later graph/rendering/audit stages at actual scale. No reliable remaining-time estimate exists for the old run, and the current privacy phase is not the last phase.

See [the execution checklist](TODO.md) for the independent data-coverage, native platform, viewer and distribution gates. Attachment/audio implementation is still open; its source inspection was paused to investigate this performance issue.
