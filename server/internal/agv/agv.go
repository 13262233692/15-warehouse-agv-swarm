package agv

import (
	"math"
	"sync"
	"time"
	"warehouse-agv-swarm/internal/gridmap"
)

const (
	StatusIdle       = 0
	StatusMoving     = 1
	StatusPicking    = 2
	StatusPlacing    = 3
	StatusCharging   = 4
	StatusError      = 5
	StatusWaiting    = 6
	StatusReturning  = 7
	StatusLowBattery = 8
)

type BatteryReport struct {
	SOC         int
	Temperature float64
	Voltage     float64
	Current     float64
}

type AGV struct {
	ID              int
	X               int
	Y               int
	TargetX         int
	TargetY         int
	Path            []gridmap.Position
	PathIndex       int
	PathOffset      float64
	Status          int
	HasCargo        bool
	Battery         int
	BatteryTemp     float64
	Voltage         float64
	CurrentDraw     float64
	Speed           float64
	CurrentTask     int
	TotalDistance   float64
	TaskDistance    float64
	Charging        bool
	NeedsCharge     bool
	ReturnToCharger bool
	PrevX           int
	PrevY           int
	mu              sync.RWMutex
	LastUpdate      int64
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
		isCharging := false
		if idx < len(charging) {
			x = charging[idx].X
			y = charging[idx].Y
			idx++
			isCharging = true
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
		startSOC := 100
		if !isCharging {
			if i%5 == 0 {
				startSOC = 12 + (i*7)%18
			} else if i%3 == 0 {
				startSOC = 25 + (i*5)%20
			} else {
				startSOC = 55 + (i*3)%40
			}
		}
		agv := &AGV{
			ID:              i,
			X:               x,
			Y:               y,
			PrevX:           x,
			PrevY:           y,
			TargetX:         x,
			TargetY:         y,
			Status:          StatusIdle,
			Battery:         startSOC,
			BatteryTemp:     25.0,
			Voltage:         48.0,
			CurrentDraw:     0.0,
			Speed:           0.15,
			PathIndex:       0,
			PathOffset:      0,
			LastUpdate:      time.Now().UnixNano(),
			Charging:      isCharging,
			NeedsCharge:     false,
			ReturnToCharger: false,
		}
		if isCharging {
			agv.Status = StatusCharging
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
	a.TaskDistance = 0
	if len(path) > 0 {
		last := path[len(path)-1]
		a.TargetX = last.X
		a.TargetY = last.Y
		if a.Status != StatusCharging && a.Status != StatusReturning && a.Status != StatusLowBattery {
			a.Status = StatusMoving
		}
	}
}

func (a *AGV) Update(deltaMs int64) (gridmap.Position, bool) {
	a.mu.Lock()
	defer a.mu.Unlock()

	a.PrevX = a.X
	a.PrevY = a.Y
	moved := false

	if a.Status == StatusCharging {
		a.chargeTick(deltaMs)
		return gridmap.Position{X: a.X, Y: a.Y}, false
	}

	if a.Status != StatusMoving && a.Status != StatusReturning && a.Status != StatusLowBattery {
		a.idleTick(deltaMs)
		return gridmap.Position{X: a.X, Y: a.Y}, false
	}

	if len(a.Path) == 0 {
		a.idleTick(deltaMs)
		return gridmap.Position{X: a.X, Y: a.Y}, false
	}

	a.LastUpdate = time.Now().UnixNano()

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
			a.TaskDistance += segLen
			a.TotalDistance += segLen
			a.consumeBatteryMove(segLen)
			moved = true
		} else {
			a.PathOffset += moveDist
			t := a.PathOffset / segLen
			a.X = int(float64(current.X) + dx*t + 0.5)
			a.Y = int(float64(current.Y) + dy*t + 0.5)
			a.TaskDistance += moveDist
			a.TotalDistance += moveDist
			a.consumeBatteryMove(moveDist)
			moveDist = 0
			moved = true
		}
	}

	reached := a.PathIndex >= len(a.Path)-1
	if reached && len(a.Path) > 0 {
		last := a.Path[len(a.Path)-1]
		a.X = last.X
		a.Y = last.Y
		if a.Status == StatusReturning || a.Status == StatusLowBattery {
			a.Status = StatusCharging
			a.Charging = true
			a.ReturnToCharger = false
			a.NeedsCharge = false
		} else {
			a.Status = StatusIdle
		}
	}

	if moved {
		prevD := math.Sqrt(float64((a.X-a.PrevX)*(a.X-a.PrevX) + (a.Y-a.PrevY)*(a.Y-a.PrevY)))
		_ = prevD
	}

	return gridmap.Position{X: a.X, Y: a.Y}, (a.PrevX != a.X) || (a.PrevY != a.Y)
}

