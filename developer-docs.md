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

<small>Generated on contributor request — keep this doc in the repo root as `DOWNLOAD_MANAGER_DEV_GUIDE.md`.</small>
