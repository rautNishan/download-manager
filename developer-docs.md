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
5. **Resume Download** — given `taskID` verify completed chunks, remove incomplete chunk files from disk (they will be redownloaded), and download remaining chunks. After final chunk verifies, merge chunks and cleanup chunk info.

If you want, I can also:

* provide a ready-made Go patch that replaces the problematic `saveChunkInfo(existingTask, chunks)` with safe calls to `UpdateChunkStatus` and preserves the canonical list, and update `getExistingChunks` with the improved loop.
* add a unit-test plan and an integration test harness (fake storage + temp files) to prevent regressions.

---

<small>Generated on contributor request — keep this doc in the repo root as `DOWNLOAD_MANAGER_DEV_GUIDE.md`.</small>
