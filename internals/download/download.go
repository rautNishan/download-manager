package download

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/rautNishan/download-manager/internals/storage"
	"github.com/rautNishan/download-manager/utils"
)

type ChunkProgressReader struct {
	Reader       io.Reader
	ChunkIndex   int
	ProgressChan chan<- ChunkProgress
	totalRead    int64 // Track total bytes read for this chunk
	lastUpdate   time.Time
	BytesRead    int64
}

type DownloadManager struct {
	URL           string
	UrlMetaData   *utils.UrlMetaData
	OutPutPath    string
	ChunkSize     int64
	MaxGoroutines int
	Storage       *storage.BoltStorage
	DownloadId    string
}

type ChunkProgress struct {
	ChunkIndex int
	BytesRead  int64
}

const (
	ChunkSize     = 1024 * 1024 * 5 // 10MB chunks
	MaxGoroutines = 20              // Number of concurrent downloads
)

func NewDownloadManager(urlMetaData *utils.UrlMetaData, storage *storage.BoltStorage, outputDir string) *DownloadManager {
	if err := os.MkdirAll(outputDir, 0755); err != nil {
		panic(err)
	}
	outPutPath := filepath.Join(outputDir, urlMetaData.FileName)
	downloadId := utils.GenerateDownloadID(urlMetaData.OriginalUrl)
	return &DownloadManager{
		Storage:       storage,
		URL:           urlMetaData.OriginalUrl, //Migh get removed
		OutPutPath:    outPutPath,
		ChunkSize:     ChunkSize,
		MaxGoroutines: MaxGoroutines,
		DownloadId:    downloadId,
		UrlMetaData:   urlMetaData,
	}
}

func (dm *DownloadManager) Download() {
	progress := &storage.DownloadProgress{
		DownloadId: dm.DownloadId,
		URL:        dm.URL,
		FileName:   dm.UrlMetaData.FileName,
		TotalSize:  dm.UrlMetaData.ContentLength,
		Status:     "Starting",
		StartTime:  time.Now(),
		LastUpdate: time.Now(),
	}
	fmt.Printf("Saving Progress: %v", progress)
	if err := dm.saveProgress(progress); err != nil {
		panic(err)
	}

	if dm.UrlMetaData.Resumable && dm.UrlMetaData.ContentLength > 0 {
		fmt.Printf("Downloading with resumable")
		dm.downloadWithResume(progress)
	}
}

func (dm *DownloadManager) saveProgress(progress *storage.DownloadProgress) error {
	return dm.Storage.SaveDownloadProgress(progress.DownloadId, progress)
}

func (dm *DownloadManager) downloadWithResume(progress *storage.DownloadProgress) {
	existingChunks, canResume := dm.checkExistingDownload()
	var chunks []storage.ChunkInfo

	if canResume {
		fmt.Println("Resuming existing download....")
		chunks = existingChunks
		progress.Status = "RESUMING"
	} else {
		chunks = dm.calculateChunks()
		progress.Status = "DOWNLOADING"
		progress.ChunkCount = len(chunks)
	}

	tempDir := dm.OutPutPath + ".tmp"
	if err := os.MkdirAll(tempDir, 0755); err != nil {
		panic(err)
	}

	if err := dm.saveChunkInfo(chunks); err != nil {
		fmt.Printf("Warning: Could not save chunk info: %v\n", err)
	}
	dm.saveProgress(progress)

	//Download Chunks concurrently
	var wg sync.WaitGroup

	semaphore := make(chan struct{}, dm.MaxGoroutines)

	errors := make(chan error, len(chunks))
	progressChan := make(chan ChunkProgress, 256)
	go dm.trackProgress(progress, progressChan)
	for i, chunk := range chunks {

		if chunk.Completed {
			fmt.Printf("Chunk %d already completed, skipping...\n", i)
			continue
		}
		wg.Add(1)
		go func(index int, c storage.ChunkInfo) {
			defer wg.Done()
			semaphore <- struct{}{}        //Acquire semaphore
			defer func() { <-semaphore }() //Release semaphore
			fmt.Printf("Starting download for chunk %d\n", index)
			if err := dm.downloadChunkWithProgress(c, progressChan); err != nil {
				fmt.Printf("Error in download Chunk with progress\n")
				errors <- fmt.Errorf("Chunk %d failed %v ", index, err)
				return

			}
			c.Completed = true
			dm.updateChunkStatus(c)
			fmt.Printf("Chunk %d completed (%d-%d)\n", index, c.Start, c.End)
		}(i, chunk)
	}
	wg.Wait()
	close(errors)
	close(progressChan)

	fmt.Printf("Finished wait group\n")
	// Check for errors
	if len(errors) > 0 {
		progress.Status = "failed"
		dm.saveProgress(progress)
	}

	// Merge chunks
	progress.Status = "merging"
	dm.saveProgress(progress)
	fmt.Printf("This is tempDir: %s\n", tempDir)
	if err := dm.mergeChunks(chunks, tempDir); err != nil {
		fmt.Println("Error in Merge Chunks")
		panic(err)
	}

	// Cleanup temp directory and chunk info from storage
	os.RemoveAll(tempDir)
	dm.cleanupChunkInfo()

	fmt.Printf("Download completed: %s\n", dm.OutPutPath)
}