func (a *AGV) consumeBatteryMove(dist float64) {
	baseDrainPerCell := 0.018
	cargoFactor := 1.0
	if a.HasCargo {
		cargoFactor = 1.8
	}
	tempFactor := 1.0
	if a.BatteryTemp > 40 {
		tempFactor = 1.0 + (a.BatteryTemp-40)*0.03
	}
	if a.Battery < 20 {
		baseDrainPerCell *= 1.3
	}
	drain := dist * baseDrainPerCell * cargoFactor * tempFactor
	a.Battery = int(math.Max(0, float64(a.Battery)-drain))

	a.CurrentDraw = 3.5 + dist*0.2
	if a.HasCargo {
		a.CurrentDraw += 2.5
	}
	a.BatteryTemp += 0.008 * dist * cargoFactor
	if a.BatteryTemp > 28 {
		a.BatteryTemp -= 0.002
	}
	if a.BatteryTemp > 55 {
		a.BatteryTemp = 55
	}

	a.Voltage = 42.0 + float64(a.Battery)*0.08 - dist*0.001
	if a.Voltage < 36 {
		a.Voltage = 36
	}
}

func (a *AGV) idleTick(deltaMs int64) {
	a.CurrentDraw = 0.4
	if a.HasCargo {
		a.CurrentDraw = 0.8
	}
	drain := float64(deltaMs) * 0.000008 * a.CurrentDraw
	a.Battery = int(math.Max(0, float64(a.Battery)-drain))
	if a.BatteryTemp > 25 {
		a.BatteryTemp -= 0.001 * float64(deltaMs) / 100
	}
}

func (a *AGV) chargeTick(deltaMs int64) {
	chargeRate := 0.008 * float64(deltaMs) / 100
	if a.Battery < 20 {
		chargeRate *= 1.5
	} else if a.Battery > 80 {
		chargeRate *= 0.5
	}
	if a.Battery >= 95 {
		a.Battery = 100
		a.Charging = false
		a.Status = StatusIdle
		a.NeedsCharge = false
		return
	}
	a.Battery = int(math.Min(100, float64(a.Battery)+chargeRate))
	a.CurrentDraw = -6.0
	a.BatteryTemp += 0.003 * float64(deltaMs) / 100
	if a.BatteryTemp > 38 {
		a.BatteryTemp = 38
	}
	a.Voltage = 42.0 + float64(a.Battery)*0.08
}

func (a *AGV) PickUp() {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.HasCargo = true
	if a.Status != StatusCharging && a.Status != StatusReturning && a.Status != StatusLowBattery {
		a.Status = StatusPicking
	}
}

func (a *AGV) PutDown() {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.HasCargo = false
	if a.Status != StatusCharging && a.Status != StatusReturning && a.Status != StatusLowBattery {
		a.Status = StatusPlacing
	}
}

func (a *AGV) SetStatus(s int) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if s == StatusCharging {
		a.Charging = true
	}
	if a.Status == StatusCharging && s != StatusCharging {
		a.Charging = false
	}
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

func (a *AGV) GetBattery() BatteryReport {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return BatteryReport{
		SOC:         a.Battery,
		Temperature: a.BatteryTemp,
		Voltage:     a.Voltage,
		Current:     a.CurrentDraw,
	}
}

func (a *AGV) ForceReturn() {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.NeedsCharge = true
	a.ReturnToCharger = true
	if a.Status != StatusCharging {
		a.Status = StatusReturning
	}
}

func (a *AGV) NeedsCharging() bool {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.NeedsCharge
}

func (a *AGV) SetNeedsCharge(v bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.NeedsCharge = v
}

func (a *AGV) IsReturning() bool {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.ReturnToCharger
}

func (a *AGV) IsCharging() bool {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.Status == StatusCharging || a.Charging
}

func (a *AGV) GetTotalDistance() float64 {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.TotalDistance
}
