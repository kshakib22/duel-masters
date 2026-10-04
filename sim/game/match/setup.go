package match

import (
	"duel-masters/game/cnd"
	"fmt"
	"time"
)

// Ways a creature in a BoardSetup enters the battle zone.
const (
	// EnterQuiet puts the creature into the battle zone as if it had always
	// been there: it is spawned into the hidden zone and moved to the battle
	// zone, so its continuous "while in the battle zone" effects install
	// (fx.InTheBattlezone) and its "when summoned" effects do not fire
	// (fx.Summoned ignores arrivals from the hidden zone).
	EnterQuiet = "quiet"
	// EnterSummon moves the creature from the hand to the battle zone, so its
	// "when summoned" effects fire and may prompt. Summoned creatures arrive
	// after every quiet one, in the order given, seat 1's first.
	EnterSummon = "summon"
)

// When a match started from a BoardSetup begins.
const (
	// BeginAtTurnStart runs the turn's begin, untap, start of turn and draw
	// steps first, exactly as a normal turn would.
	BeginAtTurnStart = "turn_start"
	// BeginAtCharge drops the player straight into their charge step with the
	// board exactly as placed.
	BeginAtCharge = "charge"
)

// SetupCard is one card in a BoardSetup zone. Only the fields that apply to the
// zone it is in are read.
type SetupCard struct {
	ID     string   `json:"id"`
	Tapped bool     `json:"tapped,omitempty"` // mana and battle zone
	FaceUp bool     `json:"faceUp,omitempty"` // shields
	Sick   bool     `json:"sick,omitempty"`   // battle zone, only with BeginAtCharge
	Enter  string   `json:"enter,omitempty"`  // battle zone: EnterQuiet (default) or EnterSummon
	Bases  []string `json:"bases,omitempty"`  // battle zone: evolution bases underneath
}

// SeatSetup places one player's cards. Deck is in order, top card first. A nil
// Deck keeps the deck the player was given; an empty one leaves the seat with
// no deck, so Deck is written even when empty (as [] or null) to survive JSON.
type SeatSetup struct {
	Deck        []string    `json:"deck"`
	ShuffleDeck bool        `json:"shuffleDeck,omitempty"`
	Hand        []string    `json:"hand,omitempty"`
	Mana        []SetupCard `json:"mana,omitempty"`
	Shields     []SetupCard `json:"shields,omitempty"`
	Battle      []SetupCard `json:"battle,omitempty"`
	Graveyard   []string    `json:"graveyard,omitempty"`
}

// BoardSetup describes a position to start a match from instead of shuffling
// and dealing. Seats index 0 is Player1. Turn is whose turn it is (1 or 2) and
// TurnNumber the turn count so far, counted like DuelFinishedMessage.Turns.
type BoardSetup struct {
	Seats      [2]SeatSetup `json:"seats"`
	Turn       byte         `json:"turn"`
	TurnNumber int          `json:"turnNumber"`
	BeginAt    string       `json:"beginAt"`
}

