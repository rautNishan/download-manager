package download

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"github.com/rautNishan/download-manager/internals/storage"
	"go.etcd.io/bbolt"
)

var (
	DownloadsBucket = []byte("downloads")
	ChunksBucket    = []byte("chunks")
	MetadataBucket  = []byte("metadata")
)

type ChunkProgressReader struct {
	Reader       io.Reader
	ChunkIndex   int
	ProgressChan chan<- ChunkProgress
	totalRead    int64 // Track total bytes read for this chunk
	lastUpdate   time.Time
	BytesRead    int64
}
type ChunkProgress struct {
	ChunkIndex int
	BytesRead  int64
}

const (
	ChunkSize     = 1024 * 1024 * 10 // 10MB chunks
	MaxGoroutines = 10               // Number of concurrent downloads
)

type DownloadManager struct {
	Storage       *storage.BoltStorage
	OutPutPath    string
	MaxGoroutines int
	// ctx             context.Context
	// cancel          context.CancelFunc
	// shutdownOnce    sync.Once
	// activeDownloads sync.WaitGroup
	// mu              sync.RWMutex
}

func NewDownloadManager(storage *storage.BoltStorage, outputDir string) *DownloadManager {
	if err := os.MkdirAll(outputDir, 0755); err != nil {
		panic(err)
	}

	outPutPath := filepath.Join(outputDir)

	// Create context for graceful shutdown
	// ctx, cancel := context.WithCancel(context.Background())

	dm := &DownloadManager{
		Storage:       storage,
		OutPutPath:    outPutPath,
		MaxGoroutines: MaxGoroutines,
		// ctx:           ctx,
		// cancel:        cancel,
	}

	// Set up signal handling for graceful shutdown
	dm.setupGracefulShutdown()
	return dm
}

func (dm *DownloadManager) setupGracefulShutdown() {
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)

	go func() {
		<-sigChan
		fmt.Println("\nReceived interrupt signal. Shutting down gracefully...")

		// Cancel all contexts immediately
		// dm.cancel()

		// Set a timeout for graceful shutdown
		shutdownTimeout := 10 * time.Second
		shutdownComplete := make(chan struct{})

		go func() {
			fmt.Println("Waiting for active downloads to complete...")
			// dm.activeDownloads.Wait()
			close(shutdownComplete)
		}()

		select {
		case <-shutdownComplete:
			fmt.Println("All downloads stopped gracefully")
		case <-time.After(shutdownTimeout):
			fmt.Println("Shutdown timeout reached, forcing exit")
		}

		// Sync database
		if dm.Storage != nil {
			dm.Storage.DB.Sync()
			fmt.Println("Database synced successfully")
		}

		fmt.Println("Shutdown complete")
		os.Exit(0)
	}()
}

