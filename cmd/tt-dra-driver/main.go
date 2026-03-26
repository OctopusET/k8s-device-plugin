package main

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"google.golang.org/grpc"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/klog/v2"
	drapb "k8s.io/kubelet/pkg/apis/dra/v1"

	resourceapi "k8s.io/api/resource/v1beta2"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/OctopusET/k8s-device-plugin/internal/device"
)

const (
	driverName = "tenstorrent.com"
	socketPath = "/var/lib/kubelet/plugins/tenstorrent.com/plugin.sock"
)

var version = "dev"

type draDriver struct {
	drapb.UnimplementedDRAPluginServer
	nodeName string
}

func (d *draDriver) NodePrepareResources(_ context.Context, req *drapb.NodePrepareResourcesRequest) (*drapb.NodePrepareResourcesResponse, error) {
	resp := &drapb.NodePrepareResourcesResponse{
		Claims: make(map[string]*drapb.NodePrepareResourceResponse),
	}
	for _, claim := range req.Claims {
		klog.Infof("Preparing claim %s/%s", claim.Namespace, claim.Name)
		resp.Claims[claim.Uid] = &drapb.NodePrepareResourceResponse{}
	}
	return resp, nil
}

func (d *draDriver) NodeUnprepareResources(_ context.Context, req *drapb.NodeUnprepareResourcesRequest) (*drapb.NodeUnprepareResourcesResponse, error) {
	resp := &drapb.NodeUnprepareResourcesResponse{
		Claims: make(map[string]*drapb.NodeUnprepareResourceResponse),
	}
	for _, claim := range req.Claims {
		klog.Infof("Unpreparing claim %s/%s", claim.Namespace, claim.Name)
		resp.Claims[claim.Uid] = &drapb.NodeUnprepareResourceResponse{}
	}
	return resp, nil
}

func publishResourceSlices(ctx context.Context, clientset *kubernetes.Clientset, nodeName string) error {
	grouped, err := device.Discover()
	if err != nil {
		return fmt.Errorf("discover: %w", err)
	}

	for class, devs := range grouped {
		poolName := fmt.Sprintf("%s-%s", nodeName, class)

		var devices []resourceapi.Device
		for _, dev := range devs {
			devices = append(devices, resourceapi.Device{
				Name: dev.ID,
				Attributes: map[resourceapi.QualifiedName]resourceapi.DeviceAttribute{
					"architecture": {StringValue: &class},
					"cardType":     {StringValue: &dev.CardType},
				},
			})
		}

		slice := &resourceapi.ResourceSlice{
			ObjectMeta: metav1.ObjectMeta{
				Name: poolName,
			},
			Spec: resourceapi.ResourceSliceSpec{
				Driver:   driverName,
				Pool:     resourceapi.ResourcePool{Name: poolName},
				NodeName: &nodeName,
				Devices:  devices,
			},
		}

		_, err := clientset.ResourceV1beta2().ResourceSlices().Create(ctx, slice, metav1.CreateOptions{})
		if err != nil {
			klog.Warningf("Failed to create ResourceSlice %s: %v", poolName, err)
			continue
		}
		klog.Infof("Published ResourceSlice %s with %d devices", poolName, len(devices))
	}

	return nil
}

func main() {
	klog.InitFlags(nil)
	klog.Infof("Tenstorrent DRA driver %s (PoC)", version)

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	nodeName := os.Getenv("NODE_NAME")
	if nodeName == "" {
		klog.Fatal("NODE_NAME env required")
	}

	config, err := rest.InClusterConfig()
	if err != nil {
		klog.Fatalf("Failed to get in-cluster config: %v", err)
	}
	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		klog.Fatalf("Failed to create clientset: %v", err)
	}

	if err := publishResourceSlices(ctx, clientset, nodeName); err != nil {
		klog.Fatalf("Failed to publish resource slices: %v", err)
	}

	dir := filepath.Dir(socketPath)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		klog.Fatalf("Failed to create socket dir: %v", err)
	}
	os.Remove(socketPath)

	lis, err := net.Listen("unix", socketPath)
	if err != nil {
		klog.Fatalf("Failed to listen: %v", err)
	}

	grpcServer := grpc.NewServer()
	drapb.RegisterDRAPluginServer(grpcServer, &draDriver{nodeName: nodeName})

	go func() {
		if err := grpcServer.Serve(lis); err != nil {
			klog.Fatalf("gRPC serve: %v", err)
		}
	}()

	klog.Infof("DRA node plugin serving on %s", socketPath)

	<-ctx.Done()
	klog.Info("Shutting down")
	grpcServer.GracefulStop()
}
