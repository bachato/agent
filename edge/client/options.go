package client

import (
	"github.com/portainer/agent"
	"github.com/portainer/agent/docker"
)

type options struct {
	version           string
	dockerSnapshotter DockerSnapshotter
	gpuOperator       bool
}

func defaultOptions() *options {
	return &options{
		version:           agent.Version,
		dockerSnapshotter: docker.CreateSnapshot,
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

func WithGPUOperator(enabled bool) Option {
	return func(o *options) {
		o.gpuOperator = enabled
	}
}
