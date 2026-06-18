package energy

import (
	"math"
	"sync"
	"warehouse-agv-swarm/internal/agv"
	"warehouse-agv-swarm/internal/gridmap"
)

const (
	SOC_CRITICAL = 10
	SOC_LOW      = 20
	SOC_WARN     = 30
	SOC_SAFE     = 45
	SOC_RESERVE  = 15
	DRAIN_PER_CELL_EMPTY  = 0.015
	DRAIN_PER_CELL_CARGO  = 0.028
	DRAIN_PER_CELL_LOW    = 0.020
	CHARGE_POINT_SAFE_DIST = 10
)

type BatteryPrediction struct {
	AGVID          int
	CurrentSOC     int
	PredictedRange float64
	DistToCharge   float64
	RemainingTasks float64
	CanReturn      bool
	UrgentLevel    int
	RequiredSOC    float64
	Temperature    float64
	Healthy        bool
}

type Manager struct {
	GM              *gridmap.GridMap
	AGVManager      *agv.Manager
	ChargerOccupied map[int]bool
	LowBatterySet   map[int]bool
	ChargeQueue     []int
	mu              sync.Mutex
	historyDrain    map[int][]float64
}

func NewManager(gm *gridmap.GridMap, am *agv.Manager) *Manager {
	return &Manager{
		GM:              gm,
		AGVManager:      am,
		ChargerOccupied: make(map[int]bool),
		LowBatterySet:   make(map[int]bool),
		ChargeQueue:     make([]int, 0),
		historyDrain:    make(map[int][]float64),
	}
}

func (em *Manager) nonlinearDrainFactor(soc int) float64 {
	x := float64(soc) / 100.0
	peak := 0.85 * math.Exp(-math.Pow(x-0.15, 2)/0.01)
	tail := 1.2 * math.Exp(-math.Pow(x-0.9, 2)/0.02)
	base := 1.0 + 0.5*math.Exp(-x*3)
	return base + peak + tail*0.6
}

func (em *Manager) tempFactor(temp float64) float64 {
	if temp < 0 {
		return 1.8 + (0-temp)*0.05
	}
	if temp < 15 {
		return 1.4 + (15-temp)*0.02
	}
	if temp > 45 {
		return 1.3 + (temp-45)*0.04
	}
	if temp > 35 {
		return 1.0 + (temp-35)*0.03
	}
	return 1.0
}

func (em *Manager) Predict(agvID int, extraDistance float64, hasCargo bool) *BatteryPrediction {
	a := em.AGVManager.Get(agvID)
	if a == nil {
		return nil
	}
	bat := a.GetBattery()
	soc := bat.SOC

	baseDrain := DRAIN_PER_CELL_EMPTY
	if hasCargo {
		baseDrain = DRAIN_PER_CELL_CARGO
	}
	if soc < 25 {
		baseDrain = DRAIN_PER_CELL_LOW
	}
	baseDrain *= em.nonlinearDrainFactor(soc)
	baseDrain *= em.tempFactor(bat.Temperature)

	if baseDrain < 0.001 {
		baseDrain = 0.001
	}
	predictedRange := float64(soc-SOC_RESERVE) / baseDrain

	nearestCharger, distToCharge := em.FindNearestCharger(a.X, a.Y, agvID)
	_ = nearestCharger

	requiredSOC := (distToCharge + CHARGE_POINT_SAFE_DIST) * baseDrain * 1.3
	canReturn := float64(soc) >= requiredSOC+extraDistance*baseDrain
	urgent := 0
	if soc < SOC_CRITICAL {
		urgent = 3
	} else if soc < SOC_LOW {
		urgent = 2
	} else if float64(soc) < requiredSOC+10 {
		urgent = 1
	}

	healthy := bat.Temperature < 50 && bat.Voltage > 38

	return &BatteryPrediction{
		AGVID:          agvID,
		CurrentSOC:     soc,
		PredictedRange: predictedRange,
		DistToCharge:   distToCharge,
		RemainingTasks: predictedRange - distToCharge - CHARGE_POINT_SAFE_DIST,
		CanReturn:      canReturn,
		UrgentLevel:    urgent,
		RequiredSOC:    requiredSOC,
		Temperature:    bat.Temperature,
		Healthy:        healthy,
	}
}

