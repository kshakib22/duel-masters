package bridge

import (
	"duel-masters/game/match"
	"encoding/json"
	"errors"
	"reflect"
	"sort"
	"testing"
)

// shape is a debug state with only what a restarted duel has to match. Card ids
// are dealt out afresh by every duel, so each id is blanked and the cards under
// an evolution creature are named by their card instead; the hidden zone has no
// order of its own, so it is sorted. With strict off it also leaves out what a
// restart is documented not to carry: the step, and the charge flags.
func shape(ds DebugState, strict bool) string {
	uid := map[string]string{}
	for _, seat := range ds.Seats {
		for _, zone := range [][]DebugCard{seat.Deck, seat.Hand, seat.Mana, seat.Shields, seat.Battle, seat.Graveyard, seat.Hidden} {
			for _, card := range zone {
				uid[card.ID] = card.UID
			}
		}
	}

	clean := func(cards []DebugCard, sorted bool) []DebugCard {
		result := make([]DebugCard, len(cards))
		for i, card := range cards {
			card.ID = ""
			if len(card.Under) > 0 {
				under := make([]string, len(card.Under))
				for j, id := range card.Under {
					under[j] = uid[id]
				}
				card.Under = under
			}
			result[i] = card
		}
		if sorted {
			sort.Slice(result, func(i, j int) bool { return result[i].UID < result[j].UID })
		}
		return result
	}

	for i, seat := range ds.Seats {
		seat.Deck = clean(seat.Deck, false)
		seat.Hand = clean(seat.Hand, false)
		seat.Mana = clean(seat.Mana, false)
		seat.Shields = clean(seat.Shields, false)
		seat.Battle = clean(seat.Battle, false)
		seat.Graveyard = clean(seat.Graveyard, false)
		seat.Hidden = clean(seat.Hidden, true)
		if !strict {
			seat.HasChargedMana = false
			seat.CanChargeMana = false
		}
		ds.Seats[i] = seat
	}

	if !strict {
		ds.Step = ""
	}

	data, _ := json.MarshalIndent(ds, "", " ")
	return string(data)
}

// rebuild reads the duel's position, sends it through JSON the way the browser
// export does, and starts a new duel from it with the same seed and rules.
func rebuild(t *testing.T, h *harness, spec *Spec) (*harness, *match.BoardSetup) {
	t.Helper()

	position, err := h.d.Position()
	if err != nil {
		t.Fatalf("Position: %v", err)
	}

	data, err := json.Marshal(position)
	if err != nil {
		t.Fatal(err)
	}
	var back match.BoardSetup
	if err := json.Unmarshal(data, &back); err != nil {
		t.Fatal(err)
	}

	again := start(t, &Spec{V: 1, Seed: spec.Seed, Rules: spec.Rules, Seats: spec.Seats, Setup: &back})
	if problems := again.d.SetupProblems(); len(problems) != 0 {
		t.Fatalf("the position could not be placed: %v", problems)
	}
	if unknown := again.d.Report().UnknownCards; len(unknown) != 0 {
		t.Fatalf("the position holds cards the engine can't play: %v", unknown)
	}
	return again, position
}

// sameDuel checks the restarted duel's debug state against the original's.
func sameDuel(t *testing.T, was, now *harness, strict bool) {
	t.Helper()
	if a, b := shape(was.d.DebugState(), strict), shape(now.d.DebugState(), strict); a != b {
		t.Fatalf("the restarted duel differs from the original\noriginal:\n%s\nrestarted:\n%s", a, b)
	}
}

// Case: an evolution creature with its base.
func TestPositionEvolutionWithItsBase(t *testing.T) {
	spec := setupSpec(func(s *match.BoardSetup) {
		s.Seats[0].Battle = []match.SetupCard{{ID: valdios, Bases: []string{vorg}}, q(vorg)}
	})
	h := start(t, spec)

	again, position := rebuild(t, h, spec)

	battle := position.Seats[0].Battle
	if len(battle) != 2 || battle[0].ID != valdios || !reflect.DeepEqual(battle[0].Bases, []string{vorg}) || len(battle[1].Bases) != 0 {
		t.Fatalf("expected Valdios on one Vorg and a lone Vorg, got %+v", battle)
	}

	sameDuel(t, h, again, true)

	evo := battleCard(t, again, 1, valdios)
	if len(evo.Under) != 1 || len(again.d.DebugState().Seats[0].Hidden) != 1 {
		t.Fatalf("the restarted Valdios should sit on one Vorg, got %+v", evo)
	}
}

