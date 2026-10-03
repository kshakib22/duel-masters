package bridge

import (
	"duel-masters/game/match"
	"strings"
	"testing"
)

func q(id string) match.SetupCard { return match.SetupCard{ID: id} }

// setupSpec starts at seat 1's charge step on turn 5, with 10 Vorgs left in
// each deck and nothing else, so each test only adds what it needs.
func setupSpec(edit func(s *match.BoardSetup)) *Spec {
	setup := &match.BoardSetup{Turn: 1, TurnNumber: 5, BeginAt: match.BeginAtCharge}
	for i := range setup.Seats {
		setup.Seats[i].Deck = deckOf(vorg, 10)
	}
	edit(setup)
	return &Spec{
		V:     1,
		Seed:  seedp(1),
		Seats: [2]Seat{{Name: "Ana"}, {Name: "Bo"}},
		Setup: setup,
	}
}

func battleCard(t *testing.T, h *harness, seatNumber int, uid string) DebugCard {
	t.Helper()
	for _, card := range h.d.DebugState().Seats[seatNumber-1].Battle {
		if card.UID == uid {
			return card
		}
	}
	t.Fatalf("seat %d has no %s in the battle zone", seatNumber, uid)
	return DebugCard{}
}

func lightMana(n int) []match.SetupCard {
	mana := make([]match.SetupCard, n)
	for i := range mana {
		mana[i] = q(szubsKin)
	}
	return mana
}

func TestSetupPlacesEveryZone(t *testing.T) {
	h := start(t, setupSpec(func(s *match.BoardSetup) {
		s.Seats[0].Hand = []string{szubsKin, holyAwe}
		s.Seats[0].Mana = []match.SetupCard{q(vorg), {ID: vorg, Tapped: true}}
		s.Seats[0].Shields = []match.SetupCard{q(vorg), {ID: holyAwe, FaceUp: true}}
		s.Seats[0].Graveyard = []string{burningMane}
		s.Seats[0].Battle = []match.SetupCard{{ID: vorg, Tapped: true}, {ID: buzzBetocchi, Sick: true}}
		s.Seats[1].Deck = []string{szubsKin, vorg, holyAwe}
	}))

	if problems := h.d.SetupProblems(); len(problems) != 0 {
		t.Fatalf("unexpected problems: %v", problems)
	}

	ds := h.d.DebugState()
	if ds.Turn != 1 || ds.TurnsPlayed != 5 || ds.Step != "ChargeStep" {
		t.Fatalf("expected seat 1 at the charge step of turn 5, got turn %d, %d played, %s", ds.Turn, ds.TurnsPlayed, ds.Step)
	}

	me := ds.Seats[0]
	if len(me.Hand) != 2 || len(me.Mana) != 2 || len(me.Shields) != 2 || len(me.Graveyard) != 1 || len(me.Battle) != 2 || len(me.Deck) != 10 {
		t.Fatalf("zones not placed as asked: %+v", me)
	}
	if me.Mana[0].Tapped || !me.Mana[1].Tapped {
		t.Fatal("mana tap states not kept")
	}
	if me.Shields[0].FaceUp || !me.Shields[1].FaceUp {
		t.Fatal("shield face-up states not kept")
	}
	if !me.Battle[0].Tapped || me.Battle[1].Tapped {
		t.Fatal("battle zone tap states not kept")
	}

	// The deck keeps the given order, top first.
	deck := ds.Seats[1].Deck
	if deck[0].UID != szubsKin || deck[1].UID != vorg || deck[2].UID != holyAwe {
		t.Fatalf("deck order not kept: %v", deck)
	}

	// The sick creature can't attack; the other one is tapped, so seat 1 has
	// nothing to attack with.
	h.send(1, map[string]any{"header": "attack_player", "virtualId": me.Battle[1].ID})
	warn, _ := h.last(1, "warn")
	if warn == nil || !strings.Contains(warn["message"].(string), "summoning sickness") {
		t.Fatalf("expected the sick creature to be refused, got %v", warn)
	}
}

