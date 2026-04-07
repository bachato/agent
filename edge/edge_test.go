package edge

import (
	"net/http"
	"testing"

	"github.com/portainer/portainer/pkg/fips"
)

func init() {
	fips.InitFIPS(false)
}

func TestBuildTransport(t *testing.T) {
	t.Parallel()
	_, ok := http.DefaultTransport.(*http.Transport)
	if !ok {
		t.Fatal("type assertion for http.DefaultTransport failed")
	}
}
