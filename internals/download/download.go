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
