package scheduler

import (
	"log"
	"math/rand"
	"sort"
	"sync"
	"time"
	"warehouse-agv-swarm/internal/agv"
	"warehouse-agv-swarm/internal/energy"
	"warehouse-agv-swarm/internal/gridmap"
	"warehouse-agv-swarm/internal/order"
	"warehouse-agv-swarm/internal/pathfinding"
)

type Scheduler struct {
	GM              *gridmap.GridMap
	AGVManager      *agv.Manager
	OrderManager    *order.Manager
	Planner         *pathfinding.ConflictBasedPlanner
	EnergyManager   *energy.Manager
	CurrentTime     int
	mu              sync.Mutex
	AGVTask         map[int]*activeTask
	AGVGoal         map[int]gridmap.Position
	replanningSet   map[int]bool
	deadlockCount   map[int]int
	lastDeadlockChk int
	lastReplanTime  map[int]int
	ReturningAGVs   map[int]bool
}

type activeTask struct {
	OrderID     int
	Phase       int
	PickupDone  bool
	DropoffDone bool
	GoalX       int
	GoalY       int
	Replans     int
	RemainingDist float64
	Preempted     bool
}

func New(gm *gridmap.GridMap, am *agv.Manager, om *order.Manager) *Scheduler {
	em := energy.NewManager(gm, am)
	return &Scheduler{
		GM:            gm,
		AGVManager:    am,
		OrderManager:  om,
		Planner:       pathfinding.NewConflictBasedPlanner(gm),
		EnergyManager: em,
		CurrentTime:   0,
		AGVTask:       make(map[int]*activeTask),
		AGVGoal:       make(map[int]gridmap.Position),
		replanningSet: make(map[int]bool),
		deadlockCount: make(map[int]int),
		lastReplanTime: make(map[int]int),
		ReturningAGVs: make(map[int]bool),
	}
}

func (s *Scheduler) Start() {
	go s.tickLoop()
	go s.orderDispatchLoop()
	go s.simulateOrderGenerator()
	go s.deadlockMonitorLoop()
	go s.recoveryLoop()
	go s.energyMonitorLoop()
}

func (s *Scheduler) tickLoop() {
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	last := time.Now()
	for range ticker.C {
		now := time.Now()
		delta := now.Sub(last).Milliseconds()
		last = now
		s.CurrentTime++

		s.mu.Lock()
		for _, a := range s.AGVManager.GetAll() {
			_, _ = a.Update(delta)
			ax, ay, _ := a.GetPosition()
			if s.ReturningAGVs[a.ID] {
				continue
			}
			if task, ok := s.AGVTask[a.ID]; ok && !task.Preempted {
				delayed, needReplan, by := s.Planner.UpdateAGVProgress(a.ID, ax, ay, s.CurrentTime)
				lastRP, _ := s.lastReplanTime[a.ID]
				coolDown := s.CurrentTime - lastRP
				if needReplan && !s.replanningSet[a.ID] && task.Replans < 3 && coolDown > 120 {
					log.Printf("[Warning] AGV %d delayed by %d steps, triggering dynamic replanning (cooldown=%d)", a.ID, by, coolDown)
					s.replanningSet[a.ID] = true
					s.lastReplanTime[a.ID] = s.CurrentTime
					go s.triggerReplanning(a.ID)
				}
				_ = delayed
				_ = by
			}
		}
		s.checkTaskCompletion()
		s.mu.Unlock()
	}
}

func (s *Scheduler) energyMonitorLoop() {
	ticker := time.NewTicker(1500 * time.Millisecond)
	defer ticker.Stop()
	for range ticker.C {
		s.runEnergyCheck()
	}
}

