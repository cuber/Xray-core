package commander

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/health"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
)

type testHealthService struct{}

func (testHealthService) Register(server *grpc.Server) {
	healthpb.RegisterHealthServer(server, health.NewServer())
}

func testAPI(t *testing.T, address string) *Commander {
	t.Helper()
	c := &Commander{listen: address, services: []Service{testHealthService{}}}
	t.Cleanup(func() { c.Close() })
	return c
}

func testHealth(t *testing.T, target string) {
	t.Helper()
	conn, err := grpc.NewClient(target, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	response, err := healthpb.NewHealthClient(conn).Check(ctx, &healthpb.HealthCheckRequest{}, grpc.WaitForReady(true))
	if err != nil || response.GetStatus() != healthpb.HealthCheckResponse_SERVING {
		t.Fatalf("health: %v %v", response, err)
	}
}

func testUnixPath(t *testing.T) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("fixture uses POSIX filesystem Unix sockets")
	}
	dir, err := os.MkdirTemp("/tmp", "xa-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	return filepath.Join(dir, "api.sock")
}

func TestDirectAPIUnixLifecycle(t *testing.T) {
	path := testUnixPath(t)
	for _, address := range []string{path, path + ",0600"} {
		c := testAPI(t, address)
		if err := c.Start(); err != nil {
			t.Fatal(err)
		}
		info, err := os.Stat(path)
		if err != nil || info.Mode().Perm() != 0600 {
			t.Fatal("private socket permission", info, err)
		}
		testHealth(t, "unix://"+path)
		if err := c.Close(); err != nil {
			t.Fatal(err)
		}
		if _, err := os.Lstat(path); !os.IsNotExist(err) {
			t.Fatal("closed socket remains", err)
		}
		if _, err := os.Lstat(path + ".lock"); !os.IsNotExist(err) {
			t.Fatal("closed socket lock remains", err)
		}
	}
}

func TestDirectAPIUnixOccupiedFailsFast(t *testing.T) {
	path := testUnixPath(t)
	first := testAPI(t, path)
	if err := first.Start(); err != nil {
		t.Fatal(err)
	}
	second := testAPI(t, path)
	start := time.Now()
	if err := second.Start(); err == nil || time.Since(start) > time.Second {
		t.Fatal("occupied API was accepted or waited on the owner", err)
	}
	testHealth(t, "unix://"+path)
}

func TestDirectAPIUnixStaleAndInvalidPaths(t *testing.T) {
	path := testUnixPath(t)
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	listener.SetUnlinkOnClose(false)
	listener.Close()
	c := testAPI(t, path)
	if err := c.Start(); err != nil {
		t.Fatal("stale socket recovery", err)
	}
	testHealth(t, "unix://"+path)
	c.Close()
	if err := os.WriteFile(path, []byte("preserve"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := testAPI(t, path).Start(); err == nil {
		t.Fatal("regular file accepted")
	}
	data, _ := os.ReadFile(path)
	if string(data) != "preserve" {
		t.Fatal("regular file changed")
	}
	os.Remove(path)
	for _, address := range []string{path + ",bad", path + ",1000", path + ",0600,0600", "api.sock,0600"} {
		if err := testAPI(t, address).Start(); err == nil {
			t.Fatal("invalid target accepted", address)
		}
	}
	occupied, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	defer occupied.Close()
	if err := testAPI(t, path).Start(); err == nil {
		t.Fatal("external active socket replaced")
	}
}

func TestDirectAPITCPUnchanged(t *testing.T) {
	reserved, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := reserved.Addr().String()
	if err := testAPI(t, address).Start(); err == nil {
		t.Fatal("occupied TCP accepted")
	}
	reserved.Close()
	c := testAPI(t, address)
	if err := c.Start(); err != nil {
		t.Fatal(err)
	}
	testHealth(t, address)
	c.Close()
	rebound, err := net.Listen("tcp", address)
	if err != nil {
		t.Fatal("TCP listener leaked", err)
	}
	rebound.Close()
}