func (dm *DownloadManager) ResumeDownload(taskId string) {
	// ctx := dm.ctx
	// dm.activeDownloads.Add(1)
	// defer dm.activeDownloads.Done()

	existingTask, err := dm.getExistingTask(taskId)
	if err != nil {
		panic(err)
	}
	if existingTask == nil {
		panic("No task found")
	}

	startTime := time.Now()
	chunks, totalChunks := dm.getExistingChunks(existingTask)
	fmt.Printf("Total chunks len: %d\n", len(totalChunks))
	if chunks == nil {
		panic("No Chunk found")
	}
	fmt.Printf("These are reamining chunks: %+v\n", chunks)
	fmt.Printf("This is total chunks %d\n", existingTask.ChunkCount)

	fmt.Printf("This is remaining chunks: %d\n", len(chunks))
	fmt.Printf("Confirming remaining chunks: %d\n", existingTask.ChunkCount-existingTask.CompletedChunks)

	tempChunksDir := existingTask.DIR + ".tmp"
	if err := os.MkdirAll(tempChunksDir, 0755); err != nil {
		panic(err)
	}
	dm.saveTask(existingTask)

	var wg sync.WaitGroup
	var taskMutex sync.Mutex
	semaphore := make(chan struct{}, dm.MaxGoroutines)

	progressChan := make(chan ChunkProgress)
	completionChan := make(chan int, len(chunks))

	go existingTask.trackProgress(progressChan, completionChan)

	for i, chuck := range chunks {
		wg.Add(1)
		go func(index int, c storage.ChunkInfo) {
			defer wg.Done()
			semaphore <- struct{}{}
			defer func() { <-semaphore }()
			if err := existingTask.downloadChunkWithProgress(c, progressChan); err != nil {
				return

			}
			taskMutex.Lock()
			info, err := os.Stat(c.FilePath)
			fmt.Printf("This is info: %+v\n", info)
			if err != nil {
				fmt.Printf("This is error: %+v\n", err)
			}
			if info.Size() != c.ChunkTotalSize {
				panic(fmt.Sprintf("Total file size was %d but downloaded %d",
					c.ChunkTotalSize, info.Size()))
			}
			chunks[index].Completed = true
			c.Completed = true
			dm.updateChunkStatus(existingTask, c.Index, true)
			fmt.Printf("Updating storage for chunk at array index %d (chunk index %d)\n", index, c.Index)
			existingTask.LastUpdate = time.Now()
			existingTask.DownloadedSize += c.ChunkTotalSize
			existingTask.CompletedChunks++
			dm.saveTask(existingTask)
			taskMutex.Unlock()
			select {
			case completionChan <- index:
			default:
				fmt.Printf("Warning: completion channel full for chunk %d\n", index)
			}
		}(i, chuck)
	}

	wg.Wait()
	close(progressChan)
	close(completionChan)

	fmt.Printf("Finished wait group\n")
	// Merge chunks
	existingTask.Status = "merging"
	dm.saveTask(existingTask)
	if err := existingTask.mergeChunks(totalChunks); err != nil {
		fmt.Println("Error in Merge Chunks")
		panic(err)
	}

	// Cleanup temp directory and chunk info from storage
	os.RemoveAll(tempChunksDir)
	dm.cleanupChunkInfo(existingTask)

	fmt.Printf("Download completed: %s\n", dm.OutPutPath)
	fmt.Printf("Download complted at %s\n", time.Since(startTime))
}

func (dm *DownloadManager) getExistingTask(taskId string) (*Task, error) {
	var task *Task
	err := dm.Storage.DB.View(func(tx *bbolt.Tx) error {
		bucket := tx.Bucket(DownloadsBucket)
		if bucket == nil {
			return fmt.Errorf("bucket %s not found", string(DownloadsBucket))
		}

		data := bucket.Get([]byte(taskId))
		if data == nil {
			return fmt.Errorf("Task with ID %s not found", taskId)
		}

		task = &Task{}
		if err := json.Unmarshal(data, task); err != nil {
			return fmt.Errorf("failed to unmarshal task: %v", err)
		}

		return nil
	})

	if err != nil {
		return nil, err
	}

	return task, nil
}

func (dm *DownloadManager) getExistingChunks(task *Task) ([]storage.ChunkInfo, []storage.ChunkInfo) {

	chunks, err := dm.Storage.GetChunkInfo(task.ID)
	if err != nil {
		fmt.Printf("error: %v\n", err)
		return nil, nil
	}

	// Validate and clean up chunks
	var validChunks []storage.ChunkInfo
	var completedCount int
	var totalCompletedSize int64

	//TODO dont remove existing chunk which is not completed
	for i := range chunks {

		if chunks[i].Completed {
			// Check if file exists on disk and has correct size
			if fi, err := os.Stat(chunks[i].FilePath); err == nil {
				expected := chunks[i].ChunkTotalSize
				if fi.Size() == expected {
					// File exists and has correct size - keep as completed
					completedCount++
					totalCompletedSize += fi.Size()
					fmt.Printf("Chunk %d verified complete (%d bytes)\n", chunks[i].Index, fi.Size())
				} else {
					// File exists but wrong size - mark as incomplete and delete
					fmt.Printf("Chunk %d has wrong size (%d/%d bytes). Marking incomplete and deleting.\n",
						chunks[i].Index, fi.Size(), expected)
					_ = os.Remove(chunks[i].FilePath)
				}
			}
			continue
		}
		_ = os.Remove(chunks[i].FilePath)
		validChunks = append(validChunks, chunks[i])

	}
	return validChunks, chunks
}

