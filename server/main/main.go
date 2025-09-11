package main

import (
	"fmt"

	"github.com/rautNishan/download-manager/server"
	"github.com/rautNishan/download-manager/server/config"
)

func main() {
	fmt.Printf("Download Manager\n")
	config := &config.Config{
		Net:     "tcp",
		Host:    "localhost",
		Port:    3000,
		Storage: "bolt",
	}
	server.Serve(config)
}
