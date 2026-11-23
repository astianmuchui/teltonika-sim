package device

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/astianmuchui/teltonika-sim/internal/codec"
	avlio "github.com/astianmuchui/teltonika-sim/internal/io"
	"github.com/astianmuchui/teltonika-sim/internal/route"
	"github.com/astianmuchui/teltonika-sim/internal/stats"
)

const testIMEI = "356307042441013"

type fakePacket struct {
	imei    string
	records []codec.Record
}

type fakeOptions struct {
	rejectFirst int
	ackOffset   int
	ackDelay    time.Duration
	noACK       bool
	sendCommand string
}

type fakeServer struct {
	ln   net.Listener
	opts fakeOptions

	mu         sync.Mutex
	handshakes []string
	packets    []fakePacket
	badCRC     int
	responses  []string
	wg         sync.WaitGroup
	stopped    bool
}

func startFakeServer(t *testing.T, opts fakeOptions) *fakeServer {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	s := &fakeServer{ln: ln, opts: opts}
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			s.wg.Add(1)
			go func() {
				defer s.wg.Done()
				s.handle(conn)
			}()
		}
	}()
	t.Cleanup(s.stop)
	return s
}

func (s *fakeServer) addr() string { return s.ln.Addr().String() }

func (s *fakeServer) stop() {
	s.mu.Lock()
	already := s.stopped
	s.stopped = true
	s.mu.Unlock()
	if already {
		return
	}
	s.ln.Close()
	s.wg.Wait()
}

func (s *fakeServer) handle(conn net.Conn) {
	defer conn.Close()
	imei, err := codec.ReadIMEI(conn)
	if err != nil {
		return
	}
	s.mu.Lock()
	s.handshakes = append(s.handshakes, imei)
	reject := len(s.handshakes) <= s.opts.rejectFirst
	s.mu.Unlock()

	if reject {
		conn.Write([]byte{codec.HandshakeReject})
		return
	}
	if _, err := conn.Write([]byte{codec.HandshakeAccept}); err != nil {
		return
	}
	if s.opts.sendCommand != "" {
		frame, err := codec.EncodeCommand(s.opts.sendCommand)
		if err != nil {
			return
		}
		if _, err := conn.Write(frame); err != nil {
			return
		}
	}

	for {
		frame, err := codec.ReadFrame(conn)
		if err != nil {
			return
		}
		p, err := codec.Decode(frame)
		if err != nil {
			if errors.Is(err, codec.ErrBadCRC) {
				s.mu.Lock()
				s.badCRC++
				s.mu.Unlock()
				continue
			}
			return
		}
		if p.Command != nil {
			s.mu.Lock()
			s.responses = append(s.responses, p.Command.Text)
			s.mu.Unlock()
			continue
		}
		s.mu.Lock()
		s.packets = append(s.packets, fakePacket{imei: imei, records: p.Records})
		s.mu.Unlock()

		if s.opts.noACK {
			continue
		}
		if s.opts.ackDelay > 0 {
			time.Sleep(s.opts.ackDelay)
		}
		if _, err := conn.Write(codec.EncodeACK(uint32(len(p.Records) + s.opts.ackOffset))); err != nil {
			return
		}
	}
}

func (s *fakeServer) handshakeCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.handshakes)
}

func (s *fakeServer) packetCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.packets)
}

func (s *fakeServer) packetAt(i int) (fakePacket, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if i >= len(s.packets) {
		return fakePacket{}, false
	}
	return s.packets[i], true
}

func (s *fakeServer) badCRCCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.badCRC
}

func (s *fakeServer) responseList() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.responses...)
}

func testConfig(addr string) Config {
	cfg := DefaultConfig()
	cfg.IMEI = testIMEI
	cfg.Server = addr
	cfg.Interval = 50 * time.Millisecond
	cfg.StoppedInterval = 50 * time.Millisecond
	cfg.ACKTimeout = 300 * time.Millisecond
	cfg.FlushTimeout = 200 * time.Millisecond
	cfg.BackoffMin = 20 * time.Millisecond
	cfg.BackoffMax = 80 * time.Millisecond
	cfg.DialTimeout = 2 * time.Second
	cfg.Seed = 1
	return cfg
}

func newTestDevice(t *testing.T, cfg Config) (*Device, *stats.Registry) {
	t.Helper()
	factory, err := route.NewFactory(route.DefaultConfig())
	if err != nil {
		t.Fatalf("route factory: %v", err)
	}
	ioCfg := avlio.DefaultConfig()
	ioCfg.CodecID = cfg.CodecID
	gen, err := avlio.NewGenerator(ioCfg)
	if err != nil {
		t.Fatalf("io generator: %v", err)
	}
	reg := stats.New("test")
	reg.SetDevicesTotal(1)
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	return New(cfg, factory.New(0), gen.NewSampler(cfg.Seed), reg, log), reg
}

