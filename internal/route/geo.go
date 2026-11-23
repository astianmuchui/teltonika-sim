package route

import "math"

const earthRadiusM = 6371008.8

func DistanceM(lat1, lon1, lat2, lon2 float64) float64 {
	p1 := lat1 * math.Pi / 180
	p2 := lat2 * math.Pi / 180
	dp := (lat2 - lat1) * math.Pi / 180
	dl := (lon2 - lon1) * math.Pi / 180
	a := math.Sin(dp/2)*math.Sin(dp/2) + math.Cos(p1)*math.Cos(p2)*math.Sin(dl/2)*math.Sin(dl/2)
	return 2 * earthRadiusM * math.Atan2(math.Sqrt(a), math.Sqrt(1-a))
}

func BearingDeg(lat1, lon1, lat2, lon2 float64) float64 {
	p1 := lat1 * math.Pi / 180
	p2 := lat2 * math.Pi / 180
	dl := (lon2 - lon1) * math.Pi / 180
	y := math.Sin(dl) * math.Cos(p2)
	x := math.Cos(p1)*math.Sin(p2) - math.Sin(p1)*math.Cos(p2)*math.Cos(dl)
	return normalizeDeg(math.Atan2(y, x) * 180 / math.Pi)
}

func Destination(lat, lon, bearing, distanceM float64) (float64, float64) {
	if distanceM == 0 {
		return lat, lon
	}
	d := distanceM / earthRadiusM
	b := bearing * math.Pi / 180
	p1 := lat * math.Pi / 180
	l1 := lon * math.Pi / 180
	p2 := math.Asin(math.Sin(p1)*math.Cos(d) + math.Cos(p1)*math.Sin(d)*math.Cos(b))
	l2 := l1 + math.Atan2(math.Sin(b)*math.Sin(d)*math.Cos(p1), math.Cos(d)-math.Sin(p1)*math.Sin(p2))
	return p2 * 180 / math.Pi, math.Mod(l2*180/math.Pi+540, 360) - 180
}

func normalizeDeg(deg float64) float64 {
	deg = math.Mod(deg, 360)
	if deg < 0 {
		deg += 360
	}
	return deg
}

func angleDelta(from, to float64) float64 {
	d := math.Mod(to-from, 360)
	if d <= -180 {
		d += 360
	}
	if d > 180 {
		d -= 360
	}
	return d
}

func clamp(v, lo, hi float64) float64 {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}
