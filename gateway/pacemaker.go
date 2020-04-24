package gateway

import (
	"sync"
	"sync/atomic"
	"time"

	"github.com/diamondburned/arikawa/utils/wsutil"
	"github.com/pkg/errors"
)

var ErrDead = errors.New("no heartbeat replied")

// Time is a UnixNano timestamp.
type Time = int64

type Pacemaker struct {
	// Heartrate is the received duration between heartbeats.
	Heartrate time.Duration

	// Time in nanoseconds, guarded by atomic read/writes.
	SentBeat Time
	EchoBeat Time

	// Any callback that returns an error will stop the pacer.
	Pace func() error

	stop  chan struct{}
	death chan error
}

func (p *Pacemaker) Echo() {
	// Swap our received heartbeats
	// p.LastBeat[0], p.LastBeat[1] = time.Now(), p.LastBeat[0]
	atomic.StoreInt64(&p.EchoBeat, time.Now().UnixNano())
}

// Dead, if true, will have Pace return an ErrDead.
func (p *Pacemaker) Dead() bool {
	/* Deprecated
	if p.LastBeat[0].IsZero() || p.LastBeat[1].IsZero() {
		return false
	}

	return p.LastBeat[0].Sub(p.LastBeat[1]) > p.Heartrate*2
	*/

	var (
		echo = atomic.LoadInt64(&p.EchoBeat)
		sent = atomic.LoadInt64(&p.SentBeat)
	)

	if echo == 0 || sent == 0 {
		return false
	}

	return sent-echo > int64(p.Heartrate)*2
}

func (p *Pacemaker) Stop() {
	if p.stop != nil {
		p.stop <- struct{}{}
		wsutil.WSDebug("(*Pacemaker).stop was sent a stop signal.")
	} else {
		wsutil.WSDebug("(*Pacemaker).stop is nil, skipping.")
	}
}

func (p *Pacemaker) start() error {
	tick := time.NewTicker(p.Heartrate)
	defer tick.Stop()

	// Echo at least once
	p.Echo()

	for {
		wsutil.WSDebug("Pacemaker loop restarted.")

		if err := p.Pace(); err != nil {
			return err
		}

		wsutil.WSDebug("Paced.")

		// Paced, save:
		atomic.StoreInt64(&p.SentBeat, time.Now().UnixNano())

		if p.Dead() {
			return ErrDead
		}

		select {
		case <-p.stop:
			wsutil.WSDebug("Received stop signal.")
			return nil

		case <-tick.C:
			wsutil.WSDebug("Ticked. Restarting.")
		}
	}
}

// StartAsync starts the pacemaker asynchronously. The WaitGroup is optional.
func (p *Pacemaker) StartAsync(wg *sync.WaitGroup) (death chan error) {
	p.death = make(chan error)
	p.stop = make(chan struct{})

	wg.Add(1)

	go func() {
		p.death <- p.start()
		// Debug.
		wsutil.WSDebug("Pacemaker returned.")
		// Mark the stop channel as nil, so later Close() calls won't block forever.
		p.stop = nil
		// Mark the pacemaker loop as done.
		wg.Done()
	}()

	return p.death
}
