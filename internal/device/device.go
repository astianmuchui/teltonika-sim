package device

import (
	"context"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math"
	"math/rand/v2"
	"net"
	"sync"
	"time"

	"github.com/astianmuchui/teltonika-sim/internal/codec"
	avlio "github.com/astianmuchui/teltonika-sim/internal/io"
	"github.com/astianmuchui/teltonika-sim/internal/route"
	"github.com/astianmuchui/teltonika-sim/internal/stats"
)

var (
	ErrACKTimeout  = errors.New("no ack within the ack timeout")
	ErrACKMismatch = errors.New("the ack count did not match the records sent")
	ErrGoOffline   = errors.New("scheduled offline period")
	ErrWriteStuck  = errors.New("the write queue is full, the connection is stuck")
)

type Chaos struct {
	Fragment   bool
	Coalesce   bool
	BadCRCRate float64
	DropRate   float64
	Offline    time.Duration
	ClockSkew  time.Duration
	NoFixRate  float64
	Garbage    bool
	SlowReader bool
}

type Config struct {
	IMEI            string
	Server          string
	CodecID         byte
	Batch           int
	Interval        time.Duration
	StoppedInterval time.Duration
	ACKTimeout      time.Duration
	FlushTimeout    time.Duration
	BufferMax       int
	BackoffMin      time.Duration
	BackoffMax      time.Duration
	DialTimeout     time.Duration
	Model           string
	Verbose         bool
	Seed            uint64
	Chaos           Chaos
}

func DefaultConfig() Config {
	return Config{
		CodecID:         codec.Codec8E,
		Batch:           1,
		Interval:        10 * time.Second,
		StoppedInterval: 60 * time.Second,
		ACKTimeout:      30 * time.Second,
		FlushTimeout:    5 * time.Second,
		BufferMax:       10000,
		BackoffMin:      time.Second,
		BackoffMax:      60 * time.Second,
		DialTimeout:     10 * time.Second,
		Model:           avlio.DefaultModel,
	}
}

type Device struct {
	cfg     Config
	log     *slog.Logger
	reg     *stats.Registry
	src     route.Source
	sampler *avlio.Sampler
	rng     *rand.Rand
	now     func() time.Time

	mu      sync.Mutex
	buffer  []codec.Record
	lastFix route.Fix
	lastIO  []codec.IOElement

	ignition     bool
	sinceRecord  time.Duration
	sentHigh     int
	offlineArmed bool
	offlineDone  bool
	sessions     int
}

func newRNG(seed uint64) *rand.Rand {
	return rand.New(rand.NewPCG(seed, 0xA0761D6478BD642F))
}

func New(cfg Config, src route.Source, sampler *avlio.Sampler, reg *stats.Registry, log *slog.Logger) *Device {
	base := DefaultConfig()
	if cfg.Batch < 1 {
		cfg.Batch = base.Batch
	}
	if cfg.Interval <= 0 {
		cfg.Interval = base.Interval
	}
	if cfg.StoppedInterval <= 0 {
		cfg.StoppedInterval = base.StoppedInterval
	}
	if cfg.ACKTimeout <= 0 {
		cfg.ACKTimeout = base.ACKTimeout
	}
	if cfg.BufferMax < 1 {
		cfg.BufferMax = base.BufferMax
	}
	if cfg.BackoffMin <= 0 {
		cfg.BackoffMin = base.BackoffMin
	}
	if cfg.BackoffMax < cfg.BackoffMin {
		cfg.BackoffMax = max(base.BackoffMax, cfg.BackoffMin)
	}
	if cfg.DialTimeout <= 0 {
		cfg.DialTimeout = base.DialTimeout
	}
	if cfg.Model == "" {
		cfg.Model = base.Model
	}
	d := &Device{
		cfg:     cfg,
		log:     log.With("imei", cfg.IMEI),
		reg:     reg,
		src:     src,
		sampler: sampler,
		rng:     newRNG(cfg.Seed),
		now:     time.Now,
	}
	d.lastFix = src.Current()
	d.ignition = d.lastFix.Ignition
	d.sinceRecord = cfg.StoppedInterval
	return d
}

func (d *Device) IMEI() string { return d.cfg.IMEI }

func (d *Device) BufferLen() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return len(d.buffer)
}

func (d *Device) tick() time.Duration {
	t := min(d.cfg.Interval, d.cfg.StoppedInterval, 500*time.Millisecond)
	if t <= 0 {
		t = time.Millisecond
	}
	return t
}

