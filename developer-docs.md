# Download Manager — Developer Guide

> Clean, reliable resumable downloads. This guide explains lifecycle, chunk lifecycle, resume semantics, and best practices for contributors.

---

## Quick Summary (high level)

1. **Create DownloadManager** — construct and configure limits (MaxGoroutines, storage, output paths).
2. **Create Task** — create task metadata (ID, URL, DIR, TotalSize, ChunkCount, etc.) and persist it.
3. **Calculate Chunks** — split file into `N` chunks (store canonical chunk list, each with index, start, end, filepath, expected size, completed flag). Persist the full chunk list under the task ID.
4. **Start Download** — for each chunk spawn worker(s) and:
   * Save chunk start state (optional)
   * Download chunk to `task.DIR + ".tmp"/chunk{index}`
   * Verify `os.Stat` size matches expected `ChunkTotalSize`
   * Mark chunk `Completed=true` and persist that single chunk update
5. **Resume Download** — given `taskID`:
   * Verify completed chunks
   * Remove incomplete chunk files from disk (they will be redownloaded)
   * Download remaining chunks
   * After final chunk verifies, merge chunks and cleanup chunk info

---

## Resume Semantics (important note)

Currently, **resume only counts chunks that are fully completed**.  
If a download is stopped/crashed (e.g. with `Ctrl+C`), **partially downloaded chunks are discarded** and will be redownloaded.  

For example:
- If reported progress was **68%**, but the active chunk was incomplete, only completed chunks are counted.  
- After resume, progress might be **~60%**, because the incomplete chunk gets removed and re-fetched.

---

## TODO / Roadmap

1. **Support partial-chunk resume**  
   - Allow resuming *within* a chunk so partially downloaded data is not discarded on process stop/crash.  
   - Persist per-chunk offset and partial-file metadata; on resume continue from the saved offset.  
   - Consider using per-chunk checksums or blob manifests to validate partial data.

2. **UI feature**  
   - Provide a basic UI (CLI TUI and/or web dashboard) for viewing active downloads, per-chunk progress, and controls (pause/resume/cancel).

3. **Expose interface for UI**  
   - Design and export a clean Go interface / API for UIs to query progress, control downloads, and subscribe to events (progress updates, errors, completion).  
   - Consider both polling endpoints and event-based websockets/callbacks.

4. **Fix progress calculation anomalies**  
   - Investigate and correct cases where progress can exceed 100%. Likely causes: double-counting session bytes, race conditions updating persistent state, or mismatch between `TotalSize` and sum of chunk sizes.  
   - Add clamping and consistency checks, and unit tests that simulate interruptions and resumptions.

5. **Support non-range (non-resumable) downloads**  
   - Detect servers that do not support `Range` (e.g., missing `Accept-Ranges` or `206` responses) and gracefully fallback to a single-stream download mode.  
   - Expose this as a policy (e.g., `ForceChunked` vs `FallbackToSingleStream`) so callers can control behavior.

6. **Torrent support (optional / extension)**  
   - Add support for BitTorrent-style downloads either by integrating a torrent client library or exposing a plugin interface so torrent backends can be added later.  
   - Define clear boundaries: torrent metadata handling, peer management, piece verification, and how it maps to the existing chunk/merge flow.

7. **Contirbute for features**
   - Can add the features/todo list here 
---

<small>Generated on contributor request — keep this doc in the repo root as `DOWNLOAD_MANAGER_DEV_GUIDE.md`.</small>
