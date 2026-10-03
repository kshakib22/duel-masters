package match

import (
	"duel-masters/game/cnd"
	"duel-masters/server"
)

// extendPlayerState adds what a computer opponent needs to read the board the
// way a person reads the printed cards: every card's printed cost, a creature's
// power (its current power in the battle zone, its printed power elsewhere),
// and whether it is a blocker or has summoning sickness. Only used when the
// match options ask for it.
//
// Current power comes from GetPower, which dispatches a GetPowerEvent. Power
// modifiers only read the board, so this never prompts or changes the match;
// the bridge tests replay a seeded duel with extended state on and off and
// compare the outcome to prove it.
func (m *Match) extendPlayerState(state *server.PlayerState, p *Player) {
	extend := func(states []server.CardState, zone string) {
		cards, err := p.Container(zone)
		if err != nil {
			return
		}

		byID := make(map[string]*Card, len(cards))
		for _, card := range cards {
			byID[card.ID] = card
		}

		for i := range states {
			card, ok := byID[states[i].CardID]
			if !ok {
				continue
			}

			cost := card.ManaCost
			states[i].Cost = &cost

			if card.HasCondition(cnd.Creature) {
				power := card.Power
				if zone == BATTLEZONE {
					power = m.GetPower(card, false)
				}
				states[i].Power = &power
			}

			flags := CardFlags(states[i].Flags)

			// The same test as fx.HasSummoningSickness, which this package
			// cannot import.
			if zone == BATTLEZONE && card.HasCondition(cnd.SummoningSickness) && !card.HasCondition(cnd.SpeedAttacker) {
				flags |= SummoningSickFlag
			}

			if card.HasCondition(cnd.Blocker) {
				flags |= BlockerFlag
			}

			states[i].Flags = uint8(flags)
		}
	}

	extend(state.Hand, HAND)
	extend(state.Manazone, MANAZONE)
	extend(state.Graveyard, GRAVEYARD)
	extend(state.Battlezone, BATTLEZONE)
}
