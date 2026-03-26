package plugin

import (
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/connectivity"
	"google.golang.org/grpc/credentials/insecure"
	"k8s.io/klog/v2"
	pluginapi "k8s.io/kubelet/pkg/apis/deviceplugin/v1beta1"

	"github.com/OctopusET/k8s-device-plugin/internal/device"
)

const (
	resourceDomain   = "tenstorrent.com"
	healthInterval   = 30 * time.Second
	hugepagesPath    = "/dev/hugepages-1G"
	stopGracePeriod  = 5 * time.Second
)

type Plugin struct {
	pluginapi.UnimplementedDevicePluginServer

	resourceName string
	socketName   string
	socketPath   string
	devices      []device.Device
	grpcServer   *grpc.Server
	stop         chan struct{}

	mu             sync.Mutex
	lastHeartbeats map[string]string
}

func New(resourceClass string, devices []device.Device) *Plugin {
	socketName := "tenstorrent-" + resourceClass + ".sock"
	return &Plugin{
		resourceName:   resourceDomain + "/" + resourceClass,
		socketName:     socketName,
		socketPath:     filepath.Join(pluginapi.DevicePluginPath, socketName),
		devices:        devices,
		stop:           make(chan struct{}),
		lastHeartbeats: make(map[string]string),
	}
}

func (p *Plugin) Run(ctx context.Context) error {
	if err := removeSocket(p.socketPath); err != nil {
		return err
	}

	if err := p.serve(); err != nil {
		return err
	}

	if err := p.waitReady(); err != nil {
		return err
	}

	if err := p.register(); err != nil {
		return err
	}

	klog.Infof("Serving %s (%d devices) on %s", p.resourceName, len(p.devices), p.socketName)

	select {
	case <-ctx.Done():
	case <-p.stop:
	}
	return nil
}

func (p *Plugin) Stop() {
	select {
	case <-p.stop:
	default:
		close(p.stop)
	}
	if p.grpcServer != nil {
		done := make(chan struct{})
		go func() {
			p.grpcServer.GracefulStop()
			close(done)
		}()
		select {
		case <-done:
		case <-time.After(stopGracePeriod):
			klog.Warningf("GracefulStop timed out for %s, forcing stop", p.resourceName)
			p.grpcServer.Stop()
		}
	}
	_ = removeSocket(p.socketPath)
}

func (p *Plugin) serve() error {
	lis, err := net.Listen("unix", p.socketPath)
	if err != nil {
		return fmt.Errorf("listen %s: %w", p.socketPath, err)
	}

	p.grpcServer = grpc.NewServer()
	pluginapi.RegisterDevicePluginServer(p.grpcServer, p)

	go func() {
		if err := p.grpcServer.Serve(lis); err != nil {
			klog.Errorf("gRPC serve error: %v", err)
		}
	}()

	return nil
}

func (p *Plugin) waitReady() error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	conn, err := grpc.NewClient("unix://"+p.socketPath,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		return fmt.Errorf("dial self: %w", err)
	}
	defer func() { _ = conn.Close() }()

	conn.Connect()
	for {
		if conn.GetState() == connectivity.Ready {
			return nil
		}
		if !conn.WaitForStateChange(ctx, conn.GetState()) {
			return fmt.Errorf("gRPC server not ready within timeout")
		}
	}
}

func (p *Plugin) register() error {
	conn, err := grpc.NewClient("unix://"+pluginapi.KubeletSocket,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		return fmt.Errorf("dial kubelet: %w", err)
	}
	defer func() { _ = conn.Close() }()

	client := pluginapi.NewRegistrationClient(conn)
	_, err = client.Register(context.Background(), &pluginapi.RegisterRequest{
		Version:      pluginapi.Version,
		Endpoint:     p.socketName,
		ResourceName: p.resourceName,
	})
	if err != nil {
		return fmt.Errorf("register %s: %w", p.resourceName, err)
	}

	return nil
}

func (p *Plugin) GetDevicePluginOptions(context.Context, *pluginapi.Empty) (*pluginapi.DevicePluginOptions, error) {
	return &pluginapi.DevicePluginOptions{}, nil
}

