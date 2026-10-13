package checker

// State is the debounced health of a target.
type State string

const (
	StateUnknown State = "unknown" // no verdict yet (startup)
	StateUp      State = "up"
	StateDown    State = "down"
)

// Transition is the alert-worthy change produced by a check result.
type Transition int

const (
	NoChange Transition = iota
	WentDown
	Recovered
)

// tracker turns a stream of raw check results into debounced state changes.
//
// A target is only declared DOWN after `threshold` consecutive failures, which
// prevents a single dropped packet from paging anyone. A single success is
// enough to declare it UP again. The first success after startup moves
// unknown -> up silently: there is nothing to "recover" from.
//
// A tracker is owned by exactly one goroutine and needs no locking.
type tracker struct {
	threshold  int
	state      State
	failures   int
	certWarned bool
}

func newTracker(threshold int) tracker {
	return tracker{threshold: max(threshold, 1), state: StateUnknown}
}

// observe records one check result and reports whether an alert should fire.
func (t *tracker) observe(up bool) Transition {
	if up {
		t.failures = 0
		prev := t.state
		t.state = StateUp
		if prev == StateDown {
			return Recovered
		}
		return NoChange
	}

	t.failures++
	if t.failures >= t.threshold && t.state != StateDown {
		t.state = StateDown
		return WentDown
	}
	return NoChange
}

// observeCert reports true once when the certificate enters the warning
// window, and re-arms when it leaves it again (i.e. after renewal).
func (t *tracker) observeCert(daysLeft int, hasCert bool, warnDays int) bool {
	if !hasCert {
		return false
	}
	if daysLeft > warnDays {
		t.certWarned = false
		return false
	}
	if t.certWarned {
		return false
	}
	t.certWarned = true
	return true
}
