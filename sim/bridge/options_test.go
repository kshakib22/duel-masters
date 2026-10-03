package bridge

import (
	"slices"
	"strings"
	"testing"
)

func vorgSpec(seed int64) *Spec {
	return &Spec{
		V:     1,
		Seed:  seedp(seed),
		Rules: Rules{StartingSeat: seat(1)},
		Seats: [2]Seat{{Deck: deckOf(vorg, 40)}, {Deck: deckOf(vorg, 40)}},
	}
}

func zoneSizes(h *harness, seatIndex int) (deck, hand, mana, shields int) {
	s := h.d.DebugState().Seats[seatIndex]
	return len(s.Deck), len(s.Hand), len(s.Mana), len(s.Shields)
}

// Shields, opening hand and the first player's skipped draw, at shobu's
// defaults: 5 shields, 5 cards, and seat 1 (going first) does not draw.
func TestStandardOpeningIsUnchanged(t *testing.T) {
	h := start(t, vorgSpec(3))

	if ds := h.d.DebugState(); ds.Turn != 1 || ds.TurnsPlayed != 1 || ds.Step != "ChargeStep" {
		t.Fatalf("expected seat 1 in the charge step of turn 1, got %+v", ds)
	}

	for i, want := range [][4]int{{30, 5, 0, 5}, {30, 5, 0, 5}} {
		deck, hand, mana, shields := zoneSizes(h, i)
		if got := [4]int{deck, hand, mana, shields}; got != want {
			t.Fatalf("seat %d: deck, hand, mana, shields = %v, want %v", i+1, got, want)
		}
	}

	// Seat 2 draws on its first turn.
	h.send(1, map[string]any{"header": "end_turn"})
	if _, hand, _, _ := zoneSizes(h, 1); hand != 6 {
		t.Fatalf("seat 2 should draw on its first turn, has %d cards", hand)
	}
}

func TestShieldsHandAndFirstDrawCanChange(t *testing.T) {
	spec := vorgSpec(3)
	spec.Rules.Shields = intp(3)
	spec.Rules.StartingHand = intp(7)
	spec.Rules.FirstPlayerSkipsDraw = boolp(false)
	spec.Seats[1].Shields = intp(8) // a boss with more shields
	h := start(t, spec)

	// Seat 1: 3 shields, 7 cards plus the first-turn draw it no longer skips.
	if deck, hand, _, shields := zoneSizes(h, 0); shields != 3 || hand != 8 || deck != 29 {
		t.Fatalf("seat 1: deck %d, hand %d, shields %d; want 29, 8, 3", deck, hand, shields)
	}
	if deck, hand, _, shields := zoneSizes(h, 1); shields != 8 || hand != 7 || deck != 25 {
		t.Fatalf("seat 2: deck %d, hand %d, shields %d; want 25, 7, 8", deck, hand, shields)
	}
}

func charge(h *harness, seatNumber int) {
	hand := h.d.DebugState().Seats[seatNumber-1].Hand
	h.send(seatNumber, map[string]any{"header": "add_to_manazone", "virtualId": hand[0].ID})
}

func TestChargesPerTurn(t *testing.T) {
	for _, tc := range []struct {
		name    string
		charges *int
		want    int
	}{
		{"standard, one charge", nil, 1},
		{"two charges", intp(2), 2},
		{"no charging", intp(0), 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			spec := vorgSpec(5)
			spec.Seats[0].ChargesPerTurn = tc.charges
			h := start(t, spec)

			for range 3 {
				charge(h, 1)
			}

			if _, _, mana, _ := zoneSizes(h, 0); mana != tc.want {
				t.Fatalf("charged %d cards, want %d", mana, tc.want)
			}

			warnings := h.messages(1, 0, "warn")
			if len(warnings) != 3-tc.want {
				t.Fatalf("expected %d refused charges, got %d", 3-tc.want, len(warnings))
			}
		})
	}
}

