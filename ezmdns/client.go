package ezmdns

import (
	"context"
	"encoding/binary"
	"fmt"
	"log/slog"
	"net"
	"strings"
	"sync"
	"time"
)

const (
	mdnsPort      = 5353
	mdnsGroupIPv4 = "224.0.0.251"
	mdnsGroupIPv6 = "ff02::fb"
	maxPacketSize = 9000 // Maximum size of mDNS packet

	// DNS record types
	dnsTypeA    uint16 = 1
	dnsTypeAAAA uint16 = 28
	dnsTypePTR  uint16 = 12
	dnsTypeSRV  uint16 = 33
	dnsTypeTXT  uint16 = 16
)

type dnsHeader struct {
	id      uint16
	flags   uint16
	qdCount uint16
	anCount uint16
	nsCount uint16
	arCount uint16
}

func (h *dnsHeader) pack() []byte {
	buf := make([]byte, 12)
	binary.BigEndian.PutUint16(buf[0:], h.id)
	binary.BigEndian.PutUint16(buf[2:], h.flags)
	binary.BigEndian.PutUint16(buf[4:], h.qdCount)
	binary.BigEndian.PutUint16(buf[6:], h.anCount)
	binary.BigEndian.PutUint16(buf[8:], h.nsCount)
	binary.BigEndian.PutUint16(buf[10:], h.arCount)
	return buf
}

func packDNSQuestion(name string, qtype uint16) []byte {
	// Convert name to DNS format (length-prefixed labels)
	labels := strings.Split(name, ".")
	size := 0
	for _, label := range labels {
		size += len(label) + 1
	}
	size++ // null terminator

	buf := make([]byte, size+4) // +4 for qtype and qclass
	pos := 0

	for _, label := range labels {
		if len(label) > 0 {
			buf[pos] = byte(len(label))
			pos++
			copy(buf[pos:], label)
			pos += len(label)
		}
	}
	buf[pos] = 0 // null terminator
	pos++

	// Add QTYPE and QCLASS
	binary.BigEndian.PutUint16(buf[pos:], qtype)
	binary.BigEndian.PutUint16(buf[pos+2:], 1) // IN class

	return buf
}

// ServiceEntry represents a discovered mDNS service instance
type ServiceEntry struct {
	Instance  string    // Service instance name
	Service   string    // Service name being announced
	Domain    string    // Usually "local."
	HostName  string    // Host announcing the service
	Port      int       // Port the service is running on
	AddrIPv4  []net.IP  // IPv4 addresses
	AddrIPv6  []net.IP  // IPv6 addresses
	Text      []string  // TXT record contents
	TTL       uint32    // Time-to-live of the records
	Timestamp time.Time // When this entry was last seen
}

type ServiceChangedEvent[X any] struct {
	Available []X    // all known available services
	New       []X    // newly discovered services since last event
	Removed   []X    // services no longer responding since last event
	Rt        uint64 // relative time since starting discovery
}

// MapStream allows mapping ServiceChangedEvent to a more useful type.
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
		out <- state
	}
}

type DiscoverOptions struct {
	Service string // required
	Domain  string // defaults to local

	PublishInterval time.Duration // defaults to 1 second
	PollInterval    time.Duration // defaults to 5 minutes
	Timeout         time.Duration // defaults to 3 seconds
	MaxFailedChecks uint          // defaults to 1
	HonorTTL        bool          // if true, honors TTL and ignores MaxFailedChecks
}

func equalServiceEntries(a, b *ServiceEntry) bool {
	return a.Port == b.Port && a.Instance == b.Instance && a.Service == b.Service
}

