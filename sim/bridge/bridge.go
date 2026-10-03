// Package bridge runs one duel in-process, with no network: the game drives
// it from a browser (through cmd/wasm) or from Node, and the tests drive it
// natively. Seats are built the way tests/scenario builds them.
//
// The engine drops a sequential event (play, charge, attack, end turn) that
// arrives while it is busy, so the bridge queues every input and delivers it
// only once the engine has settled: no event running, or the running one
// waiting for the answer of the seat the input comes from. Nothing valid is
// ever dropped; an input that can never apply (an answer when nothing is being
// asked) is rejected with a message to its seat.
package bridge

import (
	"duel-masters/game/cards"
	"duel-masters/game/match"
	"duel-masters/server"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// Version is the engine build, the fork commit it was built from. The browser
// build sets it with -ldflags "-X duel-masters/bridge.Version=<commit>".
var Version = "dev"

// maxSeed keeps seeds inside the range a JavaScript number holds exactly, so a
// seed reported to the game always comes back unchanged.
const maxSeed = 1 << 53

// ErrEnded is returned for inputs sent after the duel ended or was closed.
var ErrEnded = errors.New("the duel has ended")

var registerOnce sync.Once

func register() {
	registerOnce.Do(func() {
		for _, set := range cards.Sets {
			for uid, ctor := range *set {
				if ctor == nil {
					continue
				}
				match.AddCard(uid, ctor)
			}
		}
		match.InitDecks()
	})
}

// EngineCards returns the id of every card the engine can play, sorted.
func EngineCards() []string {
	register()
	return match.GetCardImages()
}

// Report describes how a duel was set up.
type Report struct {
	Engine       string        `json:"engine"`
	Seed         int64         `json:"seed"`
	StartingSeat int           `json:"startingSeat"`
	UnknownCards []UnknownCard `json:"unknownCards"`
}

// UnknownCard is a card id the engine can't play. It was left out of the duel.
type UnknownCard struct {
	Seat int    `json:"seat"`
	Zone string `json:"zone"`
	ID   string `json:"id"`
}

// Result is how a duel ended, read from the engine's finish message.
type Result struct {
	Finished bool   `json:"finished"`
	Winner   int    `json:"winner"`
	Reason   string `json:"reason"`
	Turns    int    `json:"turns"`
}

// Input is one input delivered to the engine, in delivery order.
type Input struct {
	Seat int             `json:"seat"`
	Msg  json.RawMessage `json:"msg"`
}

type inputKind int

const (
	kindSequential inputKind = iota // goes through the event loop
	kindAnswer                      // answers the seat's open prompt
	kindParallel                    // chat and resigning
)

type input struct {
	seat int
	kind inputKind
	msg  []byte
}

// Duel is one running duel.
type Duel struct {
	m       *match.Match
	players [2]*match.PlayerReference
	out     func(seat int, msg []byte)
	outMu   sync.Mutex

	mu            sync.Mutex
	queue         []input
	inputs        []Input
	result        Result
	setupProblems []string

	// Counted before an input is handed to the engine, so the engine looks
	// unsettled until it has caught up with them.
	expected         atomic.Uint64
	answersDelivered [2]atomic.Uint64

	wake      chan struct{}
	done      chan struct{}
	closeOnce sync.Once

	report Report
}

// New creates a duel from spec and starts it. out receives every message the
// engine sends: seat 1 or 2 for a seat's messages (the same JSON shobu's board
// reads), and seat 0 for the bridge's own (the setup report). out is called
// from the engine's goroutines, one call at a time; it must not block.
func New(spec *Spec, out func(seat int, msg []byte)) (*Duel, *Report, error) {
	register()

	if err := spec.validate(); err != nil {
		return nil, nil, err
	}

	seed := rand.Int63n(maxSeed)
	if spec.Seed != nil {
		seed = *spec.Seed
	}

	startingSeat, _ := spec.Rules.startingSeat()
	if startingSeat == 0 {
		startingSeat = rand.New(rand.NewSource(seed)).Intn(2) + 1
	}

	d := &Duel{
		out:  out,
		wake: make(chan struct{}, 1),
		done: make(chan struct{}),
		report: Report{
			Engine:       Version,
			Seed:         seed,
			StartingSeat: startingSeat,
			UnknownCards: make([]UnknownCard, 0),
		},
	}

	names := [2]string{}
	for i, seat := range spec.Seats {
		names[i] = strings.TrimSpace(seat.Name)
		if names[i] == "" {
			names[i] = fmt.Sprintf("Seat %d", i+1)
		}
	}

	system := match.NewSystem()
	m := system.NewMatch("duel", seatUID(1), names[0], nil, seatUID(2), names[1], nil, false, false, match.FormatDescriptor{}, seatUID(1))
	m.SetOptions(spec.options(seed))
	d.m = m

	for i := range d.players {
		seat := i + 1
		p := match.NewPlayer(m, byte(seat))
		ref := match.NewPlayerReference(p, server.NewSocket(&seatConn{duel: d, seat: seat}, m, seatUID(seat), names[i]))

		deck := d.known(seat, "deck", spec.Seats[i].Deck)
		ref.DeckStr = strings.Join(deck, ",")
		p.CreateDeck(deck)
		p.Ready = true

		d.players[i] = ref
	}
	m.Player1 = d.players[0]
	m.Player2 = d.players[1]

	var setup *match.BoardSetup
	if spec.Setup != nil {
		setup = d.knownSetup(spec.Setup)
		d.report.StartingSeat = int(setup.Turn)
		if setup.Turn != 2 {
			d.report.StartingSeat = 1
		}
	}

	go d.pump()

	d.expected.Store(1)
	m.ScheduleSequential(func() {
		if setup != nil {
			problems := m.StartFromSetup(setup)
			d.mu.Lock()
			d.setupProblems = problems
			d.mu.Unlock()
			if data, err := json.Marshal(struct {
				Header   string   `json:"header"`
				Problems []string `json:"problems"`
			}{"setup_report", problems}); err == nil {
				d.emit(0, data)
			}
			return
		}

		// BeginNewTurn hands the turn over before the first turn starts.
		if startingSeat == 1 {
			m.Turn = 2
		} else {
			m.Turn = 1
		}
		m.Chat("Server", fmt.Sprintf("%s goes first", names[startingSeat-1]))
		m.Start()
	})

	return d, &d.report, nil
}

func seatUID(seat int) string {
	return fmt.Sprintf("seat-%d", seat)
}

// known returns the ids the engine can play and reports the others.
func (d *Duel) known(seat int, zone string, ids []string) []string {
	if ids == nil {
		return nil
	}

	result := make([]string, 0, len(ids))
	for _, id := range ids {
		if _, err := match.CardCtor(id); err != nil {
			d.report.UnknownCards = append(d.report.UnknownCards, UnknownCard{Seat: seat, Zone: zone, ID: id})
			continue
		}
		result = append(result, id)
	}
	return result
}

func (d *Duel) knownCards(seat int, zone string, entries []match.SetupCard) []match.SetupCard {
	result := make([]match.SetupCard, 0, len(entries))
	for _, entry := range entries {
		if len(d.known(seat, zone, []string{entry.ID})) == 0 {
			continue
		}
		entry.Bases = d.known(seat, zone+" (evolution base)", entry.Bases)
		result = append(result, entry)
	}
	return result
}

func (d *Duel) knownSetup(setup *match.BoardSetup) *match.BoardSetup {
	s := *setup
	for i := range s.Seats {
		seat := i + 1
		zones := s.Seats[i]
		zones.Deck = d.known(seat, "setup deck", zones.Deck)
		zones.Hand = d.known(seat, "hand", zones.Hand)
		zones.Graveyard = d.known(seat, "graveyard", zones.Graveyard)
		zones.Mana = d.knownCards(seat, "mana", zones.Mana)
		zones.Shields = d.knownCards(seat, "shields", zones.Shields)
		zones.Battle = d.knownCards(seat, "battle zone", zones.Battle)
		s.Seats[i] = zones
	}
	return &s
}

// Send queues an input from a seat: one of the messages shobu's board sends
// (add_to_manazone, add_to_playzone, attack_player, attack_creature,
// tap_ability, end_turn, action, chat, resign). It returns at once; the input
// is delivered when the engine has settled.
func (d *Duel) Send(seat int, msg []byte) error {
	if seat != 1 && seat != 2 {
		return fmt.Errorf("seat must be 1 or 2, not %d", seat)
	}

	var header struct {
		Header string `json:"header"`
	}
	if err := json.Unmarshal(msg, &header); err != nil {
		return fmt.Errorf("the input is not valid JSON: %w", err)
	}

	var kind inputKind
	switch header.Header {
	case "add_to_manazone", "add_to_playzone", "attack_player", "attack_creature", "tap_ability", "end_turn":
		kind = kindSequential
	case "action":
		kind = kindAnswer
		// The engine ignores these two kinds of answer without passing them on,
		// which would leave the bridge waiting for an answer that never lands,
		// so they are rejected here with the engine's own words.
		var action match.PlayerAction
		if err := json.Unmarshal(msg, &action); err != nil {
			d.reject(seat, "action_error", "Invalid selection")
			return fmt.Errorf("the answer is not valid: %w", err)
		}
		seen := make(map[string]bool, len(action.Cards))
		for _, id := range action.Cards {
			if seen[id] {
				d.reject(seat, "action_error", "You cannot select the same card multiple times")
				return errors.New("the answer selects the same card more than once")
			}
			seen[id] = true
		}
	case "chat", "resign":
		kind = kindParallel
	default:
		return fmt.Errorf("%q is not something a seat can send", header.Header)
	}

	if d.m.IsClosed() {
		return ErrEnded
	}

	d.mu.Lock()
	d.queue = append(d.queue, input{seat: seat, kind: kind, msg: append([]byte(nil), msg...)})
	d.mu.Unlock()

	select {
	case d.wake <- struct{}{}:
	default:
	}

	return nil
}

// Settled reports whether every input sent so far has been delivered and the
// engine has finished with it: either nothing is running, or a seat is being
// asked a question. Once the duel has ended it is true as soon as the engine
// has stopped.
func (d *Duel) Settled() bool {
	d.mu.Lock()
	queued := len(d.queue)
	d.mu.Unlock()

	settled, _, _ := d.engineState()
	if d.m.IsClosed() {
		return settled
	}

	return queued == 0 && settled
}

// engineState reports whether the engine has settled, whether it is idle (no
// event running), and which seats it is waiting on.
func (d *Duel) engineState() (settled bool, idle bool, awaiting [2]bool) {
	if d.m.IsClosed() {
		// The event that ended the duel keeps running until it unwinds, so
		// the duel has only settled once the event loop has stopped.
		stopped := d.m.EventLoopStopped() && d.m.ParallelEventsInFlight() == 0
		return stopped, stopped, awaiting
	}

	if d.m.ParallelEventsInFlight() > 0 {
		return false, false, awaiting
	}

	for i, ref := range d.players {
		if ref.Player.AnswersReceived() < d.answersDelivered[i].Load() {
			return false, false, awaiting
		}
	}

	if !d.m.EventLoopBusy() {
		idle = d.m.EventsProcessed() >= d.expected.Load()
		return idle, idle, awaiting
	}

	for i, ref := range d.players {
		awaiting[i] = ref.Player.AwaitingAction()
	}

	return awaiting[0] || awaiting[1], false, awaiting
}

// pump delivers queued inputs as the engine settles. It is the only goroutine
// the bridge starts, and it stops when the duel ends or is closed.
func (d *Duel) pump() {
	for {
		select {
		case <-d.done:
			return
		case <-d.wake:
		}

		d.deliverQueued()

		if d.m.IsClosed() {
			d.mu.Lock()
			d.queue = nil
			d.mu.Unlock()
			return
		}
	}
}

func (d *Duel) deliverQueued() {
	spins := 0

	for {
		if d.m.IsClosed() {
			return
		}

		d.mu.Lock()
		empty := len(d.queue) == 0
		d.mu.Unlock()
		if empty {
			return
		}

		settled, idle, awaiting := d.engineState()
		if !settled {
			// The engine is still working through an input. Yield to it, and
			// back off to a short sleep if it takes a while.
			spins++
			if spins < 1000 {
				runtime.Gosched()
			} else {
				time.Sleep(100 * time.Microsecond)
			}
			continue
		}
		spins = 0

		next, stale, ok := d.take(idle, awaiting)

		for _, in := range stale {
			d.reject(in.seat, "action_error", "There is no question to answer right now")
		}

		if !ok {
			if len(stale) > 0 {
				continue
			}
			// A seat is being asked a question and every queued input is
			// waiting for that answer. The next Send wakes the pump.
			return
		}

		d.deliver(next)
	}
}

// take removes and returns the first queued input the engine can take now,
// plus any answers that can no longer apply because nothing is being asked.
func (d *Duel) take(idle bool, awaiting [2]bool) (input, []input, bool) {
	d.mu.Lock()
	defer d.mu.Unlock()

	var stale []input
	kept := d.queue[:0:0]
	var next input
	found := false

	for _, in := range d.queue {
		if found {
			kept = append(kept, in)
			continue
		}

		switch {
		case in.kind == kindAnswer && awaiting[in.seat-1]:
			next, found = in, true
		case in.kind == kindAnswer && idle:
			stale = append(stale, in)
		case in.kind != kindAnswer && idle:
			next, found = in, true
		default:
			kept = append(kept, in)
		}
	}

	d.queue = kept

	if found {
		// Counted before the engine sees the input, so it looks unsettled
		// until it has dealt with it.
		switch next.kind {
		case kindSequential:
			d.expected.Add(1)
		case kindAnswer:
			d.answersDelivered[next.seat-1].Add(1)
		}
		d.inputs = append(d.inputs, Input{Seat: next.seat, Msg: next.msg})
	}

	return next, stale, found
}

func (d *Duel) deliver(in input) {
	d.m.Parse(d.players[in.seat-1].Socket, in.msg)
}

func (d *Duel) reject(seat int, header string, message string) {
	data, err := json.Marshal(server.WarningMessage{Header: header, Message: message})
	if err == nil {
		d.emit(seat, data)
	}
}

func (d *Duel) emit(seat int, data []byte) {
	d.outMu.Lock()
	defer d.outMu.Unlock()
	if d.out != nil {
		d.out(seat, data)
	}
}

// Inputs returns every input delivered so far, in delivery order. Sending them
// again, in order, to a duel made from the same spec replays it exactly.
func (d *Duel) Inputs() []Input {
	d.mu.Lock()
	defer d.mu.Unlock()
	return append([]Input(nil), d.inputs...)
}

// Result returns how the duel ended, once it has.
func (d *Duel) Result() Result {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.result
}

// SetupProblems returns what could not be placed as asked when the duel
// started from a BoardSetup. It is filled in once the duel has started.
func (d *Duel) SetupProblems() []string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return append([]string(nil), d.setupProblems...)
}

