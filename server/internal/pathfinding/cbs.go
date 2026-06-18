package pathfinding

import (
	"container/heap"
	"fmt"
	"math"
	"sync"
	"warehouse-agv-swarm/internal/gridmap"
)

type Node struct {
	X        int
	Y        int
	G        float64
	H        float64
	F        float64
	Parent   *Node
	TimeStep int
}

type PriorityQueue []*Node

func (pq PriorityQueue) Len() int { return len(pq) }
func (pq PriorityQueue) Less(i, j int) bool { return pq[i].F < pq[j].F }
func (pq PriorityQueue) Swap(i, j int) { pq[i], pq[j] = pq[j], pq[i] }
func (pq *PriorityQueue) Push(x interface{}) { *pq = append(*pq, x.(*Node)) }
func (pq *PriorityQueue) Pop() interface{} {
	old := *pq
	n := len(old)
	node := old[n-1]
	*pq = old[0 : n-1]
	return node
}

type VertexReservation struct {
	AGVID    int
	TimeFrom int
	TimeTo   int
}

type EdgeReservation struct {
	AGVID    int
	FromX    int
	FromY    int
	ToX      int
	ToY      int
	TimeFrom int
	TimeTo   int
}

type ConflictBasedPlanner struct {
	GM                  *gridmap.GridMap
	VertexReservations  map[string]*VertexReservation
	EdgeReservations    map[string]*EdgeReservation
	AGVPathInfo         map[int]*AGVPathMeta
	mu                  sync.RWMutex
	MaxTimeHorizon      int
	CollisionBuffer     int
	DelayThreshold      int
}

type AGVPathMeta struct {
	AGVID           int
	Path            []gridmap.Position
	StartTime       int
	ExpectedStep    int
	LastProgress    int
	DelayCount      int
	Priority        int
	ConsecutiveIdle int
}

type PlanRequest struct {
	AGVID     int
	StartX    int
	StartY    int
	GoalX     int
	GoalY     int
	StartTime int
	Priority  int
}

func NewConflictBasedPlanner(gm *gridmap.GridMap) *ConflictBasedPlanner {
	return &ConflictBasedPlanner{
		GM:                 gm,
		VertexReservations: make(map[string]*VertexReservation),
		EdgeReservations:   make(map[string]*EdgeReservation),
		AGVPathInfo:        make(map[int]*AGVPathMeta),
		MaxTimeHorizon:     500,
		CollisionBuffer:    1,
		DelayThreshold:     25,
	}
}

func (cbp *ConflictBasedPlanner) vkey(x, y, t int) string {
	return fmt.Sprintf("%d,%d,%d", x, y, t)
}

func (cbp *ConflictBasedPlanner) vposKey(x, y int) string {
	return fmt.Sprintf("%d,%d", x, y)
}

func (cbp *ConflictBasedPlanner) ekey(fx, fy, tx, ty, t int) string {
	if fx < tx || (fx == tx && fy < ty) {
		return fmt.Sprintf("e:%d,%d->%d,%d@%d", fx, fy, tx, ty, t)
	}
	return fmt.Sprintf("e:%d,%d->%d,%d@%d", tx, ty, fx, fy, t)
}

func heuristic(x1, y1, x2, y2 int) float64 {
	return math.Abs(float64(x1-x2)) + math.Abs(float64(y1-y2))
}

func (cbp *ConflictBasedPlanner) isVertexOccupied(x, y, t int, selfAGV int, avoid map[int]bool) (int, bool) {
	cbp.mu.RLock()
	defer cbp.mu.RUnlock()
	for dt := -cbp.CollisionBuffer; dt <= cbp.CollisionBuffer; dt++ {
		kt := t + dt
		if kt < 0 {
			continue
		}
		if vr, ok := cbp.VertexReservations[cbp.vkey(x, y, kt)]; ok {
			if vr.AGVID == selfAGV {
				continue
			}
			if avoid != nil && avoid[vr.AGVID] {
				continue
			}
			return vr.AGVID, true
		}
	}
	return -1, false
}

