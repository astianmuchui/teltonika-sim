package receiver

import (
	"context"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/astianmuchui/teltonika-sim/internal/codec"
	"github.com/astianmuchui/teltonika-sim/internal/device"
	avlio "github.com/astianmuchui/teltonika-sim/internal/io"
	"github.com/astianmuchui/teltonika-sim/internal/route"
	"github.com/astianmuchui/teltonika-sim/internal/stats"
)

func quietLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func startServer(t *testing.T, cfg Config) (*Server, *stats.Registry) {
	t.Helper()
	if cfg.Listen == "" {
		cfg.Listen = "127.0.0.1:0"
	}
	reg := stats.New("serve")
	srv := New(cfg, reg, quietLogger())
	if err := srv.Listen(); err != nil {
		t.Fatalf("listen: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		if err := srv.Serve(ctx); err != nil {
			t.Errorf("serve: %v", err)
		}
	}()
	t.Cleanup(func() {
		cancel()
		srv.Close()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Error("the receiver did not stop within 5 s")
		}
	})
	return srv, reg
}

type fleet struct {
	devices []*device.Device
	reg     *stats.Registry
	wg      sync.WaitGroup
	cancel  context.CancelFunc
}

func startFleet(t *testing.T, addr string, count int, shape func(*device.Config)) *fleet {
	t.Helper()
	routeCfg := route.DefaultConfig()
	routeCfg.Devices = count
	factory, err := route.NewFactory(routeCfg)
	if err != nil {
		t.Fatalf("route factory: %v", err)
	}
	imeis, err := device.GenerateIMEIs("35630704", count)
	if err != nil {
		t.Fatalf("imeis: %v", err)
	}

	reg := stats.New("run")
	reg.SetDevicesTotal(count)
	f := &fleet{reg: reg}
	ctx, cancel := context.WithCancel(context.Background())
	f.cancel = cancel

	for i, imei := range imeis {
		cfg := device.DefaultConfig()
		cfg.IMEI = imei
		cfg.Server = addr
		cfg.Interval = 100 * time.Millisecond
		cfg.StoppedInterval = 100 * time.Millisecond
		cfg.ACKTimeout = 5 * time.Second
		cfg.FlushTimeout = 5 * time.Second
		cfg.BackoffMin = 20 * time.Millisecond
		cfg.BackoffMax = 100 * time.Millisecond
		cfg.Batch = 10
		cfg.Seed = uint64(i + 1)
		if shape != nil {
			shape(&cfg)
		}
		ioCfg := avlio.DefaultConfig()
		ioCfg.CodecID = cfg.CodecID
		ioCfg.WithNX = cfg.CodecID == codec.Codec8E
		gen, err := avlio.NewGenerator(ioCfg)
		if err != nil {
			t.Fatalf("io generator: %v", err)
		}
		dev := device.New(cfg, factory.New(i), gen.NewSampler(uint64(i+1)), reg, quietLogger())
		f.devices = append(f.devices, dev)
		f.wg.Add(1)
		go func() {
			defer f.wg.Done()
			if err := dev.Run(ctx); err != nil {
				t.Errorf("device %s: %v", dev.IMEI(), err)
			}
		}()
	}
	t.Cleanup(f.stop)
	return f
}

func (f *fleet) stop() {
	f.cancel()
	f.wg.Wait()
}

func TestEndToEndFragmentedAndCoalesced(t *testing.T) {
	srv, srvReg := startServer(t, Config{})
	fleet := startFleet(t, srv.Addr(), 20, func(cfg *device.Config) {
		cfg.Chaos.Fragment = true
		cfg.Chaos.Coalesce = true
	})

	time.Sleep(5 * time.Second)
	fleet.stop()

	run := fleet.reg.Snapshot()
	serve := srvReg.Snapshot()

	if run.RecordsGenerated == 0 {
		t.Fatal("no records were generated")
	}
	if run.RecordsACKed != run.RecordsGenerated {
		t.Errorf("acked %d of %d generated records", run.RecordsACKed, run.RecordsGenerated)
	}
	if run.Resends != 0 {
		t.Errorf("resends %d, want 0", run.Resends)
	}
	if run.ACKMismatch != 0 || run.ACKTimeout != 0 {
		t.Errorf("ack mismatches %d timeouts %d, want none", run.ACKMismatch, run.ACKTimeout)
	}
	if run.RecordsDropped != 0 {
		t.Errorf("dropped %d records", run.RecordsDropped)
	}
	if run.HandshakeOK != 20 {
		t.Errorf("handshakes accepted %d, want 20", run.HandshakeOK)
	}
	if serve.RecordsReceived != run.RecordsSent {
		t.Errorf("the receiver read %d records, the devices sent %d", serve.RecordsReceived, run.RecordsSent)
	}
	if serve.PacketsBadCRC != 0 {
		t.Errorf("the receiver saw %d packets with a bad crc", serve.PacketsBadCRC)
	}
	if serve.HandshakeOK != 20 {
		t.Errorf("the receiver accepted %d handshakes, want 20", serve.HandshakeOK)
	}
	if serve.ACKsWrong != 0 {
		t.Errorf("the receiver sent %d wrong acks", serve.ACKsWrong)
	}
	t.Logf("20 devices, 5 s: %d records generated, %d sent, %d acked, %d packets, ack p50 %s p99 %s",
		run.RecordsGenerated, run.RecordsSent, run.RecordsACKed, run.PacketsSent, run.ACKp50, run.ACKp99)
}