// Case: a nested pile (an evolution on an evolution) is flattened, and no card
// is lost on the way. There is no card pair in the engine's data that builds
// one by play within a test setup, so the pile is built directly while the
// engine is idle, the way the other tests in this package do.
func TestPositionFlattensANestedPile(t *testing.T) {
	spec := setupSpec(func(s *match.BoardSetup) {
		s.Seats[0].Battle = []match.SetupCard{{ID: valdios, Bases: []string{vorg}}}
	})
	h := start(t, spec)

	player := h.d.players[0].Player
	extra, err := player.SpawnCard(valdios, match.HIDDENZONE)
	if err != nil {
		t.Fatal(err)
	}
	battle, _ := player.Container(match.BATTLEZONE)
	top := battle[0]
	lower := top.Attachments()[0]
	top.ClearAttachments()
	extra.Attach(lower)
	top.Attach(extra)

	position, err := h.d.Position()
	if err != nil {
		t.Fatalf("Position: %v", err)
	}
	if got := position.Seats[0].Battle[0].Bases; !reflect.DeepEqual(got, []string{valdios, vorg}) {
		t.Fatalf("expected the lower Valdios then its Vorg as bases, got %v", got)
	}
}

// Case: a tapped mana card. Mana of three kinds (a creature, a spell, a
// two-civilization card), tapped and not, because the restart gives every card
// the conditions the untap step gives it.
func TestPositionTappedMana(t *testing.T) {
	spec := setupSpec(func(s *match.BoardSetup) {
		s.Seats[0].Mana = []match.SetupCard{q(vorg), {ID: holyAwe, Tapped: true}, {ID: buzzBetocchi, Tapped: true}, q(szubsKin), {ID: vorg, Tapped: true}}
		s.Seats[1].Mana = []match.SetupCard{{ID: burningMane, Tapped: true}}
	})
	h := start(t, spec)

	again, position := rebuild(t, h, spec)

	want := []match.SetupCard{q(vorg), {ID: holyAwe, Tapped: true}, {ID: buzzBetocchi, Tapped: true}, q(szubsKin), {ID: vorg, Tapped: true}}
	if !reflect.DeepEqual(position.Seats[0].Mana, want) {
		t.Fatalf("mana not read as placed: %+v", position.Seats[0].Mana)
	}

	sameDuel(t, h, again, true)

	mana := again.d.DebugState().Seats[0].Mana
	if mana[0].Tapped || !mana[1].Tapped || !mana[2].Tapped || mana[3].Tapped || !mana[4].Tapped {
		t.Fatalf("tapped states lost in the restart: %+v", mana)
	}
}

// Case: a face-up shield.
func TestPositionFaceUpShield(t *testing.T) {
	spec := setupSpec(func(s *match.BoardSetup) {
		s.Seats[0].Shields = []match.SetupCard{q(vorg), {ID: holyAwe, FaceUp: true}, q(szubsKin)}
		s.Seats[1].Shields = []match.SetupCard{{ID: vorg, FaceUp: true}}
	})
	h := start(t, spec)

	again, position := rebuild(t, h, spec)

	want := []match.SetupCard{q(vorg), {ID: holyAwe, FaceUp: true}, q(szubsKin)}
	if !reflect.DeepEqual(position.Seats[0].Shields, want) {
		t.Fatalf("shields not read as placed: %+v", position.Seats[0].Shields)
	}

	sameDuel(t, h, again, true)

	shields := again.d.DebugState().Seats[0].Shields
	if shields[0].FaceUp || !shields[1].FaceUp || shields[2].FaceUp {
		t.Fatalf("face-up states lost in the restart: %+v", shields)
	}
}

