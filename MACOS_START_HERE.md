# Start here: Pieces Export for Mac

Version **0.18.0-rc2**. This early-access build is **Developer ID signed by Mesh Intelligent Technologies, Inc. and notarized by Apple**. It has passed Mac export/recovery checks. Keep the Mac online for first launch so it can retrieve Apple's notarization ticket. The complete consumer instructions are in `README.md` inside each platform ZIP.

1. Check **Apple menu → About This Mac**. An M-series chip needs the ZIP ending in `darwin_arm64_notarized.zip`; an Intel processor needs `darwin_amd64_notarized.zip`. Intel was tested under Rosetta on an Apple-silicon Mac, not on native Intel hardware. Choose only one ZIP.
2. Save the ZIP and matching `.sha256` file together. In Terminal, type `cd `, drag that folder into the window and press Return. Verify the Apple-silicon ZIP with the command below (substitute `amd64` for `arm64` on Intel):

   ```sh
   shasum -a 256 -c pieces-export_0.18.0-rc2_darwin_arm64_notarized.zip.sha256
   ```

   Require `OK`; stop on a mismatch. Extract the ZIP, then use `cd ` and drag the extracted folder into Terminal.
3. Run `./pieces-export version` and check it reports `0.18.0-rc2`. If extraction lost execution permission, run `chmod u+x ./pieces-export`. If macOS blocks the tool, confirm you are online and verified the current notarized download, then contact the sender with the exact message if it persists. Do not disable system security globally. The full README includes signature/Gatekeeper checks.
4. Open Pieces OS manually and wait for it to be ready. Run:

   ```sh
   caffeinate -i ./pieces-export export --format markdown \
     --launch-os=false --close-desktop=false \
     --output ./my-pieces-export \
     --work ./private-work --recovery-keys ./private-keys
   ```

   Review the scan and answer `Y` or press Return. The command leaves Pieces Desktop open. It reads the local source without deleting it. Use a writable local disk with room for the export and private recovery files. Keep the terminal open and Mac awake; don't close the laptop lid or update/reinstall Pieces OS during the run. Large exports can take tens of minutes.
5. Open `my-pieces-export/index.md` in a Markdown viewer with relative-link support. Keep the whole export folder together. Timestamps use the Mac's system timezone automatically. Exact source timestamps remain in JSON. Summaries, persona histories and linked pipeline folders are included; this preview's supported scope excludes PDFs, audio/attachments and complete raw event history.

Exit **0** with a finalized archive means completed for the selected scope; **2** means a finalized partial archive with reported limitations; **1** means failure. Interactive cancellation can also return 0 without an archive. Read `coverage.md` and `manifest.json`. A `.partial` directory is unfinished and should not be presented as a completed export.

For recovery, retain both private folders and choose a new destination:

```sh
./pieces-export resume --work ./private-work --recovery-keys ./private-keys --inspect
caffeinate -i ./pieces-export resume --work ./private-work \
  --recovery-keys ./private-keys --output ./recovered-export
```

Don't send your recovery workspace or keys to anyone. Secret filtering is best effort; review before sharing. Banking/adult-site filters need separately configured domain lists. For unattended runs, add `--yes`; progress is text on stderr, final/preflight information is on stdout, and `manifest.json` is the structured result. The full README covers logging and exit-code handling for agents.

To report a problem, provide the tool version, macOS version/chip and last stage/error after reviewing them for private information. You do not need to send the export itself. Keep the tool folder until your export and recovery folders have been moved somewhere permanent.
