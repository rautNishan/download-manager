package storage

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"go.etcd.io/bbolt"
)

type ChunkInfo struct {
	Index          int
	Start          int64
	End            int64
	FilePath       string
	Completed      bool
	ChunkTotalSize int64
	DownloadedSize int64
}

var (
	DownloadsBucket = []byte("downloads")
	ChunksBucket    = []byte("chunks")
	MetadataBucket  = []byte("metadata")
)

type Storage string

const StorageBolt Storage = "Bolt"

type BoltStorage struct {
	DB   *bbolt.DB
	Path string
}

func NewBoltStorage(dir string) *BoltStorage {
	if err := os.MkdirAll(dir, 0755); err != nil {
		panic(err)
	}
	path := filepath.Join(dir, "storage.db")
	db, err := bbolt.Open(path, 0600, nil)
	if err != nil {
		panic(err)
	}
	// Initialize buckets
	err = db.Update(func(tx *bbolt.Tx) error {
		// Create buckets if they don't exist
		buckets := [][]byte{DownloadsBucket, ChunksBucket, MetadataBucket}
		for _, bucket := range buckets {
			if _, err := tx.CreateBucketIfNotExists(bucket); err != nil {
				return err
			}
		}
		return nil
	})

	if err != nil {
		panic(fmt.Sprintf("Failed to initialize buckets: %v", err))
	}

	return &BoltStorage{
		DB:   db,
		Path: path,
	}
}

func (bs *BoltStorage) GetChunkInfo(downloadId string) ([]ChunkInfo, error) {
	var chunks []ChunkInfo
	err := bs.DB.View(func(tx *bbolt.Tx) error {
		bucket := tx.Bucket(ChunksBucket)
		data := bucket.Get([]byte(downloadId))
		if data == nil {
			return fmt.Errorf("Chunks not found for download:%s ", downloadId)
		}
		return json.Unmarshal(data, &chunks)
	})
	return chunks, err
}

func (bs *BoltStorage) SaveChunkInfo(downloadId string, chunks []ChunkInfo) error {
	return bs.DB.Update(func(tx *bbolt.Tx) error {
		bucket := tx.Bucket(ChunksBucket)

		data, err := json.Marshal(chunks)
		if err != nil {
			return fmt.Errorf("Failed to marshal chunks: %v\n", err)
		}

		return bucket.Put([]byte(downloadId), data)
	})
}

func (bs *BoltStorage) UpdateChunkStatus(downloadID string, chunkIndex int, completed bool) error {
	return bs.DB.Update(func(tx *bbolt.Tx) error {
		bucket := tx.Bucket(ChunksBucket)
		data := bucket.Get([]byte(downloadID))
		if data == nil {
			return fmt.Errorf("chunks not found for download: %s", downloadID)
		}

		var chunks []ChunkInfo
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

		return bucket.Put([]byte(downloadID), updatedData)
	})
}

func (bs *BoltStorage) DeleteChunkInfo(downloadID string) error {
	return bs.DB.Update(func(tx *bbolt.Tx) error {
		bucket := tx.Bucket(ChunksBucket)
		return bucket.Delete([]byte(downloadID))
	})
}