func TestReceiverRejectsListedIMEI(t *testing.T) {
	imeis, err := device.GenerateIMEIs("35630704", 2)
	if err != nil {
		t.Fatalf("imeis: %v", err)
	}
	srv, srvReg := startServer(t, Config{RejectIMEI: []string{imeis[0]}})
	fleet := startFleet(t, srv.Addr(), 2, nil)

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		s := srvReg.Snapshot()
		if s.HandshakeReject >= 2 && s.HandshakeOK >= 1 && s.RecordsReceived >= 1 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	fleet.stop()

	s := srvReg.Snapshot()
	if s.HandshakeReject < 2 {
		t.Errorf("rejected %d handshakes, want the listed imei to be turned away repeatedly", s.HandshakeReject)
	}
	if s.HandshakeOK < 1 {
		t.Error("the imei that was not listed never got in")
	}
	run := fleet.reg.Snapshot()
	if run.HandshakeReject < 2 {
		t.Errorf("the device side counted %d rejections", run.HandshakeReject)
	}
}

func TestReceiverDoesNotACKABadCRC(t *testing.T) {
	srv, srvReg := startServer(t, Config{})
	fleet := startFleet(t, srv.Addr(), 1, func(cfg *device.Config) {
		cfg.Chaos.BadCRCRate = 1
		cfg.ACKTimeout = 300 * time.Millisecond
	})

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if srvReg.Snapshot().PacketsBadCRC >= 2 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	fleet.stop()

	s := srvReg.Snapshot()
	if s.PacketsBadCRC < 2 {
		t.Errorf("the receiver saw %d bad crc packets, want at least 2", s.PacketsBadCRC)
	}
	if s.ACKsSent != 0 {
		t.Errorf("the receiver sent %d acks for packets with a bad crc, want 0", s.ACKsSent)
	}
	if s.RecordsReceived != 0 {
		t.Errorf("the receiver counted %d records from undecodable packets", s.RecordsReceived)
	}
	run := fleet.reg.Snapshot()
	if run.ACKTimeout == 0 {
		t.Error("the device never hit an ack timeout even though nothing was acked")
	}
	if run.RecordsACKed != 0 {
		t.Errorf("records acked %d, want 0", run.RecordsACKed)
	}
}

func TestReceiverWrongACKRateMakesDevicesResend(t *testing.T) {
	srv, srvReg := startServer(t, Config{ACKWrongRate: 1})
	fleet := startFleet(t, srv.Addr(), 1, func(cfg *device.Config) {
		cfg.Batch = 1
	})

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if fleet.reg.Snapshot().Resends >= 1 && srvReg.Snapshot().ACKsWrong >= 2 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	fleet.stop()

	run := fleet.reg.Snapshot()
	if run.ACKMismatch == 0 {
		t.Error("the device never saw an ack mismatch")
	}
	if run.Resends == 0 {
		t.Error("the device never resent anything")
	}
	if run.RecordsACKed != 0 {
		t.Errorf("records acked %d, want 0 since every ack was wrong", run.RecordsACKed)
	}
}

func TestReceiverCodec12RoundTrip(t *testing.T) {
	srv, srvReg := startServer(t, Config{SendCommand: "getinfo", CommandEvery: 100 * time.Millisecond})
	fleet := startFleet(t, srv.Addr(), 1, nil)

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		s := srvReg.Snapshot()
		if s.CommandsSent >= 2 && s.CommandsReceived >= 2 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	fleet.stop()

	s := srvReg.Snapshot()
	if s.CommandsSent < 2 {
		t.Errorf("the receiver sent %d commands, want at least 2", s.CommandsSent)
	}
	if s.CommandsReceived < 2 {
		t.Errorf("the receiver read %d responses, want at least 2", s.CommandsReceived)
	}
	run := fleet.reg.Snapshot()
	if run.CommandsAnswered < 2 {
		t.Errorf("the device answered %d commands", run.CommandsAnswered)
	}
	if run.RecordsACKed == 0 {
		t.Error("record flow stopped while commands were being answered")
	}
}

func TestReceiverDelayedACKIsStillAccepted(t *testing.T) {
	srv, _ := startServer(t, Config{ACKDelay: 150 * time.Millisecond})
	fleet := startFleet(t, srv.Addr(), 2, nil)

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if fleet.reg.Snapshot().RecordsACKed >= 4 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	fleet.stop()

	run := fleet.reg.Snapshot()
	if run.RecordsACKed < 4 {
		t.Errorf("acked %d records with a 150 ms ack delay, want at least 4", run.RecordsACKed)
	}
	if run.ACKTimeout != 0 {
		t.Errorf("ack timeouts %d, want none since the delay is well under the timeout", run.ACKTimeout)
	}
	if run.ACKp50 < 100*time.Millisecond {
		t.Errorf("ack latency p50 is %s, want it to show the 150 ms delay", run.ACKp50)
	}
}

func TestReceiverSurvivesGarbage(t *testing.T) {
	srv, srvReg := startServer(t, Config{})
	fleet := startFleet(t, srv.Addr(), 1, func(cfg *device.Config) {
		cfg.Chaos.Garbage = true
	})
	fleet.wg.Wait()

	if s := srvReg.Snapshot(); s.HandshakeOK != 0 {
		t.Errorf("the receiver accepted %d handshakes from an http client", s.HandshakeOK)
	}

	good := startFleet(t, srv.Addr(), 1, nil)
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if srvReg.Snapshot().RecordsReceived >= 1 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	good.stop()
	if s := srvReg.Snapshot(); s.RecordsReceived == 0 {
		t.Error("the receiver stopped working after being sent garbage")
	}
}
