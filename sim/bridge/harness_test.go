package bridge

import (
	"encoding/json"
	"fmt"
	"sync"
	"testing"
	"time"
)

// Cards used by the tests, by their engine ids.
const (
	vorg         = "af3bc221-1cc2-4f58-83ea-2673ac2c66c5" // Immortal Baron, Vorg: vanilla Fire Human, 2000
	szubsKin     = "eccceb7c-834c-4bf9-b0cd-c2dc6fad3dbf" // Szubs Kin, Twilight Guardian: Light blocker, 6000
	holyAwe      = "0ec572b0-ffaf-4abd-a540-ba26c98aacc5" // Holy Awe: Light spell, shield trigger
	meteosaur    = "d067285f-10ea-4666-99c8-bc23e27e3262" // Meteosaur: when summoned, may destroy a 2000-or-less creature
	bronzeArm    = "015fd6bb-37a9-45cf-bb6b-a5497412b880" // Bronze-Arm Tribe: when summoned, top card of deck to mana
	elfX         = "60d8c6a6-20c1-425c-9ecc-b56981a70e21" // Elf-X: continuous, your creatures cost 1 less
	valdios      = "eac1bc57-bdf8-4629-86b3-9609d1bf2aba" // Armored Blaster Valdios: Human evolution, 6000
	buzzBetocchi = "124aff9d-aaca-4c88-b40b-2529127a1214" // Buzz Betocchi: Fire and Nature
	bolshack     = "0ffdcae3-9db2-401b-8a82-dfad707b83cd" // Bolshack Dragon
	burningMane  = "1d72eb3e-5185-449a-a16f-391bd2338343" // Burning Mane: vanilla Nature
	jabaha       = "5646364b-4018-4556-9b20-265ef3fb372d" // Uberdragon Jabaha: our card 228, the one id override
	wiseStarnoid = "ba894f7e-b7d7-409e-8393-cab4285a879c" // Wise Starnoid: in shobu's card list, not implemented
)

func deckOf(counts ...any) []string {
	deck := make([]string, 0)
	for i := 0; i < len(counts); i += 2 {
		for range counts[i+1].(int) {
			deck = append(deck, counts[i].(string))
		}
	}
	return deck
}

// mixedDeck has a vanilla creature, a blocker, a shield trigger, a "when
// summoned" creature that prompts, a continuous-effect creature, an evolution
// with its bases, and a two-civilization card.
func mixedDeck() []string {
	return deckOf(
		vorg, 8, burningMane, 6, szubsKin, 4, holyAwe, 4, meteosaur, 4,
		bronzeArm, 4, elfX, 2, valdios, 4, buzzBetocchi, 4,
	)
}

func intp(v int) *int            { return &v }
func boolp(v bool) *bool         { return &v }
func seedp(v int64) *int64       { return &v }
func seat(v int) json.RawMessage { return json.RawMessage(fmt.Sprint(v)) }

type harness struct {
	t    *testing.T
	d    *Duel
	mu   sync.Mutex
	msgs [3][]string
}

func start(t *testing.T, spec *Spec) *harness {
	t.Helper()

	h := &harness{t: t}
	d, _, err := New(spec, func(seat int, msg []byte) {
		h.mu.Lock()
		h.msgs[seat] = append(h.msgs[seat], string(msg))
		h.mu.Unlock()
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	h.d = d
	t.Cleanup(d.Close)

	h.settle()
	return h
}

func (h *harness) settle() {
	h.t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !h.d.Settled() {
		if time.Now().After(deadline) {
			d := h.d
			in := d.Inputs()
			last := ""
			if len(in) > 0 {
				last = string(in[len(in)-1].Msg)
			}
			h.t.Fatalf("the duel did not settle after %s: parallel=%d busy=%v processed=%d expected=%d answers=[%d/%d %d/%d] awaiting=[%v %v] queued=%d",
				last, d.m.ParallelEventsInFlight(), d.m.EventLoopBusy(), d.m.EventsProcessed(), d.expected.Load(),
				d.players[0].Player.AnswersReceived(), d.answersDelivered[0].Load(),
				d.players[1].Player.AnswersReceived(), d.answersDelivered[1].Load(),
				d.players[0].Player.AwaitingAction(), d.players[1].Player.AwaitingAction(), len(d.queue))
		}
		time.Sleep(50 * time.Microsecond)
	}
}

func (h *harness) send(seat int, v any) {
	h.t.Helper()
	data, err := json.Marshal(v)
	if err != nil {
		h.t.Fatal(err)
	}
	if err := h.d.Send(seat, data); err != nil {
		h.t.Fatalf("Send: %v", err)
	}
	h.settle()
}

func (h *harness) count(seat int) int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.msgs[seat])
}

// messages returns the seat's messages with the given header, from index since.
func (h *harness) messages(seat int, since int, header string) []map[string]any {
	h.mu.Lock()
	defer h.mu.Unlock()

	result := make([]map[string]any, 0)
	for _, raw := range h.msgs[seat][since:] {
		var msg map[string]any
		if json.Unmarshal([]byte(raw), &msg) == nil && msg["header"] == header {
			result = append(result, msg)
		}
	}
	return result
}

// last returns the seat's latest message with one of the headers, and its index.
func (h *harness) last(seat int, headers ...string) (map[string]any, int) {
	h.mu.Lock()
	defer h.mu.Unlock()

	for i := len(h.msgs[seat]) - 1; i >= 0; i-- {
		var msg map[string]any
		if json.Unmarshal([]byte(h.msgs[seat][i]), &msg) != nil {
			continue
		}
		for _, header := range headers {
			if msg["header"] == header {
				return msg, i
			}
		}
	}
	return nil, -1
}

