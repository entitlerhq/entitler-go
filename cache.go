package entitler

import (
	"container/list"
	"context"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Cache is a store for kept answers, which the server client can share between servers (Redis,
// Memcached…) through [WithCache]. Implementations must be safe for concurrent use. A Get error
// counts as a miss and a Set error is skipped; both go to onError and neither fails the call.
// Entries are private to this SDK: sharing a store with SDKs in other languages is not supported.
type Cache interface {
	// Get answers the entry kept under key, and whether there is one.
	Get(ctx context.Context, key string) (CacheEntry, bool, error)
	// Set keeps an entry under key. ttl is staleFor plus the entry's max-age: a store that can
	// expire entries should expire this one after ttl, since it can never answer later.
	Set(ctx context.Context, key string, entry CacheEntry, ttl time.Duration) error
}

// CacheEntry is a kept answer: a plain value any encoder can keep.
type CacheEntry struct {
	// V is the entry format's version, 1.
	V int
	// Body is the answer body, as raw JSON text.
	Body string
	// ETag is the answer's ETag, or "".
	ETag string
	// CacheControl is the answer's Cache-Control directives, or "".
	CacheControl string
	// Age is the answer's Age header in seconds, or 0.
	Age int64
	// ReceivedAt is when the answer was received.
	ReceivedAt time.Time
}

// MemoryCache is the in-memory answer store, least recently used first out. It is safe for
// concurrent use.
type MemoryCache struct {
	mu      sync.Mutex
	size    int
	order   *list.List
	entries map[string]*list.Element
}

type memoryItem struct {
	key     string
	entry   CacheEntry
	expires time.Time
}

// NewMemoryCache answers an in-memory store holding at most size answers (at least 1).
func NewMemoryCache(size int) *MemoryCache {
	return &MemoryCache{size: max(size, 1), order: list.New(), entries: map[string]*list.Element{}}
}

// Get answers the entry kept under key.
func (m *MemoryCache) Get(_ context.Context, key string) (CacheEntry, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	el, ok := m.entries[key]
	if !ok {
		return CacheEntry{}, false, nil
	}
	item := el.Value.(*memoryItem)
	if time.Now().After(item.expires) {
		m.order.Remove(el)
		delete(m.entries, key)
		return CacheEntry{}, false, nil
	}
	m.order.MoveToBack(el)
	return item.entry, true, nil
}

// Set keeps an entry under key for ttl, evicting the least recently used entry when full.
func (m *MemoryCache) Set(_ context.Context, key string, entry CacheEntry, ttl time.Duration) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	expires := time.Now().Add(clampDuration(ttl))
	if el, ok := m.entries[key]; ok {
		el.Value = &memoryItem{key: key, entry: entry, expires: expires}
		m.order.MoveToBack(el)
		return nil
	}
	m.entries[key] = m.order.PushBack(&memoryItem{key: key, entry: entry, expires: expires})
	for m.order.Len() > m.size {
		oldest := m.order.Front()
		m.order.Remove(oldest)
		delete(m.entries, oldest.Value.(*memoryItem).key)
	}
	return nil
}

func (m *MemoryCache) clear() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.order.Init()
	clear(m.entries)
}

// maxDuration bounds every duration handed to a timer or a clock, so none overflows.
const maxDuration = 100 * 365 * 24 * time.Hour

func clampDuration(d time.Duration) time.Duration {
	return min(max(d, 0), maxDuration)
}

func directive(control, name string) bool {
	for part := range strings.SplitSeq(control, ",") {
		part = strings.TrimSpace(part)
		if k, _, _ := strings.Cut(part, "="); strings.EqualFold(strings.TrimSpace(k), name) {
			return true
		}
	}
	return false
}

func maxAgeOf(control string) (time.Duration, bool) {
	for part := range strings.SplitSeq(control, ",") {
		k, v, ok := strings.Cut(strings.TrimSpace(part), "=")
		if !ok || !strings.EqualFold(strings.TrimSpace(k), "max-age") {
			continue
		}
		return secondsOf(strings.Trim(strings.TrimSpace(v), `"`))
	}
	return 0, false
}