func (d *Device) Run(ctx context.Context) error {
	b := newBackoff(d.cfg.BackoffMin, d.cfg.BackoffMax, newRNG(d.cfg.Seed^0x5151))
	for ctx.Err() == nil {
		conn, err := d.dial(ctx)
		if err != nil {
			d.reg.ConnectFailed()
			d.log.Warn("connect failed", "server", d.cfg.Server, "error", err)
			if d.idle(ctx, b.next()) != nil {
				break
			}
			d.reg.Reconnect()
			continue
		}
		if d.cfg.Chaos.Garbage {
			return d.sendGarbage(conn)
		}

		accepted, err := d.handshake(conn)
		if err != nil {
			conn.Close()
			d.log.Warn("handshake failed", "server", d.cfg.Server, "error", err)
			if d.idle(ctx, b.next()) != nil {
				break
			}
			d.reg.Reconnect()
			continue
		}
		if !accepted {
			conn.Close()
			d.reg.HandshakeRejected()
			wait := b.next()
			d.log.Warn("handshake rejected", "server", d.cfg.Server, "retry in", wait)
			if d.idle(ctx, wait) != nil {
				break
			}
			d.reg.Reconnect()
			continue
		}

		b.reset()
		d.sessions++
		d.reg.HandshakeOK()
		d.reg.Connected(1)
		d.log.Info("connected", "server", d.cfg.Server, "codec", fmt.Sprintf("0x%02X", d.cfg.CodecID),
			"local", conn.LocalAddr().String())

		err = d.runSession(ctx, conn)
		d.reg.Connected(-1)
		d.reg.Disconnect()
		conn.Close()

		if ctx.Err() != nil {
			break
		}
		wait := b.next()
		switch {
		case errors.Is(err, ErrGoOffline):
			wait = d.cfg.Chaos.Offline
			d.log.Info("going offline, records keep piling up", "for", wait, "buffered", d.BufferLen())
		case err != nil:
			d.log.Warn("session ended", "error", err, "buffered", d.BufferLen(), "retry in", wait)
		default:
			d.log.Info("session ended", "buffered", d.BufferLen(), "retry in", wait)
		}
		if d.idle(ctx, wait) != nil {
			break
		}
		d.reg.Reconnect()
	}
	return nil
}

func (d *Device) dial(ctx context.Context) (net.Conn, error) {
	dialer := net.Dialer{Timeout: d.cfg.DialTimeout}
	return dialer.DialContext(ctx, "tcp", d.cfg.Server)
}

func (d *Device) sendGarbage(conn net.Conn) error {
	defer conn.Close()
	body := "GET / HTTP/1.1\r\nHost: " + d.cfg.Server + "\r\nUser-Agent: teltonika-sim\r\n\r\n"
	if err := conn.SetWriteDeadline(time.Now().Add(d.cfg.DialTimeout)); err != nil {
		return err
	}
	if _, err := conn.Write([]byte(body)); err != nil {
		return err
	}
	d.log.Info("sent an http request instead of a handshake, exiting", "bytes", len(body))
	return nil
}

func (d *Device) handshake(conn net.Conn) (bool, error) {
	payload, err := codec.EncodeIMEI(d.cfg.IMEI)
	if err != nil {
		return false, err
	}
	if err := conn.SetDeadline(time.Now().Add(d.cfg.DialTimeout)); err != nil {
		return false, err
	}
	defer conn.SetDeadline(time.Time{})
	if _, err := conn.Write(payload); err != nil {
		return false, err
	}
	var reply [1]byte
	if _, err := io.ReadFull(conn, reply[:]); err != nil {
		return false, err
	}
	if d.cfg.Verbose {
		d.log.Debug("handshake reply", "byte", fmt.Sprintf("0x%02X", reply[0]))
	}
	return reply[0] == codec.HandshakeAccept, nil
}

type event struct {
	ack     uint32
	hasACK  bool
	command *codec.Command
	err     error
}

func (d *Device) runSession(ctx context.Context, conn net.Conn) error {
	sctx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	defer cancel()

	writes := make(chan []byte, 8)
	events := make(chan event, 8)
	emit := func(ev event) {
		select {
		case events <- ev:
		case <-sctx.Done():
		}
	}

	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		d.writeLoop(sctx, conn, writes, newRNG(d.cfg.Seed^uint64(d.sessions)*0x9E3779B1), emit)
	}()
	go func() {
		defer wg.Done()
		d.readLoop(sctx, conn, newRNG(d.cfg.Seed^uint64(d.sessions)*0x85EBCA77), emit)
	}()

	err := d.sendLoop(ctx, sctx, writes, events)
	cancel()
	conn.Close()
	wg.Wait()
	return err
}

