package device

import (
	"os"
	"path/filepath"
	"testing"
)

func setupFakeSysfs(t *testing.T, devices map[string]string) (string, string) {
	t.Helper()
	root := t.TempDir()

	devBase := filepath.Join(root, "dev", "tenstorrent")
	sysBase := filepath.Join(root, "sys", "class", "tenstorrent")

	for id, cardType := range devices {
		// Create /dev/tenstorrent/N
		devPath := filepath.Join(devBase, id)
		if err := os.MkdirAll(devPath, 0o755); err != nil {
			t.Fatal(err)
		}

		// Create sysfs dir with card type
		sysDir := filepath.Join(sysBase, "tenstorrent!"+id)
		if err := os.MkdirAll(sysDir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(sysDir, "tt_card_type"), []byte(cardType+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}

		// Create device/numa_node
		deviceDir := filepath.Join(sysDir, "device")
		if err := os.MkdirAll(deviceDir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(deviceDir, "numa_node"), []byte("0\n"), 0o644); err != nil {
			t.Fatal(err)
		}

		// Create hwmon
		hwmonDir := filepath.Join(deviceDir, "hwmon", "hwmon0")
		if err := os.MkdirAll(hwmonDir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(hwmonDir, "temp1_input"), []byte("45000\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	return devBase, sysBase
}

func TestDiscoverSingleBlackhole(t *testing.T) {
	devBase, sysBase := setupFakeSysfs(t, map[string]string{
		"0": "p100a",
	})

	grouped, err := discover(devBase, sysBase)
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}

	devs, ok := grouped["blackhole"]
	if !ok {
		t.Fatal("expected blackhole class")
	}
	if len(devs) != 1 {
		t.Fatalf("expected 1 device, got %d", len(devs))
	}
	if devs[0].CardType != "p100a" {
		t.Errorf("expected card type p100a, got %s", devs[0].CardType)
	}
	if devs[0].NumaNode != 0 {
		t.Errorf("expected numa node 0, got %d", devs[0].NumaNode)
	}
	if devs[0].HwmonDir == "" {
		t.Error("expected hwmon dir to be set")
	}
	if devs[0].SysfsDir == "" {
		t.Error("expected sysfs dir to be set")
	}
}

func TestDiscoverMultipleClasses(t *testing.T) {
	devBase, sysBase := setupFakeSysfs(t, map[string]string{
		"0": "p100a",
		"1": "p150a",
		"2": "n300",
		"3": "e75",
	})

	grouped, err := discover(devBase, sysBase)
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}

	if len(grouped["blackhole"]) != 2 {
		t.Errorf("expected 2 blackhole devices, got %d", len(grouped["blackhole"]))
	}
	if len(grouped["n300"]) != 1 {
		t.Errorf("expected 1 n300 device, got %d", len(grouped["n300"]))
	}
	if len(grouped["grayskull"]) != 1 {
		t.Errorf("expected 1 grayskull device, got %d", len(grouped["grayskull"]))
	}
}

func TestDiscoverUnknownCardType(t *testing.T) {
	devBase, sysBase := setupFakeSysfs(t, map[string]string{
		"0": "unknown_card",
	})

	grouped, err := discover(devBase, sysBase)
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if len(grouped) != 0 {
		t.Errorf("expected no devices, got %d classes", len(grouped))
	}
}

func TestCardTypeToClass(t *testing.T) {
	tests := []struct {
		cardType string
		want     string
	}{
		{"p100a", "blackhole"},
		{"p150a", "blackhole"},
		{"p300c", "blackhole"},
		{"n150", "n150"},
		{"n300", "n300"},
		{"n300l", "n300"},
		{"n300s", "n300"},
		{"e75", "grayskull"},
		{"e150", "grayskull"},
		{"unknown", ""},
	}
	for _, tt := range tests {
		got := cardTypeToClass(tt.cardType)
		if got != tt.want {
			t.Errorf("cardTypeToClass(%q) = %q, want %q", tt.cardType, got, tt.want)
		}
	}
}

func TestIsNumericASCIIOnly(t *testing.T) {
	if !isNumeric("0") {
		t.Error("expected 0 to be numeric")
	}
	if !isNumeric("10") {
		t.Error("expected 10 to be numeric")
	}
	if !isNumeric("123") {
		t.Error("expected 123 to be numeric")
	}
	if isNumeric("") {
		t.Error("expected empty to be non-numeric")
	}
	if isNumeric("abc") {
		t.Error("expected abc to be non-numeric")
	}
	// Arabic-Indic digits must NOT pass.
	if isNumeric("\u0660") {
		t.Error("expected Arabic-Indic digit to be rejected")
	}
}