func (dm *DownloadManager) checkExistingDownload() ([]storage.ChunkInfo, bool) {
	chunks, err := dm.Storage.GetChunkInfo(dm.DownloadId)
	if err != nil {
		fmt.Printf("error: %v\n", err)
	}
	allExist := true

	for i := range chunks {
		if !chunks[i].Completed {
			continue
		}
		if _, err := os.Stat(chunks[i].FilePath); os.IsNotExist(err) {
			chunks[i].Completed = false
			allExist = false
		}

	}
	return chunks, len(chunks) > 0 && !allExist
}

func (dm *DownloadManager) calculateChunks() []storage.ChunkInfo {
	var chunks []storage.ChunkInfo

	totalSize := dm.UrlMetaData.ContentLength

	if totalSize <= dm.ChunkSize {
		chunks = append(chunks, storage.ChunkInfo{
			Index:    0,
			Start:    0,
			End:      totalSize - 1,
			FilePath: fmt.Sprintf("%s.tmp/chunk_0", dm.OutPutPath),
		})
		return chunks
	}

	numChunks := (totalSize + dm.ChunkSize - 1) / dm.ChunkSize

	for i := int64(0); i < numChunks; i++ {
		start := i * dm.ChunkSize
		end := start + dm.ChunkSize - 1
		if end >= totalSize {
			end = totalSize - 1
		}
		chunks = append(chunks, storage.ChunkInfo{
			Index:    int(i),
			Start:    start,
			End:      end,
			FilePath: fmt.Sprintf("%s.tmp/chunk%d", dm.OutPutPath, i),
		})
	}
	return chunks
}

func (dm *DownloadManager) saveChunkInfo(chunks []storage.ChunkInfo) error {
	return dm.Storage.SaveChunkInfo(dm.DownloadId, chunks)
}

