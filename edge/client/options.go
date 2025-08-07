package client

import (
	"github.com/portainer/agent"
	"github.com/portainer/agent/docker"
)

type options struct {
	version           string
	dockerSnapshotter DockerSnapshotter
}

func defaultOptions() *options {
	return &options{
		version:           agent.Version,
		dockerSnapshotter: docker.Snapshotter{},
	}
}

type Option func(*options)

func WithVersion(version string) Option {
	return func(o *options) {
		o.version = version
	}
}

func WithDockerSnapshotter(snapshotter DockerSnapshotter) Option {
	return func(o *options) {
		o.dockerSnapshotter = snapshotter
	}
}
