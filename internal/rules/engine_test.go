package rules

import (
	"testing"
	"time"
)

func number(value float64) *float64 { return &value }

func TestThresholdDurationAndRecovery(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	rule := Rule{
		Type:      "threshold",
		Condition: Condition{Operator: ">=", Value: 80},
		Recovery:  &Condition{Operator: "<", Value: 75},
		For:       time.Minute,
	}
	pending, err := Evaluate(now, rule, []Point{{At: now, Value: number(85)}}, Memory{State: StateOK}, false)
	if err != nil || pending.State != StatePending {
		t.Fatalf("expected pending, got %#v, %v", pending, err)
	}
	firing, err := Evaluate(now.Add(time.Minute), rule, []Point{{At: now.Add(time.Minute), Value: number(86)}}, Memory{
		State: StatePending, PendingFrom: pending.PendingFrom,
	}, false)
	if err != nil || firing.State != StateFiring {
		t.Fatalf("expected firing, got %#v, %v", firing, err)
	}
	resolved, _ := Evaluate(now.Add(2*time.Minute), rule, []Point{{At: now.Add(2 * time.Minute), Value: number(72)}}, Memory{State: StateFiring}, false)
	if resolved.State != StateResolved {
		t.Fatalf("expected resolved, got %#v", resolved)
	}
}

func TestCounterResetDoesNotFireAndMaintenanceSilences(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	counter := Rule{Type: "counter_delta", Condition: Condition{Operator: ">", Value: 10, Window: time.Minute}}
	result, err := Evaluate(now, counter, []Point{
		{At: now.Add(-time.Minute), Value: number(65530)},
		{At: now, Value: number(4)},
	}, Memory{}, false)
	if err != nil || result.State != StateOK {
		t.Fatalf("counter reset fired: %#v, %v", result, err)
	}
	threshold := Rule{Type: "threshold", Condition: Condition{Operator: ">", Value: 1}}
	result, _ = Evaluate(now, threshold, []Point{{At: now, Value: number(2)}}, Memory{}, true)
	if result.State != StateSilenced {
		t.Fatalf("expected silenced, got %#v", result)
	}
}

func TestMissingDataPolicy(t *testing.T) {
	now := time.Now()
	result, err := Evaluate(now, Rule{MissingMode: "alert", For: 0}, nil, Memory{}, false)
	if err != nil || result.State != StateFiring {
		t.Fatalf("expected missing data alert, got %#v, %v", result, err)
	}
}