func (s *Scheduler) runEnergyCheck() {
	s.mu.Lock()
	defer s.mu.Unlock()

	preds := s.EnergyManager.ScanAll()
	for _, pred := range preds {
		a := s.AGVManager.Get(pred.AGVID)
		if a == nil {
			continue
		}
		st := a.GetStatus()
		if st == agv.StatusCharging || a.IsCharging() {
			s.EnergyManager.SetLowBattery(pred.AGVID, false)
			delete(s.ReturningAGVs, pred.AGVID)
			continue
		}
		if s.ReturningAGVs[pred.AGVID] {
			continue
		}

		remainingDist := 80.0
		if task, ok := s.AGVTask[pred.AGVID]; ok {
			remainingDist = task.RemainingDist
			if remainingDist < 30 {
				remainingDist = 30
			}
		}

		if pred.UrgentLevel >= 2 || !pred.CanReturn || pred.RemainingTasks < remainingDist {
			log.Printf("[Energy] AGV %d SOC=%d%% urgent=%d canReturn=%v remaining=%.1f → FORCE RETURN to charger",
				pred.AGVID, pred.CurrentSOC, pred.UrgentLevel, pred.CanReturn, pred.RemainingTasks)
			s.preemptAndReturn(pred.AGVID, pred)
		} else if pred.UrgentLevel == 1 {
			s.EnergyManager.SetLowBattery(pred.AGVID, true)
		} else {
			s.EnergyManager.SetLowBattery(pred.AGVID, false)
		}
	}
}

func (s *Scheduler) preemptAndReturn(agvID int, pred *energy.BatteryPrediction) {
	a := s.AGVManager.Get(agvID)
	if a == nil {
		return
	}

	if task, ok := s.AGVTask[agvID]; ok && !task.Preempted {
		o := s.OrderManager.Get(task.OrderID)
		if o != nil {
			log.Printf("[Energy] AGV %d preempting Order %d due to low battery SOC=%d%%", agvID, o.ID, pred.CurrentSOC)
			s.OrderManager.UpdateStatus(o.ID, order.StatusPending, -1)
		}
		task.Preempted = true
	}

	charger, dist := s.EnergyManager.FindNearestCharger(a.X, a.Y, agvID)
	if charger == nil {
		log.Printf("[Energy] AGV %d no available charger, queueing. SOC=%d%%", agvID, pred.CurrentSOC)
		a.SetStatus(agv.StatusLowBattery)
		s.EnergyManager.EnqueueCharge(agvID)
		return
	}

	a.ForceReturn()
	s.ReturningAGVs[agvID] = true
	s.EnergyManager.SetLowBattery(agvID, true)

	s.Planner.ClearReservations(agvID)
	avoid := make(map[int]bool)
	for id := range s.ReturningAGVs {
		if id != agvID {
			avoid[id] = true
		}
	}
	returnPath := s.Planner.FindPath(agvID, a.X, a.Y, charger.X, charger.Y, s.CurrentTime+1, avoid)
	if returnPath != nil {
		s.Planner.ReservePath(agvID, returnPath, s.CurrentTime+1)
		a.SetPath(returnPath, -1)
		_ = dist
		log.Printf("[Energy] AGV %d dispatched to charger (%d,%d) dist=%.0f path=%d steps",
			agvID, charger.X, charger.Y, dist, len(returnPath))
	} else {
		log.Printf("[Energy] AGV %d cannot find path to charger, waiting", agvID)
		a.SetStatus(agv.StatusLowBattery)
		s.EnergyManager.EnqueueCharge(agvID)
	}
}

func (s *Scheduler) triggerReplanning(agvID int) {
	time.Sleep(50 * time.Millisecond)
	s.mu.Lock()
	defer s.mu.Unlock()

	if !s.replanningSet[agvID] {
		return
	}
	delete(s.replanningSet, agvID)

	a := s.AGVManager.Get(agvID)
	if a == nil {
		return
	}
	task, ok := s.AGVTask[agvID]
	if !ok {
		return
	}
	ax, ay, _ := a.GetPosition()

	var gx, gy int
	if s.ReturningAGVs[agvID] {
		charger, _ := s.EnergyManager.FindNearestCharger(ax, ay, agvID)
		if charger != nil {
			gx, gy = charger.X, charger.Y
		} else {
			return
		}
	} else if task.Phase == 0 {
		o := s.OrderManager.Get(task.OrderID)
		if o == nil {
			return
		}
		gx, gy = o.PickupX, o.PickupY
	} else {
		o := s.OrderManager.Get(task.OrderID)
		if o == nil {
			return
		}
		gx, gy = o.DropoffX, o.DropoffY
	}

	s.Planner.EmergencyYield(agvID, s.CurrentTime, ax, ay)

	newPath := s.Planner.ReplanningForAGV(agvID, ax, ay, gx, gy, s.CurrentTime+2)
	if newPath != nil {
		task.Replans++
		a.SetPath(newPath, s.OrderManager.NextTaskID())
		log.Printf("[Replanning] AGV %d recalculated path to (%d,%d), %d waypoints, attempt %d",
			agvID, gx, gy, len(newPath), task.Replans)
	} else {
		log.Printf("[Replanning-FAIL] AGV %d could not find alternate path, waiting", agvID)
		a.SetStatus(agv.StatusWaiting)
		s.Planner.ClearReservations(agvID)
	}
}

