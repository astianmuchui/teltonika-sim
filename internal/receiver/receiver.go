package receiver

import (
	"bufio"
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"github.com/astianmuchui/teltonika-sim/internal/codec"
	"github.com/astianmuchui/teltonika-sim/internal/stats"
)

type Config struct {
	Listen         string
	RejectIMEI     []string
	ACKWrongRate   float64
	ACKDelay       time.Duration
	SendCommand    string
	CommandEvery   time.Duration
	DisconnectRate float64
	Verbose        bool
	Seed           uint64
}

func DefaultConfig() Config {
	return Config{Listen: ":5027"}
}

type Server struct {
	cfg     Config
	log     *slog.Logger
	reg     *stats.Registry
	reject  map[string]bool
	ln      net.Listener
	wg      sync.WaitGroup
	conns   atomic.Int64
	seq     atomic.Uint64
	closing atomic.Bool
}

func New(cfg Config, reg *stats.Registry, log *slog.Logger) *Server {
	reject := make(map[string]bool, len(cfg.RejectIMEI))
	for _, imei := range cfg.RejectIMEI {
		if imei != "" {
			reject[imei] = true
		}
	}
	return &Server{cfg: cfg, log: log, reg: reg, reject: reject}
}

func (s *Server) Listen() error {
	if s.ln != nil {
		return errors.New("the receiver is already listening")
	}
	ln, err := net.Listen("tcp", s.cfg.Listen)
	if err != nil {
		return err
	}
	s.ln = ln
	return nil
}

func (s *Server) Addr() string {
	if s.ln == nil {
		return s.cfg.Listen
	}
	return s.ln.Addr().String()
}

func (s *Server) Close() error {
	if s.ln == nil {
		return nil
	}
	s.closing.Store(true)
	err := s.ln.Close()
	s.wg.Wait()
	return err
}

func (s *Server) Serve(ctx context.Context) error {
	if s.ln == nil {
		if err := s.Listen(); err != nil {
			return err
		}
	}
	s.log.Info("listening", "addr", s.Addr())

	go func() {
		<-ctx.Done()
		s.closing.Store(true)
		s.ln.Close()
	}()

	for {
		conn, err := s.ln.Accept()
		if err != nil {
			s.wg.Wait()
			if s.closing.Load() || ctx.Err() != nil {
				return nil
			}
			return err
		}
		s.wg.Add(1)
		go func() {
			defer s.wg.Done()
			s.handle(ctx, conn)
		}()
	}
}

func (s *Server) handle(ctx context.Context, conn net.Conn) {
	defer conn.Close()
	log := s.log.With("remote", conn.RemoteAddr().String())
	rng := rand.New(rand.NewPCG(s.cfg.Seed^s.seq.Add(1), 0x2545F4914F6CDD1D))

	imei, err := codec.ReadIMEI(conn)
	if err != nil {
		log.Warn("handshake failed, closing", "error", err)
		return
	}
	log = log.With("imei", imei)

	if s.reject[imei] {
		s.reg.HandshakeRejected()
		log.Info("handshake rejected")
		conn.Write([]byte{codec.HandshakeReject})
		return
	}
	if _, err := conn.Write([]byte{codec.HandshakeAccept}); err != nil {
		log.Warn("could not accept the handshake", "error", err)
		return
	}
	s.reg.HandshakeOK()
	s.reg.Connected(1)
	s.conns.Add(1)
	log.Info("handshake ok")

	cctx, cancel := context.WithCancel(ctx)
	writes := make(chan []byte, 8)
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		s.writeLoop(cctx, conn, writes, log)
	}()
	if s.cfg.SendCommand != "" && s.cfg.CommandEvery > 0 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			s.commandLoop(cctx, writes, log)
		}()
	}

	err = s.readLoop(cctx, conn, writes, rng, log)

	cancel()
	conn.Close()
	wg.Wait()
	s.reg.Connected(-1)
	s.conns.Add(-1)
	s.reg.Disconnect()
	if err != nil {
		log.Info("connection closed", "reason", err)
		return
	}
	log.Info("connection closed")
}

