package checker

import "testing"

func TestTrackerThreshold(t *testing.T) {
	type step struct {
		up        bool
		wantState State
		want      Transition
	}
	tests := []struct {
		name      string
		threshold int
		steps     []step
	}{
		{
			name:      "startup success is silent",
			threshold: 3,
			steps:     []step{{true, StateUp, NoChange}},
		},
		{
			name:      "flap below threshold never alerts",
			threshold: 3,
			steps: []step{
				{true, StateUp, NoChange},
				{false, StateUp, NoChange},
				{false, StateUp, NoChange},
				{true, StateUp, NoChange}, // counter resets
				{false, StateUp, NoChange},
				{false, StateUp, NoChange},
			},
		},
		{
			name:      "down after threshold then recover",
			threshold: 3,
			steps: []step{
				{true, StateUp, NoChange},
				{false, StateUp, NoChange},
				{false, StateUp, NoChange},
				{false, StateDown, WentDown},
				{false, StateDown, NoChange}, // no repeated alerts while down
				{true, StateUp, Recovered},
			},
		},
		{
			name:      "down from unknown at startup",
			threshold: 2,
			steps: []step{
				{false, StateUnknown, NoChange},
				{false, StateDown, WentDown},
			},
		},
		{
			name:      "threshold one alerts immediately",
			threshold: 1,
			steps: []step{
				{false, StateDown, WentDown},
				{true, StateUp, Recovered},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tr := newTracker(tt.threshold)
			for i, s := range tt.steps {
				got := tr.observe(s.up)
				if got != s.want || tr.state != s.wantState {
					t.Fatalf("step %d (up=%v): transition=%v state=%s, want %v %s",
						i, s.up, got, tr.state, s.want, s.wantState)
				}
			}
		})
	}
}

func TestTrackerCertWarning(t *testing.T) {
	tr := newTracker(1)
	const warnDays = 14

	if tr.observeCert(0, false, warnDays) {
		t.Error("no cert must never warn")
	}
	if tr.observeCert(30, true, warnDays) {
		t.Error("30 days left should not warn")
	}
	if !tr.observeCert(10, true, warnDays) {
		t.Error("entering the window should warn")
	}
	if tr.observeCert(9, true, warnDays) {
		t.Error("should warn only once per window")
	}
	if tr.observeCert(90, true, warnDays) {
		t.Error("renewed cert should not warn")
	}
	if !tr.observeCert(14, true, warnDays) {
		t.Error("warning should re-arm after renewal")
	}
}