// Case: a summoning-sick creature, placed by setup.
func TestPositionSummoningSickCreature(t *testing.T) {
	spec := setupSpec(func(s *match.BoardSetup) {
		s.Seats[0].Battle = []match.SetupCard{{ID: vorg, Sick: true}, q(burningMane)}
	})
	h := start(t, spec)

	again, position := rebuild(t, h, spec)

	battle := position.Seats[0].Battle
	if !battle[0].Sick || battle[1].Sick {
		t.Fatalf("summoning sickness not read as placed: %+v", battle)
	}

	sameDuel(t, h, again, true)

	// The restarted creature really can't attack, and the other one can.
	ds := again.d.DebugState()
	again.send(1, map[string]any{"header": "attack_player", "virtualId": ds.Seats[0].Battle[0].ID})
	if warn, _ := again.last(1, "warn"); warn == nil {
		t.Fatal("the restarted sick creature was allowed to attack")
	}
}

// The same four cases, reached by playing instead of by setup: a creature that
// is sick because it was just summoned (with the mana that paid for it tapped),
// and an evolution made by playing it onto a creature.
func TestPositionAfterRealPlay(t *testing.T) {
	spec := setupSpec(func(s *match.BoardSetup) {
		s.Seats[0].Hand = []string{vorg, valdios}
		s.Seats[0].Mana = []match.SetupCard{q(vorg), q(vorg), q(vorg), q(vorg), q(vorg), q(vorg)}
		s.Seats[0].Battle = []match.SetupCard{q(vorg)}
	})
	h := start(t, spec)

	play := func(card string, mana int) {
		t.Helper()
		ds := h.d.DebugState()
		var id string
		for _, c := range ds.Seats[0].Hand {
			if c.UID == card {
				id = c.ID
			}
		}
		h.send(1, map[string]any{"header": "add_to_playzone", "virtualId": id})

		var pay []string
		for _, c := range h.d.DebugState().Seats[0].Mana {
			if !c.Tapped && len(pay) < mana {
				pay = append(pay, c.ID)
			}
		}
		h.send(1, map[string]any{"header": "action", "cards": pay})
	}

	play(vorg, 2)

	after := h.d.DebugState().Seats[0]
	if len(after.Battle) != 2 || after.Battle[1].UID != vorg {
		t.Fatalf("expected the new Vorg in the battle zone, got %+v", after.Battle)
	}

	again, position := rebuild(t, h, spec)
	if !position.Seats[0].Battle[1].Sick || position.Seats[0].Battle[0].Sick {
		t.Fatalf("only the summoned Vorg should be sick: %+v", position.Seats[0].Battle)
	}
	tapped := 0
	for _, c := range position.Seats[0].Mana {
		if c.Tapped {
			tapped++
		}
	}
	if tapped != 2 {
		t.Fatalf("the two mana that paid should be tapped, got %d", tapped)
	}
	sameDuel(t, h, again, false)

	// Now evolve the first Vorg.
	play(valdios, 4)
	if prompt := h.d.DebugState().Prompt; prompt != 0 {
		ds := h.d.DebugState()
		h.send(prompt, map[string]any{"header": "action", "cards": []string{ds.Seats[0].Battle[0].ID}})
	}

	ds := h.d.DebugState().Seats[0]
	evo := battleCard(t, h, 1, valdios)
	if len(evo.Under) != 1 || len(ds.Hidden) != 1 {
		t.Fatalf("expected Valdios on a Vorg after playing it, got %+v", evo)
	}

	again, position = rebuild(t, h, spec)
	var found bool
	for _, entry := range position.Seats[0].Battle {
		if entry.ID == valdios {
			found = true
			if !reflect.DeepEqual(entry.Bases, []string{vorg}) || entry.Sick {
				t.Fatalf("an evolution made by play should hold its Vorg and not be sick: %+v", entry)
			}
		}
	}
	if !found {
		t.Fatalf("Valdios missing from the position: %+v", position.Seats[0].Battle)
	}
	sameDuel(t, h, again, false)
}