func TestSetupTurnStartRunsTheTurnsBeginning(t *testing.T) {
	h := start(t, setupSpec(func(s *match.BoardSetup) {
		s.Turn = 2
		s.BeginAt = match.BeginAtTurnStart
		s.Seats[1].Mana = []match.SetupCard{{ID: vorg, Tapped: true}}
		s.Seats[1].Battle = []match.SetupCard{{ID: vorg, Tapped: true}}
		s.Seats[0].Battle = []match.SetupCard{{ID: vorg, Tapped: true}}
	}))

	ds := h.d.DebugState()
	if ds.Turn != 2 || ds.TurnsPlayed != 5 || ds.Step != "ChargeStep" {
		t.Fatalf("expected seat 2 at the charge step of turn 5, got turn %d, %d played, %s", ds.Turn, ds.TurnsPlayed, ds.Step)
	}
	if len(ds.Seats[1].Hand) != 1 || len(ds.Seats[1].Deck) != 9 {
		t.Fatal("seat 2 should have drawn its card for the turn")
	}
	if ds.Seats[1].Mana[0].Tapped || ds.Seats[1].Battle[0].Tapped {
		t.Fatal("seat 2's untap step should untap its own cards")
	}
	if !ds.Seats[0].Battle[0].Tapped {
		t.Fatal("seat 1's creature is not untapped on seat 2's turn")
	}
}

// Quiet case 1: a continuous-effect creature placed quietly really applies its
// effect. Elf-X makes its owner's creatures cost 1 less, so 4 Light mana pays
// for a 5-cost Szubs Kin only with Elf-X in play.
func TestQuietContinuousEffectApplies(t *testing.T) {
	for _, withElf := range []bool{false, true} {
		for _, beginAt := range []string{match.BeginAtCharge, match.BeginAtTurnStart} {
			h := start(t, setupSpec(func(s *match.BoardSetup) {
				s.BeginAt = beginAt
				s.Seats[0].Hand = []string{szubsKin}
				s.Seats[0].Mana = lightMana(4)
				if withElf {
					s.Seats[0].Battle = []match.SetupCard{q(elfX)}
				}
			}))

			var szubs map[string]any
			for _, card := range list(h.state(1)["me"].(map[string]any)["hand"]) {
				if card["uid"] == szubsKin {
					szubs = card
				}
			}
			playable := flags(szubs)&2 != 0
			if playable != withElf {
				t.Fatalf("Elf-X %v, %s: Szubs Kin playable %v", withElf, beginAt, playable)
			}
		}
	}
}

// Quiet case 2: a "when summoned" creature placed quietly does not prompt,
// while the same creature placed with "summon" does. Seat 2 has two targets,
// because with only one the engine destroys it without asking.
func TestQuietPlacementSkipsSummonEffects(t *testing.T) {
	for _, enter := range []string{match.EnterQuiet, match.EnterSummon} {
		h := start(t, setupSpec(func(s *match.BoardSetup) {
			s.Seats[0].Battle = []match.SetupCard{{ID: meteosaur, Enter: enter}}
			s.Seats[1].Battle = []match.SetupCard{q(vorg), q(vorg)}
		}))

		ds := h.d.DebugState()
		if enter == match.EnterQuiet {
			if ds.Prompt != 0 || len(ds.Seats[1].Battle) != 2 {
				t.Fatalf("quiet Meteosaur must not trigger: prompt %d, seat 2 battle %d", ds.Prompt, len(ds.Seats[1].Battle))
			}
			continue
		}

		if ds.Prompt != 1 {
			t.Fatalf("summoned Meteosaur should ask seat 1 to destroy a creature, prompt is %d", ds.Prompt)
		}
		h.send(1, map[string]any{"header": "action", "cards": []string{ds.Seats[1].Battle[0].ID}})
		if after := h.d.DebugState(); len(after.Seats[1].Battle) != 1 || after.Step != "ChargeStep" {
			t.Fatalf("expected the Vorg destroyed and the duel at the charge step, got %+v", after.Seats[1].Battle)
		}
	}

	// A summon effect that doesn't prompt fires too: Bronze-Arm Tribe puts the
	// top card of the deck into mana only when summoned.
	for _, enter := range []string{match.EnterQuiet, match.EnterSummon} {
		h := start(t, setupSpec(func(s *match.BoardSetup) {
			s.Seats[0].Battle = []match.SetupCard{{ID: bronzeArm, Enter: enter}}
		}))
		want := 0
		if enter == match.EnterSummon {
			want = 1
		}
		if got := len(h.d.DebugState().Seats[0].Mana); got != want {
			t.Fatalf("Bronze-Arm Tribe placed %s: %d mana, want %d", enter, got, want)
		}
	}
}