func (h *harness) state(seat int) map[string]any {
	msg, _ := h.last(seat, "state_update")
	if msg == nil {
		h.t.Fatalf("seat %d has no state yet", seat)
	}
	return msg["state"].(map[string]any)
}

func list(v any) []map[string]any {
	items, _ := v.([]any)
	result := make([]map[string]any, 0, len(items))
	for _, item := range items {
		result = append(result, item.(map[string]any))
	}
	return result
}

func flags(card map[string]any) int {
	f, _ := card["flags"].(float64)
	return int(f)
}

// player is a simple scripted player for both seats. Every choice it makes
// comes from the messages the seats received, so a seeded duel driven by it
// plays out the same way every time.
type player struct {
	h        *harness
	attempts map[string]int
	tried    map[string]bool
	inputs   int
}

func newPlayer(h *harness) *player {
	return &player{h: h, attempts: map[string]int{}, tried: map[string]bool{}}
}

// step sends one input and waits for the duel to settle. It returns false once
// the duel has ended.
func (p *player) step() bool {
	h := p.h
	h.t.Helper()

	ds := h.d.DebugState()
	if ds.Ended {
		return false
	}

	p.inputs++

	if ds.Prompt != 0 {
		p.answer(ds.Prompt)
		return true
	}

	turn := ds.Turn
	key := func(what, id string) string { return fmt.Sprintf("%d/%s/%s", ds.TurnsPlayed, what, id) }
	st := h.state(turn)
	me := st["me"].(map[string]any)

	hand := list(me["hand"])
	if added, _ := st["hasAddedManaThisRound"].(bool); !added && len(hand) > 0 && !p.tried[key("charge", "")] {
		p.tried[key("charge", "")] = true
		h.send(turn, map[string]any{"header": "add_to_manazone", "virtualId": hand[len(hand)-1]["virtualId"]})
		return true
	}

	for _, card := range hand {
		id := card["virtualId"].(string)
		if flags(card)&2 != 0 && !p.tried[key("play", id)] {
			p.tried[key("play", id)] = true
			h.send(turn, map[string]any{"header": "add_to_playzone", "virtualId": id})
			return true
		}
	}

	for _, card := range list(me["playzone"]) {
		id := card["virtualId"].(string)
		if flags(card)&1 == 0 && !p.tried[key("attack", id)] {
			p.tried[key("attack", id)] = true
			h.send(turn, map[string]any{"header": "attack_player", "virtualId": id})
			return true
		}
	}

	h.send(turn, map[string]any{"header": "end_turn"})
	return true
}

func (p *player) answer(seat int) {
	h := p.h
	msg, index := h.last(seat, "action", "show_cards_non_dismissible")
	if msg == nil {
		h.t.Fatalf("seat %d is being asked something but received no prompt", seat)
	}

	key := fmt.Sprintf("%d/%d", seat, index)
	attempt := p.attempts[key]
	p.attempts[key] = attempt + 1

	cancel := map[string]any{"header": "action", "cancel": true}

	if msg["header"] == "show_cards_non_dismissible" {
		h.send(seat, cancel)
		return
	}

	cancellable, _ := msg["cancellable"].(bool)
	minimum := int(msg["minSelections"].(float64))
	maximum := int(msg["maxSelections"].(float64))

	if attempt >= 3 && cancellable {
		h.send(seat, cancel)
		return
	}
	if attempt >= 40 {
		h.t.Fatalf("seat %d could not answer prompt %q", seat, msg["text"])
	}

	switch msg["actionType"] {
	case "count":
		h.send(seat, map[string]any{"header": "action", "count": minimum})
		return
	case "question", "searchable":
		if attempt > 0 && cancellable {
			h.send(seat, cancel)
			return
		}
		h.send(seat, map[string]any{"header": "action", "count": 0, "cancel": false})
		return
	case "order":
		ids := make([]string, 0)
		for _, card := range list(msg["cards"]) {
			ids = append(ids, card["virtualId"].(string))
		}
		h.send(seat, map[string]any{"header": "action", "cards": ids})
		return
	}

	unselectable := map[string]bool{}
	for _, card := range list(msg["unselectableCards"]) {
		unselectable[card["virtualId"].(string)] = true
	}

	candidates := make([]string, 0)
	collect := func(cards any) {
		for _, card := range list(cards) {
			if id := card["virtualId"].(string); !unselectable[id] {
				candidates = append(candidates, id)
			}
		}
	}
	if groups, ok := msg["cards"].(map[string]any); ok {
		keys := make([]string, 0, len(groups))
		for k := range groups {
			keys = append(keys, k)
		}
		sortStrings(keys)
		for _, k := range keys {
			collect(groups[k])
		}
	} else {
		collect(msg["cards"])
	}

	n := minimum
	if n == 0 && maximum > 0 && len(candidates) > 0 {
		n = 1
	}
	if n > len(candidates) {
		n = len(candidates)
	}

	picked := make([]string, 0, n)
	for i := 0; i < n; i++ {
		picked = append(picked, candidates[(i+attempt)%len(candidates)])
	}

	h.send(seat, map[string]any{"header": "action", "cards": picked, "count": len(picked)})
}

func sortStrings(s []string) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j] < s[j-1]; j-- {
			s[j], s[j-1] = s[j-1], s[j]
		}
	}
}

// playOut plays the duel to its end, recording the debug state after every
// input.
func (p *player) playOut(maxInputs int) []string {
	p.h.t.Helper()

	states := []string{debugJSON(p.h.d)}
	for p.step() {
		states = append(states, debugJSON(p.h.d))
		if p.inputs > maxInputs {
			p.h.t.Fatalf("the duel did not end within %d inputs", maxInputs)
		}
	}
	return states
}

func debugJSON(d *Duel) string {
	data, _ := json.Marshal(d.DebugState())
	return string(data)
}