func (dm *DownloadManager) NewDownload(task *Task) {
	fmt.Printf("This is task: %+v\n", task)
	// ctx := dm.ctx
	// dm.activeDownloads.Add(1)
	// defer dm.activeDownloads.Done()

	startTime := time.Now()
	chunks := getCaclucatedChunks(task)
	task.ChunkCount = len(chunks)
	fmt.Printf("Chunk info: %+v\n", chunks)
	dm.saveChunkInfo(task, chunks)

	tempChunksDir := task.DIR + ".tmp"
	if err := os.MkdirAll(tempChunksDir, 0755); err != nil {
		panic(err)
	}
	dm.saveTask(task)
	var wg sync.WaitGroup
	var taskMutex sync.Mutex
	semaphore := make(chan struct{}, dm.MaxGoroutines)

	progressChan := make(chan ChunkProgress)
	completionChan := make(chan int, len(chunks))

	go task.trackProgress(progressChan, completionChan)
	activeGoroutines := 0

	for i, chuck := range chunks {
		if chuck.Completed {
			fmt.Printf("Chunk %d already completed, skipping...\n", i)
			continue
		}
		wg.Add(1)
		activeGoroutines++
		go func(index int, c storage.ChunkInfo) {
			defer wg.Done()

			semaphore <- struct{}{}
			defer func() { <-semaphore }()
			if err := task.downloadChunkWithProgress(c, progressChan); err != nil {

				return
			}

			taskMutex.Lock()
			chunks[index].Completed = true
			c.Completed = true
			dm.updateChunkStatus(task, c.Index, true)
			fmt.Printf("Updating storage for chunk at array index %d (chunk index %d)\n", index, c.Index)
			task.LastUpdate = time.Now()
			task.DownloadedSize += c.ChunkTotalSize
			task.CompletedChunks++
			dm.saveTask(task)
			taskMutex.Unlock()
			select {
			case completionChan <- index:
			default:
				fmt.Printf("Warning: completion channel full for chunk %d\n", index)
			}
		}(i, chuck)
	}
	wg.Wait()
	close(progressChan)
	close(completionChan)

	fmt.Printf("Finished wait group\n")
	// Merge chunks
	task.Status = "merging"
	dm.saveTask(task)
	if err := task.mergeChunks(chunks); err != nil {
		fmt.Println("Error in Merge Chunks")
		panic(err)
	}

	// Cleanup temp directory and chunk info from storage
	os.RemoveAll(tempChunksDir)
	dm.cleanupChunkInfo(task)

	fmt.Printf("Download completed: %s\n", dm.OutPutPath)
	fmt.Printf("Download complted at %s\n", time.Since(startTime))
}

func (dm *DownloadManager) cleanupChunkInfo(task *Task) error {
	return dm.Storage.DB.Update(func(tx *bbolt.Tx) error {
		bucket := tx.Bucket(ChunksBucket)
		return bucket.Delete([]byte(task.ID))
	})
}

func getCaclucatedChunks(task *Task) []storage.ChunkInfo {
	var chunks []storage.ChunkInfo

	totalSize := task.TotalSize

	if totalSize <= 0 {
		return chunks
	}
	totalNumberOfChunks := (totalSize + ChunkSize - 1) / ChunkSize
	for i := int64(0); i < totalNumberOfChunks; i++ {
		start := i * ChunkSize
		end := start + ChunkSize - 1
		if end >= totalSize {
			end = totalSize - 1
		}
		chunks = append(chunks, storage.ChunkInfo{
			Index:          int(i),
			Start:          start,
			End:            end,
			FilePath:       fmt.Sprintf("%s.tmp/chunk%d", task.DIR, i),
			ChunkTotalSize: (end - start + 1),
		})
	}
	return chunks
}