func (p *Plugin) ListAndWatch(_ *pluginapi.Empty, stream pluginapi.DevicePlugin_ListAndWatchServer) error {
	if err := stream.Send(&pluginapi.ListAndWatchResponse{Devices: p.buildDeviceList()}); err != nil {
		return err
	}

	ticker := time.NewTicker(healthInterval)
	defer ticker.Stop()

	for {
		select {
		case <-p.stop:
			return nil
		case <-ticker.C:
			if err := stream.Send(&pluginapi.ListAndWatchResponse{Devices: p.buildDeviceList()}); err != nil {
				return err
			}
		}
	}
}

func (p *Plugin) Allocate(_ context.Context, req *pluginapi.AllocateRequest) (*pluginapi.AllocateResponse, error) {
	var responses []*pluginapi.ContainerAllocateResponse

	for _, creq := range req.ContainerRequests {
		var devSpecs []*pluginapi.DeviceSpec
		var ids []string

		for _, id := range creq.DevicesIds {
			dev := p.findDevice(id)
			if dev == nil {
				return nil, fmt.Errorf("unknown device: %s", id)
			}
			devSpecs = append(devSpecs, &pluginapi.DeviceSpec{
				HostPath:      dev.DevPath,
				ContainerPath: dev.DevPath,
				Permissions:   "rw",
			})
			ids = append(ids, id)
		}

		mounts := []*pluginapi.Mount{
			{
				HostPath:      "/sys",
				ContainerPath: "/sys",
				ReadOnly:      true,
			},
		}
		if _, err := os.Stat(hugepagesPath); err == nil {
			mounts = append(mounts, &pluginapi.Mount{
				HostPath:      hugepagesPath,
				ContainerPath: hugepagesPath,
				ReadOnly:      false,
			})
		}

		responses = append(responses, &pluginapi.ContainerAllocateResponse{
			Envs: map[string]string{
				"TT_VISIBLE_DEVICES": strings.Join(ids, ","),
			},
			Devices: devSpecs,
			Mounts:  mounts,
		})
	}

	return &pluginapi.AllocateResponse{ContainerResponses: responses}, nil
}

func (p *Plugin) GetPreferredAllocation(context.Context, *pluginapi.PreferredAllocationRequest) (*pluginapi.PreferredAllocationResponse, error) {
	return &pluginapi.PreferredAllocationResponse{}, nil
}

func (p *Plugin) PreStartContainer(context.Context, *pluginapi.PreStartContainerRequest) (*pluginapi.PreStartContainerResponse, error) {
	return &pluginapi.PreStartContainerResponse{}, nil
}

func (p *Plugin) buildDeviceList() []*pluginapi.Device {
	list := make([]*pluginapi.Device, len(p.devices))
	for i, dev := range p.devices {
		d := &pluginapi.Device{
			ID:     dev.ID,
			Health: p.checkHealth(dev),
		}
		if dev.NumaNode >= 0 {
			d.Topology = &pluginapi.TopologyInfo{
				Nodes: []*pluginapi.NUMANode{{ID: dev.NumaNode}},
			}
		}
		list[i] = d
	}
	return list
}

func (p *Plugin) checkHealth(dev device.Device) string {
	if dev.HwmonDir != "" {
		_, err := os.ReadFile(filepath.Join(dev.HwmonDir, "temp1_input"))
		if err != nil {
			klog.Warningf("Device %s unhealthy (temp sensor): %v", dev.ID, err)
			return pluginapi.Unhealthy
		}
	}

	if dev.SysfsDir != "" {
		hb, err := device.ReadSysfs(filepath.Join(dev.SysfsDir, "tt_heartbeat"))
		if err == nil {
			p.mu.Lock()
			prev, hasPrev := p.lastHeartbeats[dev.ID]
			p.lastHeartbeats[dev.ID] = hb
			p.mu.Unlock()

			if hasPrev && prev == hb {
				klog.Warningf("Device %s unhealthy (heartbeat stalled at %s)", dev.ID, hb)
				return pluginapi.Unhealthy
			}
		}
	}

	return pluginapi.Healthy
}

func removeSocket(path string) error {
	err := os.Remove(path)
	if err != nil && !os.IsNotExist(err) {
		klog.Warningf("Failed to remove socket %s: %v", path, err)
		return err
	}
	return nil
}

func (p *Plugin) findDevice(id string) *device.Device {
	for i := range p.devices {
		if p.devices[i].ID == id {
			return &p.devices[i]
		}
	}
	return nil
}