func (d *Device) writeLoop(ctx context.Context, conn net.Conn, in <-chan []byte, rng *rand.Rand, emit func(event)) {
	for {
		select {
		case <-ctx.Done():
			return
		case buf := <-in:
			if d.cfg.Chaos.Coalesce {
				select {
				case more := <-in:
					buf = append(buf, more...)
					d.log.Debug("coalesced two packets into one write", "bytes", len(buf))
				default:
				}
			}
			if err := d.writeBytes(conn, buf, rng); err != nil {
				emit(event{err: err})
				return
			}
		}
	}
}

func (d *Device) writeBytes(conn net.Conn, buf []byte, rng *rand.Rand) error {
	if err := conn.SetWriteDeadline(time.Now().Add(d.cfg.ACKTimeout)); err != nil {
		return err
	}
	if d.cfg.Verbose {
		d.log.Debug("write", "bytes", len(buf), "hex", hex.EncodeToString(buf))
	}
	if rate := d.cfg.Chaos.DropRate; rate > 0 && rng.Float64() < rate {
		cut := 1 + rng.IntN(len(buf))
		if _, err := conn.Write(buf[:cut]); err != nil {
			return err
		}
		return fmt.Errorf("dropped the connection after %d of %d bytes", cut, len(buf))
	}
	if d.cfg.Chaos.Fragment {
		for off := 0; off < len(buf); {
			n := min(1+rng.IntN(64), len(buf)-off)
			if _, err := conn.Write(buf[off : off+n]); err != nil {
				return err
			}
			off += n
			if gap := time.Duration(rng.IntN(21)) * time.Millisecond; gap > 0 && off < len(buf) {
				time.Sleep(gap)
			}
		}
		return nil
	}
	_, err := conn.Write(buf)
	return err
}

func (d *Device) readLoop(ctx context.Context, conn net.Conn, rng *rand.Rand, emit func(event)) {
	for ctx.Err() == nil {
		if d.cfg.Chaos.SlowReader {
			delay := time.Duration(500+rng.IntN(1500)) * time.Millisecond
			d.log.Debug("slow reader, delaying the read", "delay", delay)
			select {
			case <-ctx.Done():
				return
			case <-time.After(delay):
			}
		}
		var head [4]byte
		if _, err := io.ReadFull(conn, head[:]); err != nil {
			emit(event{err: err})
			return
		}
		if d.cfg.Verbose {
			d.log.Debug("read", "bytes", 4, "hex", hex.EncodeToString(head[:]))
		}
		if n := binary.BigEndian.Uint32(head[:]); n != 0 {
			d.reg.PacketReceived(4)
			emit(event{ack: n, hasACK: true})
			continue
		}

		var size [4]byte
		if err := conn.SetReadDeadline(time.Now().Add(500 * time.Millisecond)); err != nil {
			emit(event{err: err})
			return
		}
		_, err := io.ReadFull(conn, size[:])
		if clearErr := conn.SetReadDeadline(time.Time{}); clearErr != nil {
			emit(event{err: clearErr})
			return
		}
		if err != nil {
			var netErr net.Error
			if errors.As(err, &netErr) && netErr.Timeout() {
				d.reg.PacketReceived(4)
				d.log.Warn("the server acked zero records")
				emit(event{ack: 0, hasACK: true})
				continue
			}
			emit(event{err: err})
			return
		}

		length := int(binary.BigEndian.Uint32(size[:]))
		if length <= 0 || length > codec.MaxPayload {
			emit(event{err: fmt.Errorf("server frame declares %d bytes of payload", length)})
			return
		}
		frame := make([]byte, codec.HeaderSize+length+codec.CRCSize)
		copy(frame[0:4], head[:])
		copy(frame[4:8], size[:])
		if _, err := io.ReadFull(conn, frame[codec.HeaderSize:]); err != nil {
			emit(event{err: err})
			return
		}
		if d.cfg.Verbose {
			d.log.Debug("read", "bytes", len(frame), "hex", hex.EncodeToString(frame))
		}
		d.reg.PacketReceived(len(frame))
		p, err := codec.Decode(frame)
		if err != nil {
			d.log.Warn("could not read what the server sent", "error", err, "hex", hex.EncodeToString(frame))
			continue
		}
		if p.Command == nil {
			d.log.Warn("the server sent an avl packet, which a device never expects", "codec", fmt.Sprintf("0x%02X", p.CodecID))
			continue
		}
		emit(event{command: p.Command})
	}
}

