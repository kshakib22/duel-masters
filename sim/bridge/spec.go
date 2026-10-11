package bridge

import (
	"duel-masters/game/match"
	"encoding/json"
	"fmt"
)

// Spec describes one duel: who sits in each seat with which deck, under which
// house rules, from which position. It is the game's DuelSpec with every deck
// already converted to engine card ids; the game app does that conversion.
type Spec struct {
	V int `json:"v"`

	// Seed makes the duel reproducible. Without one the bridge picks a random
	// seed and reports it, so every duel can be replayed.
	Seed *int64 `json:"seed,omitempty"`

	Rules Rules   `json:"rules"`
	Seats [2]Seat `json:"seats"`

	// Setup starts the duel from a position instead of a shuffle and deal.
	Setup *match.BoardSetup `json:"setup,omitempty"`

	// ExtendedState adds power, cost, summoning sickness and blocker to every
	// state update, for computer opponents. It is not a house rule: it changes
	// what is reported, never how the duel plays.
	ExtendedState bool `json:"extendedState,omitempty"`

	// Memory card placed face down in Player 2's shields. Breaking it wins with reason "memory".
	Memory string `json:"memory,omitempty"`
}

// Rules are the house rules. Every unset field keeps shobu's standard rule.
type Rules struct {
	Shields              *int       `json:"shields,omitempty"`
	StartingHand         *int       `json:"startingHand,omitempty"`
	FirstPlayerSkipsDraw *bool      `json:"firstPlayerSkipsDraw,omitempty"`
	ChargesPerTurn       *int       `json:"chargesPerTurn,omitempty"`
	DeckOut              string     `json:"deckOut,omitempty"`
	TurnLimit            *TurnLimit `json:"turnLimit,omitempty"`
	Memory               string     `json:"memory,omitempty"`

	// StartingSeat is 1, 2 or "random" (the default), drawn from the seed.
	StartingSeat json.RawMessage `json:"startingSeat,omitempty"`

	SandboxConsole bool `json:"sandboxConsole,omitempty"`
}

// TurnLimit ends the duel after Turns turns (each player's turn counts as one)
// with the player in seat Winner winning: "survive N turns".
type TurnLimit struct {
	Turns  int `json:"turns"`
	Winner int `json:"winner"`
}

// Seat is one player. Deck holds engine card ids. Shields, StartingHand and
// ChargesPerTurn override the house rules for this seat only.
type Seat struct {
	Name           string   `json:"name"`
	Deck           []string `json:"deck"`
	Shields        *int     `json:"shields,omitempty"`
	StartingHand   *int     `json:"startingHand,omitempty"`
	ChargesPerTurn *int     `json:"chargesPerTurn,omitempty"`
	Memory         string   `json:"memory,omitempty"`
}

// ParseSpec reads a Spec from JSON.
func ParseSpec(data []byte) (*Spec, error) {
	spec := &Spec{}
	if err := json.Unmarshal(data, spec); err != nil {
		return nil, fmt.Errorf("the duel description is not valid: %w", err)
	}
	return spec, nil
}

// startingSeat returns 1, 2, or 0 for random.
func (r Rules) startingSeat() (int, error) {
	if len(r.StartingSeat) == 0 {
		return 0, nil
	}

	var seat int
	if err := json.Unmarshal(r.StartingSeat, &seat); err == nil {
		if seat == 1 || seat == 2 {
			return seat, nil
		}
		return 0, fmt.Errorf("startingSeat must be 1, 2 or \"random\", not %d", seat)
	}

	var word string
	if err := json.Unmarshal(r.StartingSeat, &word); err == nil && word == "random" {
		return 0, nil
	}

	return 0, fmt.Errorf("startingSeat must be 1, 2 or \"random\", not %s", string(r.StartingSeat))
}

func (s *Spec) validate() error {
	if s.V != 0 && s.V != 1 {
		return fmt.Errorf("this engine reads duel descriptions of version 1, not %d", s.V)
	}

	counts := map[string]*int{
		"shields":        s.Rules.Shields,
		"startingHand":   s.Rules.StartingHand,
		"chargesPerTurn": s.Rules.ChargesPerTurn,
	}
	for i, seat := range s.Seats {
		counts[fmt.Sprintf("seat %d shields", i+1)] = seat.Shields
		counts[fmt.Sprintf("seat %d startingHand", i+1)] = seat.StartingHand
		counts[fmt.Sprintf("seat %d chargesPerTurn", i+1)] = seat.ChargesPerTurn
	}
	for name, value := range counts {
		if value != nil && *value < 0 {
			return fmt.Errorf("%s can't be negative", name)
		}
	}

	if s.Rules.DeckOut != "" && s.Rules.DeckOut != match.DeckOutLose && s.Rules.DeckOut != match.DeckOutOff {
		return fmt.Errorf("deckOut must be %q or %q", match.DeckOutLose, match.DeckOutOff)
	}

	if t := s.Rules.TurnLimit; t != nil {
		if t.Turns < 1 {
			return fmt.Errorf("a turn limit needs at least 1 turn")
		}
		if t.Winner != 1 && t.Winner != 2 {
			return fmt.Errorf("a turn limit's winner must be seat 1 or 2")
		}
	}

	if _, err := s.Rules.startingSeat(); err != nil {
		return err
	}

	return nil
}

// options turns the spec into the engine's match options.
func (s *Spec) options(seed int64) *match.MatchOptions {
	mem := s.Rules.Memory
	if mem == "" {
		mem = s.Memory
	}

	o := &match.MatchOptions{
		Seed:                 &seed,
		DeckOut:              s.Rules.DeckOut,
		FirstPlayerSkipsDraw: s.Rules.FirstPlayerSkipsDraw,
		SandboxConsole:       s.Rules.SandboxConsole,
		ExtendedState:        s.ExtendedState,
		MemoryCard:           mem,
	}

	if t := s.Rules.TurnLimit; t != nil {
		o.TurnLimit = &match.TurnLimit{Turns: t.Turns, Winner: byte(t.Winner)}
	}

	for i, seat := range s.Seats {
		seatMem := seat.Memory
		if seatMem == "" && i == 1 {
			seatMem = mem
		}
		o.Seats[i] = match.SeatOptions{
			Shields:        firstSet(seat.Shields, s.Rules.Shields),
			OpeningHand:    firstSet(seat.StartingHand, s.Rules.StartingHand),
			ChargesPerTurn: firstSet(seat.ChargesPerTurn, s.Rules.ChargesPerTurn),
			MemoryCard:     seatMem,
		}
	}

	return o
}

func firstSet(values ...*int) *int {
	for _, v := range values {
		if v != nil {
			return v
		}
	}
	return nil
}