// secondsOf reads one or more ASCII digits of seconds, saturating rather than overflowing.
func secondsOf(v string) (time.Duration, bool) {
	if v == "" {
		return 0, false
	}
	for i := 0; i < len(v); i++ {
		if v[i] < '0' || v[i] > '9' {
			return 0, false
		}
	}
	n, err := strconv.ParseInt(v, 10, 64)
	if err != nil || n > int64(maxDuration/time.Second) {
		return maxDuration, true
	}
	return time.Duration(n) * time.Second, true
}

const trackedCustomers = 10000

// generations tracks writes per customer, so a read that raced a write is never kept as fresh and
// entries kept before a write are revalidated. It is bounded: a forgotten customer raises a floor
// at or below which every entry is revalidated.
type generations struct {
	mu        sync.Mutex
	counter   uint64
	forgotten uint64
	byID      map[string]*list.Element
	order     *list.List
	floor     time.Time
	limit     int
}

type generation struct {
	customer string
	number   uint64
	at       time.Time
}

type genMark struct {
	number    uint64
	forgotten uint64
}

func newGenerations(limit int) *generations {
	return &generations{byID: map[string]*list.Element{}, order: list.New(), limit: limit}
}

func (g *generations) mark(customer string) genMark {
	g.mu.Lock()
	defer g.mu.Unlock()
	m := genMark{forgotten: g.forgotten}
	if el, ok := g.byID[customer]; ok {
		m.number = el.Value.(*generation).number
	}
	return m
}

// moved reports whether the customer was written since the mark.
func (g *generations) moved(customer string, since genMark) bool {
	now := g.mark(customer)
	return now.number != since.number || now.number == 0 && now.forgotten != since.forgotten
}

func (g *generations) bump(customer string, at time.Time) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.counter++
	if el, ok := g.byID[customer]; ok {
		gen := el.Value.(*generation)
		gen.number, gen.at = g.counter, at
		g.order.MoveToBack(el)
		return
	}
	g.byID[customer] = g.order.PushBack(&generation{customer: customer, number: g.counter, at: at})
	if g.order.Len() > g.limit {
		oldest := g.order.Front()
		gen := oldest.Value.(*generation)
		g.order.Remove(oldest)
		delete(g.byID, gen.customer)
		g.forgotten++
		if gen.at.After(g.floor) {
			g.floor = gen.at
		}
	}
}

// keptAfterWrites reports whether an entry received at receivedAt postdates every write tracked
// for the customer and the floor.
func (g *generations) keptAfterWrites(customer string, receivedAt time.Time) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	if !receivedAt.After(g.floor) {
		return false
	}
	el, ok := g.byID[customer]
	return !ok || receivedAt.After(el.Value.(*generation).at)
}

// outage is the window after a read failed as unreachable, while reads with a young kept entry
// answer it without a request and one test request at a time asks the API.
type outage struct {
	mu      sync.Mutex
	until   time.Time
	down    bool
	probing bool
}

// enter decides how a read with a kept entry proceeds: served stale without a request, or sent,
// and whether it is the test request.
func (o *outage) enter(now time.Time, usable bool) (stale, probe bool) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if !o.down {
		return false, false
	}
	if usable && (now.Before(o.until) || o.probing) {
		return true, false
	}
	if o.probing {
		return false, false
	}
	o.probing = true
	return false, true
}

func (o *outage) leave(probe bool) {
	if !probe {
		return
	}
	o.mu.Lock()
	o.probing = false
	o.mu.Unlock()
}

func (o *outage) fail(now time.Time, retryAfter *time.Duration, staleFor time.Duration) {
	window := 30 * time.Second
	if retryAfter != nil && *retryAfter > window {
		window = *retryAfter
	}
	window = clampDuration(min(window, staleFor))
	o.mu.Lock()
	o.down, o.until = true, now.Add(window)
	o.mu.Unlock()
}

func (o *outage) recover() {
	o.mu.Lock()
	o.down, o.until = false, time.Time{}
	o.mu.Unlock()
}