func (s *Server) writeLoop(ctx context.Context, conn net.Conn, in <-chan []byte, log *slog.Logger) {
	for {
		select {
		case <-ctx.Done():
			return
		case buf := <-in:
			if err := conn.SetWriteDeadline(time.Now().Add(30 * time.Second)); err != nil {
				return
			}
			if s.cfg.Verbose {
				log.Debug("write", "bytes", len(buf), "hex", hex.EncodeToString(buf))
			}
			if _, err := conn.Write(buf); err != nil {
				log.Warn("write failed", "error", err)
				return
			}
		}
	}
}

func (s *Server) commandLoop(ctx context.Context, writes chan []byte, log *slog.Logger) {
	ticker := time.NewTicker(s.cfg.CommandEvery)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			frame, err := codec.EncodeCommand(s.cfg.SendCommand)
			if err != nil {
				log.Error("could not encode the command", "command", s.cfg.SendCommand, "error", err)
				return
			}
			select {
			case writes <- frame:
				s.reg.CommandSent()
				log.Info("command sent", "command", s.cfg.SendCommand)
			case <-ctx.Done():
				return
			default:
				log.Warn("write queue full, skipped a command", "command", s.cfg.SendCommand)
			}
		}
	}
}

func (s *Server) readLoop(ctx context.Context, conn net.Conn, writes chan []byte, rng *rand.Rand, log *slog.Logger) error {
	reader := bufio.NewReader(conn)
	for ctx.Err() == nil {
		frame, err := codec.ReadFrame(reader)
		if err != nil {
			return err
		}
		s.reg.PacketReceived(len(frame))
		if s.cfg.Verbose {
			log.Debug("read", "bytes", len(frame), "hex", hex.EncodeToString(frame))
		}

		p, err := codec.Decode(frame)
		if err != nil {
			if errors.Is(err, codec.ErrBadCRC) {
				s.reg.PacketBadCRC()
				log.Warn("bad crc, not acking", "bytes", len(frame), "error", err)
				continue
			}
			return fmt.Errorf("undecodable packet: %w", err)
		}

		if p.Command != nil {
			s.reg.CommandReceived()
			log.Info("codec 12 message", "type", p.Command.TypeName(), "text", p.Command.Text)
			continue
		}

		for _, r := range p.Records {
			log.Info("record",
				"time", r.Timestamp.Format(time.RFC3339Nano),
				"lat", fmt.Sprintf("%.7f", r.GPS.Latitude),
				"lon", fmt.Sprintf("%.7f", r.GPS.Longitude),
				"speed", r.GPS.Speed,
				"satellites", r.GPS.Satellites,
				"priority", r.Priority,
				"event", r.IO.EventID,
				"io", len(r.IO.Elements))
		}
		s.reg.RecordsReceived(len(p.Records))

		if s.cfg.ACKDelay > 0 {
			select {
			case <-time.After(s.cfg.ACKDelay):
			case <-ctx.Done():
				return ctx.Err()
			}
		}

		count := uint32(len(p.Records))
		wrong := s.cfg.ACKWrongRate > 0 && rng.Float64() < s.cfg.ACKWrongRate
		if wrong {
			count++
			log.Warn("acking the wrong record count on purpose", "records", len(p.Records), "ack", count)
		}
		select {
		case writes <- codec.EncodeACK(count):
			s.reg.ACKSent(wrong)
		case <-ctx.Done():
			return ctx.Err()
		default:
			return errors.New("write queue full, dropping the connection")
		}

		if rate := s.cfg.DisconnectRate; rate > 0 && rng.Float64() < rate {
			return errors.New("dropped the connection on purpose")
		}
	}
	return ctx.Err()
}
