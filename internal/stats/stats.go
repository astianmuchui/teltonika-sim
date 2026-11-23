package stats

import (
	"fmt"
	"net/http"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"text/tabwriter"
	"time"
)

const latencyWindow = 8192

type Registry struct {
	role    string
	started time.Time

	devicesTotal     atomic.Int64
	devicesConnected atomic.Int64
	handshakeOK      atomic.Uint64
	handshakeReject  atomic.Uint64
	connectFailed    atomic.Uint64
	reconnects       atomic.Uint64
	disconnects      atomic.Uint64

	recordsGenerated atomic.Uint64
	recordsReceived  atomic.Uint64
	recordsSent      atomic.Uint64
	recordsACKed     atomic.Uint64
	recordsDropped   atomic.Uint64
	recordsBuffered  atomic.Int64
	resends          atomic.Uint64

	packetsSent     atomic.Uint64
	packetsReceived atomic.Uint64
	packetsBadCRC   atomic.Uint64
	bytesSent       atomic.Uint64
	bytesReceived   atomic.Uint64

	crcCorrupted atomic.Uint64
	ackMismatch  atomic.Uint64
	ackTimeout   atomic.Uint64
	acksSent     atomic.Uint64
	acksWrong    atomic.Uint64

	commandsSent     atomic.Uint64
	commandsReceived atomic.Uint64
	commandsAnswered atomic.Uint64

	mu   sync.Mutex
	lat  [latencyWindow]time.Duration
	latN int
	latI int
}

func New(role string) *Registry {
	return &Registry{role: role, started: time.Now()}
}

func (r *Registry) Role() string { return r.role }

func (r *Registry) SetDevicesTotal(n int)  { r.devicesTotal.Store(int64(n)) }
func (r *Registry) Connected(delta int)    { r.devicesConnected.Add(int64(delta)) }
func (r *Registry) HandshakeOK()           { r.handshakeOK.Add(1) }
func (r *Registry) HandshakeRejected()     { r.handshakeReject.Add(1) }
func (r *Registry) ConnectFailed()         { r.connectFailed.Add(1) }
func (r *Registry) Reconnect()             { r.reconnects.Add(1) }
func (r *Registry) Disconnect()            { r.disconnects.Add(1) }
func (r *Registry) RecordsGenerated(n int) { r.recordsGenerated.Add(uint64(n)) }
func (r *Registry) RecordsReceived(n int)  { r.recordsReceived.Add(uint64(n)) }
func (r *Registry) RecordsSent(n int)      { r.recordsSent.Add(uint64(n)) }
func (r *Registry) RecordsACKed(n int)     { r.recordsACKed.Add(uint64(n)) }
func (r *Registry) RecordsDropped(n int)   { r.recordsDropped.Add(uint64(n)) }
func (r *Registry) BufferedDelta(n int)    { r.recordsBuffered.Add(int64(n)) }
func (r *Registry) Resends(n int)          { r.resends.Add(uint64(n)) }
func (r *Registry) PacketSent(bytes int)   { r.packetsSent.Add(1); r.bytesSent.Add(uint64(bytes)) }
func (r *Registry) PacketReceived(b int)   { r.packetsReceived.Add(1); r.bytesReceived.Add(uint64(b)) }
func (r *Registry) PacketBadCRC()          { r.packetsBadCRC.Add(1) }
func (r *Registry) CRCCorrupted()          { r.crcCorrupted.Add(1) }
func (r *Registry) ACKMismatch()           { r.ackMismatch.Add(1) }
func (r *Registry) ACKTimeout()            { r.ackTimeout.Add(1) }
func (r *Registry) ACKSent(wrong bool) {
	r.acksSent.Add(1)
	if wrong {
		r.acksWrong.Add(1)
	}
}
func (r *Registry) CommandSent()     { r.commandsSent.Add(1) }
func (r *Registry) CommandReceived() { r.commandsReceived.Add(1) }
func (r *Registry) CommandAnswered() { r.commandsAnswered.Add(1) }

