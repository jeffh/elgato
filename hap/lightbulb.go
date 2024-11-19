package hap

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/brutella/hap/accessory"
	"github.com/brutella/hap/characteristic"
	"github.com/brutella/hap/service"
	"github.com/jeffh/elgato"
)

type ElgatoLightAccessory struct {
	*accessory.A

	Lightbulb *ElgatoLightbulb
	L         elgato.LightAccessory
}

func hash(s string) uint64 {
	hash := uint64(0x811c9dc5)
	prime := uint64(0x1000193)
	for _, r := range s {
		hash ^= uint64(r)
		hash *= prime
	}
	return hash
}

// NewElgatoLightAccessory creates a new ElgatoAccessory from an elgato.LightAccessory
// If refreshInterval=0, then it defaults to 5 seconds. Any negative value disables refreshing.
func NewElgatoLightAccessory(ctx context.Context, L elgato.LightAccessory, refreshInterval time.Duration) *ElgatoLightAccessory {
	dlog := slog.Default().With(
		slog.String("serial", L.Info.SerialNumber),
		slog.String("addr", L.Addr),
	)

	a := ElgatoLightAccessory{}
	a.A = accessory.New(accessory.Info{
		Name:         L.Info.DisplayName,
		SerialNumber: L.Info.SerialNumber,
		Manufacturer: "Elgato",
		Firmware:     fmt.Sprintf("%s b%d", L.Info.FirmwareVersion, L.Info.FirmwareBuildNumber),
	}, accessory.TypeLightbulb)
	a.A.Id = hash(L.Info.SerialNumber)
	a.A.IdentifyFunc = func(r *http.Request) {
		L.Identify(r.Context())
	}

	if refreshInterval == 0 {
		refreshInterval = 5 * time.Second
	}

	if refreshInterval > 0 {
		go func() {
			ticker := time.NewTicker(refreshInterval)
			defer ticker.Stop()
			for {
				select {
				case <-ctx.Done():
					return
				case <-ticker.C:
					if err := L.Refresh(ctx); err != nil {
						dlog.Error("Refresh", "error", err)
						continue
					}
					if len(L.Options.Lights) == 0 {
						dlog.Error("No lights found")
						continue
					}
					a.Lightbulb.On.SetValue(L.Options.Lights[0].On != 0)
					a.Lightbulb.Brightness.SetValue(L.Options.Lights[0].Brightness)
					a.Lightbulb.ColorTemperature.SetValue(L.Options.Lights[0].Temperature)
				}
			}
		}()
	}

	a.Lightbulb = NewElgatoLightbulb()
	a.L = L
	a.A.AddS(a.Lightbulb.S)

	if len(L.Options.Lights) == 0 {
		dlog.Error("No lights found")
		return nil
	}

	a.Lightbulb.On.SetValue(L.Options.Lights[0].On != 0)
	a.Lightbulb.On.OnSetRemoteValue(func(v bool) error {
		if err := L.SetOn(context.Background(), v); err != nil {
			dlog.Error("SetOn", "value", v, "error", err)
			return err
		}
		return nil
	})

	a.Lightbulb.Brightness.SetValue(L.Options.Lights[0].Brightness)
	a.Lightbulb.Brightness.OnSetRemoteValue(func(v int) error {
		if err := L.SetBrightness(context.Background(), v); err != nil {
			dlog.Error("SetBrightness", "value", v, "error", err)
			return err
		}
		return nil
	})

	a.Lightbulb.ColorTemperature.SetValue(L.Options.Lights[0].Temperature)
	a.Lightbulb.ColorTemperature.OnSetRemoteValue(func(v int) error {
		if err := L.SetTemperature(context.Background(), v); err != nil {
			dlog.Error("SetTemperature", "value", v, "error", err)
			return err
		}
		return nil
	})

	return &a
}

type ElgatoLightbulb struct {
	*service.S

	On               *characteristic.On
	Brightness       *characteristic.Brightness
	ColorTemperature *characteristic.ColorTemperature
}

func NewElgatoLightbulb() *ElgatoLightbulb {
	s := ElgatoLightbulb{}
	s.S = service.New(service.TypeLightbulb)

	s.On = characteristic.NewOn()
	s.S.AddC(s.On.C)

	s.Brightness = characteristic.NewBrightness()
	s.S.AddC(s.Brightness.C)

	s.ColorTemperature = characteristic.NewColorTemperature()
	s.S.AddC(s.ColorTemperature.C)

	s.ColorTemperature.SetMinValue(143)
	s.ColorTemperature.SetMaxValue(344)

	return &s
}