// Every zone of both seats, a turn that is not the first and not seat 1's:
// the position is the very setup it was started from.
func TestPositionOfAWholeBoard(t *testing.T) {
	spec := setupSpec(func(s *match.BoardSetup) {
		s.Turn = 2
		s.TurnNumber = 8
		s.Seats[0].Deck = []string{szubsKin, vorg, holyAwe, burningMane}
		s.Seats[0].Hand = []string{szubsKin, holyAwe}
		s.Seats[0].Mana = []match.SetupCard{q(vorg), {ID: vorg, Tapped: true}}
		s.Seats[0].Shields = []match.SetupCard{q(vorg), {ID: holyAwe, FaceUp: true}}
		s.Seats[0].Graveyard = []string{burningMane, vorg}
		s.Seats[0].Battle = []match.SetupCard{{ID: valdios, Tapped: true, Bases: []string{vorg}}, {ID: elfX, Sick: true}, q(szubsKin)}
		s.Seats[1].Deck = []string{vorg, holyAwe, szubsKin}
		s.Seats[1].Hand = []string{vorg}
		s.Seats[1].Battle = []match.SetupCard{q(bronzeArm), {ID: meteosaur, Tapped: true}}
	})
	h := start(t, spec)

	again, position := rebuild(t, h, spec)

	if position.Turn != 2 || position.TurnNumber != 8 || position.BeginAt != match.BeginAtCharge {
		t.Fatalf("whose turn and which turn not read: %+v", position)
	}
	// Compared as JSON: a zone left unset and a zone read back empty are the
	// same position.
	a, _ := json.MarshalIndent(position.Seats, "", " ")
	b, _ := json.MarshalIndent(spec.Setup.Seats, "", " ")
	if string(a) != string(b) {
		t.Fatalf("the position is not the setup it started from\nposition:\n%s\nsetup:\n%s", a, b)
	}

	sameDuel(t, h, again, true)
	if ds := again.d.DebugState(); ds.Prompt != 0 || ds.Turn != 2 || ds.TurnsPlayed != 8 {
		t.Fatalf("the restart should be at seat 2, turn 8, with nothing asked: %+v", ds)
	}
}

// A seat with no deck left must stay empty when the position goes through JSON.
// A deck left out of the JSON means "keep the deck the seat was given", which
// would put the whole deck back; this failed while SeatSetup.Deck was omitted
// when empty.
func TestPositionOfAnEmptyDeckStaysEmpty(t *testing.T) {
	spec := setupSpec(func(s *match.BoardSetup) {
		s.Seats[0].Deck = []string{}
		s.Seats[0].Hand = []string{vorg}
	})
	spec.Seats[0].Deck = deckOf(vorg, 20)
	// Under the standard rule an empty deck loses the duel on the spot.
	spec.Rules.DeckOut = match.DeckOutOff
	h := start(t, spec)

	if n := len(h.d.DebugState().Seats[0].Deck); n != 0 {
		t.Fatalf("the original should have an empty deck, has %d", n)
	}

	again, position := rebuild(t, h, spec)

	if position.Seats[0].Deck == nil || len(position.Seats[0].Deck) != 0 {
		t.Fatalf("an empty deck should be an empty list, not %#v", position.Seats[0].Deck)
	}
	if n := len(again.d.DebugState().Seats[0].Deck); n != 0 {
		t.Fatalf("the restart refilled the deck with %d cards", n)
	}
	sameDuel(t, h, again, true)
}

// Position at every point of a whole seeded duel where nothing is being asked:
// each time, a duel restarted from it has the same cards in the same places
// and the same whose turn and which turn, and it plays on.
func TestPositionRoundTripsThroughAWholeDuel(t *testing.T) {
	spec := mixedSpec(7)
	h := start(t, spec)
	p := newPlayer(h)

	checked := 0
	for {
		ds := h.d.DebugState()
		if ds.Ended {
			break
		}
		if ds.Prompt == 0 {
			again, position := rebuild(t, h, spec)
			if int(position.Turn) != ds.Turn || position.TurnNumber != ds.TurnsPlayed {
				t.Fatalf("input %d: position says seat %d turn %d, the duel is at seat %d turn %d", p.inputs, position.Turn, position.TurnNumber, ds.Turn, ds.TurnsPlayed)
			}
			sameDuel(t, h, again, false)
			checked++
			again.d.Close()
		}
		if !p.step() {
			break
		}
		if p.inputs > 3000 {
			t.Fatal("the duel did not end")
		}
	}

	if checked < 40 {
		t.Fatalf("only %d positions were checked, expected a whole duel's worth", checked)
	}
	t.Logf("%d positions round-tripped over %d inputs", checked, p.inputs)
}

