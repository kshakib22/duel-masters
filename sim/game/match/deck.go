package match

import (
	"fmt"
	"slices"
	"strings"
	"sync"
)

var cardUIDs []string
var nameToUID = make(map[string]string)
var lookupMutex sync.RWMutex

func InitDecks() {
	lookupMutex.Lock()
	defer lookupMutex.Unlock()

	cardUIDs = make([]string, 0, len(ctors))
	nameToUID = make(map[string]string)
	for uid, ctor := range ctors {
		cardUIDs = append(cardUIDs, uid)
		if ctor != nil {
			c := &Card{}
			ctor(c)
			if c.Name != "" {
				nameToUID[c.Name] = uid
				nameToUID[strings.ToLower(c.Name)] = uid
			}
		}
	}

	slices.Sort(cardUIDs)
}

func GetCardImages() []string {
	lookupMutex.RLock()
	defer lookupMutex.RUnlock()

	return slices.Clone(cardUIDs)
}

// ResolveCardUID returns the card UID from either a direct UID or a card name.
func ResolveCardUID(idOrName string) (string, error) {
	if _, err := CardCtor(idOrName); err == nil {
		return idOrName, nil
	}

	lookupMutex.RLock()
	uid, ok := nameToUID[idOrName]
	if !ok {
		uid, ok = nameToUID[strings.ToLower(idOrName)]
	}
	lookupMutex.RUnlock()

	if ok {
		return uid, nil
	}

	// Fallback in case InitDecks() was not called or card was added dynamically
	lower := strings.ToLower(idOrName)
	for u, ctor := range ctors {
		if ctor != nil {
			c := &Card{}
			ctor(c)
			if c.Name == idOrName || strings.ToLower(c.Name) == lower {
				return u, nil
			}
		}
	}

	return "", fmt.Errorf("unknown card %q", idOrName)
}