// Report returns how the duel was set up.
func (d *Duel) Report() Report {
	return d.report
}

// Close ends the duel and releases it. It is safe to call more than once.
func (d *Duel) Close() {
	d.closeOnce.Do(func() {
		close(d.done)
		d.m.Dispose()
	})
}

// recordFinish reads the engine's finish message.
func (d *Duel) recordFinish(data []byte) {
	var finished server.DuelFinishedMessage
	if err := json.Unmarshal(data, &finished); err != nil {
		return
	}

	d.mu.Lock()
	defer d.mu.Unlock()

	if d.result.Finished {
		return
	}

	d.result = Result{Finished: true, Reason: finished.Reason, Turns: finished.Turns}
	if finished.Winner != nil {
		switch finished.Winner.UID {
		case seatUID(1):
			d.result.Winner = 1
		case seatUID(2):
			d.result.Winner = 2
		}
	}
}

// seatConn is a seat's connection: everything the engine writes to it goes to
// the duel's out callback.
type seatConn struct {
	duel *Duel
	seat int
}

func (c *seatConn) SetReadLimit(int64)                 {}
func (c *seatConn) SetReadDeadline(t time.Time) error  { return nil }
func (c *seatConn) SetPongHandler(func(string) error)  {}
func (c *seatConn) SetWriteDeadline(t time.Time) error { return nil }
func (c *seatConn) WriteMessage(int, []byte) error     { return nil }
func (c *seatConn) Close() error                       { return nil }
func (c *seatConn) ReadMessage() (int, []byte, error) {
	return 0, nil, errors.New("a bridge seat is never read from")
}

func (c *seatConn) WriteJSON(v interface{}) error {
	data, err := json.Marshal(v)
	if err != nil {
		return err
	}

	if finished, ok := v.(server.DuelFinishedMessage); ok && finished.Header == "duel_finished" {
		c.duel.recordFinish(data)
	}

	c.duel.emit(c.seat, data)
	return nil
}
