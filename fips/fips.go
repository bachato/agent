package fips

import (
	"os"
	"strings"

	"github.com/rs/zerolog/log"
)

const goDebugEnvVar = "GODEBUG"
const fipsGoDebugVar = "fips140"

var fipsMode = false
var isInitialised = false

func InitFIPS(enabled bool) {
	isInitialised = true
	fipsMode = enabled
	if enabled {
		godebugFlags := os.Getenv(goDebugEnvVar)
		pairs := strings.Split(godebugFlags, ",")
		// TODO: once we update to go 1.24 then we can use https://pkg.go.dev/crypto/fips140#Enabled to check this.
		fipsGodebugVar := false
		for _, pair := range pairs {
			k, v, found := strings.Cut(pair, "=")
			if found && k == fipsGoDebugVar && (v == "on" || v == "only") {
				fipsGodebugVar = true
				break
			}
		}

		if enabled && !fipsGodebugVar {
			log.Fatal().Msg("If FIPS mode is enabled then the fips140 GODEBUG environment variable must be set")
		}
	}
}

func FIPSMode() bool {
	if !isInitialised {
		log.Fatal().Msg("Could not determine if FIPS mode is enabled because InitFIPS was never called")
	}
	return fipsMode
}

func CanTLSSkipVerify() bool {
	if !isInitialised {
		log.Fatal().Msg("Could not determine if FIPS mode is enabled because InitFIPS was never called")
	}
	return !fipsMode
}