func (cbp *ConflictBasedPlanner) isEdgeOccupied(fx, fy, tx, ty, t int, selfAGV int, avoid map[int]bool) (int, bool) {
	cbp.mu.RLock()
	defer cbp.mu.RUnlock()
	for dt := -cbp.CollisionBuffer; dt <= cbp.CollisionBuffer; dt++ {
		kt := t + dt
		if kt < 0 {
			continue
		}
		if er, ok := cbp.EdgeReservations[cbp.ekey(fx, fy, tx, ty, kt)]; ok {
			if er.AGVID == selfAGV {
				continue
			}
			if avoid != nil && avoid[er.AGVID] {
				continue
			}
			sameDir := (er.FromX == fx && er.FromY == fy && er.ToX == tx && er.ToY == ty)
			opposite := (er.FromX == tx && er.FromY == ty && er.ToX == fx && er.ToY == fy)
			if sameDir || opposite {
				return er.AGVID, true
			}
		}
	}
	return -1, false
}

func (cbp *ConflictBasedPlanner) checkSwappingConflict(fx, fy, tx, ty, t int, selfAGV int, avoid map[int]bool) (int, bool) {
	cbp.mu.RLock()
	defer cbp.mu.RUnlock()
	if vr, ok := cbp.VertexReservations[cbp.vkey(tx, ty, t)]; ok {
		if vr.AGVID != selfAGV {
			if avoid == nil || !avoid[vr.AGVID] {
				if vr2, ok2 := cbp.VertexReservations[cbp.vkey(fx, fy, t+1)]; ok2 {
					if vr2.AGVID == vr.AGVID {
						return vr.AGVID, true
					}
				}
			}
		}
	}
	return -1, false
}

func (cbp *ConflictBasedPlanner) FindPath(agvID, sx, sy, gx, gy, startTime int, avoidAGVs map[int]bool) []gridmap.Position {
	cbp.mu.RLock()
	_ = cbp.AGVPathInfo
	cbp.mu.RUnlock()

	openList := &PriorityQueue{}
	heap.Init(openList)
	closedSet := make(map[string]bool)
	bestGoal := -1.0
	var bestNode *Node

	start := &Node{X: sx, Y: sy, G: 0, H: heuristic(sx, sy, gx, gy), TimeStep: startTime}
	start.F = start.G + start.H
	heap.Push(openList, start)

	directions := [][]int{{0, 0}, {1, 0}, {-1, 0}, {0, 1}, {0, -1}}
	maxExpand := 150000
	expanded := 0

	for openList.Len() > 0 && expanded < maxExpand {
		current := heap.Pop(openList).(*Node)
		expanded++

		if current.X == gx && current.Y == gy {
			if bestGoal < 0 || current.F < bestGoal {
				bestGoal = current.F
				bestNode = current
				if current.TimeStep-start.TimeStep < 5 {
					break
				}
			}
			if current.TimeStep-start.TimeStep > 3 {
				break
			}
		}

		ck := cbp.vkey(current.X, current.Y, current.TimeStep)
		if closedSet[ck] {
			continue
		}
		closedSet[ck] = true

		if current.TimeStep-start.TimeStep > cbp.MaxTimeHorizon {
			continue
		}

		for _, dir := range directions {
			nx := current.X + dir[0]
			ny := current.Y + dir[1]
			nt := current.TimeStep + 1

			if !cbp.GM.IsWalkable(nx, ny) {
				continue
			}

			if _, occupied := cbp.isVertexOccupied(nx, ny, nt, agvID, avoidAGVs); occupied {
				if dir[0] == 0 && dir[1] == 0 {
					if vid2, occ2 := cbp.isVertexOccupied(current.X, current.Y, nt, agvID, avoidAGVs); occ2 && vid2 != agvID {
						continue
					}
				} else {
					continue
				}
			}

			if dir[0] != 0 || dir[1] != 0 {
				if _, edgeOcc := cbp.isEdgeOccupied(current.X, current.Y, nx, ny, current.TimeStep, agvID, avoidAGVs); edgeOcc {
					continue
				}
				if _, swapOcc := cbp.checkSwappingConflict(current.X, current.Y, nx, ny, current.TimeStep, agvID, avoidAGVs); swapOcc {
					continue
				}
			}

			waitPenalty := 0.0
			if dir[0] == 0 && dir[1] == 0 {
				waitPenalty = 0.15
			}
			ng := current.G + 1 + waitPenalty
			nh := heuristic(nx, ny, gx, gy)
			neighbor := &Node{
				X:        nx,
				Y:        ny,
				G:        ng,
				H:        nh,
				F:        ng + nh,
				Parent:   current,
				TimeStep: nt,
			}
			heap.Push(openList, neighbor)
		}
	}

	if bestNode != nil {
		path := []gridmap.Position{}
		for node := bestNode; node != nil; node = node.Parent {
			path = append([]gridmap.Position{{X: node.X, Y: node.Y}}, path...)
		}
		return path
	}

	return nil
}

