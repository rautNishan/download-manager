package download

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"

	"github.com/rautNishan/download-manager/internals/storage"
	"github.com/rautNishan/download-manager/utils"
)

type Task struct {
	ID              string
	URL             string
	DIR             string
	TotalSize       int64
	DownloadedSize  int64
	Status          string
	ChunkCount      int
	CompletedChunks int
	StartTime       time.Time
	LastUpdate      time.Time
	Speed           float64
	IsResumable     bool
}

func NewTask(UrlMeta *utils.UrlMetaData, downloadManager *DownloadManager) *Task {
	return &Task{
		ID:              utils.GenerateDownloadID(UrlMeta.OriginalUrl),
		Status:          "pending",
		StartTime:       time.Now(),
		URL:             UrlMeta.OriginalUrl,
		TotalSize:       UrlMeta.ContentLength,
		DownloadedSize:  0,
		ChunkCount:      0,
		CompletedChunks: 0,
		Speed:           0,
		LastUpdate:      time.Now(),
		DIR:             downloadManager.OutPutPath + "/" + UrlMeta.FileName,
		IsResumable:     UrlMeta.Resumable,
	}
}

func (task *Task) trackProgress(progressChan <-chan ChunkProgress, completionChan <-chan int, totalDownloadTillNow int64) {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	currentSessionProgress := make(map[int]int64)
	completedInSession := make(map[int]bool)
	// Track total bytes from chunks completed in this session
	var sessionCompletedBytes int64

	for {
		select {
		case cp, ok := <-progressChan:
			if !ok {
				fmt.Println("DEBUG: Progress channel closed")
				return
			}
			// Only record bytes while chunk isn't marked completed in this session
			if !completedInSession[cp.ChunkIndex] {
				currentSessionProgress[cp.ChunkIndex] = cp.BytesRead
			}

		case chunkIndex, ok := <-completionChan:
			if !ok {
				fmt.Println("DEBUG: Completion channel closed")
				return
			}
			// Mark as completed and add its bytes to session completed total
			if !completedInSession[chunkIndex] {
				completedInSession[chunkIndex] = true
				if chunkBytes, exists := currentSessionProgress[chunkIndex]; exists {
					sessionCompletedBytes += chunkBytes
					delete(currentSessionProgress, chunkIndex)
				}
			}

		case <-ticker.C:
			var currentSessionActive int64

			// Sum bytes from chunks still actively downloading
			for _, chunkBytes := range currentSessionProgress {
				currentSessionActive += chunkBytes
			}

			// Total = base from previous sessions + completed in this session + currently active
			totalDownloaded := totalDownloadTillNow + sessionCompletedBytes + currentSessionActive
			progress := float64(totalDownloaded) / float64(task.TotalSize) * 100

			fmt.Printf("Task total size: %d | Downloaded: %d (base: %d + session completed: %d + session active: %d) | Progress: %.2f%%\n",
				task.TotalSize, totalDownloaded, totalDownloadTillNow, sessionCompletedBytes, currentSessionActive, progress)
		}
	}
}

func (task *Task) downloadChunkWithProgress(chunk storage.ChunkInfo, progressChan chan<- ChunkProgress) error {
	tr := &http.Transport{
		ResponseHeaderTimeout: 15 * time.Second,
	}
	client := &http.Client{Transport: tr}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, "GET", task.URL, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Range", fmt.Sprintf("bytes=%d-%d", chunk.Start, chunk.End))

	startReq := time.Now()
	resp, err := client.Do(req)
	if err != nil {
		// Check if error is due to cancellation
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
			fmt.Printf("client.Do error (chunk %d): %T %v (elapsed %s)\n", chunk.Index, err, err, time.Since(startReq))
			return err
		}
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

	_, err = io.Copy(file, progressReader)
	if err != nil {
		fmt.Printf("This is error in Download with chunk progress: %+v\n", err)
	}

	// fmt.Printf("Chunk %d downloaded in %s\n", chunk.Index, elapsed)
	return nil
}

// Read implements io.Reader interface
func (cpr *ChunkProgressReader) Read(p []byte) (n int, err error) {
	// Check for cancellation before reading

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
