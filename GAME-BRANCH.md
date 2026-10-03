# The `game` branch: every change we make to shobu's engine

This branch is shobu's engine plus our additions for the Duel Sandbox and the story game
(`kshakib22/duelmasters-archive`, `game/`). The fork's `main` stays an untouched copy of
`sindreslungaard/duel-masters`; all our work lives here.

**Rules for this list:** every change to the engine gets a number, in the right category,
the moment it is committed. Numbers are never reused or renumbered. A change that is later
undone stays, marked "Removed in <commit>". Every change is additive: with no options set,
the engine behaves exactly like upstream.

**Taking shobu's updates:** fetch `upstream`, fast-forward `main` to `upstream/main`, merge
`main` into `game`, run `go test ./...` and `go test -race ./...` in `sim/`. If
`git diff <old> <new> -- sim/DuelMastersCards.json` is not empty, the card database needs
scripts 02, 03 and 04 re-run (on a machine with PowerShell) in the same parent commit.

## A. Determinism and replay

1. **Persistent effects run in the order they were applied** instead of Go's random map
   order (`game/match/persistenteffect.go`, `HandleFx`). Upstreamable on its own. `18de6151`
2. **One random source per match** (`Match.Rand`): the shuffle, the coin toss and random
   discards (`fx/common_effects.go`, `cards/dm07/hedrian.go`) use it. Seeded through match
   options; unseeded matches keep Go's global source. `5287e940`
3. **Numbered card ids in seeded matches** (`c1`, `c2`, ...) instead of random short ids,
   so the same seed gives the same ids. `5287e940`

## B. House rules (`match.MatchOptions`, `game/match/options.go`)

4. **Shields per seat** (default 5). `5287e940`
5. **Opening hand per seat** (default 5). `5287e940`
6. **Charges per turn per seat** (default 1); `HasChargedMana` still means "no charges
   left", so shobu's board reads it unchanged. `5287e940`
7. **First player skips their first draw**, on or off (default on). `5287e940`
8. **Deck-out** "lose" or "off" (default "lose"); with "off" an empty deck draws nothing.
   `5287e940`
9. **Turn limit** ("survive N turns", a named seat wins, reason `turn_limit`). `5287e940`
10. **Sandbox console per match**, as `local_dev=true` does for every match. `5287e940`

## C. Starting positions (`game/match/setup.go`)

11. **`StartFromSetup`**: any position for both seats (deck order, hand, mana tapped or
    not, shields face up or down, battle zone, graveyard, evolution bases), whose turn and
    which turn, starting at the turn's beginning or straight at the charge step. `5287e940`
12. **Quiet and summon placement.** Quiet creatures enter from the hidden zone, so
    continuous effects install and "when summoned" effects don't fire; summoned creatures
    enter from the hand after every quiet one. Proven for a continuous effect (Elf-X), a
    summon effect (Meteosaur, Bronze-Arm Tribe), an evolution pile (Valdios on Vorg), a
    blocker (Szubs Kin) and a shield trigger (Holy Awe). `5287e940`
13. **`SpawnCard` accepts the hidden zone.** `5287e940`

## D. What the engine reports

14. **Extended state** (opt-in): `power`, `cost`, and flag bits 16 (summoning sick) and 32
    (blocker) in state updates, for computer opponents. Proven not to change play.
    `5287e940`
15. **End reason** in `duel_finished`: `shields`, `deck_out`, `resign`, `turn_limit`,
    `card_effect`, `disconnect` (`Match.EndWithReason`). `5287e940`

## E. Driving a match without a network

16. **Settle signals**: event loop busy flag, processed-event count (counted after the
    loop's drain), parallel events in flight, per player "awaiting an answer" and answers
    received, `TurnsPlayed`, and `ScheduleSequential`. `5287e940`
17. **`sim/bridge`**: runs one duel in-process from a JSON spec, queues inputs and
    delivers them only when the engine has settled, reports unknown card ids, keeps the
    delivered input log, and exposes the debug view (nothing hidden) and the result.
    9f1d5bb4
18. **`sim/cmd/wasm`**: the browser build, `globalThis.duelEngine`. 9f1d5bb4
19. **`internal/mail.go` is left out of the browser build** (`//go:build !js`, with a
    stub), which drops the AWS library from it. 9f1d5bb4

## F. The duel board

20. **`transport?` prop on `Duel` and `useDuel`** (`DuelTransport`: connect, send, close),
    allowing duel interfaces to be driven in-process or by a Web Worker without opening a
    WebSocket. Defaults to standard WebSocket behavior.
21. **`cardImageUrl?` prop on `Duel`, `Card`, `Action`, and previews** (`CardImageUrlResolver`:
    `(uid?: string, back?: boolean) => string`), replacing all ten hardcoded scans.shobu.io
    image URLs with configurable asset resolution. Defaults to scans.shobu.io URLs.
