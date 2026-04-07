package edge

import (
	"testing"
	"time"

	"github.com/portainer/agent"
)

func TestKeyDataRace(t *testing.T) {
	t.Parallel()
	mgr := NewManager(&ManagerParameters{
		Options: &agent.Options{
			DataPath: t.TempDir(),
		},
	})

	go func() {
		_ = mgr.SetKey(encodeKey(&edgeKey{}))
	}()

	time.Sleep(1 * time.Second)
	mgr.IsKeySet()
	time.Sleep(1 * time.Second)
}
