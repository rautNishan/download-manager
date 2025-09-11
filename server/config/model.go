package config

import (
	"github.com/rautNishan/download-manager/server/storage"
)

type Config struct {
	Net               string //e.g (TCP/UDP)
	Host              string //e.g (localhost)
	Port              uint32
	Storage           storage.Storage
	StorageDir        string
	RefreshInterval   int
	WhiteDownloadDirs []string
}
