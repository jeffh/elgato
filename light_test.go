package elgato

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// mockLightServer creates a test server that simulates an Elgato light device
func mockLightServer(t *testing.T) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/elgato/lights/settings":
			json.NewEncoder(w).Encode(LightSettings{
				ColorChangeDurationMsec: 100,
				PowerOnBehavior:         1,
				PowerOnBrightness:       50,
				PowerOnTempurature:      4000,
				SwitchOffDurationMsec:   300,
				SwitchOnDurationMsec:    200,
			})
		case "/elgato/accessory-info":
			json.NewEncoder(w).Encode(LightAccessoryInfo{
				DisplayName:         "Test Light",
				ProductName:         "Elgato Key Light",
				SerialNumber:        "KL12345",
				HardwareBoardType:   200,
				Features:            []string{"lights"},
				FirmwareBuildNumber: 214,
				FirmwareVersion:     "1.0.3",
				WifiInfo: WifiInfo{
					Ssid:         "TestWifi",
					FrequencyMhz: 5000,
					Rssi:         -50,
				},
			})
		case "/elgato/lights":
			if r.Method == "GET" {
				json.NewEncoder(w).Encode(LightOptions{
					NumberOfLights: 1,
					Lights: []LightConfig{
						{Brightness: 50, On: 1, Temperature: 4000},
					},
				})
			} else if r.Method == "PUT" {
				var req struct {
					NumberOfLights int           `json:"numberOfLights"`
					Lights         []LightConfig `json:"lights"`
				}
				if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
					t.Error("failed to decode request:", err)
					w.WriteHeader(http.StatusBadRequest)
					return
				}
				// Echo back the request as the response
				json.NewEncoder(w).Encode(req)
			}
		case "/elgato/identify":
			if r.Method != "POST" {
				w.WriteHeader(http.StatusMethodNotAllowed)
				return
			}
			w.WriteHeader(http.StatusOK)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
}

func TestConnect(t *testing.T) {
	srv := mockLightServer(t)
	defer srv.Close()

	// Extract host and port from test server
	light := LightAccessory{Addr: srv.Listener.Addr().String()}

	ctx := context.Background()
	if err := light.Connect(ctx); err != nil {
		t.Fatalf("Connect failed: %v", err)
	}

	// Verify settings
	if light.Settings.PowerOnBrightness != 50 {
		t.Errorf("expected PowerOnBrightness=50, got %d", light.Settings.PowerOnBrightness)
	}

	// Verify info
	if light.Info.SerialNumber != "KL12345" {
		t.Errorf("expected SerialNumber=KL12345, got %s", light.Info.SerialNumber)
	}

	// Verify options
	if len(light.Options.Lights) != 1 {
		t.Errorf("expected 1 light, got %d", len(light.Options.Lights))
	}
}

func TestSetBrightness(t *testing.T) {
	srv := mockLightServer(t)
	defer srv.Close()

	light := LightAccessory{Addr: srv.Listener.Addr().String()}
	ctx := context.Background()

	// Connect first
	if err := light.Connect(ctx); err != nil {
		t.Fatalf("Connect failed: %v", err)
	}

	// Test setting brightness
	if err := light.SetBrightness(ctx, 75); err != nil {
		t.Fatalf("SetBrightness failed: %v", err)
	}

	// Verify the light config was updated
	if light.Options.Lights[0].Brightness != 75 {
		t.Errorf("expected Brightness=75, got %d", light.Options.Lights[0].Brightness)
	}
}

func TestSetTemperature(t *testing.T) {
	srv := mockLightServer(t)
	defer srv.Close()

	light := LightAccessory{Addr: srv.Listener.Addr().String()}
	ctx := context.Background()

	if err := light.Connect(ctx); err != nil {
		t.Fatalf("Connect failed: %v", err)
	}

	if err := light.SetTemperature(ctx, 5000); err != nil {
		t.Fatalf("SetTemperature failed: %v", err)
	}

	if light.Options.Lights[0].Temperature != 5000 {
		t.Errorf("expected Temperature=5000, got %d", light.Options.Lights[0].Temperature)
	}
}

func TestSetOn(t *testing.T) {
	srv := mockLightServer(t)
	defer srv.Close()

	light := LightAccessory{Addr: srv.Listener.Addr().String()}
	ctx := context.Background()

	if err := light.Connect(ctx); err != nil {
		t.Fatalf("Connect failed: %v", err)
	}

	// Test turning off
	if err := light.SetOn(ctx, false); err != nil {
		t.Fatalf("SetOn(false) failed: %v", err)
	}

	if light.Options.Lights[0].On != 0 {
		t.Errorf("expected On=0, got %d", light.Options.Lights[0].On)
	}

	// Test turning on
	if err := light.SetOn(ctx, true); err != nil {
		t.Fatalf("SetOn(true) failed: %v", err)
	}

	if light.Options.Lights[0].On != 1 {
		t.Errorf("expected On=1, got %d", light.Options.Lights[0].On)
	}
}

func TestIdentify(t *testing.T) {
	srv := mockLightServer(t)
	defer srv.Close()

	light := LightAccessory{Addr: srv.Listener.Addr().String()}
	ctx := context.Background()

	if err := light.Connect(ctx); err != nil {
		t.Fatalf("Connect failed: %v", err)
	}

	if err := light.Identify(ctx); err != nil {
		t.Fatalf("Identify failed: %v", err)
	}
}

func TestSet(t *testing.T) {
	srv := mockLightServer(t)
	defer srv.Close()

	light := LightAccessory{Addr: srv.Listener.Addr().String()}
	ctx := context.Background()

	if err := light.Connect(ctx); err != nil {
		t.Fatalf("Connect failed: %v", err)
	}

	config := LightConfig{
		Brightness:  80,
		On:          1,
		Temperature: 4500,
	}

	if err := light.Set(ctx, config); err != nil {
		t.Fatalf("Set failed: %v", err)
	}

	// Verify all values were updated
	if light.Options.Lights[0].Brightness != 80 {
		t.Errorf("expected Brightness=80, got %d", light.Options.Lights[0].Brightness)
	}
	if light.Options.Lights[0].On != 1 {
		t.Errorf("expected On=1, got %d", light.Options.Lights[0].On)
	}
	if light.Options.Lights[0].Temperature != 4500 {
		t.Errorf("expected Temperature=4500, got %d", light.Options.Lights[0].Temperature)
	}
}