// StartFromSetup starts the match from the given position instead of Start's
// shuffle and deal. Card ids the engine does not know are skipped and reported
// in the returned list, as is anything that could not be placed as asked.
//
// "summon" placements fire their summon effects, which can prompt, so this must
// run on the match event loop (see ScheduleSequential).
func (m *Match) StartFromSetup(setup *BoardSetup) []string {

	problems := make([]string, 0)

	m.Started = true
	m.startedAt = time.Now().Unix()
	m.placingSetup = true

	turn := setup.Turn
	if turn != 2 {
		turn = 1
	}
	m.Turn = turn

	turnNumber := setup.TurnNumber
	if turnNumber < 1 {
		turnNumber = 1
	}

	players := [2]*Player{m.Player1.Player, m.Player2.Player}

	spawn := func(seat int, p *Player, id string, zone string) *Card {
		card, err := p.SpawnCard(id, zone)
		if err != nil {
			problems = append(problems, fmt.Sprintf("seat %d: %s could not be placed in the %s: %v", seat, id, zone, err))
			return nil
		}
		return card
	}

	type placement struct {
		seat  int
		card  *Card
		entry SetupCard
		from  string
	}
	battle := make([]placement, 0)

	for i, p := range players {
		seat := i + 1
		s := setup.Seats[i]

		if s.Deck != nil {
			p.DestroyDeck()
			for _, id := range s.Deck {
				spawn(seat, p, id, DECK)
			}
		}

		if s.ShuffleDeck {
			p.ShuffleDeck()
		}

		for _, id := range s.Hand {
			spawn(seat, p, id, HAND)
		}

		for _, entry := range s.Mana {
			if card := spawn(seat, p, entry.ID, MANAZONE); card != nil {
				card.Tapped = entry.Tapped
			}
		}

		for _, entry := range s.Shields {
			if card := spawn(seat, p, entry.ID, SHIELDZONE); card != nil {
				card.ShieldFaceUp = entry.FaceUp
			}
		}

		for _, id := range s.Graveyard {
			spawn(seat, p, id, GRAVEYARD)
		}

		for _, entry := range s.Battle {
			from := HIDDENZONE
			if entry.Enter == EnterSummon {
				from = HAND
			} else if entry.Enter != "" && entry.Enter != EnterQuiet {
				problems = append(problems, fmt.Sprintf("seat %d: %s has an unknown way to enter (%q), placed quietly", seat, entry.ID, entry.Enter))
			}

			card := spawn(seat, p, entry.ID, from)
			if card == nil {
				continue
			}

			battle = append(battle, placement{seat: seat, card: card, entry: entry, from: from})
		}
	}

	// Give every card the conditions the untap step gives it (creature, spell,
	// blocker, shield trigger, evolution...), running only its own handlers, as
	// the sandbox console does for spawned cards. Battle zone arrivals below
	// depend on them. A creature's untap handler leaves mana alone, and battle
	// zone tap states are set after the creatures arrive.
	for _, p := range players {
		for _, card := range p.Cards() {
			initCard(m, card)
		}
	}

	// Quiet creatures first, then summoned ones, so a summon effect sees the
	// whole position (both seats' quiet creatures) as it was set up.
	ordered := make([]placement, 0, len(battle))
	for _, b := range battle {
		if b.from == HIDDENZONE {
			ordered = append(ordered, b)
		}
	}
	for _, b := range battle {
		if b.from == HAND {
			ordered = append(ordered, b)
		}
	}

	for _, b := range ordered {
		p := b.card.Player

		if len(b.entry.Bases) > 0 {
			if !b.card.HasCondition(cnd.Evolution) {
				problems = append(problems, fmt.Sprintf("seat %d: %s is not an evolution creature, so its bases were left out", b.seat, b.card.Name))
			} else {
				bases := make([]*Card, 0, len(b.entry.Bases))
				for _, id := range b.entry.Bases {
					base := spawn(b.seat, p, id, HIDDENZONE)
					if base == nil {
						continue
					}
					initCard(m, base)
					bases = append(bases, base)
				}
				b.card.ClearAttachments()
				b.card.Attach(bases...)
			}
		}

		if b.from == HAND && b.entry.Sick {
			b.card.AddCondition(cnd.SummoningSickness, nil, nil)
		}

		p.MoveCard(b.card.ID, b.from, BATTLEZONE, "setup")

		if m.IsEnding() {
			return problems
		}

		if b.card.Zone != BATTLEZONE {
			problems = append(problems, fmt.Sprintf("seat %d: %s did not stay in the battle zone (it is in the %s)", b.seat, b.card.Name, b.card.Zone))
			continue
		}

		b.card.Tapped = b.entry.Tapped

		if b.entry.Sick && !b.card.HasCondition(cnd.SummoningSickness) {
			b.card.AddCondition(cnd.SummoningSickness, nil, nil)
		}
	}

	m.Chat("Server", "The duel has begun from a set up position!")

	if setup.BeginAt == BeginAtCharge {

		m.turnsPlayed = turnNumber
		m.isFirstTurn = turnNumber == 1

		for _, p := range players {
			p.resetCharges()
			p.CanChargeMana = true
		}

		m.placingSetup = false

		m.ChargeStep()
		m.BroadcastState()

		return problems
	}

	if setup.BeginAt != "" && setup.BeginAt != BeginAtTurnStart {
		problems = append(problems, fmt.Sprintf("unknown beginAt %q, the turn starts from the beginning", setup.BeginAt))
	}

	// Leave the board as it is between two turns: the previous turn's end
	// cleared every card's conditions, and the untap step about to run gives
	// them back. Without this, conditions given above would be given twice.
	// Summoning sickness ends here too, as it would at the end of a turn.
	for _, p := range players {
		for _, card := range p.Cards() {
			card.ClearConditions()
		}
	}

	m.turnsPlayed = turnNumber - 1
	m.isFirstTurn = turnNumber == 1

	// BeginNewTurn hands the turn over, so start from the other seat.
	if turn == 1 {
		m.Turn = 2
	} else {
		m.Turn = 1
	}

	m.placingSetup = false

	m.BeginNewTurn()

	return problems
}

// initCard runs a card's own handlers for the untap step, which is where cards
// set up their conditions.
func initCard(m *Match, card *Card) {
	ctx := NewContext(m, &UntapStep{})
	for _, h := range card.handlers {
		h(card, ctx)
	}
}
