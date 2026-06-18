package scheduler

import (
	"log"
	"math/rand"
	"sort"
	"sync"
	"time"
	"warehouse-agv-swarm/internal/agv"
	"warehouse-agv-swarm/internal/gridmap"
	"warehouse-agv-swarm/internal/order"
	"warehouse-agv-swarm/internal/pathfinding"
)

type Scheduler struct {
	GM             *gridmap.GridMap
	AGVManager     *agv.Manager
	OrderManager   *order.Manager
	Planner        *pathfinding.ConflictBasedPlanner
	CurrentTime    int
	mu             sync.Mutex
	AGVTask        map[int]*activeTask
	AGVGoal        map[int]gridmap.Position
	replanningSet  map[int]bool
	deadlockCount  map[int]int
	lastDeadlockChk int
	lastReplanTime map[int]int
}

type activeTask struct {
	OrderID     int
	Phase       int
	PickupDone  bool
	DropoffDone bool
	GoalX       int
	GoalY       int
	Replans     int
}

func New(gm *gridmap.GridMap, am *agv.Manager, om *order.Manager) *Scheduler {
	return &Scheduler{
		GM:            gm,
		AGVManager:    am,
		OrderManager:  om,
		Planner:       pathfinding.NewConflictBasedPlanner(gm),
		CurrentTime:   0,
		AGVTask:       make(map[int]*activeTask),
		AGVGoal:       make(map[int]gridmap.Position),
		replanningSet: make(map[int]bool),
		deadlockCount: make(map[int]int),
		lastReplanTime: make(map[int]int),
	}
}

func (s *Scheduler) Start() {
	go s.tickLoop()
	go s.orderDispatchLoop()
	go s.simulateOrderGenerator()
	go s.deadlockMonitorLoop()
	go s.recoveryLoop()
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
			if task, ok := s.AGVTask[a.ID]; ok {
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
	if task.Phase == 0 {
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
	s.AGVTask[a.ID] = &activeTask{
		OrderID: o.ID,
		Phase:   0,
		GoalX:   o.PickupX,
		GoalY:   o.PickupY,
	}
	s.AGVGoal[a.ID] = gridmap.Position{X: o.PickupX, Y: o.PickupY}
	s.OrderManager.UpdateStatus(o.ID, order.StatusInProgress, a.ID)
	ax, ay, _ := a.GetPosition()
	path := s.Planner.FindPath(a.ID, ax, ay, o.PickupX, o.PickupY, s.CurrentTime, nil)
	if path != nil {
		s.Planner.ReservePath(a.ID, path, s.CurrentTime)
		a.SetPath(path, s.OrderManager.NextTaskID())
		log.Printf("[Scheduler] AGV %d assigned Order %d: pickup (%d,%d) → dropoff (%d,%d)",
			a.ID, o.ID, o.PickupX, o.PickupY, o.DropoffX, o.DropoffY)
	} else {
		log.Printf("[Scheduler] AGV %d Order %d: initial path not found, queued for retry", a.ID, o.ID)
		a.SetStatus(agv.StatusWaiting)
	}
}

func (s *Scheduler) checkTaskCompletion() {
	for agvID, task := range s.AGVTask {
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
			ti := s.AGVTask[agvIDs[i]]
			tj := s.AGVTask[agvIDs[j]]
			ri := 0
			rj := 0
			if ti != nil {
				ri = ti.Replans
			}
			if tj != nil {
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
				if task, ok := s.AGVTask[id]; ok {
					log.Printf("[Deadlock-Resolve] AGV %d (highest priority) proceeding first", id)
					s.replanningSet[id] = true
					go s.triggerReplanning(id)
					_ = task
				}
			}
		}
	}
}

func (s *Scheduler) recoverWaitingAGVs() {
	s.mu.Lock()
	defer s.mu.Unlock()

	for agvID, task := range s.AGVTask {
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
