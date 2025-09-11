package download

import "github.com/rautNishan/download-manager/server/internal/controller"

type DownloaderConfig struct {
	RefreshInterval   int //Ms
	WhiteDownloadDirs []string
	StorageDir        string

	Controller *controller.Controller
}
