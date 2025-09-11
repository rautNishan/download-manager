package download

import "github.com/rautNishan/download-manager/server/internal/controller"

func (downloadCfg *DownloaderConfig) Init() *DownloaderConfig {
	if downloadCfg.Controller == nil {
		downloadCfg.Controller = controller.NewController()
	}
	return &DownloaderConfig{}
}
