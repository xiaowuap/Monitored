// Package rules implements Monitored's deterministic alert state machine.
// It deliberately contains no LLM hooks: models may explain an alert but can
// never decide whether a rule fires.
package rules

import (
	"errors"
	"fmt"
	"math"
	"strings"
	"time"
)

type State string

const (
	StateOK       State = "OK"
	StatePending  State = "Pending"
	StateFiring   State = "Firing"
	StateResolved State = "Resolved"
	StateSilenced State = "Silenced"
)

type Point struct {
	At    time.Time
	Value *float64
	Text  string
}

type Condition struct {
	Operator string
	Value    float64
	Text     string
	Window   time.Duration
}

type Rule struct {
	Type        string
	Condition   Condition
	Recovery    *Condition
	For         time.Duration
	MissingMode string
	StaleAfter  time.Duration
}

type Evaluation struct {
	State       State
	Previous    State
	Reason      string
	Value       *float64
	Changed     bool
	PendingFrom time.Time
}

type Memory struct {
	State       State
	PendingFrom time.Time
	LastPoint   *Point
}

func Evaluate(now time.Time, rule Rule, points []Point, previous Memory, maintenance bool) (Evaluation, error) {
	if previous.State == "" {
		previous.State = StateOK
	}
	current := latest(points)
	result := Evaluation{State: previous.State, Previous: previous.State}

	missing := current == nil || (rule.StaleAfter > 0 && now.Sub(current.At) > rule.StaleAfter)
	if missing {
		switch rule.MissingMode {
		case "", "ignore":
			result.Reason = "missing data ignored"
			return result, nil
		case "resolve":
			return transition(result, StateResolved, "missing data resolved the alert", nil), nil
		case "alert":
			return advance(now, rule.For, previous, result, true, "required data is missing", nil, maintenance), nil
		default:
			return result, fmt.Errorf("unsupported missing data mode %q", rule.MissingMode)
		}
	}

	matched, value, err := matches(rule.Type, rule.Condition, points, previous.LastPoint)
	if err != nil {
		return result, err
	}
	if matched {
		return advance(now, rule.For, previous, result, true, "firing condition matched", value, maintenance), nil
	}

	recovered := true
	if rule.Recovery != nil {
		recovered, _, err = matches("threshold", *rule.Recovery, points, previous.LastPoint)
		if err != nil {
			return result, err
		}
	}
	if (previous.State == StateFiring || previous.State == StatePending || previous.State == StateSilenced) && recovered {
		return transition(result, StateResolved, "recovery condition matched", value), nil
	}
	if previous.State == StateResolved {
		return transition(result, StateOK, "alert returned to steady state", value), nil
	}
	result.State = StateOK
	result.Changed = result.State != result.Previous
	result.Value = value
	result.Reason = "condition did not match"
	return result, nil
}

func advance(now time.Time, duration time.Duration, previous Memory, result Evaluation, matched bool, reason string, value *float64, maintenance bool) Evaluation {
	if !matched {
		return result
	}
	if maintenance {
		return transition(result, StateSilenced, reason+" during maintenance", value)
	}
	if duration <= 0 || (previous.State == StatePending && !previous.PendingFrom.IsZero() && now.Sub(previous.PendingFrom) >= duration) {
		result.PendingFrom = previous.PendingFrom
		return transition(result, StateFiring, reason, value)
	}
	if previous.State == StateFiring {
		return transition(result, StateFiring, reason, value)
	}
	result = transition(result, StatePending, reason+"; duration not yet met", value)
	if previous.State == StatePending && !previous.PendingFrom.IsZero() {
		result.PendingFrom = previous.PendingFrom
	} else {
		result.PendingFrom = now
	}
	return result
}

func transition(result Evaluation, next State, reason string, value *float64) Evaluation {
	result.State = next
	result.Reason = reason
	result.Value = value
	result.Changed = next != result.Previous
	if next != StatePending {
		result.PendingFrom = time.Time{}
	}
	return result
}

func matches(kind string, condition Condition, points []Point, previous *Point) (bool, *float64, error) {
	current := latest(points)
	if current == nil {
		return false, nil, nil
	}
	switch kind {
	case "threshold":
		if current.Value == nil || math.IsNaN(*current.Value) || math.IsInf(*current.Value, 0) {
			return false, current.Value, nil
		}
		return compare(*current.Value, condition.Operator, condition.Value), current.Value, nil
	case "state", "state_change":
		if kind == "state_change" && previous != nil && previous.Text == current.Text {
			return false, nil, nil
		}
		operator := condition.Operator
		if operator == "" {
			operator = "=="
		}
		switch operator {
		case "==", "=":
			return current.Text == condition.Text, nil, nil
		case "!=":
			return current.Text != condition.Text, nil, nil
		default:
			return false, nil, fmt.Errorf("unsupported text operator %q", operator)
		}
	case "rate", "counter_delta":
		window := condition.Window
		if window <= 0 {
			return false, nil, errors.New("rate and counter rules require a positive window")
		}
		first, last := windowPoints(points, current.At.Add(-window))
		if first == nil || last == nil || first.Value == nil || last.Value == nil || last.At.Equal(first.At) {
			return false, nil, nil
		}
		delta := *last.Value - *first.Value
		if kind == "counter_delta" && delta < 0 {
			// A negative monotonic delta is a reset/wrap. Do not emit a huge
			// synthetic increase without a declared counter width.
			delta = 0
		}
		value := delta
		if kind == "rate" {
			value = delta / last.At.Sub(first.At).Seconds()
		}
		return compare(value, condition.Operator, condition.Value), &value, nil
	default:
		return false, nil, fmt.Errorf("unsupported rule type %q", kind)
	}
}

func compare(value float64, operator string, expected float64) bool {
	switch strings.TrimSpace(operator) {
	case ">":
		return value > expected
	case ">=":
		return value >= expected
	case "<":
		return value < expected
	case "<=":
		return value <= expected
	case "=", "==":
		return value == expected
	case "!=":
		return value != expected
	default:
		return false
	}
}

func latest(points []Point) *Point {
	var result *Point
	for index := range points {
		if result == nil || points[index].At.After(result.At) {
			result = &points[index]
		}
	}
	return result
}

func windowPoints(points []Point, since time.Time) (*Point, *Point) {
	var first, last *Point
	for index := range points {
		point := &points[index]
		if point.At.Before(since) {
			continue
		}
		if first == nil || point.At.Before(first.At) {
			first = point
		}
		if last == nil || point.At.After(last.At) {
			last = point
		}
	}
	return first, last
}