// RunDiscovery continuously discovers local instances via mDNS until ctx expires
func RunDiscovery(ctx context.Context, opt DiscoverOptions) (chan ServiceChangedEvent[*ServiceEntry], error) {
	if !strings.HasPrefix(opt.Service, "_") ||
		(!strings.Contains(opt.Service, "._tcp") && !strings.Contains(opt.Service, "._udp")) {
		return nil, fmt.Errorf("service must be in format '_service._tcp' or '_service._udp', got %s", opt.Service)
	}

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
	if opt.Domain == "" {
		opt.Domain = "local"
	}
	if !strings.HasSuffix(opt.Domain, ".") {
		opt.Domain += "."
	}

	results := make(chan []*ServiceEntry, 4)
	if err := mdnsQuery(ctx, opt.Service, opt.Domain, opt.Timeout, results); err != nil {
		return nil, err
	}

	out := make(chan ServiceChangedEvent[*ServiceEntry], 0)
	go func() {
		pubT := time.NewTicker(opt.PublishInterval)
		qT := time.NewTicker(opt.PollInterval)
		defer pubT.Stop()
		defer qT.Stop()
		defer close(out)

		ttlRef := time.Now()
		state := ServiceChangedEvent[*ServiceEntry]{}
		var unhealthy map[string]uint
		if !opt.HonorTTL {
			unhealthy = make(map[string]uint)
		}

		for {
			select {
			case <-ctx.Done():
				return
			case instances := <-results:
				var toDelete []int
				// Handle removals
				for i, a := range state.Available {
					keep := false
					for _, res := range instances {
						if equalServiceEntries(res, a) {
							keep = true
							break
						}
					}
					if !keep {
						key := a.Instance
						if opt.HonorTTL {
							elapsed := uint32(time.Since(ttlRef).Seconds())
							if elapsed > a.TTL {
								toDelete = append(toDelete, i)
								state.Removed = append(state.Removed, a)
							}
						} else {
							unhealthy[key]++
							if unhealthy[key] >= opt.MaxFailedChecks {
								toDelete = append(toDelete, i)
								state.Removed = append(state.Removed, a)
								delete(unhealthy, key)
							}
						}
					}
				}

				// Remove deleted entries
				for i := len(toDelete) - 1; i >= 0; i-- {
					di := toDelete[i]
					state.Available[di] = state.Available[len(state.Available)-1]
					state.Available = state.Available[:len(state.Available)-1]
				}

				// Handle additions
				for _, res := range instances {
					delete(unhealthy, res.Instance)
					known := false
					for i, a := range state.Available {
						if equalServiceEntries(a, res) {
							known = true
							state.Available[i] = res // update existing entry
							break
						}
					}
					if !known {
						state.Available = append(state.Available, res)
						state.New = append(state.New, res)
					}
				}

			case <-qT.C:
				ttlRef = time.Now()
				err := mdnsQuery(ctx, opt.Service, opt.Domain, opt.Timeout, results)
				if err != nil {
					slog.Error("mdns query failed",
						"error", err,
						"service", opt.Service,
						"domain", opt.Domain)
				}

			case <-pubT.C:
				if len(state.New) > 0 || len(state.Removed) > 0 {
					state.Rt++
					out <- state
					state.New = state.New[:0]
					state.Removed = state.Removed[:0]
				}
			}
		}
	}()

	return out, nil
}

// mdnsQuery performs a multicast DNS query and returns discovered services
func mdnsQuery(ctx context.Context, service, domain string, timeout time.Duration, results chan<- []*ServiceEntry) error {
	var wg sync.WaitGroup
	entries := make(chan *ServiceEntry, 32)
	collected := make([]*ServiceEntry, 0)

	// Set up collection goroutine
	done := make(chan struct{})
	go func() {
		defer close(done)
		for entry := range entries {
			collected = append(collected, entry)
		}
	}()

	// Query both IPv4 and IPv6
	wg.Add(2)
	errChan := make(chan error, 2)

	// IPv4 query
	go func() {
		defer wg.Done()
		if err := queryMDNS(ctx, mdnsGroupIPv4, service, domain, timeout, entries); err != nil {
			errChan <- fmt.Errorf("IPv4 query failed: %w", err)
		}
	}()

	// IPv6 query
	go func() {
		defer wg.Done()
		if err := queryMDNS(ctx, mdnsGroupIPv6, service, domain, timeout, entries); err != nil {
			errChan <- fmt.Errorf("IPv6 query failed: %w", err)
		}
	}()

	// Wait for queries to complete
	wg.Wait()
	close(entries)
	<-done

	// Check for errors
	select {
	case err := <-errChan:
		return err
	default:
		if len(collected) > 0 {
			results <- collected
		}
		return nil
	}
}

func queryMDNS(ctx context.Context, groupIP, service, domain string, timeout time.Duration, entries chan<- *ServiceEntry) error {
	addr, err := net.ResolveUDPAddr("udp", net.JoinHostPort(groupIP, fmt.Sprintf("%d", mdnsPort)))
	if err != nil {
		return fmt.Errorf("failed to resolve multicast address: %w", err)
	}

	ifaces, err := net.Interfaces()
	if err != nil {
		return fmt.Errorf("failed to get network interfaces: %w", err)
	}

	// Prepare the DNS query
	queryBuf := prepareMDNSQuery(service, domain)

	var wg sync.WaitGroup
	for _, iface := range ifaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagMulticast == 0 {
			continue
		}

		wg.Add(1)
		go func(intf *net.Interface) {
			defer wg.Done()

			conn, err := net.ListenMulticastUDP("udp", intf, addr)
			if err != nil {
				slog.Debug("failed to listen on interface",
					"interface", intf.Name,
					"error", err)
				return
			}
			defer conn.Close()

			// Send query
			_, err = conn.WriteToUDP(queryBuf, addr)
			if err != nil {
				slog.Debug("failed to send query",
					"interface", intf.Name,
					"error", err)
				return
			}

			// Read responses
			readCtx, cancel := context.WithTimeout(ctx, timeout)
			defer cancel()

			go func() {
				buf := make([]byte, maxPacketSize)
				for {
					n, _, err := conn.ReadFromUDP(buf)
					if err != nil {
						if !strings.Contains(err.Error(), "use of closed network connection") {
							slog.Debug("failed to read response",
								"interface", intf.Name,
								"error", err)
						}
						return
					}

					entry := parseMDNSResponse(buf[:n], service, domain)
					if entry != nil {
						entries <- entry
					}
				}
			}()

			<-readCtx.Done()
		}(&iface)
	}

	wg.Wait()
	return nil
}

