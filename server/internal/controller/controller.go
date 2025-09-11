package controller

import (
	"net/http"
	"net/url"
	"os"

	"github.com/rautNishan/download-manager/server/base"
)

type Controller struct {
	GetConfig func(v any)
	GetProxy  func(base.RequestPorxy) func(*http.Request) (*url.URL, error)
	FileController
}

type FileController interface {
	Touch(name string, size int64) (file *os.File, err error)
}

func NewController() *Controller {
	return &Controller{}
}
