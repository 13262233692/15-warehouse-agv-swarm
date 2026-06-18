package pathfinding

import (
	"container/heap"
	"math"
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

func (pq PriorityQueue) Less(i, j int) bool {
	return pq[i].F < pq[j].F
}

func (pq PriorityQueue) Swap(i, j int) {
	pq[i], pq[j] = pq[j], pq[i]
}

func (pq *PriorityQueue) Push(x interface{}) {
	node := x.(*Node)
	*pq = append(*pq, node)
}

func (pq *PriorityQueue) Pop() interface{} {
	old := *pq
	n := len(old)
	node := old[n-1]
	*pq = old[0 : n-1]
	return node
}

type Reservation struct {
	AGVID int
	X     int
	Y     int
	Time  int
}

type ConflictBasedPlanner struct {
	GM           *gridmap.GridMap
	Reservations map[string]int
}

func NewConflictBasedPlanner(gm *gridmap.GridMap) *ConflictBasedPlanner {
	return &ConflictBasedPlanner{
		GM:           gm,
		Reservations: make(map[string]int),
	}
}

func (cbp *ConflictBasedPlanner) key(x, y, t int) string {
	return string(rune(x)) + "," + string(rune(y)) + "," + string(rune(t))
}

func (cbp *ConflictBasedPlanner) posKey(x, y int) string {
	return string(rune(x)) + "," + string(rune(y))
}

func heuristic(x1, y1, x2, y2 int) float64 {
	return math.Abs(float64(x1-x2)) + math.Abs(float64(y1-y2))
}

func (cbp *ConflictBasedPlanner) FindPath(agvID, sx, sy, gx, gy, startTime int, avoidAGVs map[int]bool) []gridmap.Position {
	openList := &PriorityQueue{}
	heap.Init(openList)
	closedSet := make(map[string]bool)

	start := &Node{X: sx, Y: sy, G: 0, H: heuristic(sx, sy, gx, gy), TimeStep: startTime}
	start.F = start.G + start.H
	heap.Push(openList, start)

	directions := [][]int{{0, 0}, {1, 0}, {-1, 0}, {0, 1}, {0, -1}}

	for openList.Len() > 0 {
		current := heap.Pop(openList).(*Node)

		if current.X == gx && current.Y == gy {
			path := []gridmap.Position{}
			for node := current; node != nil; node = node.Parent {
				path = append([]gridmap.Position{{X: node.X, Y: node.Y}}, path...)
			}
			return path
		}

		ck := cbp.key(current.X, current.Y, current.TimeStep)
		if closedSet[ck] {
			continue
		}
		closedSet[ck] = true

		for _, dir := range directions {
			nx := current.X + dir[0]
			ny := current.Y + dir[1]
			nt := current.TimeStep + 1

			if !cbp.GM.IsWalkable(nx, ny) {
				continue
			}

			rk := cbp.key(nx, ny, nt)
			if ownerID, exists := cbp.Reservations[rk]; exists {
				if avoidAGVs != nil {
					if _, avoid := avoidAGVs[ownerID]; avoid {
						continue
					}
				} else if ownerID != agvID {
					continue
				}
			}

			swk := cbp.key(current.X, current.Y, nt)
			nwk := cbp.key(nx, ny, current.TimeStep)
			if ownerID1, e1 := cbp.Reservations[swk]; e1 {
				if ownerID2, e2 := cbp.Reservations[nwk]; e2 {
					if ownerID1 != agvID && ownerID2 != agvID && ownerID1 != ownerID2 {
						continue
					}
				}
			}

			ng := current.G + 1
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

	return nil
}

func (cbp *ConflictBasedPlanner) ReservePath(agvID int, path []gridmap.Position, startTime int) {
	for i, pos := range path {
		t := startTime + i
		cbp.Reservations[cbp.key(pos.X, pos.Y, t)] = agvID
	}
}

func (cbp *ConflictBasedPlanner) ClearReservations(agvID int) {
	for k, v := range cbp.Reservations {
		if v == agvID {
			delete(cbp.Reservations, k)
		}
	}
}

func (cbp *ConflictBasedPlanner) PlanForMultiple(agvs []*PlanRequest) map[int][]gridmap.Position {
	results := make(map[int][]gridmap.Position)

	for _, req := range agvs {
		cbp.ClearReservations(req.AGVID)
	}

	for _, req := range agvs {
		path := cbp.FindPath(req.AGVID, req.StartX, req.StartY, req.GoalX, req.GoalY, req.StartTime, nil)
		if path != nil {
			results[req.AGVID] = path
			cbp.ReservePath(req.AGVID, path, req.StartTime)
		}
	}

	return results
}

type PlanRequest struct {
	AGVID     int
	StartX    int
	StartY    int
	GoalX     int
	GoalY     int
	StartTime int
}