func (dm *DownloadManager) verifyChunks(task *Task, chunks []storage.ChunkInfo) error {
	var totalSize int64
	for _, chunk := range chunks {
		info, err := os.Stat(chunk.FilePath)
		if err != nil {
			return fmt.Errorf("chunk %d stat error: %v", chunk.Index, err)
		}
		if info.Size() != chunk.ChunkTotalSize {
			return fmt.Errorf("chunk %d size mismatch: expected %d, got %d",
				chunk.Index, chunk.ChunkTotalSize, info.Size())
		}
		totalSize += info.Size()
	}

	if totalSize != task.TotalSize {
		return fmt.Errorf("total chunks size %d doesn't match expected %d",
			totalSize, task.TotalSize)
	}

	return nil
}

func (dm *DownloadManager) saveTask(task *Task) error {
	fmt.Printf("Saving task: %+v\n", task)
	return dm.Storage.DB.Update(func(tx *bbolt.Tx) error {
		bucket := tx.Bucket(DownloadsBucket)
		data, err := json.Marshal(task)

		if err != nil {
			return fmt.Errorf("Failed to marshal task: %v\n", err)
		}

		return bucket.Put([]byte(task.ID), data)
	})
}

func (dm *DownloadManager) saveChunkInfo(task *Task, chunks []storage.ChunkInfo) error {
	return dm.Storage.DB.Update(func(tx *bbolt.Tx) error {
		bucket := tx.Bucket(ChunksBucket)

		data, err := json.Marshal(chunks)
		if err != nil {
			return fmt.Errorf("Failed to marshal chunks: %v\n", err)
		}

		return bucket.Put([]byte(task.ID), data)
	})
}

func (dm *DownloadManager) updateChunkStatus(task *Task, chunkIndex int, completed bool) error {
	return dm.Storage.DB.Update(func(tx *bbolt.Tx) error {
		bucket := tx.Bucket(ChunksBucket)
		data := bucket.Get([]byte(task.ID))
		if data == nil {
			return fmt.Errorf("chunks not found for download: %s", task.ID)
		}

		var chunks []storage.ChunkInfo
		if err := json.Unmarshal(data, &chunks); err != nil {
			return err
		}

		// Find and update the specific chunk
		for i := range chunks {
			if chunks[i].Index == chunkIndex {
				chunks[i].Completed = completed
				break
			}
		}

		// Save updated chunks
		updatedData, err := json.Marshal(chunks)
		if err != nil {
			return err
		}

		return bucket.Put([]byte(task.ID), updatedData)
	})
}