// Quiet case 3: a placed evolution creature leaves the battle zone with its
// pile.
func TestQuietEvolutionLeavesWithItsPile(t *testing.T) {
	h := start(t, setupSpec(func(s *match.BoardSetup) {
		s.Seats[0].Battle = []match.SetupCard{{ID: valdios, Bases: []string{vorg}}}
	}))

	ds := h.d.DebugState()
	evo := battleCard(t, h, 1, valdios)
	if len(evo.Under) != 1 || len(ds.Seats[0].Hidden) != 1 || ds.Seats[0].Hidden[0].ID != evo.Under[0] {
		t.Fatalf("expected Valdios on top of one Vorg, got %+v and hidden %+v", evo, ds.Seats[0].Hidden)
	}

	// Destroy it directly, the way the scenario tests drive the engine while
	// its event loop is idle.
	card, err := h.d.players[0].Player.GetCard(evo.ID, match.BATTLEZONE)
	if err != nil {
		t.Fatal(err)
	}
	h.d.m.Destroy(card, card, match.DestroyedByMiscAbility)

	after := h.d.DebugState().Seats[0]
	if len(after.Battle) != 0 || len(after.Hidden) != 0 || len(after.Graveyard) != 2 {
		t.Fatalf("expected Valdios and its base in the graveyard, got battle %d, hidden %d, graveyard %d", len(after.Battle), len(after.Hidden), len(after.Graveyard))
	}
}

// Quiet case 4: a placed blocker can block. Quiet case 5: a placed shield
// trigger fires when its shield is broken.
func TestQuietBlockerBlocksAndPlacedShieldTriggerFires(t *testing.T) {
	h := start(t, setupSpec(func(s *match.BoardSetup) {
		s.Seats[0].Battle = []match.SetupCard{q(vorg)}
		s.Seats[1].Battle = []match.SetupCard{q(szubsKin)}
		s.Seats[1].Shields = []match.SetupCard{q(holyAwe)}
	}))

	ds := h.d.DebugState()
	attacker := battleCard(t, h, 1, vorg)
	blocker := battleCard(t, h, 2, szubsKin)

	h.send(1, map[string]any{"header": "attack_player", "virtualId": attacker.ID})
	if ds := h.d.DebugState(); ds.Prompt != 1 {
		t.Fatalf("expected seat 1 to choose a shield, prompt %d", ds.Prompt)
	}
	h.send(1, map[string]any{"header": "action", "cards": []string{ds.Seats[1].Shields[0].ID}})

	block, _ := h.last(2, "action")
	if h.d.DebugState().Prompt != 2 || block == nil || !offers(block, blocker.ID) {
		t.Fatalf("expected seat 2 to be offered Szubs Kin as a blocker, got %v", block)
	}

	// Don't block: the shield breaks and Holy Awe offers itself as a trigger.
	h.send(2, map[string]any{"header": "action", "cancel": true})

	trigger, _ := h.last(2, "action")
	if h.d.DebugState().Prompt != 2 || trigger == nil || !strings.HasPrefix(trigger["text"].(string), "Shield trigger!") {
		t.Fatalf("expected the Holy Awe shield trigger, got %v", trigger)
	}

	h.send(2, map[string]any{"header": "action", "cards": []string{list(trigger["cards"])[0]["virtualId"].(string)}})
	if !battleCard(t, h, 1, vorg).Tapped {
		t.Fatal("Holy Awe should have tapped seat 1's creatures")
	}
}

func offers(action map[string]any, id string) bool {
	for _, card := range list(action["cards"]) {
		if card["virtualId"] == id {
			return true
		}
	}
	return false
}

func TestSetupProblemsAreReported(t *testing.T) {
	h := start(t, setupSpec(func(s *match.BoardSetup) {
		s.Seats[0].Battle = []match.SetupCard{{ID: vorg, Bases: []string{vorg}}, {ID: "not-a-card"}, {ID: burningMane, Enter: "sideways"}}
		s.Seats[0].Hand = []string{wiseStarnoid}
	}))

	problems := strings.Join(h.d.SetupProblems(), "\n")
	if !strings.Contains(problems, "not an evolution creature") || !strings.Contains(problems, "unknown way to enter") {
		t.Fatalf("expected the bases and the entry to be reported, got:\n%s", problems)
	}

	unknown := h.d.Report().UnknownCards
	if len(unknown) != 2 || unknown[0].ID != wiseStarnoid || unknown[1].ID != "not-a-card" {
		t.Fatalf("expected the two unknown cards to be reported, got %+v", unknown)
	}

	if report, _ := h.last(0, "setup_report"); report == nil {
		t.Fatal("expected the setup report on seat 0")
	}

	// Both creatures that could be placed are there.
	if n := len(h.d.DebugState().Seats[0].Battle); n != 2 {
		t.Fatalf("expected 2 creatures placed, got %d", n)
	}
}
