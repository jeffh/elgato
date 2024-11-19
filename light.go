package elgato

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
)

type LightAccessory struct {
	Addr   string
	Client http.Client

	// Filled by Connect()
	Settings LightSettings
	Info     LightAccessoryInfo
	Options  LightOptions
}

func MakeLightAccessory(addr string) LightAccessory {
	return LightAccessory{Addr: addr}
}

type LightSettings struct {
	ColorChangeDurationMsec int `json:"colorChangeDurationMs"`
	PowerOnBehavior         int `json:"powerOnBehavior"`
	PowerOnBrightness       int `json:"powerOnBrightness"`
	PowerOnTempurature      int `json:"powerOnTemperature"`
	SwitchOffDurationMsec   int `json:"switchOffDurationMs"`
	SwitchOnDurationMsec    int `json:"switchOnDurationMs"`
}

type LightAccessoryInfo struct {
	DisplayName         string   `json:"displayName"`
	ProductName         string   `json:"productName"`
	SerialNumber        string   `json:"serialNumber"`
	HardwareBoardType   int      `json:"hardwareBoardType"`   // eg - 200
	Features            []string `json:"features"`            // []string{"lights"}
	FirmwareBuildNumber int      `json:"firmwareBuildNumber"` // eg - 214
	FirmwareVersion     string   `json:"firmwareVersion"`     // eg - "1.0.3"
	WifiInfo            WifiInfo `json:"wifi-info"`
}

type WifiInfo struct {
	Ssid         string `json:"ssid"`
	FrequencyMhz int    `json:"frequencyMhz"`
	Rssi         int    `json:"rssi"`
}

type LightOptions struct {
	NumberOfLights int           `json:"numberOfLights"`
	Lights         []LightConfig `json:"lights"`
}

type LightConfig struct {
	Brightness  int `json:"brightness"`
	On          int `json:"on"`
	Temperature int `json:"temperature"`
}

// Connect pulls the necessary information about the light accessory to make changes.
//
// Since this is an HTTP API, there isn't any close or disconnection needed.
func (L *LightAccessory) Connect(ctx context.Context) error {
	err := L.request(ctx, "GET", "/elgato/lights/settings", nil, &L.Settings)
	if err != nil {
		return err
	}

	err = L.request(ctx, "GET", "/elgato/accessory-info", nil, &L.Info)
	if err != nil {
		return err
	}

	err = L.request(ctx, "GET", "/elgato/lights", nil, &L.Options)
	if err != nil {
		return err
	}

	return nil
}

func (L *LightAccessory) Identify(ctx context.Context) error {
	slog.Info("Elgato.Identify", slog.String("Serial", L.Info.SerialNumber))
	return L.request(ctx, "POST", "/elgato/identify", nil, nil)
}

// Refreshes the light state from the accessory (on/off, brightness, temperature)
func (L *LightAccessory) Refresh(ctx context.Context) error {
	// too noisy
	// slog.Info("Elgato.Refresh", slog.String("Serial", L.Info.SerialNumber))
	return L.request(ctx, "GET", "/elgato/lights", nil, &L.Options)
}

// Set assigns the same config to all connected lights for the particular light accessory.
//
// This is more efficient than using SetBrightness, SetOn, SetTemperature
func (L *LightAccessory) Set(ctx context.Context, c LightConfig) error {
	slog.Info(
		"Elgato.Set",
		slog.String("Serial", L.Info.SerialNumber),
		slog.Bool("On", c.On != 0),
		slog.Int("Brightness", c.Brightness),
		slog.Int("Temperature", c.Temperature),
	)
	for i := range L.Options.Lights {
		if c.Brightness != 0 {
			L.Options.Lights[i].Brightness = c.Brightness
		}
		if c.Temperature != 0 {
			L.Options.Lights[i].Temperature = c.Temperature
		}
		L.Options.Lights[i].On = c.On
	}
	return L.Update(ctx, L.Options.Lights)
}

// SetBrightness assigns light brightness value to all lights attached to the accessory
// value ranges from 0 to 100
func (L *LightAccessory) SetBrightness(ctx context.Context, value int) error {
	slog.Info(
		"Elgato.SetBrightness",
		slog.String("Serial", L.Info.SerialNumber),
		slog.Int("Brightness", value),
	)
	for i := range L.Options.Lights {
		L.Options.Lights[i].Brightness = value
	}
	return L.Update(ctx, L.Options.Lights)
}

// SetTemperature assigns light temperature value to all lights attached to the accessory
// Ranges for Key Light Air are 143 - 344
func (L *LightAccessory) SetTemperature(ctx context.Context, value int) error {
	slog.Info(
		"Elgato.SetTemperature",
		slog.String("Serial", L.Info.SerialNumber),
		slog.Int("Temperature", value),
	)
	for i := range L.Options.Lights {
		L.Options.Lights[i].Temperature = value
	}
	return L.Update(ctx, L.Options.Lights)
}

// SetOn turns on or off lights attached to the given accessory
func (L *LightAccessory) SetOn(ctx context.Context, isOn bool) error {
	slog.Info(
		"Elgato.SetOn",
		slog.String("Serial", L.Info.SerialNumber),
		slog.Bool("On", isOn),
	)
	var v int
	if isOn {
		v = 1
	} else {
		v = 0
	}
	return L.Set(ctx, LightConfig{On: v})
}

// Update updates configuration for each light attached to the accessory
func (L *LightAccessory) Update(ctx context.Context, lc []LightConfig) error {
	type req struct {
		NumberOfLights int           `json:"numberOfLights"`
		Lights         []LightConfig `json:"lights"`
	}
	r := req{
		NumberOfLights: len(lc),
		Lights:         lc,
	}
	err := L.request(ctx, "PUT", "/elgato/lights", &r, &L.Options)
	return err
}

func (L *LightAccessory) request(ctx context.Context, method, path string, input, output interface{}) error {
	var body io.Reader
	if input != nil {
		buf, err := json.Marshal(input)
		if err != nil {
			return err
		}

		body = bytes.NewReader(buf)
	}
	req, err := http.NewRequestWithContext(ctx, method, "http://"+L.Addr+path, body)
	if err != nil {
		return err
	}
	res, err := L.Client.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()

	if res.StatusCode < 200 || res.StatusCode > 300 {
		return fmt.Errorf("unexpected http status code: %d %s", res.StatusCode, res.Status)
	}

	if output == nil {
		return err
	}

	buf, err := io.ReadAll(res.Body)
	if err != nil {
		return err
	}

	if err = json.Unmarshal(buf, output); err != nil {
		return err
	}
	return nil
}
