package utils

import (
	"fmt"
	"net/http"
	"path"
	"strconv"
	"strings"
)

type UrlMetaData struct {
	Resumable     bool
	ContentLength int64
	FileName      string
	FileExtension string
	Etag          string
	AcceptRange   string
	ContentType   string
	OriginalUrl   string
}

func ParseResponse(res *http.Response) *UrlMetaData {
	var cLength int64
	fileName := ""
	extension := ""
	// Accept-Range
	accpetRange := res.Header.Get("Accept-Ranges")
	resumable := strings.Contains(strings.ToLower(accpetRange), "bytes")
	//Content Lenght
	if contentLenght := res.Header.Get("Content-Length"); contentLenght != "" {
		if v, err := strconv.ParseInt(contentLenght, 10, 64); err == nil {
			cLength = v
			fmt.Printf("This is content-length: %d\n", cLength)
		}
	}
	if contentDispostion := res.Header.Get("Content-Disposition"); contentDispostion != "" {
		parts := strings.Split(contentDispostion, ";")
		for _, part := range parts {
			part = strings.TrimSpace(part)
			if strings.HasPrefix(part, "filename=") {
				fileName := strings.Trim(part[9:], `"`)
				extension = path.Ext(fileName)
				break
			}
		}
	}
	//FALLBACK If no filename from Content-Disposition, try to extract from URL
	if fileName == "" {
		urlPath := res.Request.URL.Path
		if urlPath != "" {
			fileName = path.Base(urlPath)
			extension = path.Ext(fileName)
		}
	}
	return &UrlMetaData{
		AcceptRange:   accpetRange,
		Resumable:     resumable,
		Etag:          res.Header.Get("ETag"),
		ContentLength: cLength,
		ContentType:   res.Header.Get("Content-Type"),
		FileExtension: extension,
		FileName:      fileName,
		OriginalUrl:   res.Request.URL.String(),
	}
}
