package bridge

import (
	"duel-masters/game/match"
	"fmt"
	"strings"
)

// DebugState is the whole duel with nothing hidden: both hands, both decks in
// order, face-down shields and the cards under evolution creatures. The Duel
// Sandbox shows it behind its "Show everything" switch, and the replay tests
// compare it. Read it only when the duel has settled.
type DebugState struct {
	Engine      string       `json:"engine"`
	Seed        int64        `json:"seed"`
	Turn        int          `json:"turn"`
	TurnsPlayed int          `json:"turnsPlayed"`
	Step        string       `json:"step"`
	Prompt      int          `json:"prompt"`
	Ended       bool         `json:"ended"`
	Seats       [2]DebugSeat `json:"seats"`
}

// DebugSeat is one player's cards, zone by zone. Deck is top card first.
type DebugSeat struct {
	Deck           []DebugCard `json:"deck"`
	Hand           []DebugCard `json:"hand"`
	Mana           []DebugCard `json:"mana"`
	Shields        []DebugCard `json:"shields"`
	Battle         []DebugCard `json:"battle"`
	Graveyard      []DebugCard `json:"graveyard"`
	Hidden         []DebugCard `json:"hidden"`
	HasChargedMana bool        `json:"hasChargedMana"`
	CanChargeMana  bool        `json:"canChargeMana"`
}

// DebugCard is one card.
type DebugCard struct {
	ID     string   `json:"virtualId"`
	UID    string   `json:"uid"`
	Name   string   `json:"name"`
	Tapped bool     `json:"tapped,omitempty"`
	FaceUp bool     `json:"faceUp,omitempty"`
	Memory bool     `json:"memory,omitempty"`
	Under  []string `json:"under,omitempty"`
}

// DebugState returns the whole duel with nothing hidden.
func (d *Duel) DebugState() DebugState {
	m := d.m

	state := DebugState{
		Engine:      Version,
		Seed:        d.report.Seed,
		Turn:        int(m.Turn),
		TurnsPlayed: m.TurnsPlayed(),
		Step:        strings.TrimPrefix(fmt.Sprintf("%T", m.Step), "*match."),
		Ended:       m.IsEnding(),
	}

	if !state.Ended {
		_, _, awaiting := d.engineState()
		for i, waiting := range awaiting {
			if waiting {
				state.Prompt = i + 1
			}
		}
	}

	for i, ref := range d.players {
		p := ref.Player
		state.Seats[i] = DebugSeat{
			Deck:           debugZone(p, match.DECK),
			Hand:           debugZone(p, match.HAND),
			Mana:           debugZone(p, match.MANAZONE),
			Shields:        debugZone(p, match.SHIELDZONE),
			Battle:         debugZone(p, match.BATTLEZONE),
			Graveyard:      debugZone(p, match.GRAVEYARD),
			Hidden:         debugZone(p, match.HIDDENZONE),
			HasChargedMana: p.HasChargedMana,
			CanChargeMana:  p.CanChargeMana,
		}
	}

	return state
}

func debugZone(p *match.Player, zone string) []DebugCard {
	cards, err := p.Container(zone)
	if err != nil {
		return nil
	}

	result := make([]DebugCard, 0, len(cards))
	for _, card := range cards {
		dc := DebugCard{
			ID:     card.ID,
			UID:    card.ImageID,
			Name:   card.Name,
			Tapped: card.Tapped,
			FaceUp: card.ShieldFaceUp,
			Memory: card.Memory,
		}
		for _, under := range card.Attachments() {
			dc.Under = append(dc.Under, under.ID)
		}
		result = append(result, dc)
	}

	return result
}
