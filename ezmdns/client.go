package ezmdns

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/grandcat/zeroconf"
)

type ServiceChangedEvent[X any] struct {
	Available []X    // all known available services
	New       []X    // newly discovered services since last event
	Removed   []X    // services no longer responding since last event
	Rt        uint64 // relative time since starting discovery
}

// MapStream allows mapping ServiceChangedEvent to a more useful type.
// Blocks, so it's recommended to call this inside a goroutine.
//
// f is a function that transforms A, unless false is returned. False trues the service entry as unavailable.
// f is only called on newly seen service entries.
func MapStream[A, B comparable](in chan ServiceChangedEvent[A], out chan ServiceChangedEvent[B], f func(A) (B, bool)) {
	known := make(map[A]B)
	state := ServiceChangedEvent[B]{}
	for e := range in {
		state.Rt = e.Rt
		for _, n := range e.New {
			mapped, ok := f(n)
			if ok {
				known[n] = mapped
			}
		}
		state.New = state.New[:0]
		state.Available = state.Available[:0]
		state.Removed = state.Removed[:0]
		for _, n := range e.Available {
			if m, ok := known[n]; ok {
				state.Available = append(state.Available, m)
			}
		}
		for _, n := range e.New {
			if m, ok := known[n]; ok {
				state.New = append(state.New, m)
			}
		}
		for _, n := range e.Removed {
			if m, ok := known[n]; ok {
				state.Removed = append(state.Removed, m)
			}
		}
		for _, n := range e.Removed {
			delete(known, n)
		}
		slog.Debug(
			"mapped mdns service entries",
			slog.Int("in.available", len(e.Available)),
			slog.Int("out.available", len(state.Available)),
			slog.Int("in.new", len(e.New)),
			slog.Int("out.new", len(state.New)),
			slog.Int("in.removed", len(e.Removed)),
			slog.Int("out.removed", len(state.Removed)),
		)
		out <- state
	}
}

type DiscoverOptions struct {
	Service string // required
	Domain  string // defaults to local

	PublishInterval time.Duration // defaults to 1 second. Delay to publish new changes after a change is detected
	PollInterval    time.Duration // defaults to 5 minutes.
	Timeout         time.Duration // defaults to 3 seconds. How long to wait for responses from services when querying
	MaxFailedChecks uint          // defaults to 1. Max number of non-responsive checks until drop
	HonorTTL        bool          // if true, attempts to honor TTL and ignores MaxFailedChecks
}

func equalServiceEntries(a, b *zeroconf.ServiceEntry) bool {
	return a.Port == b.Port && a.ServiceInstanceName() == b.ServiceInstanceName()
}

