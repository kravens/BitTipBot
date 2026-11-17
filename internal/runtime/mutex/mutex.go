package mutex

import (
	"context"
	"fmt"
	"net/http"
	"sort"
	"sync"

	"github.com/gorilla/mux"
	log "github.com/sirupsen/logrus"
)

// entry is one named lock. refs counts the holder and all waiters, so the
// entry stays in the map (and every caller uses the same sync.Mutex) until
// the last of them has unlocked.
type entry struct {
	mu   sync.Mutex
	refs int
	held bool
}

var (
	mapMu sync.Mutex
	locks = map[string]*entry{}
	// soft counts how often a request (uid) has locked an object, so handlers
	// that call each other with the same context do not deadlock on it.
	soft = map[string]int{}
)

// IsEmpty reports whether no lock is held or waited for.
func IsEmpty() bool {
	mapMu.Lock()
	defer mapMu.Unlock()
	return len(locks) == 0
}

func keys() []string {
	mapMu.Lock()
	defer mapMu.Unlock()
	k := make([]string, 0, len(locks))
	for s := range locks {
		k = append(k, s)
	}
	sort.Strings(k)
	return k
}

func ServeHTTP(w http.ResponseWriter, r *http.Request) {
	k := keys()
	w.Write([]byte(fmt.Sprintf("Current number of locks: %d\nLocks: %+v\nUse /mutex/unlock/{id} endpoint to mutex", len(k), k)))
}

func UnlockHTTP(w http.ResponseWriter, r *http.Request) {
	id := mux.Vars(r)["id"]
	if !Unlock(id) {
		w.Write([]byte(fmt.Sprintf("Mutex %s not found!", id)))
		return
	}
	k := keys()
	w.Write([]byte(fmt.Sprintf("Unlocked mutex %s.\nCurrent number of locks: %d\nLocks: %+v", id, len(k), k)))
}

func softKey(ctx context.Context, s string) string {
	uid, _ := ctx.Value("uid").(string)
	if len(uid) == 0 {
		log.Error("[Mutex] context has no uid")
	}
	if len(s) == 0 {
		log.Error("[Mutex] lock name is empty")
	}
	return uid + "|" + s
}

// LockWithContext locks s unless the request in ctx already holds it.
// LockWithContext should be used to lock objects like faucets etc. The
// context carries a uid that is unique to each request (message, button
// press, etc.); nested handlers of the same request only count up.
func LockWithContext(ctx context.Context, s string) {
	k := softKey(ctx, s)
	mapMu.Lock()
	n := soft[k]
	soft[k] = n + 1
	mapMu.Unlock()
	if n == 0 {
		Lock(s)
	} else {
		log.Tracef("[Mutex] Skip lock %s (nLocks: %d)", s, n)
	}
}

// UnlockWithContext undoes one LockWithContext and unlocks s when the
// request's last hold on it is released.
func UnlockWithContext(ctx context.Context, s string) {
	k := softKey(ctx, s)
	mapMu.Lock()
	n := soft[k]
	if n == 0 {
		mapMu.Unlock()
		log.Errorf("[Mutex] UnlockWithContext %s without a lock. Skip.", s)
		return
	}
	n--
	if n == 0 {
		delete(soft, k)
	} else {
		soft[k] = n
	}
	mapMu.Unlock()
	if n == 0 {
		Unlock(s)
	} else {
		log.Tracef("[Mutex] Skip unlock %s (nLocks: %d)", s, n)
	}
}

// Lock blocks until the named lock s is free and takes it.
func Lock(s string) {
	log.Tracef("[Mutex] Attempt Lock %s", s)
	mapMu.Lock()
	e := locks[s]
	if e == nil {
		e = &entry{}
		locks[s] = e
	}
	e.refs++
	mapMu.Unlock()

	e.mu.Lock()

	mapMu.Lock()
	e.held = true
	mapMu.Unlock()
	log.Tracef("[Mutex] Locked %s", s)
}

// Unlock releases the named lock s. Unlocking a lock that is not held is
// logged and ignored (sync.Mutex would abort the whole process instead).
// It reports whether a lock was released.
func Unlock(s string) bool {
	mapMu.Lock()
	e := locks[s]
	if e == nil || !e.held {
		mapMu.Unlock()
		log.Errorf("[Mutex] ⚠️⚠️⚠️ Unlock %s not held. Skip.", s)
		return false
	}
	e.held = false
	e.refs--
	if e.refs == 0 {
		delete(locks, s)
	}
	mapMu.Unlock()
	e.mu.Unlock()
	log.Tracef("[Mutex] Unlocked %s", s)
	return true
}