func (s *Scheduler) orderDispatchLoop() {
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	for range ticker.C {
		s.dispatchOrders()
	}
}

func (s *Scheduler) simulateOrderGenerator() {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for range ticker.C {
		s.generateRandomOrder()
	}
}

func (s *Scheduler) deadlockMonitorLoop() {
	ticker := time.NewTicker(1500 * time.Millisecond)
	defer ticker.Stop()
	for range ticker.C {
		s.detectAndResolveDeadlocks()
	}
}

func (s *Scheduler) recoveryLoop() {
	ticker := time.NewTicker(800 * time.Millisecond)
	defer ticker.Stop()
	for range ticker.C {
		s.recoverWaitingAGVs()
	}
}

func (s *Scheduler) generateRandomOrder() {
	shelves := s.GM.GetAllShelves()
	if len(shelves) == 0 {
		return
	}
	if rand.Intn(2) == 0 {
		dock := s.GM.GetFreeDockingIn()
		if dock == nil {
			return
		}
		shelf := shelves[rand.Intn(len(shelves))]
		target := s.findAdjacentWalkable(shelf.X, shelf.Y)
		if target == nil {
			return
		}
		s.OrderManager.CreateInbound(*dock, *target)
	} else {
		dock := s.GM.GetFreeDockingOut()
		if dock == nil {
			return
		}
		shelf := shelves[rand.Intn(len(shelves))]
		target := s.findAdjacentWalkable(shelf.X, shelf.Y)
		if target == nil {
			return
		}
		s.OrderManager.CreateOutbound(*target, *dock)
	}
}

func (s *Scheduler) findAdjacentWalkable(x, y int) *gridmap.Position {
	dirs := [][]int{{1, 0}, {-1, 0}, {0, 1}, {0, -1}, {2, 0}, {-2, 0}, {0, 2}, {0, -2}}
	for _, d := range dirs {
		nx, ny := x+d[0], y+d[1]
		if s.GM.IsWalkable(nx, ny) {
			return &gridmap.Position{X: nx, Y: ny}
		}
	}
	return nil
}

func (s *Scheduler) dispatchOrders() {
	s.mu.Lock()
	defer s.mu.Unlock()

	for {
		o := s.OrderManager.TakePending()
		if o == nil {
			break
		}
		freeAGV := s.findFreeAGV()
		if freeAGV == nil {
			s.OrderManager.UpdateStatus(o.ID, order.StatusPending, -1)
			break
		}
		s.assignOrder(freeAGV, o)
	}
}

func (s *Scheduler) findFreeAGV() *agv.AGV {
	list := make([]*agv.AGV, 0)
	for _, a := range s.AGVManager.GetAll() {
		st := a.GetStatus()
		if s.ReturningAGVs[a.ID] || a.IsReturning() || a.NeedsCharging() {
			continue
		}
		pred := s.EnergyManager.Predict(a.ID, 120, false)
		if pred != nil && (pred.UrgentLevel >= 1 || pred.RemainingTasks < 60) {
			continue
		}
		if st == agv.StatusIdle || st == agv.StatusWaiting {
			list = append(list, a)
		}
	}
	if len(list) == 0 {
		return nil
	}
	sort.Slice(list, func(i, j int) bool {
		return list[i].ID < list[j].ID
	})
	return list[0]
}