func (r *Registry) ObserveACKLatency(d time.Duration) {
	r.mu.Lock()
	r.lat[r.latI] = d
	r.latI = (r.latI + 1) % latencyWindow
	if r.latN < latencyWindow {
		r.latN++
	}
	r.mu.Unlock()
}

type Snapshot struct {
	Role             string
	Uptime           time.Duration
	DevicesTotal     int64
	DevicesConnected int64
	HandshakeOK      uint64
	HandshakeReject  uint64
	ConnectFailed    uint64
	Reconnects       uint64
	Disconnects      uint64
	RecordsGenerated uint64
	RecordsReceived  uint64
	RecordsSent      uint64
	RecordsACKed     uint64
	RecordsDropped   uint64
	RecordsBuffered  int64
	Resends          uint64
	PacketsSent      uint64
	PacketsReceived  uint64
	PacketsBadCRC    uint64
	BytesSent        uint64
	BytesReceived    uint64
	CRCCorrupted     uint64
	ACKMismatch      uint64
	ACKTimeout       uint64
	ACKsSent         uint64
	ACKsWrong        uint64
	CommandsSent     uint64
	CommandsReceived uint64
	CommandsAnswered uint64
	ACKp50           time.Duration
	ACKp99           time.Duration
	ACKSamples       int
}

func (r *Registry) Snapshot() Snapshot {
	s := Snapshot{
		Role:             r.role,
		Uptime:           time.Since(r.started).Round(time.Second),
		DevicesTotal:     r.devicesTotal.Load(),
		DevicesConnected: r.devicesConnected.Load(),
		HandshakeOK:      r.handshakeOK.Load(),
		HandshakeReject:  r.handshakeReject.Load(),
		ConnectFailed:    r.connectFailed.Load(),
		Reconnects:       r.reconnects.Load(),
		Disconnects:      r.disconnects.Load(),
		RecordsGenerated: r.recordsGenerated.Load(),
		RecordsReceived:  r.recordsReceived.Load(),
		RecordsSent:      r.recordsSent.Load(),
		RecordsACKed:     r.recordsACKed.Load(),
		RecordsDropped:   r.recordsDropped.Load(),
		RecordsBuffered:  r.recordsBuffered.Load(),
		Resends:          r.resends.Load(),
		PacketsSent:      r.packetsSent.Load(),
		PacketsReceived:  r.packetsReceived.Load(),
		PacketsBadCRC:    r.packetsBadCRC.Load(),
		BytesSent:        r.bytesSent.Load(),
		BytesReceived:    r.bytesReceived.Load(),
		CRCCorrupted:     r.crcCorrupted.Load(),
		ACKMismatch:      r.ackMismatch.Load(),
		ACKTimeout:       r.ackTimeout.Load(),
		ACKsSent:         r.acksSent.Load(),
		ACKsWrong:        r.acksWrong.Load(),
		CommandsSent:     r.commandsSent.Load(),
		CommandsReceived: r.commandsReceived.Load(),
		CommandsAnswered: r.commandsAnswered.Load(),
	}
	r.mu.Lock()
	samples := make([]time.Duration, r.latN)
	copy(samples, r.lat[:r.latN])
	r.mu.Unlock()
	s.ACKSamples = len(samples)
	if len(samples) > 0 {
		slices.Sort(samples)
		s.ACKp50 = quantile(samples, 0.50)
		s.ACKp99 = quantile(samples, 0.99)
	}
	return s
}

func quantile(sorted []time.Duration, q float64) time.Duration {
	if len(sorted) == 0 {
		return 0
	}
	i := max(int(float64(len(sorted)-1)*q), 0)
	if i >= len(sorted) {
		i = len(sorted) - 1
	}
	return sorted[i].Round(time.Microsecond)
}

