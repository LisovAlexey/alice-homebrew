//go:build airplay

package main

import (
	"net"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/hashicorp/mdns"
	"github.com/miekg/dns"
)

var mdnsGroup = &net.UDPAddr{IP: net.IPv4(224, 0, 0, 251), Port: 5353}

const mdnsTTL = 120 // matches hashicorp/mdns record TTL

// mdnsResponder answers on the multicast group and announces the service.
// hashicorp/mdns.Server only unicasts replies back to the querier and never
// announces, which iPhones and Avahi often ignore, so the speaker is missing
// from the AirPlay menu. Its zone is still used to build the records.
type mdnsResponder struct {
	zone    *mdns.MDNSService
	service string // fully qualified, e.g. _raop._tcp.local.
	conn    *net.UDPConn
	done    chan struct{}
	wg      sync.WaitGroup
}

func newMDNSResponder(zone *mdns.MDNSService, iface *net.Interface) (*mdnsResponder, error) {
	conn, err := net.ListenMulticastUDP("udp4", iface, mdnsGroup)
	if err != nil {
		return nil, err
	}
	// RFC 6762 section 11: link-local mDNS packets are sent with TTL 255.
	if raw, err := conn.SyscallConn(); err == nil {
		_ = raw.Control(func(fd uintptr) {
			_ = syscall.SetsockoptInt(int(fd), syscall.IPPROTO_IP, syscall.IP_MULTICAST_TTL, 255)
			_ = syscall.SetsockoptInt(int(fd), syscall.IPPROTO_IP, syscall.IP_TTL, 255)
		})
	}
	service := strings.Trim(zone.Service, ".") + "." + strings.Trim(zone.Domain, ".") + "."
	r := &mdnsResponder{zone: zone, service: service, conn: conn, done: make(chan struct{})}
	r.wg.Add(2)
	go r.serve()
	go r.announce()
	return r, nil
}

func (r *mdnsResponder) instanceRecords(ttl uint32) []dns.RR {
	records := r.zone.Records(dns.Question{Name: r.service, Qtype: dns.TypePTR, Qclass: dns.ClassINET})
	for _, rr := range records {
		rr.Header().Ttl = ttl
	}
	return records
}

func (r *mdnsResponder) multicast(records []dns.RR) {
	if len(records) == 0 {
		return
	}
	msg := &dns.Msg{MsgHdr: dns.MsgHdr{Response: true, Authoritative: true}, Answer: records}
	if buf, err := msg.Pack(); err == nil {
		_, _ = r.conn.WriteToUDP(buf, mdnsGroup)
	}
}

func (r *mdnsResponder) announce() {
	defer r.wg.Done()
	// Three startup announcements one and two seconds apart (RFC 6762 section 8.3),
	// then a refresh well inside the 120 s record TTL.
	delays := []time.Duration{0, time.Second, 2 * time.Second}
	for i := 0; ; i++ {
		delay := time.Minute
		if i < len(delays) {
			delay = delays[i]
		}
		select {
		case <-r.done:
			return
		case <-time.After(delay):
			r.multicast(r.instanceRecords(mdnsTTL))
		}
	}
}

func (r *mdnsResponder) serve() {
	defer r.wg.Done()
	buf := make([]byte, 9000)
	for {
		n, from, err := r.conn.ReadFromUDP(buf)
		if err != nil {
			select {
			case <-r.done:
				return
			default:
				continue
			}
		}
		var query dns.Msg
		if query.Unpack(buf[:n]) != nil || query.Response || query.Opcode != dns.OpcodeQuery || query.Rcode != 0 {
			continue
		}
		var answers []dns.RR
		unicast := from.Port != mdnsGroup.Port
		for _, q := range query.Question {
			if q.Qclass&0x8000 != 0 {
				unicast = true
				q.Qclass &^= 0x8000
			}
			answers = append(answers, r.zone.Records(q)...)
		}
		if len(answers) == 0 {
			continue
		}
		if !unicast {
			r.multicast(answers)
			continue
		}
		// Unicast-response bit or legacy one-shot resolver: reply directly,
		// echoing the ID and questions as RFC 6762 section 6.7 requires.
		reply := &dns.Msg{MsgHdr: dns.MsgHdr{Id: query.Id, Response: true, Authoritative: true}, Answer: answers}
		if from.Port != mdnsGroup.Port {
			reply.Question = query.Question
		}
		if packed, err := reply.Pack(); err == nil {
			_, _ = r.conn.WriteToUDP(packed, from)
		}
	}
}

// Shutdown sends a goodbye (TTL 0) so clients drop the speaker immediately.
func (r *mdnsResponder) Shutdown() {
	select {
	case <-r.done:
		return
	default:
	}
	r.multicast(r.instanceRecords(0))
	close(r.done)
	_ = r.conn.Close()
	r.wg.Wait()
}
