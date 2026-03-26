package main

import (
	"context"
	"os/signal"
	"sync"
	"syscall"

	"github.com/fsnotify/fsnotify"
	"k8s.io/klog/v2"
	pluginapi "k8s.io/kubelet/pkg/apis/deviceplugin/v1beta1"

	"github.com/OctopusET/k8s-device-plugin/internal/device"
	"github.com/OctopusET/k8s-device-plugin/internal/plugin"
)

var version = "dev"

func main() {
	klog.InitFlags(nil)
	klog.Infof("Tenstorrent device plugin %s", version)

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	var mu sync.Mutex
	plugins := startPlugins(ctx)

	go watchKubelet(ctx, func() {
		mu.Lock()
		defer mu.Unlock()
		for _, p := range plugins {
			p.Stop()
		}
		plugins = startPlugins(ctx)
	})

	<-ctx.Done()
	klog.Info("Shutting down")
	mu.Lock()
	for _, p := range plugins {
		p.Stop()
	}
	mu.Unlock()
}

func startPlugins(ctx context.Context) []*plugin.Plugin {
	grouped, err := device.Discover()
	if err != nil {
		klog.Fatalf("Device discovery failed: %v", err)
	}
	if len(grouped) == 0 {
		klog.Fatal("No Tenstorrent devices found")
	}

	var plugins []*plugin.Plugin
	for class, devs := range grouped {
		p := plugin.New(class, devs)
		plugins = append(plugins, p)

		go func(p *plugin.Plugin) {
			if err := p.Run(ctx); err != nil {
				klog.Errorf("Plugin error: %v", err)
			}
		}(p)
	}
	return plugins
}

func watchKubelet(ctx context.Context, restart func()) {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		klog.Fatalf("Failed to create fsnotify watcher: %v", err)
	}
	defer func() { _ = watcher.Close() }()

	if err := watcher.Add(pluginapi.DevicePluginPath); err != nil {
		klog.Fatalf("Failed to watch %s: %v", pluginapi.DevicePluginPath, err)
	}

	for {
		select {
		case <-ctx.Done():
			return
		case event := <-watcher.Events:
			if event.Name == pluginapi.KubeletSocket && event.Has(fsnotify.Create) {
				klog.Info("Kubelet restarted, re-registering")
				restart()
			}
		case err := <-watcher.Errors:
			klog.Errorf("fsnotify error: %v", err)
		}
	}
}
