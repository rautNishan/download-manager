package main

import (
	"fmt"
	"net/http"
	"time"

	"github.com/rautNishan/download-manager/internals/download"
	"github.com/rautNishan/download-manager/internals/storage"
	"github.com/rautNishan/download-manager/utils"
)

func main() {
	//First initialize storage
	boltStorage := storage.NewBoltStorage("./internals/storage")
	defer boltStorage.DB.Close()
	url := "url"
	metadata := getUrlMetaData(url)
	fmt.Printf("THis is url: %s", metadata.OriginalUrl)
	dm := download.NewDownloadManager(metadata, boltStorage, "./Downloads")
	dm.Download()
	fmt.Printf("Download completed: %s\n", dm.OutPutPath)
}

func getUrlMetaData(url string) *utils.UrlMetaData {

	client := &http.Client{
		Timeout: 30 * time.Second,
	}

	req, err := http.NewRequest("HEAD", url, nil)
	if err != nil {
		panic(err)
	}
	res, err := client.Do(req)
	if err != nil {
		panic(err)
	}
	defer res.Body.Close()
	return utils.ParseResponse(res)
}
