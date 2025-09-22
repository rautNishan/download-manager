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
	dm := download.NewDownloadManager(boltStorage, "./Downloads")
	url := ""
	metadata := getUrlMetaData(url)
	taskId := ""
	var task *download.Task
	if taskId == "" {
		fmt.Printf("No task found so New Download\n")
		task = download.NewTask(metadata, dm)
		dm.NewDownload(task)
	} else {
		fmt.Printf("Resuming download for %s\n", taskId)
		dm.ResumeDownload(taskId)
	}
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
