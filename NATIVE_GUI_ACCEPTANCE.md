# Native file-manager and PDF acceptance

These checks used synthetic fixtures on macOS 15.7.3 (24G419), Apple M4 Max, on 2026-09-29. The live export continued independently. No private records were opened in a viewer, and no live OS requests, lifecycle actions, permission changes, or application preference changes were needed.

## Finder and Spotlight

| Check | Observed result |
| --- | --- |
| Ascending Name order | `000000` newer summary precedes `000001` older summary; relationship siblings remain adjacent. |
| Finder Get Info tags | Original tag, normalized source tag, and normalized website tag were visible. |
| Spotlight `kMDItemUserTags` and `kMDItemFinderComment` | Both returned the expected approved values for the Markdown/PDF fixture. |
| PDF description/title/keywords | Finder More Info displayed the embedded PDF values. |
| Editable Finder Get Info Comments | Empty, despite successful comment-xattr readback and Spotlight lookup. This remains a limitation. |

`native_status: applied` means the writer wrote and read back the requested attributes. It does not certify Finder's editable Comments field, Spotlight indexing on every volume, or preservation after copying/ZIP extraction. Portable metadata sidecars and descriptions in the document remain available. No Finder automation or global indexing changes were added to work around the Comments field.

## PDF navigation

The former implementation wrote relative paths to Markdown using PDF URI actions. The files existed and automated path checks passed, but Preview's “Open in Another App” confirmation did not open them. Directly opening the Markdown file did work in the installed handler. File existence alone was therefore insufficient acceptance evidence.

The current implementation uses relative `GoToR` file actions to the mirrored PDFs. Each action includes a Unicode filename (`UF`) and a MacRoman filename (`F`). Tests of PDFKit/Preview on this machine showed that it ignored `UF` and interpreted `F` as MacRoman. Plain UTF-8, UTF-16, or percent-escaped Unicode filenames failed in this field. The filenames themselves retain their Unicode spelling; paths contain no absolute export location.

- Local document links open the destination PDF's first page. Markdown heading fragments do not become PDF destinations.
- JSON and other non-PDF destinations retain visible text without a PDF link. Their Markdown links remain available.
- A filename outside MacRoman's repertoire retains an unlinked PDF label and produces a warning plus `unsupported_pdf_link_filename`/partial status. This prevents a knowingly incompatible clickable destination. Markdown preserves the complete graph.
- External approved HTTP/HTTPS/mail links remain URI actions. The exporter never tests them by contacting the destination.
- No Launch, executable, JavaScript, or chained action is generated. File actions must stay inside the export, use matching filename encodings, and resolve to an existing PDF before finalization.

The renderer reserves fixed-size annotation slots in its pinned PDF writer, then replaces each with an equal-length file action. Object offsets and xref entries remain unchanged. Missing, duplicate, or unexpected slots fail closed. This code only handles newly generated PDFs; it is not a general-purpose PDF editor or importer.

### Actual viewer result and remaining restriction

On a relocated Go-generated archive, Preview navigated from the accented summary to `index.pdf` and back after both documents had been opened directly. Before the destination had been opened, Preview displayed a file-permission alert. The exporter did not widen filesystem permissions or change Preview security settings. This verifies relative path/encoding/navigation after access is granted; it does **not** establish frictionless first-use navigation to every document in a large archive.

Use Markdown for the complete navigable archive. PDF mode remains subject to viewer access rules and filename limitations. Acrobat/Edge and a Linux viewer still require actual GUI acceptance. Do not mark the cross-platform PDF release gate complete from parser tests or macOS results alone.

## Reproduction and regression checks

1. Generate a fresh synthetic archive with `PIECES_EXPORT_FIXTURE_OUTPUT="$PWD/exports/qa-native-NEW" go test ./internal/exporter -run '^TestReadableSummaryGraphPDFAndMetadata$' -count=1`. The destination must not already exist.
2. Move the whole folder. Inspect ascending filename order and summary/relationship metadata in Finder. Check Spotlight separately using `mdls`; do not assume native readback means the UI displayed it.
3. Open `index.pdf` and follow a summary link. Record any permission alert. Open the target explicitly if needed, then verify the index backlink and return link. Include an accented filename and nested persona/pipeline paths.
4. Inspect pages visually and parse all PDFs independently. The initial Go fixture had 28 PDFs and 144 existing PDF file links; pypdf 6.19.0 strict parsing accepted every document without repair. A packaged rebuild of the legacy fixture separately produced 28 PDFs and 149 existing file links, all accepted by the same strict parser. Poppler rendering of its final two-page summary was visually reviewed with no clipping, including the plain JSON label and separate clickable index label.
5. Run `go test ./internal/exporter -run 'TestPDF|TestReadableSummaryGraphPDFAndMetadata|TestOrganizedCanonicalPathsSharedMembershipAndPrivacy' -count=1`. Regressions cover moved folders, accents/spaces/parentheses/percent/hash characters, Unicode fallback, retained labels, missing targets, root escape, Windows absolute paths, unsupported actions, filename mismatch, and action-slot corruption.

The CLI packages remain development builds until the live export, recovery/reconciliation, other native runtimes/viewers, and configured download/install path pass the broader checklist in [TODO.md](TODO.md).