func TestDeckOut(t *testing.T) {
	t.Run("a 10 card deck loses at setup", func(t *testing.T) {
		spec := vorgSpec(1)
		spec.Seats[0].Deck = deckOf(vorg, 10)
		h := start(t, spec)

		if r := h.d.Result(); !r.Finished || r.Winner != 2 || r.Reason != "deck_out" {
			t.Fatalf("got %+v", r)
		}
	})

	t.Run("an 11 card deck loses on its first draw", func(t *testing.T) {
		spec := vorgSpec(1)
		spec.Seats[1].Deck = deckOf(vorg, 11)
		h := start(t, spec)

		if r := h.d.Result(); r.Finished {
			t.Fatalf("the duel ended before anyone drew: %+v", r)
		}

		h.send(1, map[string]any{"header": "end_turn"})

		if r := h.d.Result(); !r.Finished || r.Winner != 1 || r.Reason != "deck_out" || r.Turns != 2 {
			t.Fatalf("got %+v", r)
		}
	})

	t.Run("with deck-out off both play on", func(t *testing.T) {
		spec := vorgSpec(1)
		spec.Rules.DeckOut = "off"
		spec.Seats[0].Deck = deckOf(vorg, 10)
		spec.Seats[1].Deck = deckOf(vorg, 11)
		h := start(t, spec)

		for range 4 {
			h.send(h.d.DebugState().Turn, map[string]any{"header": "end_turn"})
		}

		ds := h.d.DebugState()
		if r := h.d.Result(); r.Finished || ds.TurnsPlayed != 5 {
			t.Fatalf("expected the duel to go on to turn 5, got %+v at turn %d", r, ds.TurnsPlayed)
		}
		if len(ds.Seats[0].Deck) != 0 || len(ds.Seats[1].Deck) != 0 {
			t.Fatalf("both decks should be empty, have %d and %d", len(ds.Seats[0].Deck), len(ds.Seats[1].Deck))
		}
		// Seat 1 drew nothing from an empty deck on turn 3 and 5.
		if len(ds.Seats[0].Hand) != 5 {
			t.Fatalf("seat 1 should still hold its 5 cards, has %d", len(ds.Seats[0].Hand))
		}
	})
}

func TestTurnLimit(t *testing.T) {
	spec := vorgSpec(9)
	spec.Rules.TurnLimit = &TurnLimit{Turns: 3, Winner: 2}
	h := start(t, spec)

	for range 3 {
		if h.d.Result().Finished {
			break
		}
		h.send(h.d.DebugState().Turn, map[string]any{"header": "end_turn"})
	}

	if r := h.d.Result(); !r.Finished || r.Winner != 2 || r.Reason != "turn_limit" || r.Turns != 3 {
		t.Fatalf("got %+v", r)
	}
}

func TestNoTurnLimitByDefault(t *testing.T) {
	h := start(t, vorgSpec(9))
	for range 6 {
		h.send(h.d.DebugState().Turn, map[string]any{"header": "end_turn"})
	}
	if r := h.d.Result(); r.Finished {
		t.Fatalf("a standard duel has no turn limit, got %+v", r)
	}
}

func TestSandboxConsole(t *testing.T) {
	for _, on := range []bool{false, true} {
		spec := vorgSpec(2)
		spec.Rules.SandboxConsole = on
		h := start(t, spec)

		h.send(1, map[string]any{"header": "chat", "message": "/add " + szubsKin + " 2"})

		_, hand, _, _ := zoneSizes(h, 0)
		want := 5
		if on {
			want = 7
		}
		if hand != want {
			t.Fatalf("sandbox console %v: hand has %d cards, want %d", on, hand, want)
		}
	}
}

func TestExtendedStateFields(t *testing.T) {
	spec := vorgSpec(4)
	spec.ExtendedState = true
	spec.Seats[0].Deck = deckOf(szubsKin, 40)
	h := start(t, spec)

	hand := list(h.state(1)["me"].(map[string]any)["hand"])
	card := hand[0]
	if card["cost"] != float64(5) || card["power"] != float64(6000) || flags(card)&32 == 0 {
		t.Fatalf("expected Szubs Kin with cost 5, power 6000 and the blocker flag, got %v", card)
	}

	// Seat 2 sees its own Vorgs with power and cost, and never seat 1's hand.
	theirs := h.state(2)
	if n := len(list(theirs["opponent"].(map[string]any)["hand"])); n != 0 {
		t.Fatalf("seat 2 can see %d of seat 1's cards", n)
	}
	if mine := list(theirs["me"].(map[string]any)["hand"]); mine[0]["power"] != float64(2000) || flags(mine[0])&32 != 0 {
		t.Fatalf("expected a non-blocker Vorg at 2000, got %v", mine[0])
	}
}

func TestSeedDecidesTheShuffleAndCardIDs(t *testing.T) {
	a := start(t, mixedSpec(11))
	b := start(t, mixedSpec(11))
	c := start(t, mixedSpec(12))

	if debugJSON(a.d) != debugJSON(b.d) {
		t.Fatal("the same seed gave two different openings")
	}

	da, dc := a.d.DebugState(), c.d.DebugState()
	if slices.EqualFunc(da.Seats[0].Deck, dc.Seats[0].Deck, func(x, y DebugCard) bool { return x.UID == y.UID }) {
		t.Fatal("two seeds gave the same deck order")
	}

	if id := da.Seats[0].Hand[0].ID; !strings.HasPrefix(id, "c") || len(id) > 4 {
		t.Fatalf("expected a short numbered card id, got %q", id)
	}
}