// RunDiscovery continuously discovers for local instances via mdns until ctx expires.
// Returns a stream of events of changes, or error if failed to start.
//
// Closes the returned channel on cancellation. This call blocks until cancellation, so it's
// recommended to run this in a goroutine if you want to do other things.
//
// Each message in the channel represents a delta of what's changed:
//   - Did a service instance become non-discoverable?
//   - Did a service instance become discoverable?
//   - What are the available service instances?
//
// Note that available is determined by mdns broadcasts, the service it advises could still be offline.
func RunDiscovery(ctx context.Context, opt DiscoverOptions) (chan ServiceChangedEvent[*zeroconf.ServiceEntry], error) {
	if opt.PublishInterval <= 0 {
		opt.PublishInterval = 1 * time.Second
	}
	if opt.PollInterval <= 0 {
		opt.PollInterval = 5 * time.Minute
	}
	if opt.Timeout <= 0 {
		opt.Timeout = 3 * time.Second
	}
	if opt.MaxFailedChecks == 0 {
		opt.MaxFailedChecks = 1
	}

	results := make(chan []*zeroconf.ServiceEntry, 4)
	if err := mdnsQuery(ctx, opt.Service, opt.Domain, 5*time.Second, results); err != nil {
		return nil, err
	}

	out := make(chan ServiceChangedEvent[*zeroconf.ServiceEntry], 0)
	go func(results chan []*zeroconf.ServiceEntry) {
		pubT := time.NewTicker(opt.PublishInterval)
		qT := time.NewTicker(opt.PollInterval)
		defer pubT.Stop()
		defer qT.Stop()
		ttlRef := time.Now()
		state := ServiceChangedEvent[*zeroconf.ServiceEntry]{}
		var unhealthy map[string]uint
		if !opt.HonorTTL {
			unhealthy = make(map[string]uint)
		}
		for {
			select {
			case <-ctx.Done():
				close(results)
				return // quit
			case instances := <-results:
				var toDelete []int
				for i, a := range state.Available {
					keep := false
					for _, res := range instances {
						if equalServiceEntries(res, a) {
							keep = true
							break
						}
					}
					if !keep {
						key := a.ServiceInstanceName()
						if opt.HonorTTL {
							elapsed := uint32(time.Since(ttlRef).Seconds())
							if elapsed > a.TTL {
								toDelete = append(toDelete, i)
								state.Removed = append(state.Removed, a)
								slog.Debug(
									"removed because of expiry of TTL",
									slog.Uint64("elapsed", uint64(elapsed)),
									slog.Uint64("ttl", uint64(a.TTL)),
									slog.String("instance", key),
								)
							}
						} else {
							unhealthy[key]++
							cnt := unhealthy[key]
							if cnt >= opt.MaxFailedChecks {
								toDelete = append(toDelete, i)
								state.Removed = append(state.Removed, a)
								delete(unhealthy, key)
								slog.Warn(
									"removed because too many failed health check",
									slog.Uint64("failed", uint64(cnt)),
									slog.String("instance", key),
								)
							} else {
								slog.Warn(
									"failed health check",
									slog.Uint64("failed", uint64(cnt)),
									slog.String("instance", key),
								)
							}
						}
					}
				}
				for i := len(toDelete) - 1; i >= 0; i-- {
					di := toDelete[i]
					end := len(state.Available) - 1
					state.Available[di] = state.Available[end]
					state.Available = state.Available[:end]
				}

				for i, res := range instances {
					key := res.ServiceInstanceName()
					delete(unhealthy, key)
					known := false
					for _, a := range state.Available {
						if equalServiceEntries(a, res) {
							known = true
							state.Available[i] = res // copy over TTL or any updated values
						}
					}
					if known {
						continue
					}

					state.Available = append(state.Available, res)
					state.New = append(state.New, res)
					slog.Debug(
						"discovered service instance",
						slog.Uint64("ttl", uint64(res.TTL)),
						slog.String("instance", key),
					)
				}
			case <-qT.C:
				ttlRef = time.Now()
				err := mdnsQuery(ctx, opt.Service, opt.Domain, opt.Timeout, results)
				if err != nil {
					slog.Error(
						"mdns query failed",
						slog.String("service", opt.Service),
						slog.String("domain", opt.Domain),
						slog.Duration("timeout", opt.Timeout),
						slog.Any("error", err),
					)
				}
			case <-pubT.C:
				if len(state.New) != 0 || len(state.Removed) != 0 {
					fmt.Printf("publish: %+v\n", state)
					state.Rt++
					out <- state
					state.New = state.New[:0]
					state.Removed = state.Removed[:0]
					slog.Debug(
						"broadcast discovery changes",
						slog.Uint64("rt", uint64(state.Rt)),
					)
				}
			}
		}
	}(results)
	return out, nil
}

func mdnsQuery(ctx context.Context, service, domain string, timeout time.Duration, out chan<- []*zeroconf.ServiceEntry) error {
	tmp := make(chan *zeroconf.ServiceEntry, 1)
	subctx, cancel := context.WithTimeout(ctx, timeout)
	// resolver isn't safe to use past one method call...
	resolver, err := zeroconf.NewResolver(nil)
	if err != nil {
		cancel()
		return err
	}
	err = resolver.Browse(subctx, service, domain, tmp)
	if err != nil {
		cancel()
		return err
	}
	go func() {
		var res []*zeroconf.ServiceEntry
		for instance := range tmp {
			res = append(res, instance)
		}
		if len(res) > 0 {
			out <- res
		}
	}()
	go func() {
		time.Sleep(timeout)
		// Browse will close the chan on our behalf
		cancel()
	}()
	return nil
}