func (s *Scheduler) assignOrder(a *agv.AGV, o *order.Order) {
	dist := float64(abs(a.X-o.PickupX) + abs(a.Y-o.PickupY) + abs(o.PickupX-o.DropoffX) + abs(o.PickupY-o.DropoffY))
	s.AGVTask[a.ID] = &activeTask{
		OrderID:       o.ID,
		Phase:         0,
		GoalX:         o.PickupX,
		GoalY:         o.PickupY,
		RemainingDist: dist,
	}
	s.AGVGoal[a.ID] = gridmap.Position{X: o.PickupX, Y: o.PickupY}
	s.OrderManager.UpdateStatus(o.ID, order.StatusInProgress, a.ID)
	ax, ay, _ := a.GetPosition()
	path := s.Planner.FindPath(a.ID, ax, ay, o.PickupX, o.PickupY, s.CurrentTime, nil)
	if path != nil {
		s.Planner.ReservePath(a.ID, path, s.CurrentTime)
		a.SetPath(path, s.OrderManager.NextTaskID())
		log.Printf("[Scheduler] AGV %d assigned Order %d: pickup (%d,%d) → dropoff (%d,%d) dist=%.0f",
			a.ID, o.ID, o.PickupX, o.PickupY, o.DropoffX, o.DropoffY, dist)
	} else {
		log.Printf("[Scheduler] AGV %d Order %d: initial path not found, queued for retry", a.ID, o.ID)
		a.SetStatus(agv.StatusWaiting)
	}
}

func (s *Scheduler) checkTaskCompletion() {
	for agvID, task := range s.AGVTask {
		if s.ReturningAGVs[agvID] {
			a := s.AGVManager.Get(agvID)
			if a != nil {
				st := a.GetStatus()
				if st == agv.StatusCharging || a.IsCharging() {
					log.Printf("[Energy] AGV %d reached charger, removed from returning set", agvID)
					delete(s.ReturningAGVs, agvID)
					s.EnergyManager.DequeueCharge(agvID)
					if task.Preempted {
						s.Planner.ClearReservations(agvID)
						delete(s.AGVTask, agvID)
						delete(s.AGVGoal, agvID)
					}
				}
			}
			continue
		}
		if task.Preempted {
			continue
		}
		a := s.AGVManager.Get(agvID)
		if a == nil {
			continue
		}
		st := a.GetStatus()
		if st != agv.StatusIdle && st != agv.StatusWaiting {
			continue
		}
		o := s.OrderManager.Get(task.OrderID)
		if o == nil {
			continue
		}
		ax, ay, _ := a.GetPosition()

		if task.Phase == 0 {
			dx := abs(ax - o.PickupX)
			dy := abs(ay - o.PickupY)
			if (dx <= 1 && dy <= 1) || st == agv.StatusWaiting {
				if o.Type == order.TypeInbound {
					a.PickUp()
				} else {
					a.PickUp()
				}
				task.Phase = 1
				task.GoalX = o.DropoffX
				task.GoalY = o.DropoffY
				task.RemainingDist = float64(abs(o.PickupX-o.DropoffX) + abs(o.PickupY-o.DropoffY))
				s.AGVGoal[agvID] = gridmap.Position{X: o.DropoffX, Y: o.DropoffY}
				a.SetStatus(agv.StatusIdle)
				path := s.Planner.FindPath(a.ID, ax, ay, o.DropoffX, o.DropoffY, s.CurrentTime, nil)
				if path != nil {
					s.Planner.ReservePath(a.ID, path, s.CurrentTime)
					a.SetPath(path, s.OrderManager.NextTaskID())
					log.Printf("[Scheduler] AGV %d Order %d: picked up, route to dropoff (%d,%d)",
						a.ID, o.ID, o.DropoffX, o.DropoffY)
				} else {
					a.SetStatus(agv.StatusWaiting)
					log.Printf("[Scheduler] AGV %d Order %d: dropoff path blocked, waiting", a.ID, o.ID)
				}
			}
		} else if task.Phase == 1 {
			dx := abs(ax - o.DropoffX)
			dy := abs(ay - o.DropoffY)
			if (dx <= 1 && dy <= 1) || st == agv.StatusWaiting {
				a.PutDown()
				a.SetStatus(agv.StatusIdle)
				s.OrderManager.UpdateStatus(o.ID, order.StatusCompleted, a.ID)
				s.Planner.ClearReservations(a.ID)
				delete(s.AGVTask, agvID)
				delete(s.AGVGoal, agvID)
				delete(s.deadlockCount, agvID)
				log.Printf("[Scheduler] AGV %d completed Order %d", a.ID, o.ID)
			}
		}
	}
}