func TestUnseededDuelReportsItsSeed(t *testing.T) {
	spec := mixedSpec(0)
	spec.Seed = nil
	h := start(t, spec)

	report := h.d.Report()
	again := mixedSpec(report.Seed)
	again.Rules.StartingSeat = seat(report.StartingSeat)
	h2 := start(t, again)

	if debugJSON(h.d) != debugJSON(h2.d) {
		t.Fatal("a duel started from the reported seed differs from the original")
	}
}

func TestStartingSeat(t *testing.T) {
	for _, want := range []int{1, 2} {
		spec := vorgSpec(8)
		spec.Rules.StartingSeat = seat(want)
		h := start(t, spec)
		if got := h.d.DebugState().Turn; got != want {
			t.Fatalf("asked seat %d to start, seat %d started", want, got)
		}
	}

	spec := vorgSpec(8)
	spec.Rules.StartingSeat = nil
	h := start(t, spec)
	if got := h.d.DebugState().Turn; got != h.d.Report().StartingSeat {
		t.Fatalf("a random start reported seat %d but seat %d started", h.d.Report().StartingSeat, got)
	}
}

func TestResignReason(t *testing.T) {
	h := start(t, vorgSpec(6))
	h.send(1, map[string]any{"header": "resign"})

	if r := h.d.Result(); !r.Finished || r.Winner != 2 || r.Reason != "resign" {
		t.Fatalf("got %+v", r)
	}

	if err := h.d.Send(1, []byte(`{"header":"end_turn"}`)); err != ErrEnded {
		t.Fatalf("an input after the end should be refused with ErrEnded, got %v", err)
	}
}

func TestUnknownCardsAreReported(t *testing.T) {
	spec := vorgSpec(1)
	spec.Seats[0].Deck = append(deckOf(vorg, 38), wiseStarnoid, "not-a-card")
	h := start(t, spec)

	unknown := h.d.Report().UnknownCards
	if len(unknown) != 2 || unknown[0].ID != wiseStarnoid || unknown[1].ID != "not-a-card" || unknown[0].Seat != 1 {
		t.Fatalf("got %+v", unknown)
	}

	// 38 cards: 5 shields, 5 in hand, 28 left.
	if deck, _, _, _ := zoneSizes(h, 0); deck != 28 {
		t.Fatalf("expected the 2 unknown cards to be left out, deck has %d", deck)
	}
}

func TestEngineCards(t *testing.T) {
	cards := EngineCards()
	for _, id := range []string{jabaha, buzzBetocchi, vorg, valdios} {
		if !slices.Contains(cards, id) {
			t.Fatalf("expected the engine to play %s", id)
		}
	}
	if slices.Contains(cards, wiseStarnoid) {
		t.Fatal("Wise Starnoid is not implemented and must not be listed")
	}
	if !slices.IsSorted(cards) {
		t.Fatal("expected the list sorted")
	}
}

func TestInvalidSpecsAreRefused(t *testing.T) {
	for name, mutate := range map[string]func(*Spec){
		"negative shields":   func(s *Spec) { s.Rules.Shields = intp(-1) },
		"unknown deck-out":   func(s *Spec) { s.Rules.DeckOut = "sometimes" },
		"turn limit of 0":    func(s *Spec) { s.Rules.TurnLimit = &TurnLimit{Turns: 0, Winner: 1} },
		"turn limit winner":  func(s *Spec) { s.Rules.TurnLimit = &TurnLimit{Turns: 3, Winner: 3} },
		"starting seat 3":    func(s *Spec) { s.Rules.StartingSeat = seat(3) },
		"spec version 2":     func(s *Spec) { s.V = 2 },
		"negative seat hand": func(s *Spec) { s.Seats[1].StartingHand = intp(-2) },
	} {
		spec := vorgSpec(1)
		mutate(spec)
		if _, _, err := New(spec, nil); err == nil {
			t.Fatalf("%s: expected the spec to be refused", name)
		}
	}
}

func TestAnswersWithNothingAskedAreRejected(t *testing.T) {
	h := start(t, vorgSpec(1))
	before := h.count(1)
	h.send(1, map[string]any{"header": "action", "cards": []string{}, "cancel": true})

	if errors := h.messages(1, before, "action_error"); len(errors) != 1 {
		t.Fatalf("expected the stray answer to be rejected once, got %d messages", len(errors))
	}

	if err := h.d.Send(1, []byte(`{"header":"action","cards":["c1","c1"]}`)); err == nil {
		t.Fatal("expected an answer naming the same card twice to be refused")
	}
	if err := h.d.Send(3, []byte(`{"header":"end_turn"}`)); err == nil {
		t.Fatal("expected seat 3 to be refused")
	}
	if err := h.d.Send(1, []byte(`{"header":"join_match"}`)); err == nil {
		t.Fatal("expected join_match to be refused")
	}
}