func (em *Manager) FindNearestCharger(x, y, excludeAGV int) (*gridmap.Position, float64) {
	em.mu.Lock()
	defer em.mu.Unlock()

	chargers := em.GM.GetChargingPositions()
	var best *gridmap.Position
	bestDist := 1e9
	for i, ch := range chargers {
		if em.ChargerOccupied[i] {
			continue
		}
		agvHere := false
		for id, a := range em.AGVManager.GetAll() {
			if id == excludeAGV {
				continue
			}
			st := a.GetStatus()
			ax, ay, _ := a.GetPosition()
			if ax == ch.X && ay == ch.Y && (st == agv.StatusCharging || a.IsReturning()) {
				agvHere = true
				break
			}
		}
		if agvHere {
			continue
		}
		d := math.Abs(float64(ch.X-x)) + math.Abs(float64(ch.Y-y))
		if d < bestDist {
			bestDist = d
			p := ch
			best = &p
		}
	}
	if best == nil {
		for i, ch := range chargers {
			_ = i
			d := math.Abs(float64(ch.X-x)) + math.Abs(float64(ch.Y-y))
			if d < bestDist {
				bestDist = d
				p := ch
				best = &p
			}
		}
	}
	return best, bestDist
}

func (em *Manager) ShouldReturnForCharge(agvID int, remainingTaskDist float64) (*BatteryPrediction, bool) {
	pred := em.Predict(agvID, remainingTaskDist, false)
	if pred == nil {
		return nil, false
	}
	if pred.UrgentLevel >= 2 {
		return pred, true
	}
	if !pred.CanReturn {
		return pred, true
	}
	if pred.RemainingTasks < remainingTaskDist {
		return pred, true
	}
	return pred, false
}

func (em *Manager) ScanAll() []*BatteryPrediction {
	preds := make([]*BatteryPrediction, 0)
	for id := range em.AGVManager.GetAll() {
		p := em.Predict(id, 50, false)
		if p != nil {
			preds = append(preds, p)
		}
	}
	return preds
}

func (em *Manager) MarkCharger(idx int, occupied bool) {
	em.mu.Lock()
	defer em.mu.Unlock()
	em.ChargerOccupied[idx] = occupied
}

func (em *Manager) GetLowBatteryAGVs() []int {
	em.mu.Lock()
	defer em.mu.Unlock()
	result := make([]int, 0, len(em.LowBatterySet))
	for id := range em.LowBatterySet {
		result = append(result, id)
	}
	return result
}

func (em *Manager) SetLowBattery(id int, low bool) {
	em.mu.Lock()
	defer em.mu.Unlock()
	if low {
		em.LowBatterySet[id] = true
	} else {
		delete(em.LowBatterySet, id)
	}
}

func (em *Manager) GetChargeQueue() []int {
	em.mu.Lock()
	defer em.mu.Unlock()
	result := make([]int, len(em.ChargeQueue))
	copy(result, em.ChargeQueue)
	return result
}

func (em *Manager) EnqueueCharge(id int) {
	em.mu.Lock()
	defer em.mu.Unlock()
	for _, existing := range em.ChargeQueue {
		if existing == id {
			return
		}
	}
	em.ChargeQueue = append(em.ChargeQueue, id)
}

func (em *Manager) DequeueCharge(id int) {
	em.mu.Lock()
	defer em.mu.Unlock()
	for i, existing := range em.ChargeQueue {
		if existing == id {
			em.ChargeQueue = append(em.ChargeQueue[:i], em.ChargeQueue[i+1:]...)
			return
		}
	}
}
