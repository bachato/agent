package main

import (
	"log"
	"os"

	credentials "github.com/docker/docker-credential-helpers/credentials"
	"github.com/portainer/portainer/api/logs"
)

func main() {
	f, err := os.OpenFile("/tmp/portainer-credential-helper.log", os.O_RDWR|os.O_CREATE|os.O_APPEND, 0666)
	if err != nil {
		log.Fatalf("error opening file: %v", err)
	}
	defer logs.CloseAndLogErr(f)
	log.SetOutput(f)

	log.Printf("running portainer-credential-helper")

	helper := portainerHelper{}
	credentials.Serve(helper)
}
