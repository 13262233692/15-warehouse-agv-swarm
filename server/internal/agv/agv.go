package agv

import (
	"math"
	"sync"
	"time"
	"warehouse-agv-swarm/internal/gridmap"
)

const (
	StatusIdle     = 0
	StatusMoving   = 1
	StatusPicking  = 2
	StatusPlacing  = 3
	StatusCharging = 4
	StatusError    = 5
	StatusWaiting  = 6
)

type AGV struct {
	ID          int
	X           int
	Y           int
	TargetX     int
	TargetY     int
	Path        []gridmap.Position
	PathIndex   int
	PathOffset  float64
	Status      int
	HasCargo    bool
	Battery     int
	Speed       float64
	CurrentTask int
	mu          sync.RWMutex
	LastUpdate  int64
}

type Manager struct {
	AGVs map[int]*AGV
	mu   sync.RWMutex
}

func NewManager(count int, gm *gridmap.GridMap) *Manager {
	m := &Manager{
		AGVs: make(map[int]*AGV),
	}
	charging := gm.GetChargingPositions()
	idx := 0
	for i := 0; i < count; i++ {
		var x, y int
		if idx < len(charging) {
			x = charging[idx].X
			y = charging[idx].Y
			idx++
		} else {
			for gx := 0; gx < gm.Width; gx++ {
				found := false
				for gy := 0; gy < gm.Height; gy++ {
					if gm.IsWalkable(gx, gy) {
						occupied := false
						for _, a := range m.AGVs {
							if a.X == gx && a.Y == gy {
								occupied = true
								break
							}
						}
						if !occupied {
							x = gx
							y = gy
							found = true
							break
						}
					}
				}
				if found {
					break
				}
			}
		}
		agv := &AGV{
			ID:         i,
			X:          x,
			Y:          y,
			TargetX:    x,
			TargetY:    y,
			Status:     StatusIdle,
			Battery:    100,
			Speed:      0.15,
			PathIndex:  0,
			PathOffset: 0,
			LastUpdate: time.Now().UnixNano(),
		}
		m.AGVs[i] = agv
	}
	return m
}

func (m *Manager) GetAll() map[int]*AGV {
	m.mu.RLock()
	defer m.mu.RUnlock()
	result := make(map[int]*AGV)
	for k, v := range m.AGVs {
		result[k] = v
	}
	return result
}

func (m *Manager) Get(id int) *AGV {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.AGVs[id]
}

func (a *AGV) SetPath(path []gridmap.Position, taskID int) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.Path = path
	a.PathIndex = 0
	a.PathOffset = 0
	a.CurrentTask = taskID
	if len(path) > 0 {
		last := path[len(path)-1]
		a.TargetX = last.X
		a.TargetY = last.Y
		a.Status = StatusMoving
	}
}

func (a *AGV) Update(deltaMs int64) (gridmap.Position, bool) {
	a.mu.Lock()
	defer a.mu.Unlock()

	if a.Status != StatusMoving || len(a.Path) == 0 {
		return gridmap.Position{X: a.X, Y: a.Y}, false
	}

	a.LastUpdate = time.Now().UnixNano()
	prevX := a.X
	prevY := a.Y

	moveDist := a.Speed * float64(deltaMs) / 16.0

	for moveDist > 0 && a.PathIndex < len(a.Path)-1 {
		current := a.Path[a.PathIndex]
		next := a.Path[a.PathIndex+1]
		dx := float64(next.X - current.X)
		dy := float64(next.Y - current.Y)
		segLen := math.Sqrt(dx*dx + dy*dy)

		if segLen == 0 {
			a.PathIndex++
			a.PathOffset = 0
			continue
		}

		remain := segLen - a.PathOffset
		if moveDist >= remain {
			moveDist -= remain
			a.PathIndex++
			a.PathOffset = 0
			a.X = next.X
			a.Y = next.Y
		} else {
			a.PathOffset += moveDist
			t := a.PathOffset / segLen
			a.X = int(float64(current.X) + dx*t + 0.5)
			a.Y = int(float64(current.Y) + dy*t + 0.5)
			moveDist = 0
		}
	}

	reached := a.PathIndex >= len(a.Path)-1
	if reached && len(a.Path) > 0 {
		last := a.Path[len(a.Path)-1]
		a.X = last.X
		a.Y = last.Y
		a.Status = StatusIdle
	}

	return gridmap.Position{X: a.X, Y: a.Y}, (prevX != a.X) || (prevY != a.Y)
}

func (a *AGV) PickUp() {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.HasCargo = true
	a.Status = StatusPicking
}

func (a *AGV) PutDown() {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.HasCargo = false
	a.Status = StatusPlacing
}

func (a *AGV) SetStatus(s int) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.Status = s
}

func (a *AGV) GetStatus() int {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.Status
}

func (a *AGV) GetPosition() (int, int, bool) {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.X, a.Y, a.HasCargo
}