func runDevice(t *testing.T, d *Device) (context.CancelFunc, <-chan struct{}) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		if err := d.Run(ctx); err != nil {
			t.Errorf("device run: %v", err)
		}
	}()
	stop := func() {
		cancel()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Error("the device did not stop within 5 s of cancelling the context")
		}
	}
	t.Cleanup(stop)
	return cancel, done
}

func waitFor(t *testing.T, what string, timeout time.Duration, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timed out after %s waiting for %s", timeout, what)
}

func TestDeviceHandshakeAndACKDrainsTheBuffer(t *testing.T) {
	srv := startFakeServer(t, fakeOptions{})
	dev, reg := newTestDevice(t, testConfig(srv.addr()))
	runDevice(t, dev)

	waitFor(t, "three acked records and an empty flash buffer", 5*time.Second, func() bool {
		return reg.Snapshot().RecordsACKed >= 3 && dev.BufferLen() == 0
	})

	s := reg.Snapshot()
	if s.HandshakeOK != 1 {
		t.Errorf("handshakes accepted %d, want 1", s.HandshakeOK)
	}
	if srv.handshakeCount() != 1 {
		t.Errorf("the server saw %d handshakes, want 1", srv.handshakeCount())
	}
	if s.RecordsSent != s.RecordsACKed {
		t.Errorf("sent %d records and acked %d, want them equal", s.RecordsSent, s.RecordsACKed)
	}
	if s.Resends != 0 {
		t.Errorf("resends %d on a clean run, want 0", s.Resends)
	}
	if s.ACKMismatch != 0 || s.ACKTimeout != 0 {
		t.Errorf("ack mismatches %d timeouts %d on a clean run, want none", s.ACKMismatch, s.ACKTimeout)
	}
	if s.ACKp50 <= 0 {
		t.Errorf("ack latency p50 is %s, want a measured value", s.ACKp50)
	}
	first, ok := srv.packetAt(0)
	if !ok {
		t.Fatal("the server recorded no packets")
	}
	if first.imei != testIMEI {
		t.Errorf("the server saw imei %q, want %q", first.imei, testIMEI)
	}
	if len(first.records) == 0 {
		t.Error("the first packet carried no records")
	}
	if n := len(first.records[0].IO.Elements); n != 15 {
		t.Errorf("the first record carried %d io elements, want the 15 fmb920 defaults", n)
	}
}

func TestDeviceResendsAfterAWrongACK(t *testing.T) {
	srv := startFakeServer(t, fakeOptions{ackOffset: 1})
	dev, reg := newTestDevice(t, testConfig(srv.addr()))
	runDevice(t, dev)

	waitFor(t, "two packets after a wrong ack", 5*time.Second, func() bool {
		return srv.packetCount() >= 2 && reg.Snapshot().Resends >= 1
	})

	first, _ := srv.packetAt(0)
	second, _ := srv.packetAt(1)
	if len(first.records) == 0 || len(second.records) == 0 {
		t.Fatal("a packet arrived with no records")
	}
	if !first.records[0].Timestamp.Equal(second.records[0].Timestamp) {
		t.Errorf("after a wrong ack the device sent record %s then %s, want the same record again",
			first.records[0].Timestamp, second.records[0].Timestamp)
	}
	s := reg.Snapshot()
	if s.ACKMismatch == 0 {
		t.Error("no ack mismatch was counted")
	}
	if s.Reconnects == 0 {
		t.Error("the device did not reconnect after the wrong ack")
	}
	if s.RecordsACKed != 0 {
		t.Errorf("records acked %d, want 0 because every ack was wrong", s.RecordsACKed)
	}
	if dev.BufferLen() == 0 {
		t.Error("the flash buffer is empty, un-acked records must be kept")
	}
	if srv.handshakeCount() < 2 {
		t.Errorf("the server saw %d handshakes, want at least 2", srv.handshakeCount())
	}
}

func TestDeviceRetriesAfterAHandshakeRejection(t *testing.T) {
	srv := startFakeServer(t, fakeOptions{rejectFirst: 2})
	dev, reg := newTestDevice(t, testConfig(srv.addr()))
	runDevice(t, dev)

	waitFor(t, "a rejected device to get through and send", 5*time.Second, func() bool {
		s := reg.Snapshot()
		return s.HandshakeReject >= 2 && s.HandshakeOK >= 1 && s.RecordsACKed >= 1
	})

	if got := srv.handshakeCount(); got < 3 {
		t.Errorf("the server saw %d handshakes, want at least 3", got)
	}
	if s := reg.Snapshot(); s.Reconnects < 2 {
		t.Errorf("reconnects %d, want at least 2", s.Reconnects)
	}
}

