package device

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/astianmuchui/teltonika-sim/internal/codec"
	"github.com/astianmuchui/teltonika-sim/internal/route"
)

const UnknownCommand = "Unknown command"

func (d *Device) answer(ctx context.Context, cmd *codec.Command, writes chan []byte) error {
	d.reg.CommandReceived()
	d.log.Info("command received", "type", cmd.TypeName(), "text", cmd.Text)
	if !cmd.IsRequest() {
		return nil
	}
	reply := d.Respond(cmd.Text)
	frame, err := codec.EncodeResponse(reply)
	if err != nil {
		return err
	}
	if err := d.enqueue(ctx, writes, frame); err != nil {
		return err
	}
	d.reg.CommandAnswered()
	d.log.Info("command answered", "text", cmd.Text, "reply", reply)
	return nil
}

func (d *Device) Respond(text string) string {
	fields := strings.Fields(strings.TrimSpace(text))
	if len(fields) == 0 {
		return UnknownCommand
	}
	d.mu.Lock()
	fix := d.lastFix
	elements := d.lastIO
	d.mu.Unlock()
	now := d.now().UTC()

	switch strings.ToLower(fields[0]) {
	case "getinfo":
		return d.getinfo(now, fix)
	case "getver":
		return d.getver(now)
	case "getgps":
		return getgps(now, fix)
	case "getio":
		return getio(elements)
	case "setdigout":
		if len(fields) < 2 {
			return UnknownCommand
		}
		return setdigout(fields[1])
	}
	return UnknownCommand
}

func (d *Device) getinfo(now time.Time, fix route.Fix) string {
	gnss := 0
	if fix.Valid {
		gnss = 1
	}
	return fmt.Sprintf("INI:%s RTC:%s RST:1 ERR:0 SR:%d BR:0 CF:0 CU:0 UT:0 SMS:0 NOGPS:0:00 GPS:%d SAT:%d RS:3 MD:3 DB:0",
		now.Format("2006/1/2 15:4"), now.Format("2006/1/2 15:4"), d.sessions, gnss, fix.Satellites)
}

func (d *Device) getver(now time.Time) string {
	return fmt.Sprintf("Ver:03.27.07_Rev:00 GPS:AXN_5.1.9 Hw:%s Mod:11 IMEI:%s Init:%s Uptime:%s",
		d.cfg.Model, d.cfg.IMEI, now.Format("2006-1-2"), time.Since(startedAt).Round(time.Second))
}

func getgps(now time.Time, fix route.Fix) string {
	valid := 0
	if fix.Valid {
		valid = 1
	}
	return fmt.Sprintf("GPS:%d Sat:%d Lat:%.6f Long:%.6f Alt:%.0f Speed:%.0f Dir:%.0f Date:%s Time:%s",
		valid, fix.Satellites, fix.Latitude, fix.Longitude, fix.Altitude, fix.SpeedKPH, fix.Bearing,
		now.Format("2006/1/2"), now.Format("15:4:5"))
}

func getio(elements []codec.IOElement) string {
	if len(elements) == 0 {
		return "no io elements sampled yet"
	}
	parts := make([]string, 0, len(elements))
	for _, e := range elements {
		if e.Variable {
			parts = append(parts, fmt.Sprintf("%d:%dB", e.ID, len(e.Value)))
			continue
		}
		parts = append(parts, fmt.Sprintf("%d:%d", e.ID, e.Uint()))
	}
	return strings.Join(parts, " ")
}

func setdigout(arg string) string {
	if arg == "" {
		return UnknownCommand
	}
	parts := make([]string, 0, len(arg))
	for i := 0; i < len(arg); i++ {
		if arg[i] != '0' && arg[i] != '1' {
			return UnknownCommand
		}
		parts = append(parts, fmt.Sprintf("DOUT%d:%c", i+1, arg[i]))
	}
	return strings.Join(parts, " ")
}

var startedAt = time.Now()