// Position only reads.
func TestPositionDoesNotChangeTheDuel(t *testing.T) {
	spec := mixedSpec(3)
	h := start(t, spec)
	p := newPlayer(h)
	for range 12 {
		p.step()
	}
	for h.d.DebugState().Prompt != 0 {
		p.step()
	}

	before := debugJSON(h.d)
	inputs := len(h.d.Inputs())

	first, err := h.d.Position()
	if err != nil {
		t.Fatal(err)
	}
	second, err := h.d.Position()
	if err != nil {
		t.Fatal(err)
	}

	if !reflect.DeepEqual(first, second) {
		t.Fatal("two reads of the same moment gave different positions")
	}
	if debugJSON(h.d) != before || len(h.d.Inputs()) != inputs {
		t.Fatal("reading the position changed the duel")
	}
}

// When the position can't be read, it says why, and returns nothing.
func TestPositionRefusals(t *testing.T) {
	// Meteosaur summoned by setup asks seat 1 which creature to destroy.
	asking := func() *harness {
		return start(t, setupSpec(func(s *match.BoardSetup) {
			s.Seats[0].Battle = []match.SetupCard{{ID: meteosaur, Enter: match.EnterSummon}}
			s.Seats[1].Battle = []match.SetupCard{q(vorg), q(vorg)}
		}))
	}

	t.Run("mid-answer", func(t *testing.T) {
		h := asking()
		if h.d.DebugState().Prompt != 1 {
			t.Fatal("expected seat 1 to be asked a question")
		}
		if position, err := h.d.Position(); position != nil || !errors.Is(err, ErrMidAnswer) {
			t.Fatalf("expected ErrMidAnswer, got %v, %v", position, err)
		}
	})

	t.Run("not settled", func(t *testing.T) {
		h := asking()
		// Seat 2's answer has to wait for seat 1's, so it stays queued.
		if err := h.d.Send(2, []byte(`{"header":"action","cancel":true}`)); err != nil {
			t.Fatal(err)
		}
		if position, err := h.d.Position(); position != nil || !errors.Is(err, ErrNotSettled) {
			t.Fatalf("expected ErrNotSettled, got %v, %v", position, err)
		}
	})

	t.Run("a card halfway through an effect", func(t *testing.T) {
		h := start(t, setupSpec(func(s *match.BoardSetup) {}))
		if _, err := h.d.players[1].Player.SpawnCard(vorg, match.HIDDENZONE); err != nil {
			t.Fatal(err)
		}
		if position, err := h.d.Position(); position != nil || !errors.Is(err, ErrUnreadable) {
			t.Fatalf("expected ErrUnreadable for a card in the hidden zone under nothing, got %v, %v", position, err)
		}
	})

	t.Run("ended", func(t *testing.T) {
		h := start(t, setupSpec(func(s *match.BoardSetup) {}))
		h.send(1, map[string]any{"header": "resign"})
		if !h.d.Result().Finished {
			t.Fatal("expected the resignation to end the duel")
		}
		if position, err := h.d.Position(); position != nil || !errors.Is(err, ErrEnded) {
			t.Fatalf("expected ErrEnded after a resignation, got %v, %v", position, err)
		}
	})

	t.Run("closed", func(t *testing.T) {
		h := start(t, setupSpec(func(s *match.BoardSetup) {}))
		h.d.Close()
		if position, err := h.d.Position(); position != nil || !errors.Is(err, ErrEnded) {
			t.Fatalf("expected ErrEnded after Close, got %v, %v", position, err)
		}
	})

	// And it is readable again once the question is answered.
	t.Run("readable after the answer", func(t *testing.T) {
		h := asking()
		h.send(1, map[string]any{"header": "action", "cards": []string{h.d.DebugState().Seats[1].Battle[0].ID}})
		if _, err := h.d.Position(); err != nil {
			t.Fatalf("expected a position once the question was answered, got %v", err)
		}
	})
}
