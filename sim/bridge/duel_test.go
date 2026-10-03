package bridge

import (
	"encoding/json"
	"reflect"
	"testing"
)

func mixedSpec(seed int64) *Spec {
	return &Spec{
		V:     1,
		Seed:  seedp(seed),
		Seats: [2]Seat{{Name: "Ana", Deck: mixedDeck()}, {Name: "Bo", Deck: mixedDeck()}},
	}
}

// A whole duel, from the shuffle to a winner, driven only through the bridge.
// Run under -race by the step's done-test.
func TestFullDuelThroughTheBridge(t *testing.T) {
	h := start(t, mixedSpec(7))
	p := newPlayer(h)
	states := p.playOut(3000)

	result := h.d.Result()
	if !result.Finished || result.Winner == 0 || result.Reason == "" {
		t.Fatalf("expected a finished duel with a winner and a reason, got %+v", result)
	}

	t.Logf("seed 7: %d inputs, %d turns, seat %d won by %s", len(h.d.Inputs()), result.Turns, result.Winner, result.Reason)

	if len(states) < 10 {
		t.Fatalf("expected a real duel, got %d inputs", len(states))
	}
}

// The same spec driven the same way gives the same duel, input for input, for
// several seeds; and replaying the recorded inputs all at once (queued, not
// one by one) reaches the identical final state.
func TestSeededDuelsReplayExactly(t *testing.T) {
	for _, seed := range []int64{1, 42, 20261004} {
		first := start(t, mixedSpec(seed))
		firstStates := newPlayer(first).playOut(3000)

		second := start(t, mixedSpec(seed))
		secondStates := newPlayer(second).playOut(3000)

		if !reflect.DeepEqual(firstStates, secondStates) {
			for i := range firstStates {
				if i >= len(secondStates) || firstStates[i] != secondStates[i] {
					t.Fatalf("seed %d: the duels differ after input %d", seed, i)
				}
			}
			t.Fatalf("seed %d: the second duel ran longer (%d against %d inputs)", seed, len(secondStates), len(firstStates))
		}

		if first.d.Result() != second.d.Result() {
			t.Fatalf("seed %d: different results %+v and %+v", seed, first.d.Result(), second.d.Result())
		}

		replay := start(t, mixedSpec(seed))
		for _, in := range first.d.Inputs() {
			if err := replay.d.Send(in.Seat, in.Msg); err != nil {
				t.Fatalf("seed %d: replaying: %v", seed, err)
			}
		}
		replay.settle()

		if got, want := debugJSON(replay.d), firstStates[len(firstStates)-1]; got != want {
			t.Fatalf("seed %d: the queued replay ended somewhere else", seed)
		}
		if replay.d.Result() != first.d.Result() {
			t.Fatalf("seed %d: the queued replay has result %+v, want %+v", seed, replay.d.Result(), first.d.Result())
		}

		t.Logf("seed %d: %d inputs, result %+v", seed, len(firstStates)-1, first.d.Result())
	}
}

// Extended state changes what is reported, never what happens: computing
// current power for every state update must not prompt or change the duel.
func TestExtendedStateDoesNotChangeTheDuel(t *testing.T) {
	plain := start(t, mixedSpec(42))
	plainStates := newPlayer(plain).playOut(3000)

	spec := mixedSpec(42)
	spec.ExtendedState = true
	extended := start(t, spec)
	extendedStates := newPlayer(extended).playOut(3000)

	if !reflect.DeepEqual(plainStates, extendedStates) {
		t.Fatal("the duel played differently with extended state on")
	}

	sawPower := false
	for _, msg := range extended.messages(1, 0, "state_update") {
		raw, _ := json.Marshal(msg)
		if json.Valid(raw) && containsKey(msg, "power") {
			sawPower = true
			break
		}
	}
	if !sawPower {
		t.Fatal("expected extended state updates to carry power")
	}

	for _, msg := range plain.messages(1, 0, "state_update") {
		if containsKey(msg, "power") || containsKey(msg, "cost") {
			t.Fatal("a standard duel must not report power or cost")
		}
	}
}

func containsKey(v any, key string) bool {
	switch value := v.(type) {
	case map[string]any:
		if _, ok := value[key]; ok {
			return true
		}
		for _, child := range value {
			if containsKey(child, key) {
				return true
			}
		}
	case []any:
		for _, child := range value {
			if containsKey(child, key) {
				return true
			}
		}
	}
	return false
}