func (task *Task) mergeChunks(chunks []storage.ChunkInfo) error {
	fmt.Printf("Lenght of chunks while merging: %d\n", len(chunks))
	fmt.Printf("Merging chunks: %+v\n", chunks)
	outputFile, err := os.Create(task.DIR)
	fmt.Printf("This is outputfile: %+v\n", outputFile)
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

// func (dm *DownloadManager) saveTask(task *storage.Taks) error {
// 	fmt.Printf("Saving taks with data: %+v\n", task)
// 	return dm.Storage.SaveDownloadtask(task.DownloadId, task)
// }

// func (dm *DownloadManager) downloadWithResume(task *storage.Taks) {
// 	startTime := time.Now()

// 	// Set up signal handling for graceful shutdown
// 	sigChan := make(chan os.Signal, 1)
// 	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)

// 	// Context for cancellation
// 	ctx, cancel := context.WithCancel(context.Background())
// 	defer cancel()

// 	// Handle shutdown signal
// 	go func() {
// 		<-sigChan
// 		fmt.Println("\nReceived interrupt signal. Saving progress and shutting down gracefully...")
// 		cancel()

// 		// Give some time for ongoing operations to complete
// 		time.Sleep(2 * time.Second)

// 		// Force sync the database
// 		if dm.Storage != nil {
// 			dm.Storage.DB.Sync()
// 			fmt.Println("Database synced successfully")
// 		}

// 		os.Exit(0)
// 	}()

// 	existingChunks, canResume, existingProgress := dm.checkExistingDownload()
// 	var chunks []storage.ChunkInfo
// 	fmt.Printf("Can Resume %v\n", canResume)

// 	if canResume {
// 		fmt.Println("Resuming existing download....")
// 		chunks = existingChunks
// 		if existingProgress != nil {
// 			task = existingProgress
// 			fmt.Printf("Resuming with existing progress: %+v\n", task)
// 		}
// 		task.Status = "RESUMING"
// 	} else {
// 		chunks = dm.calculateChunks()
// 		task.Status = "DOWNLOADING"
// 		task.ChunkCount = len(chunks)
// 	}

// 	tempDir := dm.OutPutPath + ".tmp"
// 	if err := os.MkdirAll(tempDir, 0755); err != nil {
// 		panic(err)
// 	}

// 	if err := dm.saveChunkInfo(chunks); err != nil {
// 		fmt.Printf("Warning: Could not save chunk info: %v\n", err)
// 	}

// 	dm.saveTask(task)

// 	//Download Chunks concurrently
// 	var wg sync.WaitGroup
// 	var taskMutex sync.Mutex
// 	semaphore := make(chan struct{}, dm.MaxGoroutines)

// 	errors := make(chan error, len(chunks))
// 	progressChan := make(chan ChunkProgress)
// 	completionChan := make(chan int, len(chunks))

// 	go dm.trackProgress(task, progressChan, completionChan)

// 	for i, chunk := range chunks {
// 		if chunk.Completed {
// 			select {
// 			case completionChan <- i:
// 				fmt.Printf("Notified progress tracker: chunk %d already completed\n", i)
// 			default:
// 				fmt.Printf("Warning: completion channel full for pre-completed chunk %d\n", i)
// 			}
// 		}
// 	}

// 	for i, chunk := range chunks {
// 		// Check for cancellation
// 		select {
// 		case <-ctx.Done():
// 			fmt.Println("Download cancelled, waiting for ongoing chunks to complete...")
// 			wg.Wait()
// 			return
// 		default:
// 		}

// 		if chunk.Completed {
// 			fmt.Printf("Chunk %d already completed, skipping...\n", i)
// 			continue
// 		}

// 		wg.Add(1)
// 		go func(index int, c storage.ChunkInfo) {
// 			defer wg.Done()

// 			// Check for cancellation in goroutine
// 			select {
// 			case <-ctx.Done():
// 				return
// 			default:
// 			}

// 			semaphore <- struct{}{}        //Acquire semaphore
// 			defer func() { <-semaphore }() //Release semaphore

// 			if err := dm.downloadChunkWithProgress(c, progressChan); err != nil {
// 				fmt.Printf("Error in download Chunk with progress\n")
// 				errors <- fmt.Errorf("Chunk %d failed %v ", index, err)
// 				return
// 			}

// 			taskMutex.Lock()
// 			chunks[index].Completed = true
// 			c.Completed = true
// 			fmt.Printf("Updating storage for chunk at array index %d (chunk index %d)\n", index, c.Index)
// 			if err := dm.Storage.UpdateChunkStatus(task.DownloadId, c.Index, true); err != nil {
// 				fmt.Printf("Warning: failed to update chunk status: %v\n", err)
// 			}
// 			task.LastUpdate = time.Now()
// 			task.DownloadedSize += c.ChunkTotalSize
// 			task.CompletedChunks++
// 			if err := dm.saveTask(task); err != nil {
// 				fmt.Printf("Warning: failed to save progress in tracker: %v\n", err)
// 			}
// 			taskMutex.Unlock()

// 			fmt.Printf("Chunk %d completed (%d-%d)\n", index, c.Start, c.End)
// 			select {
// 			case completionChan <- index:
// 			default:
// 				fmt.Printf("Warning: completion channel full for chunk %d\n", index)
// 			}
// 		}(i, chunk)
// 	}

// 	wg.Wait()
// 	close(errors)
// 	close(progressChan)
// 	close(completionChan)

// 	fmt.Printf("Finished wait group\n")
// 	// Check for errors
// 	if len(errors) > 0 {
// 		task.Status = "failed"
// 		dm.saveTask(task)
// 	}

// 	// Merge chunks
// 	task.Status = "merging"
// 	dm.saveTask(task)
// 	fmt.Printf("This is tempDir: %s\n", tempDir)
// 	if err := dm.mergeChunks(chunks, tempDir); err != nil {
// 		fmt.Println("Error in Merge Chunks")
// 		panic(err)
// 	}

// 	// Cleanup temp directory and chunk info from storage
// 	os.RemoveAll(tempDir)
// 	dm.cleanupChunkInfo()

// 	fmt.Printf("Download completed: %s\n", dm.OutPutPath)
// 	fmt.Printf("Download complted at %s\n", time.Since(startTime))
// }
// func (dm *DownloadManager) checkExistingDownload() ([]storage.ChunkInfo, bool, *storage.Taks) {
// 	existingTaks, err := dm.Storage.GetDownloadedtask("dl_1758444669781315700")

// 	if existingTaks != nil {
// 		fmt.Printf("Existing progress found: %+v\n", existingTaks)
// 	}

// 	if err != nil {
// 		fmt.Printf("No existing progress found, starting fresh download: %v\n", err)
// 		return nil, false, nil
// 	}

// 	chunks, err := dm.Storage.GetChunkInfo("dl_1758444669781315700")
// 	fmt.Printf("This is chunks from storage: %+v\n", chunks)
// 	if err != nil {
// 		fmt.Printf("error: %v\n", err)
// 		return nil, false, existingTaks
// 	}

// 	// Validate and clean up chunks
// 	var validChunks []storage.ChunkInfo
// 	var completedCount int
// 	var totalCompletedSize int64

// 	for i := range chunks {

// 		if chunks[i].Completed {
// 			// Check if file exists on disk and has correct size
// 			if fi, err := os.Stat(chunks[i].FilePath); err == nil {
// 				expected := int64(chunks[i].End - chunks[i].Start + 1)
// 				if fi.Size() == expected {
// 					// File exists and has correct size - keep as completed
// 					completedCount++
// 					totalCompletedSize += fi.Size()
// 					fmt.Printf("Chunk %d verified complete (%d bytes)\n", chunks[i].Index, fi.Size())
// 				} else {
// 					// File exists but wrong size - mark as incomplete and delete
// 					fmt.Printf("Chunk %d has wrong size (%d/%d bytes). Marking incomplete and deleting.\n",
// 						chunks[i].Index, fi.Size(), expected)
// 					_ = os.Remove(chunks[i].FilePath)
// 				}
// 			}
// 			continue
// 		}
// 		_ = os.Remove(chunks[i].FilePath)
// 		validChunks = append(validChunks, chunks[i])

// 	}
// 	fmt.Printf("Valid chunks after verification: %d\n", len(validChunks))
// 	fmt.Printf("Completed chunks verified: %d, total size: %d\n", completedCount, totalCompletedSize)
// 	canResume := existingTaks != nil && existingTaks.DownloadedSize == totalCompletedSize
// 	if canResume && totalCompletedSize != existingTaks.DownloadedSize {
// 		fmt.Printf("Data mismatch: total completed chunk size %d != recorded downloaded size %d\n",
// 			totalCompletedSize, existingTaks.DownloadedSize)
// 		panic("Data mismatch between chunks and task info")
// 	}
// 	return validChunks, canResume, existingTaks
// }

// func (dm *DownloadManager) calculateChunks() []storage.ChunkInfo {
// 	var chunks []storage.ChunkInfo

// 	totalSize := dm.UrlMetaData.ContentLength

// 	if totalSize <= dm.ChunkSize {
// 		chunks = append(chunks, storage.ChunkInfo{
// 			Index:    0,
// 			Start:    0,
// 			End:      totalSize - 1,
// 			FilePath: fmt.Sprintf("%s.tmp/chunk_0", dm.OutPutPath),
// 		})
// 		return chunks
// 	}

// 	/**
// 	100 ÷ 30 → 3.33 → 4 chunks.
// 	Problem with integer division
// 	100 / 30 == 3  // truncates decimal
// 	Adding ChunkSize - 1 ensures that any remainder pushes the
// 	division result up by 1, giving an extra chunk for leftover bytes
// 	 **/
// 	numChunks := (totalSize + dm.ChunkSize - 1) / dm.ChunkSize
// 	testTotal := int64(0)
// 	for i := int64(0); i < numChunks; i++ {
// 		start := i * dm.ChunkSize
// 		end := start + dm.ChunkSize - 1
// 		if end >= totalSize {
// 			//Adjust the end
// 			end = totalSize - 1
// 		}
// 		chunks = append(chunks, storage.ChunkInfo{
// 			Index:          int(i),
// 			Start:          start,
// 			End:            end,
// 			FilePath:       fmt.Sprintf("%s.tmp/chunk%d", dm.OutPutPath, i),
// 			ChunkTotalSize: (end - start + 1),
// 		})
// 		testTotal += (end - start + 1)
// 	}
// 	return chunks
// }

// func (dm *DownloadManager) saveChunkInfo(chunks []storage.ChunkInfo) error {
// 	return dm.Storage.SaveChunkInfo(dm.DownloadId, chunks)
// }

// func (dm *DownloadManager) trackProgress(task *storage.Taks, progressChan <-chan ChunkProgress, completionChan <-chan int) {
// 	ticker := time.NewTicker(2 * time.Second)
// 	defer ticker.Stop()

// 	// Track current session progress per chunk (only for active chunks)
// 	currentSessionProgress := make(map[int]int64)
// 	completedInSession := make(map[int]bool)

// 	for {
// 		select {
// 		case cp, ok := <-progressChan:
// 			if !ok {
// 				fmt.Println("DEBUG: Progress channel closed")
// 				return
// 			}

// 			// Only track if not completed
// 			if !completedInSession[cp.ChunkIndex] {
// 				currentSessionProgress[cp.ChunkIndex] = cp.BytesRead
// 				// Remove this debug line or make it less frequent
// 				// fmt.Printf("Chunk %d session progress: %d bytes\n", cp.ChunkIndex, cp.BytesRead)
// 			}

// 		case chunkIndex, ok := <-completionChan:
// 			if !ok {
// 				fmt.Println("DEBUG: Completion channel closed")
// 				return
// 			}
// 			// Mark chunk as completed and remove from active tracking
// 			completedInSession[chunkIndex] = true
// 			delete(currentSessionProgress, chunkIndex)
// 			fmt.Printf("Chunk %d completed, removed from active tracking\n", chunkIndex)

// 		case <-ticker.C:
// 			// Sum current session progress from only active chunks
// 			var currentSessionTotal int64
// 			for _, chunkSessionBytes := range currentSessionProgress {
// 				currentSessionTotal += chunkSessionBytes
// 			}

// 			// Total = what was already downloaded (persistent) + active session progress
// 			totalDownloaded := task.DownloadedSize + currentSessionTotal
// 			progress := float64(totalDownloaded) / float64(task.TotalSize) * 100
// 			fmt.Printf("Task total size: %d | Downloaded: %d (previous: %d + session: %d) | Progress: %.2f%%\n",
// 				task.TotalSize, totalDownloaded, task.DownloadedSize, currentSessionTotal, progress)
// 		}
// 	}
// }
// func (dm *DownloadManager) downloadChunkWithProgress(chunk storage.ChunkInfo, progressChan chan<- ChunkProgress) error {
// 	tr := &http.Transport{
// 		ResponseHeaderTimeout: 15 * time.Second,
// 	}
// 	client := &http.Client{Transport: tr}

// 	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
// 	defer cancel()

// 	req, err := http.NewRequestWithContext(ctx, "GET", dm.URL, nil)
// 	if err != nil {
// 		return err
// 	}
// 	req.Header.Set("Range", fmt.Sprintf("bytes=%d-%d", chunk.Start, chunk.End))

// 	startReq := time.Now()
// 	resp, err := client.Do(req)
// 	if err != nil {
// 		fmt.Printf("client.Do error (chunk %d): %T %v (elapsed %s)\n", chunk.Index, err, err, time.Since(startReq))
// 		return err
// 	}
// 	defer resp.Body.Close()

// 	if resp.StatusCode != http.StatusPartialContent {
// 		return fmt.Errorf("server doesn't support range requests, got status: %s", resp.Status)
// 	}

// 	file, err := os.Create(chunk.FilePath)
// 	if err != nil {
// 		return err
// 	}
// 	defer file.Close()
// 	progressReader := &ChunkProgressReader{
// 		Reader:       resp.Body,
// 		ChunkIndex:   chunk.Index,
// 		ProgressChan: progressChan,
// 	}

// 	copyStart := time.Now()
// 	_, err = io.Copy(file, progressReader)
// 	elapsed := time.Since(copyStart)
// 	if err != nil {
// 		// Print error type and elapsed time for debugging
// 		fmt.Printf("Error while copying (chunk %d): %T %v (elapsed %s)\n", chunk.Index, err, err, elapsed)
// 		return err
// 	}

// 	// fmt.Printf("Chunk %d downloaded in %s\n", chunk.Index, elapsed)
// 	return nil
// }

// func (dm *DownloadManager) updateChunkStatus(chunk storage.ChunkInfo) error {
// 	fmt.Printf("Updating chunk status: %+v\n", chunk)
// 	return dm.Storage.UpdateChunkStatus(dm.DownloadId, chunk.Index, chunk.Completed)
// }

// func (dm *DownloadManager) mergeChunks(chunks []storage.ChunkInfo, tempDir string) error {
// 	outputFile, err := os.Create(dm.OutPutPath)
// 	if err != nil {
// 		return err
// 	}
// 	defer outputFile.Close()

// 	for _, chunk := range chunks {
// 		chunkFile, err := os.Open(chunk.FilePath)
// 		if err != nil {
// 			return err
// 		}

// 		_, err = io.Copy(outputFile, chunkFile)
// 		chunkFile.Close()

// 		if err != nil {
// 			return err
// 		}
// 	}

// 	return nil
// }

// func (dm *DownloadManager) cleanupChunkInfo() error {
// 	return dm.Storage.DeleteChunkInfo(dm.DownloadId)
// }

// // Read implements io.Reader interface
// func (cpr *ChunkProgressReader) Read(p []byte) (n int, err error) {
// 	n, err = cpr.Reader.Read(p)
// 	if n > 0 {
// 		cpr.BytesRead += int64(n) // Update total bytes read

// 		// Throttle updates to avoid flooding the channel (update every 100ms or more)
// 		now := time.Now()
// 		if now.Sub(cpr.lastUpdate) >= 100*time.Millisecond {
// 			cpr.lastUpdate = now

// 			// Send progress update - don't block
// 			select {
// 			case cpr.ProgressChan <- ChunkProgress{
// 				ChunkIndex: cpr.ChunkIndex,
// 				BytesRead:  cpr.BytesRead, // Total bytes read for this chunk
// 			}:
// 			default:
// 				// Channel full, skip this update to avoid blocking
// 			}
// 		}
// 	}
// 	return n, err
// }