func bytesHuman(n uint64) string {
	switch {
	case n >= 1<<30:
		return fmt.Sprintf("%.1f GiB", float64(n)/(1<<30))
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MiB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.1f KiB", float64(n)/(1<<10))
	}
	return fmt.Sprintf("%d B", n)
}

func (s Snapshot) Table() string {
	rows := [][2]string{
		{"devices connected", fmt.Sprintf("%d/%d", s.DevicesConnected, s.DevicesTotal)},
		{"handshakes ok/rejected", fmt.Sprintf("%d/%d", s.HandshakeOK, s.HandshakeReject)},
		{"records generated", fmt.Sprint(s.RecordsGenerated)},
		{"records received", fmt.Sprint(s.RecordsReceived)},
		{"records sent", fmt.Sprint(s.RecordsSent)},
		{"records acked", fmt.Sprint(s.RecordsACKed)},
		{"records buffered", fmt.Sprint(s.RecordsBuffered)},
		{"records dropped", fmt.Sprint(s.RecordsDropped)},
		{"resends", fmt.Sprint(s.Resends)},
		{"packets sent", fmt.Sprintf("%d (%s)", s.PacketsSent, bytesHuman(s.BytesSent))},
		{"packets received", fmt.Sprintf("%d (%s)", s.PacketsReceived, bytesHuman(s.BytesReceived))},
		{"crc corrupted on purpose", fmt.Sprint(s.CRCCorrupted)},
		{"packets with bad crc", fmt.Sprint(s.PacketsBadCRC)},
		{"acks sent/wrong", fmt.Sprintf("%d/%d", s.ACKsSent, s.ACKsWrong)},
		{"ack mismatches", fmt.Sprint(s.ACKMismatch)},
		{"ack timeouts", fmt.Sprint(s.ACKTimeout)},
		{"reconnects", fmt.Sprint(s.Reconnects)},
		{"disconnects", fmt.Sprint(s.Disconnects)},
		{"connect failures", fmt.Sprint(s.ConnectFailed)},
		{"commands sent", fmt.Sprint(s.CommandsSent)},
		{"commands received/answered", fmt.Sprintf("%d/%d", s.CommandsReceived, s.CommandsAnswered)},
		{"ack latency p50/p99", fmt.Sprintf("%s/%s (%d samples)", s.ACKp50, s.ACKp99, s.ACKSamples)},
	}

	var b strings.Builder
	fmt.Fprintf(&b, "---- teltonika-sim %s ---- uptime %s ----\n", s.Role, s.Uptime)
	w := tabwriter.NewWriter(&b, 0, 0, 2, ' ', 0)
	for i := 0; i < len(rows); i += 2 {
		left := rows[i]
		if i+1 < len(rows) {
			right := rows[i+1]
			fmt.Fprintf(w, "  %s\t%s\t  %s\t%s\n", left[0], left[1], right[0], right[1])
			continue
		}
		fmt.Fprintf(w, "  %s\t%s\t\t\n", left[0], left[1])
	}
	w.Flush()
	return b.String()
}

type metric struct {
	name  string
	kind  string
	help  string
	value string
}