func prepareMDNSQuery(service, domain string) []byte {
	header := &dnsHeader{
		flags:   0x0000, // Standard query
		qdCount: 1,      // One question
	}

	// Service should already be in form "_service._tcp"
	// Just append domain
	queryName := fmt.Sprintf("%s.%s", strings.TrimSuffix(service, "."), strings.TrimSuffix(domain, "."))
	question := packDNSQuestion(queryName, 12) // Type PTR = 12

	// Combine header and question
	query := append(header.pack(), question...)
	return query
}

type dnsParser struct {
	data []byte
	off  int
}

func (p *dnsParser) readUint16() uint16 {
	if p.off+2 > len(p.data) {
		return 0
	}
	v := binary.BigEndian.Uint16(p.data[p.off:])
	p.off += 2
	return v
}

func (p *dnsParser) readUint32() uint32 {
	if p.off+4 > len(p.data) {
		return 0
	}
	v := binary.BigEndian.Uint32(p.data[p.off:])
	p.off += 4
	return v
}

func (p *dnsParser) readName() string {
	var name []byte
	for {
		if p.off >= len(p.data) {
			return ""
		}
		length := int(p.data[p.off])
		p.off++

		if length == 0 {
			break
		}

		// Handle compression
		if length&0xC0 == 0xC0 {
			if p.off >= len(p.data) {
				return ""
			}
			// Pointer to another location in the message
			pointer := int(length&0x3F)<<8 | int(p.data[p.off])
			p.off++

			// Save current position
			savedOff := p.off
			p.off = pointer

			// Recursively read the referenced name
			referencedName := p.readName()
			if len(name) > 0 {
				name = append(name, '.')
			}
			name = append(name, []byte(referencedName)...)

			// Restore position
			p.off = savedOff
			return string(name)
		}

		if p.off+length > len(p.data) {
			return ""
		}

		if len(name) > 0 {
			name = append(name, '.')
		}
		name = append(name, p.data[p.off:p.off+length]...)
		p.off += length
	}
	return string(name)
}

func parseMDNSResponse(data []byte, service, domain string) *ServiceEntry {
	if len(data) < 12 {
		return nil
	}

	p := &dnsParser{data: data}

	// Skip header ID
	p.off += 2

	// Read flags
	flags := p.readUint16()
	if flags&0x8000 == 0 {
		return nil // Not a response
	}

	// Read counts
	qdCount := p.readUint16()
	anCount := p.readUint16()
	nsCount := p.readUint16()
	arCount := p.readUint16()

	// Skip questions
	for i := uint16(0); i < qdCount; i++ {
		p.readName() // Skip name
		p.off += 4   // Skip type and class
	}

	entry := &ServiceEntry{
		Service:   service,
		Domain:    domain,
		Timestamp: time.Now(),
		AddrIPv4:  make([]net.IP, 0),
		AddrIPv6:  make([]net.IP, 0),
		Text:      make([]string, 0),
	}

	// Process all records (answers, authorities, and additionals)
	for i := uint16(0); i < anCount+nsCount+arCount; i++ {
		name := p.readName()
		if p.off+10 > len(data) {
			break
		}

		recordType := p.readUint16()
		_ = p.readUint16() // class
		ttl := p.readUint32()
		rdLength := p.readUint16()

		if p.off+int(rdLength) > len(data) {
			break
		}

		switch recordType {
		case dnsTypePTR:
			expectedPrefix := fmt.Sprintf("%s.%s", strings.TrimSuffix(service, "."), strings.TrimSuffix(domain, "."))
			if strings.HasPrefix(name, expectedPrefix) {
				entry.Instance = p.readName()
				entry.TTL = ttl
			}

		case dnsTypeSRV:
			if strings.HasPrefix(name, entry.Instance) {
				_ = p.readUint16() // priority
				_ = p.readUint16() // weight
				entry.Port = int(p.readUint16())
				entry.HostName = p.readName()
			}

		case dnsTypeTXT:
			if strings.HasPrefix(name, entry.Instance) {
				endOff := p.off + int(rdLength)
				for p.off < endOff {
					length := int(p.data[p.off])
					p.off++
					if p.off+length <= endOff {
						entry.Text = append(entry.Text, string(p.data[p.off:p.off+length]))
						p.off += length
					}
				}
			}

		case dnsTypeA:
			if strings.HasPrefix(name, entry.HostName) {
				ip := net.IP(p.data[p.off : p.off+4])
				entry.AddrIPv4 = append(entry.AddrIPv4, ip)
				p.off += 4
			}

		case dnsTypeAAAA:
			if strings.HasPrefix(name, entry.HostName) {
				ip := net.IP(p.data[p.off : p.off+16])
				entry.AddrIPv6 = append(entry.AddrIPv6, ip)
				p.off += 16
			}

		default:
			p.off += int(rdLength)
		}
	}

	// Only return entry if we have the minimum required information
	if entry.Instance != "" && entry.HostName != "" && entry.Port != 0 {
		return entry
	}

	return nil
}