func (d *Device) sendLoop(ctx, sctx context.Context, writes chan []byte, events chan event) error {
	tick := d.tick()
	ticker := time.NewTicker(tick)
	defer ticker.Stop()

	inflight := 0
	var sentAt time.Time
	var ackDeadline <-chan time.Time

	send := func() error {
		frame, n, err := d.buildPacket(d.cfg.Batch)
		if err != nil {
			return err
		}
		if n == 0 {
			return nil
		}
		if err := d.enqueue(sctx, writes, frame); err != nil {
			return err
		}
		inflight = n
		sentAt = time.Now()
		ackDeadline = time.After(d.cfg.ACKTimeout)
		return nil
	}

	if err := send(); err != nil {
		return err
	}

	for {
		select {
		case <-ctx.Done():
			return d.finalFlush(sctx, writes, events, inflight, sentAt)

		case <-ticker.C:
			d.generate(tick)
			if d.offlineArmed && !d.offlineDone {
				d.offlineDone = true
				d.offlineArmed = false
				return ErrGoOffline
			}
			if inflight == 0 {
				if err := send(); err != nil {
					return err
				}
			}

		case <-ackDeadline:
			d.reg.ACKTimeout()
			d.log.Warn("ack timeout", "records", inflight, "after", d.cfg.ACKTimeout)
			return ErrACKTimeout

		case ev := <-events:
			switch {
			case ev.err != nil:
				return ev.err
			case ev.command != nil:
				if err := d.answer(sctx, ev.command, writes); err != nil {
					return err
				}
			case ev.hasACK:
				if inflight == 0 {
					d.log.Warn("ack arrived with nothing in flight", "ack", ev.ack)
					continue
				}
				if int(ev.ack) != inflight {
					d.reg.ACKMismatch()
					d.log.Warn("ack mismatch", "ack", ev.ack, "records", inflight)
					return ErrACKMismatch
				}
				d.commit(inflight, sentAt)
				inflight = 0
				ackDeadline = nil
				if err := send(); err != nil {
					return err
				}
			}
		}
	}
}

func (d *Device) enqueue(ctx context.Context, writes chan []byte, frame []byte) error {
	select {
	case writes <- frame:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	default:
		return ErrWriteStuck
	}
}

func (d *Device) finalFlush(sctx context.Context, writes chan []byte, events chan event, inflight int, sentAt time.Time) error {
	if d.cfg.FlushTimeout <= 0 {
		return nil
	}
	deadline := time.After(d.cfg.FlushTimeout)
	for {
		if inflight == 0 {
			frame, n, err := d.buildPacket(0xFF)
			if err != nil || n == 0 {
				return err
			}
			if err := d.enqueue(sctx, writes, frame); err != nil {
				return err
			}
			inflight = n
			sentAt = time.Now()
			d.log.Info("final flush", "records", n)
		}
		select {
		case <-deadline:
			d.log.Warn("final flush gave up", "buffered", d.BufferLen(), "deadline", d.cfg.FlushTimeout)
			return nil
		case ev := <-events:
			switch {
			case ev.err != nil:
				return nil
			case ev.command != nil:
			case ev.hasACK:
				if int(ev.ack) != inflight {
					d.reg.ACKMismatch()
					return nil
				}
				d.commit(inflight, sentAt)
				inflight = 0
				if d.BufferLen() == 0 {
					return nil
				}
			}
		}
	}
}

func (d *Device) buildPacket(limit int) ([]byte, int, error) {
	if limit > 0xFF {
		limit = 0xFF
	}
	d.mu.Lock()
	n := min(len(d.buffer), limit)
	records := make([]codec.Record, n)
	copy(records, d.buffer[:n])
	resent := min(n, d.sentHigh)
	if n > d.sentHigh {
		d.sentHigh = n
	}
	d.mu.Unlock()
	if n == 0 {
		return nil, 0, nil
	}

	frame, err := codec.EncodeAVL(d.cfg.CodecID, records)
	if err != nil {
		return nil, 0, err
	}
	corrupted := false
	if rate := d.cfg.Chaos.BadCRCRate; rate > 0 && d.rng.Float64() < rate {
		frame[len(frame)-1] ^= 0xFF
		corrupted = true
		d.reg.CRCCorrupted()
	}
	d.reg.RecordsSent(n)
	d.reg.PacketSent(len(frame))
	if resent > 0 {
		d.reg.Resends(resent)
	}
	d.log.Info("packet sent", "records", n, "bytes", len(frame), "resent", resent, "bad crc", corrupted,
		"buffered", d.BufferLen())
	return frame, n, nil
}