func (s *Scheduler) detectAndResolveDeadlocks() {
	s.mu.Lock()
	defer s.mu.Unlock()

	deadlocks := s.Planner.FindDeadlockedAGVs(s.CurrentTime)
	if len(deadlocks) == 0 {
		return
	}

	for spot, agvIDs := range deadlocks {
		log.Printf("[Deadlock] Detected at zone %s involving AGVs: %v", spot, agvIDs)

		sort.Slice(agvIDs, func(i, j int) bool {
			ri := 0
			rj := 0
			if ti, ok := s.AGVTask[agvIDs[i]]; ok {
				ri = ti.Replans
			}
			if tj, ok := s.AGVTask[agvIDs[j]]; ok {
				rj = tj.Replans
			}
			if ri != rj {
				return ri < rj
			}
			return agvIDs[i] < agvIDs[j]
		})

		for idx, id := range agvIDs {
			s.deadlockCount[id]++
			if idx < len(agvIDs)-1 {
				a := s.AGVManager.Get(id)
				if a == nil {
					continue
				}
				ax, ay, _ := a.GetPosition()
				s.Planner.EmergencyYield(id, s.CurrentTime, ax, ay)
				s.Planner.ClearReservations(id)
				a.SetStatus(agv.StatusWaiting)
				log.Printf("[Deadlock-Resolve] AGV %d yielded priority", id)
			} else {
				if _, ok := s.AGVTask[id]; ok {
					log.Printf("[Deadlock-Resolve] AGV %d (highest priority) proceeding first", id)
					s.replanningSet[id] = true
					go s.triggerReplanning(id)
				}
			}
		}
	}
}

func (s *Scheduler) recoverWaitingAGVs() {
	s.mu.Lock()
	defer s.mu.Unlock()

	for agvID, task := range s.AGVTask {
		if task.Preempted {
			continue
		}
		if s.ReturningAGVs[agvID] {
			a := s.AGVManager.Get(agvID)
			if a != nil && a.GetStatus() == agv.StatusLowBattery {
				charger, _ := s.EnergyManager.FindNearestCharger(a.X, a.Y, agvID)
				if charger != nil {
					path := s.Planner.FindPath(agvID, a.X, a.Y, charger.X, charger.Y, s.CurrentTime+1, nil)
					if path != nil {
						s.Planner.ReservePath(agvID, path, s.CurrentTime+1)
						a.ForceReturn()
						a.SetPath(path, -1)
						log.Printf("[Recovery-Energy] AGV %d routed to charger (%d,%d)", agvID, charger.X, charger.Y)
					}
				}
			}
			continue
		}
		a := s.AGVManager.Get(agvID)
		if a == nil || a.GetStatus() != agv.StatusWaiting {
			continue
		}
		ax, ay, _ := a.GetPosition()
		var gx, gy int
		o := s.OrderManager.Get(task.OrderID)
		if o == nil {
			continue
		}
		if task.Phase == 0 {
			gx, gy = o.PickupX, o.PickupY
		} else {
			gx, gy = o.DropoffX, o.DropoffY
		}
		newPath := s.Planner.FindPath(agvID, ax, ay, gx, gy, s.CurrentTime+1, nil)
		if newPath != nil {
			s.Planner.ReservePath(agvID, newPath, s.CurrentTime+1)
			a.SetPath(newPath, s.OrderManager.NextTaskID())
			log.Printf("[Recovery] AGV %d recovered with new path to (%d,%d)", agvID, gx, gy)
		}
	}
}

func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}
