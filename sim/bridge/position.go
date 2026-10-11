package bridge

import (
	"duel-masters/game/cnd"
	"duel-masters/game/match"
	"errors"
)

// Why Position can't be read right now. Each is a plain statement the game can
// show to the Game Master as it is.
var (
	// ErrNotSettled means an input is still queued or the engine is still
	// working through one. Try again once Settled reports true.
	ErrNotSettled = errors.New("the duel is still working through an input, so its position can't be read yet")

	// ErrMidAnswer means a seat is being asked a question (a shield to break,
	// a creature to choose, a shield trigger to use). Mid-question some cards
	// are halfway through an effect, so there is no honest position to read.
	// Answer the question first.
	ErrMidAnswer = errors.New("a player is in the middle of answering a question, so the position can't be read")

	// ErrUnreadable means a card is halfway through an effect that the position
	// can't describe: one waiting in the spell zone, or one in the hidden zone
	// that is not under an evolution creature.
	ErrUnreadable = errors.New("a card is in the middle of an effect, so the position can't be read")
)

// Position returns the duel as it stands now, in the same BoardSetup that a
// Spec's Setup takes, so a duel started from it (StartFromSetup) has the same
// cards in the same places: every zone of both seats in order (the deck top
// card first), mana and creatures tapped or not, shields face up or down,
// summoning sickness, and the cards under each evolution creature.
//
// It is only valid once the duel has settled, with nobody mid-answer. The
// errors say why not: ErrEnded (the duel has finished or was closed),
// ErrNotSettled, ErrMidAnswer or ErrUnreadable. Position only reads; it never
// changes the duel.
//
// The position is described as the start of the current turn's charge step
// (BeginAt "charge"), with the turn's number and whose turn it is, because
// that is the nearest thing StartFromSetup offers to "carry on from here". What
// it does not carry, and so what a duel restarted from it does not remember:
//
//   - which step of the turn it was (the restart is at the charge step), and
//     whether the player has already charged mana this turn;
//   - effects that last "until end of turn", and any other condition a card was
//     given along the way (only summoning sickness is kept);
//   - the order of a nested evolution pile (an evolution on top of another
//     evolution): every card under the top one is listed as a base, in order,
//     and the restart makes one flat pile of them.
//
// Every creature is placed quietly (continuous effects install, "when
// summoned" effects do not fire again), and the card ids of the engine change
// because the restarted duel deals them out afresh.
func (d *Duel) Position() (*match.BoardSetup, error) {
	if d.m.IsEnding() {
		return nil, ErrEnded
	}

	select {
	case <-d.done:
		return nil, ErrEnded
	default:
	}

	if !d.Settled() {
		return nil, ErrNotSettled
	}

	if _, _, awaiting := d.engineState(); awaiting[0] || awaiting[1] {
		return nil, ErrMidAnswer
	}

	setup := &match.BoardSetup{
		Turn:       d.m.Turn,
		TurnNumber: d.m.TurnsPlayed(),
		BeginAt:    match.BeginAtCharge,
	}

	for i, ref := range d.players {
		seat, err := positionSeat(ref.Player)
		if err != nil {
			return nil, err
		}
		setup.Seats[i] = seat
	}

	return setup, nil
}

func positionSeat(p *match.Player) (match.SeatSetup, error) {
	// A spell being cast sits in the spell zone until it has resolved.
	if spells, err := p.Container(match.SPELLZONE); err != nil || len(spells) > 0 {
		return match.SeatSetup{}, ErrUnreadable
	}

	// Every card in the hidden zone must be under an evolution creature, or it
	// would be left out of the position without a word.
	hidden, err := p.Container(match.HIDDENZONE)
	if err != nil {
		return match.SeatSetup{}, ErrUnreadable
	}
	battle, err := p.Container(match.BATTLEZONE)
	if err != nil {
		return match.SeatSetup{}, ErrUnreadable
	}

	seat := match.SeatSetup{
		Deck:      positionIDs(p, match.DECK),
		Hand:      positionIDs(p, match.HAND),
		Graveyard: positionIDs(p, match.GRAVEYARD),
		Mana:      make([]match.SetupCard, 0),
		Shields:   make([]match.SetupCard, 0),
		Battle:    make([]match.SetupCard, 0, len(battle)),
	}

	if cards, err := p.Container(match.MANAZONE); err == nil {
		for _, card := range cards {
			seat.Mana = append(seat.Mana, match.SetupCard{ID: card.ImageID, Tapped: card.Tapped})
		}
	}

	if cards, err := p.Container(match.SHIELDZONE); err == nil {
		for _, card := range cards {
			seat.Shields = append(seat.Shields, match.SetupCard{
				ID:     card.ImageID,
				FaceUp: card.ShieldFaceUp,
				Memory: card.Memory,
			})
		}
	}

	piled := 0
	for _, card := range battle {
		entry := match.SetupCard{
			ID:     card.ImageID,
			Tapped: card.Tapped,
			Sick:   card.HasCondition(cnd.SummoningSickness),
		}
		for _, base := range pileUnder(card) {
			entry.Bases = append(entry.Bases, base.ImageID)
			piled++
		}
		seat.Battle = append(seat.Battle, entry)
	}

	if piled != len(hidden) {
		return match.SeatSetup{}, ErrUnreadable
	}

	return seat, nil
}

// positionIDs lists a zone's card ids in order. It is never nil, so an empty
// deck stays an empty deck instead of meaning "keep the deck you were given".
func positionIDs(p *match.Player, zone string) []string {
	ids := make([]string, 0)
	if cards, err := p.Container(zone); err == nil {
		for _, card := range cards {
			ids = append(ids, card.ImageID)
		}
	}
	return ids
}

// pileUnder returns every card under an evolution creature, in order. An
// evolution on top of another evolution holds the lower one, which holds its
// own cards, and the setup has one flat list, so each card is followed by what
// is under it.
func pileUnder(card *match.Card) []*match.Card {
	var pile []*match.Card
	for _, under := range card.Attachments() {
		pile = append(pile, under)
		pile = append(pile, pileUnder(under)...)
	}
	return pile
}