func TestDeviceResendsWithAGoodCRCAfterCorruptingOne(t *testing.T) {
	srv := startFakeServer(t, fakeOptions{})
	cfg := testConfig(srv.addr())
	cfg.Chaos.BadCRCRate = 0.5
	dev, reg := newTestDevice(t, cfg)
	runDevice(t, dev)

	waitFor(t, "a corrupt packet the server refused to ack", 10*time.Second, func() bool {
		return srv.badCRCCount() >= 1 && reg.Snapshot().ACKTimeout >= 1
	})
	waitFor(t, "the same records to arrive again with a good crc", 10*time.Second, func() bool {
		return reg.Snapshot().RecordsACKed >= 1 && reg.Snapshot().Resends >= 1
	})

	s := reg.Snapshot()
	if s.CRCCorrupted == 0 {
		t.Error("no packet was corrupted even though the rate was 0.5")
	}
	if srv.badCRCCount() == 0 {
		t.Error("the server never saw a bad crc")
	}
	if s.RecordsACKed == 0 {
		t.Error("nothing was ever acked, the good crc resend never arrived")
	}
}

func TestDeviceAnswersCodec12WhileSending(t *testing.T) {
	srv := startFakeServer(t, fakeOptions{sendCommand: "getinfo"})
	dev, reg := newTestDevice(t, testConfig(srv.addr()))
	runDevice(t, dev)

	waitFor(t, "the device to answer getinfo and keep sending", 5*time.Second, func() bool {
		s := reg.Snapshot()
		return s.CommandsAnswered >= 1 && s.RecordsACKed >= 2
	})

	replies := srv.responseList()
	if len(replies) == 0 {
		t.Fatal("the server received no codec 12 response")
	}
	if len(replies[0]) == 0 || replies[0] == UnknownCommand {
		t.Errorf("getinfo was answered with %q", replies[0])
	}
}

func TestDeviceKeepsGeneratingWhileTheServerIsDown(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	addr := ln.Addr().String()
	ln.Close()

	dev, reg := newTestDevice(t, testConfig(addr))
	runDevice(t, dev)

	waitFor(t, "records to pile up while nothing is listening", 5*time.Second, func() bool {
		s := reg.Snapshot()
		return s.ConnectFailed >= 2 && s.RecordsGenerated >= 3 && dev.BufferLen() >= 3
	})
	if s := reg.Snapshot(); s.RecordsSent != 0 || s.RecordsACKed != 0 {
		t.Errorf("sent %d and acked %d records with no server, want none", s.RecordsSent, s.RecordsACKed)
	}
}

func TestDeviceSendsGarbageThenExits(t *testing.T) {
	srv := startFakeServer(t, fakeOptions{})
	cfg := testConfig(srv.addr())
	cfg.Chaos.Garbage = true
	dev, _ := newTestDevice(t, cfg)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := dev.Run(ctx); err != nil {
		t.Fatalf("garbage run: %v", err)
	}
	if srv.handshakeCount() != 0 {
		t.Errorf("the server accepted %d handshakes from a garbage client", srv.handshakeCount())
	}
	if srv.packetCount() != 0 {
		t.Errorf("the server read %d packets from a garbage client", srv.packetCount())
	}
}

func TestFlashBufferDropsTheOldestWhenFull(t *testing.T) {
	cfg := testConfig("127.0.0.1:1")
	cfg.BufferMax = 5
	dev, reg := newTestDevice(t, cfg)
	for range 20 {
		dev.generate(cfg.Interval)
	}
	if got := dev.BufferLen(); got != 5 {
		t.Fatalf("flash buffer holds %d records, want the cap of 5", got)
	}
	s := reg.Snapshot()
	if s.RecordsDropped < 15 {
		t.Errorf("dropped %d records, want at least 15", s.RecordsDropped)
	}
	if s.RecordsBuffered != 5 {
		t.Errorf("buffered gauge is %d, want 5", s.RecordsBuffered)
	}
}

func TestRespondCoversTheDocumentedCommands(t *testing.T) {
	dev, _ := newTestDevice(t, testConfig("127.0.0.1:1"))
	dev.generate(50 * time.Millisecond)

	for _, tc := range []struct {
		command string
		want    string
	}{
		{"setdigout 1", "DOUT1:1"},
		{"setdigout 0", "DOUT1:0"},
		{"setdigout 10", "DOUT1:1 DOUT2:0"},
		{"setdigout", UnknownCommand},
		{"setdigout 2", UnknownCommand},
		{"reboot", UnknownCommand},
		{"", UnknownCommand},
	} {
		if got := dev.Respond(tc.command); got != tc.want {
			t.Errorf("respond(%q) = %q, want %q", tc.command, got, tc.want)
		}
	}

	for _, command := range []string{"getinfo", "getver", "getgps", "getio", "GETINFO"} {
		got := dev.Respond(command)
		if got == UnknownCommand || got == "" {
			t.Errorf("respond(%q) = %q", command, got)
		}
	}
	if got := dev.Respond("getver"); !strings.Contains(got, testIMEI) {
		t.Errorf("getver answered %q, want it to carry the imei", got)
	}
}
