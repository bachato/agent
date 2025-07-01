package main

import (
	"errors"
	"os"

	"github.com/portainer/agent/edge/health"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
)

func init() {
	zerolog.TimeFieldFormat = zerolog.TimeFormatUnixNano

	log.Logger = log.Logger.With().Caller().Logger()
}

var errNotHealthy = errors.New("agent is not healthy: cannot connect to Portainer")

func main() {
	if !health.Healthy() {
		log.Err(errNotHealthy).Msg("Unhealthy")
		os.Exit(1)
	}
	log.Log().Msg("Healthy")
}
