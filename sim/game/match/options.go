package match

import (
	"fmt"
	"math/rand"
	"strconv"
	"sync"
)

// Deck-out rules for MatchOptions.DeckOut.
const (
	DeckOutLose = "lose"
	DeckOutOff  = "off"
)

// End reasons reported in DuelFinishedMessage.Reason.
const (
	EndReasonShields    = "shields"
	EndReasonDeckOut    = "deck_out"
	EndReasonResign     = "resign"
	EndReasonTurnLimit  = "turn_limit"
	EndReasonCardEffect = "card_effect"
	EndReasonDisconnect = "disconnect"
)

// SeatOptions holds the house rules that can differ between the two seats.
// A nil field keeps the engine's standard behaviour.
type SeatOptions struct {
	Shields        *int
	OpeningHand    *int
	ChargesPerTurn *int
}

// TurnLimit ends the duel once Turns turns have been played, counted the way
// DuelFinishedMessage.Turns counts them (each player's turn is one turn). The
// player in seat Winner (1 or 2) wins.
type TurnLimit struct {
	Turns  int
	Winner byte
}

// MatchOptions configures a match for house rules, replays and tooling. A match
// without options, or with every field unset, behaves exactly like a standard
// match.
type MatchOptions struct {
	// Seed makes the match reproducible: every shuffle, coin toss and random
	// discard comes from it, and card ids are numbered instead of random.
	Seed *int64

	// DeckOut is DeckOutLose (the default) or DeckOutOff. With DeckOutOff a
	// player with no cards left in their deck keeps playing and draws nothing.
	DeckOut string

	// FirstPlayerSkipsDraw defaults to true.
	FirstPlayerSkipsDraw *bool

	TurnLimit *TurnLimit

	// SandboxConsole enables the chat commands (/add, /mana, ...) for this match
	// as the local_dev environment variable does for every match.
	SandboxConsole bool

	// ExtendedState adds current power, printed cost, and the summoning sickness
	// and blocker flags to every state update.
	ExtendedState bool

	// Seats holds the per seat rules, index 0 for Player1 and 1 for Player2.
	Seats [2]SeatOptions
}

// SetOptions configures the match. It must be called before the match starts.
func (m *Match) SetOptions(options *MatchOptions) {
	m.options = options

	if options != nil && options.Seed != nil {
		m.rng = newLockedRand(*options.Seed)
		m.seeded = true
	}
}

// Options returns the match options, or nil for a standard match.
func (m *Match) Options() *MatchOptions {
	return m.options
}

// Rand returns the match's source of randomness. Every random decision the
// match or a card makes goes through it, so a seeded match can be replayed. An
// unseeded match uses the process wide source, exactly as before options
// existed.
func (m *Match) Rand() *rand.Rand {
	if m.rng == nil {
		return globalRand
	}

	return m.rng
}

func (m *Match) seatOptions(p *Player) *SeatOptions {
	if m.options == nil {
		return nil
	}

	if m.Player2 != nil && m.Player2.Player == p {
		return &m.options.Seats[1]
	}

	return &m.options.Seats[0]
}

func (m *Match) shieldCount(p *Player) int {
	if s := m.seatOptions(p); s != nil && s.Shields != nil {
		return *s.Shields
	}

	return 5
}

func (m *Match) openingHand(p *Player) int {
	if s := m.seatOptions(p); s != nil && s.OpeningHand != nil {
		return *s.OpeningHand
	}

	return 5
}

func (m *Match) chargesPerTurn(p *Player) int {
	if s := m.seatOptions(p); s != nil && s.ChargesPerTurn != nil {
		return *s.ChargesPerTurn
	}

	return 1
}

func (m *Match) firstPlayerSkipsDraw() bool {
	if m.options != nil && m.options.FirstPlayerSkipsDraw != nil {
		return *m.options.FirstPlayerSkipsDraw
	}

	return true
}

// deckOutLoses reports whether running out of cards loses the game. It is
// false while a board setup is being placed, because zones are filled one card
// at a time and a deck may be empty halfway through.
func (m *Match) deckOutLoses() bool {
	if m.placingSetup {
		return false
	}

	return m.options == nil || m.options.DeckOut != DeckOutOff
}

func (m *Match) sandboxConsole() bool {
	return m.options != nil && m.options.SandboxConsole
}

func (m *Match) extendedState() bool {
	return m.options != nil && m.options.ExtendedState
}

func (m *Match) turnLimit() *TurnLimit {
	if m.options == nil || m.options.TurnLimit == nil || m.options.TurnLimit.Turns < 1 {
		return nil
	}

	return m.options.TurnLimit
}

// endByTurnLimit ends a "survive N turns" duel in favour of the seat named by
// the limit.
func (m *Match) endByTurnLimit(limit *TurnLimit) {
	winner := m.Player1
	if limit.Winner == 2 {
		winner = m.Player2
	}

	m.EndWithReason(winner.Player, fmt.Sprintf("%s won: the turn limit of %d turns was reached", winner.Username, limit.Turns), EndReasonTurnLimit)
}

// resetCharges gives the player their charges for a new turn.
func (p *Player) resetCharges() {
	p.chargesUsed = 0
	p.HasChargedMana = p.match.chargesPerTurn(p) <= 0
}

// useCharge records a mana charge. HasChargedMana keeps its meaning of "no
// charges left this turn", which is what the duel interface reads.
func (p *Player) useCharge() {
	p.chargesUsed++
	p.HasChargedMana = p.chargesUsed >= p.match.chargesPerTurn(p)
}

// nextCardID returns a short, stable card id for a seeded match.
func (m *Match) nextCardID() string {
	return "c" + strconv.FormatInt(m.cardIDs.Add(1), 36)
}

// newLockedRand returns a *rand.Rand that can be shared between the goroutines
// a match runs events on.
func newLockedRand(seed int64) *rand.Rand {
	return rand.New(&lockedSource{src: rand.NewSource(seed).(rand.Source64)})
}

type lockedSource struct {
	mu  sync.Mutex
	src rand.Source64
}

func (s *lockedSource) Int63() int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.src.Int63()
}

func (s *lockedSource) Uint64() uint64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.src.Uint64()
}

func (s *lockedSource) Seed(seed int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.src.Seed(seed)
}

// globalRand draws from math/rand's package level source, which is safe for
// concurrent use.
var globalRand = rand.New(globalSource{})

type globalSource struct{}

func (globalSource) Int63() int64    { return rand.Int63() }
func (globalSource) Uint64() uint64  { return rand.Uint64() }
func (globalSource) Seed(seed int64) {}