func (cbp *ConflictBasedPlanner) ReservePath(agvID int, path []gridmap.Position, startTime int) {
	cbp.mu.Lock()
	defer cbp.mu.Unlock()

	cbp.clearReservationsLocked(agvID)

	for i, pos := range path {
		t := startTime + i
		dur := 1
		if i == len(path)-1 {
			dur = 30
		}
		for dt := 0; dt < dur; dt++ {
			cbp.VertexReservations[cbp.vkey(pos.X, pos.Y, t+dt)] = &VertexReservation{
				AGVID:    agvID,
				TimeFrom: t,
				TimeTo:   t + dur,
			}
		}
		if i < len(path)-1 {
			next := path[i+1]
			if pos.X != next.X || pos.Y != next.Y {
				cbp.EdgeReservations[cbp.ekey(pos.X, pos.Y, next.X, next.Y, t)] = &EdgeReservation{
					AGVID:    agvID,
					FromX:    pos.X,
					FromY:    pos.Y,
					ToX:      next.X,
					ToY:      next.Y,
					TimeFrom: t,
					TimeTo:   t + 1,
				}
			}
		}
	}

	cbp.AGVPathInfo[agvID] = &AGVPathMeta{
		AGVID:        agvID,
		Path:         path,
		StartTime:    startTime,
		ExpectedStep: 0,
		LastProgress: startTime,
		DelayCount:   0,
	}
}

func (cbp *ConflictBasedPlanner) clearReservationsLocked(agvID int) {
	for k, v := range cbp.VertexReservations {
		if v.AGVID == agvID {
			delete(cbp.VertexReservations, k)
		}
	}
	for k, v := range cbp.EdgeReservations {
		if v.AGVID == agvID {
			delete(cbp.EdgeReservations, k)
		}
	}
}

func (cbp *ConflictBasedPlanner) ClearReservations(agvID int) {
	cbp.mu.Lock()
	defer cbp.mu.Unlock()
	cbp.clearReservationsLocked(agvID)
	delete(cbp.AGVPathInfo, agvID)
}

func (cbp *ConflictBasedPlanner) UpdateAGVProgress(agvID int, currentX, currentY int, currentTime int) (delayed bool, needsReplanning bool, by int) {
	cbp.mu.Lock()
	defer cbp.mu.Unlock()

	meta, ok := cbp.AGVPathInfo[agvID]
	if !ok || len(meta.Path) == 0 {
		return false, false, 0
	}

	expectedStep := currentTime - meta.StartTime
	if expectedStep < 0 {
		expectedStep = 0
	}
	if expectedStep >= len(meta.Path) {
		expectedStep = len(meta.Path) - 1
	}
	meta.ExpectedStep = expectedStep

	foundStep := -1
	searchWindow := 40
	startS := expectedStep
	if startS > len(meta.Path)-1 {
		startS = len(meta.Path) - 1
	}
	endS := expectedStep - searchWindow
	if endS < 0 {
		endS = 0
	}

	for s := startS; s >= endS; s-- {
		if meta.Path[s].X == currentX && meta.Path[s].Y == currentY {
			foundStep = s
			break
		}
	}

	if foundStep >= 0 {
		delay := expectedStep - foundStep
		if delay > 0 {
			meta.DelayCount++
			meta.ConsecutiveIdle = 0
		} else {
			meta.DelayCount = 0
			meta.ConsecutiveIdle = 0
			meta.LastProgress = currentTime
		}
		if delay >= cbp.DelayThreshold && meta.DelayCount >= 10 {
			return true, true, delay
		}
	} else {
		meta.ConsecutiveIdle++
		if meta.ConsecutiveIdle > 80 {
			return true, true, cbp.DelayThreshold
		}
		if meta.ConsecutiveIdle > 20 {
			meta.DelayCount++
		}
	}

	return false, false, 0
}

