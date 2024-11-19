package elgato

import (
	"context"
	"log/slog"
	"net"
	"strconv"
	"time"

	"github.com/jeffh/elgato/ezmdns"
)

const (
	LightService  = "_elg._tcp"
	CameraService = "_egstreamwrap._tcp"
)

// DiscoverLocalLights continuously finds for local elgato light accessories
func DiscoverLocalLjghts(ctx context.Context, opt ezmdns.DiscoverOptions) (chan ezmdns.ServiceChangedEvent[*LightAccessory], error) {
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

func connectToLight(ctx context.Context, e *ezmdns.ServiceEntry) (*LightAccessory, bool) {
	L := MakeLightAccessory(net.JoinHostPort(e.HostName, strconv.Itoa(e.Port)))
	subctx, cancel := context.WithTimeout(ctx, 1*time.Second)
	firstAddr := ""
	if len(e.AddrIPv4) > 0 {
		firstAddr = e.AddrIPv4[0].String()
	}
	if len(e.AddrIPv6) > 0 {
		firstAddr = e.AddrIPv6[0].String()
	}
	if err := L.Connect(subctx); err != nil {
		L = MakeLightAccessory(net.JoinHostPort(firstAddr, strconv.Itoa(e.Port)))
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
