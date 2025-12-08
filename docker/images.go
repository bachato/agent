package docker

import (
	"context"
	"os"

	"github.com/docker/docker/api/types/image"
	"github.com/docker/docker/client"
)

func ImageDelete(name string, opts image.RemoveOptions) (r []image.DeleteResponse, err error) {
	err = withCli(func(cli *client.Client) error {
		r, err = cli.ImageRemove(context.Background(), name, opts)

		return err
	})

	return r, err
}

func ImageLoad(imagePath string) error {
	return withCli(func(cli *client.Client) error {
		file, err := os.Open(imagePath)
		if err != nil {
			return err
		}
		defer file.Close()

		_, err = cli.ImageLoad(context.Background(), file)
		return err
	})
}
