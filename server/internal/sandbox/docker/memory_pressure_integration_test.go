package docker

import (
	"context"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// TestMemoryPressureLiveDocker exercises GetMemoryPressure against a real running
// sandbox container when Docker is available (skips otherwise).
func TestMemoryPressureLiveDocker(t *testing.T) {
	if testing.Short() {
		t.Skip("short mode")
	}
	out, err := exec.Command("docker", "ps", "--filter", "name=hwr-sbx-", "--format", "{{.Names}}").Output()
	if err != nil {
		t.Skipf("docker not available: %v", err)
	}
	names := strings.Fields(strings.TrimSpace(string(out)))
	if len(names) == 0 {
		t.Skip("no hwr-sbx-* container running")
	}
	name := names[0]
	sandboxID := strings.TrimPrefix(name, "hwr-sbx-")
	if sandboxID == name {
		t.Fatalf("unexpected container name %q", name)
	}

	b := NewBackend(120, 4096)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	p, err := b.GetMemoryPressure(ctx, sandboxID)
	if err != nil {
		t.Fatalf("GetMemoryPressure: %v", err)
	}
	t.Logf("sandbox=%s usage=%.2f%% near=%v reason=%q", sandboxID, p.UsagePercent, p.NearLimit, p.Reason)
	if p.LimitBytes <= 0 && p.UsagePercent <= 0 {
		t.Errorf("expected positive limit or usage from docker/cgroup")
	}
}
