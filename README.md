# Elgato

Control elgato lights.

# Installation

```
go get github.com/jeffh/elgato
```

# Usage

You can discover local elgato lights by using `DiscoverLocalLights`:

```go
import "github.com/jeffh/elgato"

func main() {
	// ....
	lights, err := elgato.DiscoverLocalLights(ctx, ezmdns.DiscoverOptions{})
	// lights is a channel of lights
	for light := range lights {
		// light Settings, Info, and Options are populated
		fmt.Printf("LIGHT: %s\n", light.Info.DisplayName)

		if len(light.Lights) > 0 && light.Lights[0].On {
			fmt.Println(" - is ON")
		}
	}
}
```

Alternatively, if you know the address & port of the light, you can use
`MakeLightAccessory` instead:

```go
light := elgato.MakeLightAccessory("elgato-key-light-air.local:9123")
err := light.Connect(ctx)
// you can now access the light information and use it
```

If you want to refresh the light state, use `Refresh`:

```go
err := light.Refresh(ctx)
```

Use `Set`, `SetBrightness`, `SetOn`, or `SetTemperature` to change the state of
the light:

```go
light.SetOn(true)
```

That's it.

## HAP

If you want to use this with [HAP][hap], then import the hap subpackage

```go
import elHap "github.com/jeffh/elgato/hap"

acc := NewElgatoLightAccessory(ctx, light, 0) // default refresh interval is 5 seconds
// append acc.A to your hap server
```
