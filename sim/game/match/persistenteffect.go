package match

import "slices"

var seqID = 1

type PersistentHandlerFunc func(ctx2 *Context, exit func())

type PersistentEffect struct {
	exit   func()
	effect PersistentHandlerFunc
}

func (match *Match) ApplyPersistentEffect(f PersistentHandlerFunc) {
	key := seqID

	fx := PersistentEffect{
		exit:   func() { match.RemovePersistentEffect(key) },
		effect: f,
	}

	match.persistentEffects[key] = fx

	seqID++
}

func (match *Match) RemovePersistentEffect(id int) {
	_, ok := match.persistentEffects[id]
	if ok {
		delete(match.persistentEffects, id)
	}
}

// persistentEffectKeys returns the keys of the match's persistent effects in
// the order they were applied.
func (match *Match) persistentEffectKeys() []int {
	keys := make([]int, 0, len(match.persistentEffects))
	for key := range match.persistentEffects {
		keys = append(keys, key)
	}

	slices.Sort(keys)

	return keys
}
