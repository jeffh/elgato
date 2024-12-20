package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"time"

	"github.com/jeffh/elgato"
	"github.com/jeffh/elgato/ezmdns"
)

func main() {
	discover := flag.Bool("discover", false, "Discover all Elgato lights on the network")
	service := flag.String("service", elgato.LightService, "Discover a specific service (format: _elg._tcp)")
	domain := flag.String("domain", "", "Discover a specific domain")
	info := flag.String("info", "", "Get information about a specific light (format: ip:port) elgato lights port is typically on 9123")
	flag.Parse()

	if !*discover && *info == "" {
		flag.Usage()
		os.Exit(1)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if *discover {
		stream, err := ezmdns.RunDiscovery(ctx, ezmdns.DiscoverOptions{
			Service:         *service,
			Domain:          *domain,
			PublishInterval: 500 * time.Millisecond,
			PollInterval:    5 * time.Second,
			Timeout:         5 * time.Second,
		})
		if err != nil {
			log.Fatal(err)
		}

		// Create a map to track unique addresses we've seen
		go func() {
			seen := make(map[string]bool)
			for e := range stream {
				for _, service := range e.New {
					addr := service.AddrIPv4[0].String()
					if !seen[addr] {
						fmt.Printf("%s:%d - addr=%q host=%q instance=%q ttl=%d\n", service.HostName, service.Port, addr, service.HostName, service.Instance, service.TTL)
						seen[addr] = true
					}
				}
			}
		}()
		<-ctx.Done()
		fmt.Println("WARN: elgato mdns discovery is buggy and may not show up in the list")
	}

	if *info != "" {
		light := elgato.MakeLightAccessory(*info)
		if err := light.Connect(ctx); err != nil {
			log.Fatal(err)
		}

		// Print light information in the specified format
		fmt.Printf("DisplayName: %s\n", light.Info.DisplayName)
		fmt.Printf("ProductName: %s\n", light.Info.ProductName)
		fmt.Printf("SerialNumber: %s\n", light.Info.SerialNumber)
		fmt.Printf("HardwareBoardType: %d\n", light.Info.HardwareBoardType)
		fmt.Printf("FirmwareBuildNumber: %d\n", light.Info.FirmwareBuildNumber)
		fmt.Printf("FirmwareVersion: %s\n", light.Info.FirmwareVersion)

		fmt.Printf("WifiInfo:\n")
		fmt.Printf("  Ssid: %s\n", light.Info.WifiInfo.Ssid)
		fmt.Printf("  FrequencyMhz: %d\n", light.Info.WifiInfo.FrequencyMhz)
		fmt.Printf("  Rssi: %d\n", light.Info.WifiInfo.Rssi)

		fmt.Printf("Features:\n")
		for _, feature := range light.Info.Features {
			fmt.Printf("  - %s\n", feature)
		}

		fmt.Printf("\nSettings:\n")
		fmt.Printf("  ColorChangeDuration: %dms\n", light.Settings.ColorChangeDurationMsec)
		fmt.Printf("  PowerOnBehavior: %d\n", light.Settings.PowerOnBehavior)
		fmt.Printf("  PowerOnBrightness: %d\n", light.Settings.PowerOnBrightness)
		fmt.Printf("  SwitchOffDuration: %dms\n", light.Settings.SwitchOffDurationMsec)
		fmt.Printf("  SwitchOnDuration: %dms\n", light.Settings.SwitchOnDurationMsec)

		fmt.Printf("\nLights:\n")
		fmt.Printf("  NumberOfLights: %d\n", light.Options.NumberOfLights)
		fmt.Printf("  Lights:\n")
		for _, l := range light.Options.Lights {
			fmt.Printf("    - Brightness: %d\n", l.Brightness)
			fmt.Printf("      On: %d\n", l.On)
			fmt.Printf("      Temperature: %d\n", l.Temperature)
		}
	}
}
