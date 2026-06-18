package order

import (
	"sync"
	"sync/atomic"
	"warehouse-agv-swarm/internal/gridmap"
)

const (
	TypeInbound  = 1
	TypeOutbound = 2
)

const (
	StatusPending    = 0
	StatusAssigned   = 1
	StatusInProgress = 2
	StatusCompleted  = 3
	StatusFailed     = 4
)

type Order struct {
	ID        int
	Type      int
	Status    int
	AGVID     int
	PickupX   int
	PickupY   int
	DropoffX  int
	DropoffY  int
	CreatedAt int64
}

type Task struct {
	ID      int
	OrderID int
	AGVID   int
	Phase   int
	X       int
	Y       int
}

type Manager struct {
	orders     map[int]*Order
	pending    []*Order
	orderSeq   int64
	taskSeq    int64
	mu         sync.RWMutex
}

func NewManager() *Manager {
	return &Manager{
		orders:  make(map[int]*Order),
		pending: []*Order{},
	}
}

func (m *Manager) CreateInbound(pickup, dropoff gridmap.Position) *Order {
	id := int(atomic.AddInt64(&m.orderSeq, 1))
	o := &Order{
		ID:       id,
		Type:     TypeInbound,
		Status:   StatusPending,
		PickupX:  pickup.X,
		PickupY:  pickup.Y,
		DropoffX: dropoff.X,
		DropoffY: dropoff.Y,
	}
	m.mu.Lock()
	m.orders[id] = o
	m.pending = append(m.pending, o)
	m.mu.Unlock()
	return o
}

func (m *Manager) CreateOutbound(pickup, dropoff gridmap.Position) *Order {
	id := int(atomic.AddInt64(&m.orderSeq, 1))
	o := &Order{
		ID:       id,
		Type:     TypeOutbound,
		Status:   StatusPending,
		PickupX:  pickup.X,
		PickupY:  pickup.Y,
		DropoffX: dropoff.X,
		DropoffY: dropoff.Y,
	}
	m.mu.Lock()
	m.orders[id] = o
	m.pending = append(m.pending, o)
	m.mu.Unlock()
	return o
}

func (m *Manager) GetPending() []*Order {
	m.mu.RLock()
	defer m.mu.RUnlock()
	result := make([]*Order, len(m.pending))
	copy(result, m.pending)
	return result
}

func (m *Manager) TakePending() *Order {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.pending) == 0 {
		return nil
	}
	o := m.pending[0]
	m.pending = m.pending[1:]
	o.Status = StatusAssigned
	return o
}

func (m *Manager) Get(id int) *Order {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.orders[id]
}

func (m *Manager) UpdateStatus(id, status int, agvID int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if o, ok := m.orders[id]; ok {
		o.Status = status
		o.AGVID = agvID
	}
}

func (m *Manager) GetAll() map[int]*Order {
	m.mu.RLock()
	defer m.mu.RUnlock()
	result := make(map[int]*Order)
	for k, v := range m.orders {
		result[k] = v
	}
	return result
}

func (m *Manager) NextTaskID() int {
	return int(atomic.AddInt64(&m.taskSeq, 1))
}
