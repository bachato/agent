package client

import "github.com/portainer/agent"

type options struct {
	version string
}

func defaultOptions() *options {
	return &options{
		version: agent.Version,
	}
}

type Option func(*options)

func WithVersion(version string) Option {
	return func(o *options) {
		o.version = version
	}
}