func (cbp *ConflictBasedPlanner) DetectDeadlock(centerX, centerY int, currentTime int) ([]int, bool) {
	cbp.mu.RLock()
	defer cbp.mu.RUnlock()

	dirs := [][]int{{1, 0}, {-1, 0}, {0, 1}, {0, -1}}
	crossroads := [][]int{
		{0, 0}, {1, 0}, {-1, 0}, {0, 1}, {0, -1}, {1, 1}, {-1, 1}, {1, -1}, {-1, -1},
	}

	agvsInArea := make(map[int]bool)
	for _, off := range crossroads {
		x, y := centerX+off[0], centerY+off[1]
		for dt := -2; dt <= 2; dt++ {
			if vr, ok := cbp.VertexReservations[cbp.vkey(x, y, currentTime+dt)]; ok {
				agvsInArea[vr.AGVID] = true
			}
		}
	}

	if len(agvsInArea) < 3 {
		return nil, false
	}

	agvList := make([]int, 0, len(agvsInArea))
	for id := range agvsInArea {
		agvList = append(agvList, id)
	}

	progress := false
	for _, id := range agvList {
		if meta, ok := cbp.AGVPathInfo[id]; ok {
			if meta.LastProgress >= currentTime-15 {
				progress = true
				break
			}
		}
	}

	if !progress && len(agvsInArea) >= 3 {
		_ = dirs
		return agvList, true
	}
	return nil, false
}

func (cbp *ConflictBasedPlanner) FindDeadlockedAGVs(currentTime int) map[string][]int {
	cbp.mu.RLock()
	hotSpots := make(map[string]int)
	for k, vr := range cbp.VertexReservations {
		_ = k
		meta, ok := cbp.AGVPathInfo[vr.AGVID]
		if !ok {
			continue
		}
		if currentTime-meta.LastProgress > 30 {
			if len(meta.Path) > 0 {
				step := meta.ExpectedStep
				if step >= len(meta.Path) {
					step = len(meta.Path) - 1
				}
				if step >= 0 {
					key := fmt.Sprintf("%d,%d", meta.Path[step].X/5, meta.Path[step].Y/5)
					hotSpots[key]++
				}
			}
		}
	}
	cbp.mu.RUnlock()

	result := make(map[string][]int)
	for key, count := range hotSpots {
		if count >= 3 {
			var cx, cy int
			fmt.Sscanf(key, "%d,%d", &cx, &cy)
			cx *= 5
			cy *= 5
			if agvs, deadlocked := cbp.DetectDeadlock(cx+2, cy+2, currentTime); deadlocked {
				result[key] = agvs
			}
		}
	}
	return result
}

func (cbp *ConflictBasedPlanner) ReplanningForAGV(agvID int, currentX, currentY, goalX, goalY, currentTime int) []gridmap.Position {
	cbp.mu.Lock()
	cbp.clearReservationsLocked(agvID)
	if meta, ok := cbp.AGVPathInfo[agvID]; ok {
		meta.DelayCount = 0
	}
	cbp.mu.Unlock()

	path := cbp.FindPath(agvID, currentX, currentY, goalX, goalY, currentTime, nil)
	if path != nil {
		cbp.ReservePath(agvID, path, currentTime)
	}
	return path
}

func (cbp *ConflictBasedPlanner) EmergencyYield(yielderID, currentTime, cx, cy int) {
	cbp.mu.Lock()
	defer cbp.mu.Unlock()
	for t := currentTime; t < currentTime+20; t++ {
		for dx := -1; dx <= 1; dx++ {
			for dy := -1; dy <= 1; dy++ {
				k := cbp.vkey(cx+dx, cy+dy, t)
				if vr, ok := cbp.VertexReservations[k]; ok && vr.AGVID == yielderID {
					delete(cbp.VertexReservations, k)
				}
			}
		}
	}
}

func (cbp *ConflictBasedPlanner) PlanForMultiple(agvs []*PlanRequest) map[int][]gridmap.Position {
	results := make(map[int][]gridmap.Position)

	sorted := make([]*PlanRequest, len(agvs))
	copy(sorted, agvs)
	for i := 1; i < len(sorted); i++ {
		for j := i; j > 0 && sorted[j].Priority > sorted[j-1].Priority; j-- {
			sorted[j], sorted[j-1] = sorted[j-1], sorted[j]
		}
	}

	for _, req := range sorted {
		cbp.ClearReservations(req.AGVID)
	}

	for _, req := range sorted {
		path := cbp.FindPath(req.AGVID, req.StartX, req.StartY, req.GoalX, req.GoalY, req.StartTime, nil)
		if path != nil {
			results[req.AGVID] = path
			cbp.ReservePath(req.AGVID, path, req.StartTime)
		}
	}

	return results
}
