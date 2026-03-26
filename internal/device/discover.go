package device

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"k8s.io/klog/v2"
)

const (
	devDir    = "/dev/tenstorrent"
	sysfsBase = "/sys/class/tenstorrent"
)

type Device struct {
	ID       string // "0", "1", etc.
	DevPath  string // "/dev/tenstorrent/0"
	CardType string // "p100a", "n150", etc.
	Class    string // "blackhole", "n150", "n300", "grayskull"
	NumaNode int64  // from sysfs, -1 if unavailable
	HwmonDir string // resolved hwmon path for health checks
	SysfsDir string // "/sys/class/tenstorrent/tenstorrent!0"
}

// Discover returns devices grouped by resource class.
func Discover() (map[string][]Device, error) {
	return discover(devDir, sysfsBase)
}

func discover(devBase, sysBase string) (map[string][]Device, error) {
	entries, err := filepath.Glob(filepath.Join(devBase, "*"))
	if err != nil {
		return nil, fmt.Errorf("glob %s: %w", devBase, err)
	}

	result := make(map[string][]Device)
	for _, entry := range entries {
		name := filepath.Base(entry)
		if !isNumeric(name) {
			continue
		}

		sysfsDir := filepath.Join(sysBase, "tenstorrent!"+name)

		cardType, err := ReadSysfs(filepath.Join(sysfsDir, "tt_card_type"))
		if err != nil {
			klog.Warningf("Skipping device %s: cannot read card type: %v", name, err)
			continue
		}

		class := cardTypeToClass(cardType)
		if class == "" {
			klog.Warningf("Skipping device %s: unknown card type %q", name, cardType)
			continue
		}

		dev := Device{
			ID:       name,
			DevPath:  entry,
			CardType: cardType,
			Class:    class,
			NumaNode: readNumaNode(filepath.Join(sysfsDir, "device", "numa_node")),
			HwmonDir: findHwmonDir(filepath.Join(sysfsDir, "device", "hwmon")),
			SysfsDir: sysfsDir,
		}
		result[class] = append(result[class], dev)
	}

	return result, nil
}

var classMap = map[string]string{
	"p100a": "blackhole",
	"p150a": "blackhole",
	"p150b": "blackhole",
	"p150c": "blackhole",
	"p300a": "blackhole",
	"p300b": "blackhole",
	"p300c": "blackhole",
	"n150":  "n150",
	"n300":  "n300",
	"n300l": "n300",
	"n300s": "n300",
	"e75":   "grayskull",
	"e150":  "grayskull",
}

func cardTypeToClass(cardType string) string {
	return classMap[cardType]
}

func ReadSysfs(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(data)), nil
}

func readNumaNode(path string) int64 {
	s, err := ReadSysfs(path)
	if err != nil {
		return -1
	}
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return -1
	}
	return n
}

func findHwmonDir(hwmonBase string) string {
	matches, err := filepath.Glob(filepath.Join(hwmonBase, "hwmon*"))
	if err != nil || len(matches) == 0 {
		return ""
	}
	return matches[0]
}

func isNumeric(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}
