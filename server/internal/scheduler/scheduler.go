package scheduler

import (
	"log"
	"math/rand"
	"sync"
	"time"
	"warehouse-agv-swarm/internal/agv"
	"warehouse-agv-swarm/internal/gridmap"
	"warehouse-agv-swarm/internal/order"
	"warehouse-agv-swarm/internal/pathfinding"
)

type Scheduler struct {
	GM            *gridmap.GridMap
	AGVManager    *agv.Manager
	OrderManager  *order.Manager
	Planner       *pathfinding.ConflictBasedPlanner
	CurrentTime   int
	mu            sync.Mutex
	AGVTask       map[int]*activeTask
}

type activeTask struct {
	OrderID     int
	Phase       int
	PickupDone  bool
	DropoffDone bool
}

func New(gm *gridmap.GridMap, am *agv.Manager, om *order.Manager) *Scheduler {
	return &Scheduler{
		GM:           gm,
		AGVManager:   am,
		OrderManager: om,
		Planner:      pathfinding.NewConflictBasedPlanner(gm),
		CurrentTime:  0,
		AGVTask:      make(map[int]*activeTask),
	}
}

func (s *Scheduler) Start() {
	go s.tickLoop()
	go s.orderDispatchLoop()
	go s.simulateOrderGenerator()
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
		for _, a := range s.AGVManager.GetAll() {
			_, _ = a.Update(delta)
		}
		s.checkTaskCompletion()
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
	ticker := time.NewTicker(3 * time.Second)
	defer ticker.Stop()
	for range ticker.C {
		s.generateRandomOrder()
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
	dirs := [][]int{{1, 0}, {-1, 0}, {0, 1}, {0, -1}}
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
	for _, a := range s.AGVManager.GetAll() {
		if a.GetStatus() == agv.StatusIdle {
			return a
		}
	}
	return nil
}

func (s *Scheduler) assignOrder(a *agv.AGV, o *order.Order) {
	s.AGVTask[a.ID] = &activeTask{
		OrderID: o.ID,
		Phase:   0,
	}
	s.OrderManager.UpdateStatus(o.ID, order.StatusInProgress, a.ID)
	ax, ay, _ := a.GetPosition()
	path := s.Planner.FindPath(a.ID, ax, ay, o.PickupX, o.PickupY, s.CurrentTime, nil)
	if path != nil {
		s.Planner.ReservePath(a.ID, path, s.CurrentTime)
		a.SetPath(path, s.OrderManager.NextTaskID())
		log.Printf("[Scheduler] AGV %d assigned Order %d: move to pickup (%d,%d)", a.ID, o.ID, o.PickupX, o.PickupY)
	}
}

func (s *Scheduler) checkTaskCompletion() {
	s.mu.Lock()
	defer s.mu.Unlock()

	for agvID, task := range s.AGVTask {
		a := s.AGVManager.Get(agvID)
		if a == nil {
			continue
		}
		if a.GetStatus() != agv.StatusIdle {
			continue
		}
		o := s.OrderManager.Get(task.OrderID)
		if o == nil {
			continue
		}
		ax, ay, _ := a.GetPosition()

		if task.Phase == 0 {
			if ax == o.PickupX && ay == o.PickupY {
				if o.Type == order.TypeInbound {
					a.PickUp()
				} else {
					a.PickUp()
				}
				task.Phase = 1
				a.SetStatus(agv.StatusIdle)
				path := s.Planner.FindPath(a.ID, ax, ay, o.DropoffX, o.DropoffY, s.CurrentTime, nil)
				if path != nil {
					s.Planner.ReservePath(a.ID, path, s.CurrentTime)
					a.SetPath(path, s.OrderManager.NextTaskID())
					log.Printf("[Scheduler] AGV %d Order %d: moving to dropoff (%d,%d)", a.ID, o.ID, o.DropoffX, o.DropoffY)
				}
			}
		} else if task.Phase == 1 {
			if ax == o.DropoffX && ay == o.DropoffY {
				a.PutDown()
				a.SetStatus(agv.StatusIdle)
				s.OrderManager.UpdateStatus(o.ID, order.StatusCompleted, a.ID)
				s.Planner.ClearReservations(a.ID)
				delete(s.AGVTask, agvID)
				log.Printf("[Scheduler] AGV %d completed Order %d", a.ID, o.ID)
			}
		}
	}
}
