package device

import (
	"math/rand/v2"
	"time"
)

type backoff struct {
	min     time.Duration
	max     time.Duration
	attempt int
	rng     *rand.Rand
}

func newBackoff(mn, mx time.Duration, rng *rand.Rand) *backoff {
	if mn <= 0 {
		mn = time.Second
	}
	if mx < mn {
		mx = mn
	}
	return &backoff{min: mn, max: mx, rng: rng}
}

func (b *backoff) next() time.Duration {
	d := b.min
	for range b.attempt {
		if d >= b.max/2 {
			d = b.max
			break
		}
		d *= 2
	}
	if d > b.max {
		d = b.max
	}
	b.attempt++
	half := d / 2
	if half <= 0 {
		return d
	}
	return half + time.Duration(b.rng.Int64N(int64(half)+1))
}

func (b *backoff) reset() { b.attempt = 0 }
