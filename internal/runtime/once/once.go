package once

import (
	"fmt"

	cmap "github.com/orcaman/concurrent-map"
	log "github.com/sirupsen/logrus"
)

var onceMap cmap.ConcurrentMap

func init() {
	onceMap = cmap.New()
}

func New(objectKey string) {
	onceMap.Set(objectKey, cmap.New())
}

// Once creates a map of keys k1 with a map of keys k2.
// The idea is that an object with ID k1 can create a list of users k2
// that have already interacted with the object. If the user k2 is in the list,
// the object is not allowed to accessed again.
func Once(k1, k2 string) error {
	// SetIfAbsent: two first callers must end up with the same user map
	if onceMap.SetIfAbsent(k1, cmap.New()) {
		log.Tracef("[Once] Added key %s to onceMap", k1)
	}
	i, _ := onceMap.Get(k1)
	return setOrReturn(i.(cmap.ConcurrentMap), k2)
}

// setOrReturn sets the key k2 in the map i if it is not already set.
func setOrReturn(objectMap cmap.ConcurrentMap, k2 string) error {
	if !objectMap.SetIfAbsent(k2, true) {
		return fmt.Errorf("[Once] %s already consumed object", k2)
	}
	return nil
}

// Remove removes the key k1 from the map. Should be called after Once was called and
// the object k1 finished.
func Remove(k1 string) {
	onceMap.Remove(k1)
	log.Tracef("[Once] Removed key %s from onceMap (len=%d)", k1, len(onceMap.Keys()))
}