func (dm *DownloadManager) trackProgress(progress *storage.DownloadProgress, progressChan <-chan ChunkProgress) {
	chunkProgress := make(map[int]int64)
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case cp, ok := <-progressChan:
			if !ok {
				fmt.Println("DEBUG: Progress channel closed")
				return // Channel closed
			}
			// Store the current total bytes for this chunk (don't add to previous value)
			chunkProgress[cp.ChunkIndex] = cp.BytesRead
			fmt.Printf("DEBUG: Chunk %d progress: %d bytes\n", cp.ChunkIndex, cp.BytesRead)

		case <-ticker.C:
			var totalDownloaded int64
			completedChunks := 0

			for chunkIndex, downloaded := range chunkProgress {
				totalDownloaded += downloaded
				if downloaded > 0 {
					completedChunks++
				}
				fmt.Printf("DEBUG: Chunk %d has downloaded %d bytes\n", chunkIndex, downloaded)
			}

			progress.DownloadedSize = totalDownloaded
			progress.CompletedChunks = completedChunks
			progress.LastUpdate = time.Now()

			// Calculate speed
			elapsed := progress.LastUpdate.Sub(progress.StartTime).Seconds()
			if elapsed > 0 {
				progress.Speed = float64(totalDownloaded) / elapsed
			}

			dm.saveProgress(progress)

			if progress.TotalSize > 0 {
				percent := float64(totalDownloaded) / float64(progress.TotalSize) * 100
				fmt.Printf("Overall Progress: %.1f%% (%d/%d) Speed: %.1f B/s\n",
					percent,
					totalDownloaded,
					progress.TotalSize,
					progress.Speed)
			} else {
				fmt.Printf("DEBUG: TotalSize is 0 or negative: %d\n", progress.TotalSize)
			}
		}
	}
}
func (dm *DownloadManager) downloadChunkWithProgress(chunk storage.ChunkInfo, progressChan chan<- ChunkProgress) error {
	tr := &http.Transport{
		ResponseHeaderTimeout: 15 * time.Second,
	}
	client := &http.Client{Transport: tr}

	// Optional: per-chunk context if you want an upper bound (e.g. 20 minutes)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, "GET", dm.URL, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Range", fmt.Sprintf("bytes=%d-%d", chunk.Start, chunk.End))

	startReq := time.Now()
	resp, err := client.Do(req)
	if err != nil {
		fmt.Printf("client.Do error (chunk %d): %T %v (elapsed %s)\n", chunk.Index, err, err, time.Since(startReq))
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusPartialContent {
		return fmt.Errorf("server doesn't support range requests, got status: %s", resp.Status)
	}

	file, err := os.Create(chunk.FilePath)
	if err != nil {
		return err
	}
	defer file.Close()

	progressReader := &ChunkProgressReader{
		Reader:       resp.Body,
		ChunkIndex:   chunk.Index,
		ProgressChan: progressChan,
	}

	copyStart := time.Now()
	_, err = io.Copy(file, progressReader)
	elapsed := time.Since(copyStart)
	if err != nil {
		// Print error type and elapsed time for debugging
		fmt.Printf("Error while copying (chunk %d): %T %v (elapsed %s)\n", chunk.Index, err, err, elapsed)
		return err
	}

	fmt.Printf("Chunk %d downloaded in %s\n", chunk.Index, elapsed)
	return nil
}

func (dm *DownloadManager) updateChunkStatus(chunk storage.ChunkInfo) error {
	return dm.Storage.UpdateChunkStatus(dm.DownloadId, chunk.Index, chunk.Completed)
}

func (dm *DownloadManager) mergeChunks(chunks []storage.ChunkInfo, tempDir string) error {
	outputFile, err := os.Create(dm.OutPutPath)
	if err != nil {
		return err
	}
	defer outputFile.Close()

	for _, chunk := range chunks {
		chunkFile, err := os.Open(chunk.FilePath)
		if err != nil {
			return err
		}

		_, err = io.Copy(outputFile, chunkFile)
		chunkFile.Close()

		if err != nil {
			return err
		}
	}

	return nil
}

func (dm *DownloadManager) cleanupChunkInfo() error {
	return dm.Storage.DeleteChunkInfo(dm.DownloadId)
}

// Read implements io.Reader interface
func (cpr *ChunkProgressReader) Read(p []byte) (n int, err error) {
	n, err = cpr.Reader.Read(p)
	if n > 0 {
		cpr.BytesRead += int64(n) // Update total bytes read

		// Throttle updates to avoid flooding the channel (update every 100ms or more)
		now := time.Now()
		if now.Sub(cpr.lastUpdate) >= 100*time.Millisecond {
			cpr.lastUpdate = now

			// Send progress update - don't block
			select {
			case cpr.ProgressChan <- ChunkProgress{
				ChunkIndex: cpr.ChunkIndex,
				BytesRead:  cpr.BytesRead, // Total bytes read for this chunk
			}:
			default:
				// Channel full, skip this update to avoid blocking
			}
		}
	}
	return n, err
}
