package elgato

import (
	"context"
	"log/slog"
	"time"

	"github.com/grandcat/zeroconf"
	"github.com/jeffh/elgato/ezmdns"
)

const (
	LightService  = "_elg._tcp"
	CameraService = "_egstreamwrap._tcp"
)

// DiscoverLocalAccessories continuously finds for local elgato light accessories
func DiscoverLocalAccessories(ctx context.Context, opt ezmdns.DiscoverOptions) (chan ezmdns.ServiceChangedEvent[*LightAccessory], error) {
	if opt.Service == "" {
		opt.Service = LightService
	}

	stream, err := ezmdns.RunDiscovery(ctx, opt)
	if err != nil {
		return nil, err
	}

	out := make(chan ezmdns.ServiceChangedEvent[*LightAccessory], 1)
	// go ezmdns.MapStream(stream, out, func(e *zeroconf.ServiceEntry) (*LightAccessory, bool) {
	// 	return connectToLight(ctx, e)
	// })
	go func() {
		lights := make(map[string]*LightAccessory)
		for e := range stream {
			for _, n := range e.New {
				if _, ok := lights[n.HostName]; !ok {
					light, ok := connectToLight(ctx, n)
					if ok {
						lights[n.HostName] = light
					}
				}
			}
		}
	}()
	return out, nil
}

func connectToLight(ctx context.Context, e *zeroconf.ServiceEntry) (*LightAccessory, bool) {
	L := Accessory(e.HostName, e.Port)
	subctx, cancel := context.WithTimeout(ctx, 1*time.Second)
	if err := L.Connect(subctx); err != nil {
		L = Accessory(e.AddrIPv4[0].String(), e.Port)
		if err := L.Connect(subctx); err != nil {
			slog.Error("connecting to Elgato device failed", "err", err, "host", e.HostName, "port", e.Port)
			cancel()
			return nil, false
		}
	}
	slog.Info("connected to Elgato device", "host", e.HostName, "port", e.Port)
	cancel()
	Lp := &L
	for i := range Lp.Options.Lights {
		Lp.Options.Lights[i].On = 0
	}
	Lp.Update(ctx, L.Options.Lights)
	return Lp, true
}
