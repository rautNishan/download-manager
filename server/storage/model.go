package storage

import "go.etcd.io/bbolt"

type Storage string

const StorageBolt Storage = "bolt"

type BoltStorage struct {
	db   *bbolt.DB
	path string
}