func (d *Device) commit(n int, sentAt time.Time) {
	d.mu.Lock()
	if n > len(d.buffer) {
		n = len(d.buffer)
	}
	d.buffer = append(d.buffer[:0], d.buffer[n:]...)
	d.sentHigh = max(0, d.sentHigh-n)
	d.mu.Unlock()

	var latency time.Duration
	if !sentAt.IsZero() {
		latency = time.Since(sentAt)
		d.reg.ObserveACKLatency(latency)
	}
	d.reg.RecordsACKed(n)
	d.reg.BufferedDelta(-n)
	if d.cfg.Chaos.Offline > 0 && !d.offlineDone {
		d.offlineArmed = true
	}
	d.log.Info("ack ok", "records", n, "latency", latency.Round(time.Microsecond), "buffered", len(d.buffer))
}

func (d *Device) idle(ctx context.Context, wait time.Duration) error {
	if wait <= 0 {
		return ctx.Err()
	}
	tick := d.tick()
	ticker := time.NewTicker(tick)
	defer ticker.Stop()
	deadline := time.After(wait)
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-deadline:
			return nil
		case <-ticker.C:
			d.generate(tick)
		}
	}
}

func (d *Device) generate(elapsed time.Duration) {
	fix := d.src.Advance(elapsed)
	d.mu.Lock()
	d.lastFix = fix
	d.mu.Unlock()

	if fix.Ignition != d.ignition {
		d.ignition = fix.Ignition
		d.log.Info("ignition changed", "on", fix.Ignition)
		d.appendRecord(fix, avlio.IgnitionEventID, codec.PriorityHigh)
	}
	d.sinceRecord += elapsed
	if d.sinceRecord >= d.recordInterval(fix) {
		d.sinceRecord = 0
		d.appendRecord(fix, 0, codec.PriorityLow)
	}
}

func (d *Device) recordInterval(fix route.Fix) time.Duration {
	if fix.Moving {
		return d.cfg.Interval
	}
	return d.cfg.StoppedInterval
}

func (d *Device) appendRecord(fix route.Fix, eventID uint16, priority uint8) {
	noFix := d.cfg.Chaos.NoFixRate > 0 && d.rng.Float64() < d.cfg.Chaos.NoFixRate

	sample := fix
	if noFix {
		sample.Valid = false
		sample.StepMeters = 0
	}
	elements, err := d.sampler.Elements(sample)
	if err != nil {
		d.log.Error("could not build the io element set", "error", err)
		return
	}

	gps := codec.GPS{
		Latitude:   fix.Latitude,
		Longitude:  fix.Longitude,
		Altitude:   int16(math.Round(fix.Altitude)),
		Angle:      uint16(math.Round(fix.Bearing)) % 360,
		Satellites: fix.Satellites,
		Speed:      uint16(math.Round(min(fix.SpeedKPH, 65535))),
	}
	if noFix {
		gps = codec.GPS{}
	}

	record := codec.Record{
		Timestamp: d.now().Add(d.cfg.Chaos.ClockSkew).UTC(),
		Priority:  priority,
		GPS:       gps,
		IO:        codec.IO{EventID: eventID, Elements: elements},
	}

	d.mu.Lock()
	d.buffer = append(d.buffer, record)
	d.lastIO = elements
	over := len(d.buffer) - d.cfg.BufferMax
	if over > 0 {
		d.buffer = append(d.buffer[:0], d.buffer[over:]...)
		d.sentHigh = max(0, d.sentHigh-over)
	}
	d.mu.Unlock()

	d.reg.RecordsGenerated(1)
	d.reg.BufferedDelta(1)
	if over > 0 {
		d.reg.RecordsDropped(over)
		d.reg.BufferedDelta(-over)
		d.log.Warn("flash buffer full, dropped the oldest records", "dropped", over, "max", d.cfg.BufferMax)
	}
	if d.cfg.Verbose {
		d.log.Debug("record", "event", eventID, "priority", priority, "lat", gps.Latitude, "lon", gps.Longitude,
			"speed", gps.Speed, "satellites", gps.Satellites, "io", len(elements))
	}
}