func (s Snapshot) Prometheus() string {
	metrics := []metric{
		{"devices_total", "gauge", "Simulated devices configured.", fmt.Sprint(s.DevicesTotal)},
		{"devices_connected", "gauge", "Simulated devices with an open connection.", fmt.Sprint(s.DevicesConnected)},
		{"handshake_ok_total", "counter", "Handshakes accepted by the server.", fmt.Sprint(s.HandshakeOK)},
		{"handshake_rejected_total", "counter", "Handshakes rejected by the server.", fmt.Sprint(s.HandshakeReject)},
		{"connect_failed_total", "counter", "Failed dial attempts.", fmt.Sprint(s.ConnectFailed)},
		{"reconnects_total", "counter", "Reconnect attempts after a session ended.", fmt.Sprint(s.Reconnects)},
		{"disconnects_total", "counter", "Sessions that ended.", fmt.Sprint(s.Disconnects)},
		{"records_generated_total", "counter", "Records produced by the route and io generators.", fmt.Sprint(s.RecordsGenerated)},
		{"records_received_total", "counter", "Records decoded from packets that arrived.", fmt.Sprint(s.RecordsReceived)},
		{"records_sent_total", "counter", "Records written to a connection.", fmt.Sprint(s.RecordsSent)},
		{"records_acked_total", "counter", "Records the server acknowledged.", fmt.Sprint(s.RecordsACKed)},
		{"records_dropped_total", "counter", "Records dropped because the flash buffer was full.", fmt.Sprint(s.RecordsDropped)},
		{"records_buffered", "gauge", "Records waiting in the flash buffer.", fmt.Sprint(s.RecordsBuffered)},
		{"resends_total", "counter", "Records sent again after a failed acknowledgement.", fmt.Sprint(s.Resends)},
		{"packets_sent_total", "counter", "Packets written.", fmt.Sprint(s.PacketsSent)},
		{"packets_received_total", "counter", "Packets read.", fmt.Sprint(s.PacketsReceived)},
		{"packets_bad_crc_total", "counter", "Packets read with a crc that did not match.", fmt.Sprint(s.PacketsBadCRC)},
		{"bytes_sent_total", "counter", "Bytes written.", fmt.Sprint(s.BytesSent)},
		{"bytes_received_total", "counter", "Bytes read.", fmt.Sprint(s.BytesReceived)},
		{"crc_corrupted_total", "counter", "Packets whose crc was corrupted on purpose.", fmt.Sprint(s.CRCCorrupted)},
		{"ack_mismatch_total", "counter", "Acknowledgements whose record count was wrong.", fmt.Sprint(s.ACKMismatch)},
		{"ack_timeout_total", "counter", "Send cycles that ended without an acknowledgement.", fmt.Sprint(s.ACKTimeout)},
		{"acks_sent_total", "counter", "Acknowledgements written by the receiver.", fmt.Sprint(s.ACKsSent)},
		{"acks_wrong_total", "counter", "Acknowledgements deliberately given a wrong count.", fmt.Sprint(s.ACKsWrong)},
		{"commands_sent_total", "counter", "Codec 12 commands written.", fmt.Sprint(s.CommandsSent)},
		{"commands_received_total", "counter", "Codec 12 commands read.", fmt.Sprint(s.CommandsReceived)},
		{"commands_answered_total", "counter", "Codec 12 responses written.", fmt.Sprint(s.CommandsAnswered)},
		{"uptime_seconds", "gauge", "Seconds since this process started.", fmt.Sprintf("%.0f", s.Uptime.Seconds())},
	}

	var b strings.Builder
	for _, m := range metrics {
		name := "teltonika_sim_" + m.name
		fmt.Fprintf(&b, "# HELP %s %s\n# TYPE %s %s\n%s{role=%q} %s\n", name, m.help, name, m.kind, name, s.Role, m.value)
	}
	name := "teltonika_sim_ack_latency_seconds"
	fmt.Fprintf(&b, "# HELP %s Time between writing a packet and reading its acknowledgement.\n# TYPE %s summary\n", name, name)
	fmt.Fprintf(&b, "%s{role=%q,quantile=\"0.5\"} %f\n", name, s.Role, s.ACKp50.Seconds())
	fmt.Fprintf(&b, "%s{role=%q,quantile=\"0.99\"} %f\n", name, s.Role, s.ACKp99.Seconds())
	fmt.Fprintf(&b, "%s_count{role=%q} %d\n", name, s.Role, s.ACKSamples)
	return b.String()
}

func Handler(r *Registry) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
		fmt.Fprint(w, r.Snapshot().Prometheus())
	})
}
