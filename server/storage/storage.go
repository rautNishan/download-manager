package storage

import (
	"fmt"
	"os"
	"path/filepath"

	"go.etcd.io/bbolt"
)

const dbFile = "mydb.db"

func NewBoltStorage(dir string) *BoltStorage {
	fmt.Printf("This is dir: %s\n", dir)
	if err := os.MkdirAll(dir, 0755); err != nil {
		panic(err)
	}

	path := filepath.Join(dir, dbFile)
	db, err := bbolt.Open(path, 0600, nil)
	if err != nil {
		panic(err)
	}

	return &BoltStorage{
		db:   db,
		path: path,
	}
}
