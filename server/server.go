package server

import (
	"fmt"

	"github.com/rautNishan/download-manager/server/config"
	"github.com/rautNishan/download-manager/server/download"
	"github.com/rautNishan/download-manager/server/storage"
)

func Serve(conf *config.Config) {
	if conf == nil {
		conf = &config.Config{}
	}
	conf.Init()
	fmt.Printf("This is config: %v\n", conf)
	downloadConfig := &download.DownloaderConfig{
		RefreshInterval:   conf.RefreshInterval,
		WhiteDownloadDirs: conf.WhiteDownloadDirs,
	}
	storage.NewBoltStorage(conf.StorageDir)
	fmt.Printf("This is downloadconfig %v\n", downloadConfig)
	downloadConfig.StorageDir = conf.StorageDir
}
