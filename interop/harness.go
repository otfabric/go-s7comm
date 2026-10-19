//go:build interop

// SPDX-License-Identifier: MIT

package interop

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// Multi-arch index digests for snap7-interop v0.3.0.
const (
	defaultNativeImage = "ghcr.io/otfabric/snap7-interop-native@sha256:66e3826813005bfacb8510ce5de33119315499325b35bf616dcf61de0506a342"
	defaultPythonImage = "ghcr.io/otfabric/snap7-interop-python@sha256:a53c81d7b6acb042a010fc1c9932f1f92b8c337e2555e3200938f4b0b46e3d54"
)

type serverEndpoint struct {
	Name         string
	HostPort     int
	ImageEnv     string
	DefaultImage string
	container    string
}

func defaultServers() []serverEndpoint {
	return []serverEndpoint{
		{
			Name:         "native",
			HostPort:     1102,
			ImageEnv:     "SNAP_INTEROP_NATIVE_IMAGE",
			DefaultImage: defaultNativeImage,
		},
		{
			Name:         "python",
			HostPort:     2102,
			ImageEnv:     "SNAP_INTEROP_PYTHON_IMAGE",
			DefaultImage: defaultPythonImage,
		},
	}
}

type harness struct {
	managed     bool
	fixturesDir string
	servers     []serverEndpoint
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	managed := os.Getenv("SNAP_INTEROP_MANAGED") != "0"
	dir, err := filepath.Abs(filepath.Join("testdata", "fixtures"))
	if err != nil {
		t.Fatalf("fixtures dir: %v", err)
	}
	h := &harness{
		managed:     managed,
		fixturesDir: dir,
		servers:     defaultServers(),
	}
	if addr := os.Getenv("SNAP_INTEROP_NATIVE_ADDR"); addr != "" {
		h.servers[0].HostPort = mustHostPort(t, addr)
	}
	if addr := os.Getenv("SNAP_INTEROP_PYTHON_ADDR"); addr != "" {
		h.servers[1].HostPort = mustHostPort(t, addr)
	}
	if managed {
		if _, err := exec.LookPath("docker"); err != nil {
			t.Skip("docker not available; set SNAP_INTEROP_MANAGED=0 to use external servers")
		}
		for i := range h.servers {
			img := getenv(h.servers[i].ImageEnv, h.servers[i].DefaultImage)
			if err := dockerPull(img); err != nil {
				t.Fatalf("docker pull %s: %v", img, err)
			}
		}
		t.Cleanup(func() { h.stopAll() })
	}
	return h
}

func (h *harness) restartWithFixture(t *testing.T, fixtureFile string) {
	t.Helper()
	if !h.managed {
		return
	}
	h.stopAll()
	for i := range h.servers {
		srv := &h.servers[i]
		img := getenv(srv.ImageEnv, srv.DefaultImage)
		name := fmt.Sprintf("go-s7comm-interop-%s-%d", srv.Name, time.Now().UnixNano())
		args := []string{
			"run", "-d", "--rm",
			"--name", name,
			"-p", fmt.Sprintf("%d:102", srv.HostPort),
			"-v", h.fixturesDir + ":/fixtures:ro",
			"-e", "SNAP_INTEROP_FIXTURE=/fixtures/" + fixtureFile,
			"-e", "SNAP_INTEROP_LISTEN_ADDRESS=0.0.0.0",
			"-e", "SNAP_INTEROP_PORT=102",
			"-e", "SNAP_INTEROP_LOG_FORMAT=json",
			"-e", "SNAP_INTEROP_STATE_PATH=/run/snap-interop/state.json",
			"--read-only",
			"--tmpfs", "/tmp",
			"--tmpfs", "/run/snap-interop:uid=10001,gid=10001,mode=0755",
			"--cap-drop", "ALL",
			"--cap-add", "NET_BIND_SERVICE",
			"--security-opt", "no-new-privileges:true",
			img,
		}
		out, err := exec.Command("docker", args...).CombinedOutput()
		if err != nil {
			t.Fatalf("docker run %s: %v\n%s", srv.Name, err, out)
		}
		srv.container = strings.TrimSpace(string(out))
	}
	for _, srv := range h.servers {
		if err := waitS7Ready("127.0.0.1", srv.HostPort, 30*time.Second); err != nil {
			logs := h.containerLogs(srv)
			t.Fatalf("%s not ready on port %d: %v\n%s", srv.Name, srv.HostPort, err, logs)
		}
	}
}

func (h *harness) stopAll() {
	for i := range h.servers {
		srv := &h.servers[i]
		if srv.container == "" {
			continue
		}
		_ = exec.Command("docker", "rm", "-f", srv.container).Run()
		srv.container = ""
	}
}

func (h *harness) containerLogs(srv serverEndpoint) string {
	if srv.container == "" {
		return ""
	}
	out, err := exec.Command("docker", "logs", "--tail", "80", srv.container).CombinedOutput()
	if err != nil {
		return fmt.Sprintf("docker logs: %v", err)
	}
	return string(out)
}

func dockerPull(image string) error {
	cmd := exec.Command("docker", "pull", image)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// waitS7Ready dials a full COTP+S7 setup. Bare TCP probes poison the Python adapter
// ("Connection closed by peer" during ISO accept).
func waitS7Ready(host string, port int, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	var last error
	for time.Now().Before(deadline) {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		c := newClient(host, port, 0, 2)
		err := c.Connect(ctx)
		_ = c.Close()
		cancel()
		if err == nil {
			return nil
		}
		last = err
		time.Sleep(300 * time.Millisecond)
	}
	return fmt.Errorf("timeout waiting for S7 on %s:%d: %v", host, port, last)
}

func getenv(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func mustHostPort(t *testing.T, addr string) int {
	t.Helper()
	_, portStr, err := net.SplitHostPort(addr)
	if err != nil {
		t.Fatalf("invalid addr %q: %v", addr, err)
	}
	port, err := strconv.Atoi(portStr)
	if err != nil {
		t.Fatalf("invalid port in %q: %v", addr, err)
	}
	return port
}
