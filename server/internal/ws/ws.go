package ws

import (
	"encoding/json"
	"log"
	"net/http"
	"sync"
	"time"
	"warehouse-agv-swarm/internal/agv"
	"warehouse-agv-swarm/internal/gridmap"
	"warehouse-agv-swarm/internal/order"

	"github.com/gorilla/websocket"
)

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool {
		return true
	},
}

type AGVState struct {
	ID          int     `json:"id"`
	X           int     `json:"x"`
	Y           int     `json:"y"`
	Status      int     `json:"status"`
	HasCargo    bool    `json:"hasCargo"`
	Battery     int     `json:"battery"`
	TaskID      int     `json:"taskId"`
	Temperature float64 `json:"temperature"`
	Voltage     float64 `json:"voltage"`
	IsCharging  bool    `json:"isCharging"`
	IsReturning bool    `json:"isReturning"`
	LowBattery  bool    `json:"lowBattery"`
}

type MapCell struct {
	X int `json:"x"`
	Y int `json:"y"`
	T int `json:"t"`
}

type OrderState struct {
	ID        int   `json:"id"`
	Type      int   `json:"type"`
	Status    int   `json:"status"`
	AGVID     int   `json:"agvId"`
	PickupX   int   `json:"pickupX"`
	PickupY   int   `json:"pickupY"`
	DropoffX  int   `json:"dropoffX"`
	DropoffY  int   `json:"dropoffY"`
}

type Message struct {
	Type    string      `json:"type"`
	Payload interface{} `json:"payload"`
}

type Server struct {
	GM           *gridmap.GridMap
	AGVManager   *agv.Manager
	OrderManager *order.Manager
	clients      map[*websocket.Conn]bool
	mu           sync.RWMutex
}

func NewServer(gm *gridmap.GridMap, am *agv.Manager, om *order.Manager) *Server {
	return &Server{
		GM:           gm,
		AGVManager:   am,
		OrderManager: om,
		clients:      make(map[*websocket.Conn]bool),
	}
}

func (s *Server) HandleWebSocket(w http.ResponseWriter, r *http.Request) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("[WS] Upgrade error: %v", err)
		return
	}
	log.Printf("[WS] New client connected: %s", conn.RemoteAddr())

	s.mu.Lock()
	s.clients[conn] = true
	s.mu.Unlock()

	defer func() {
		s.mu.Lock()
		delete(s.clients, conn)
		s.mu.Unlock()
		conn.Close()
		log.Printf("[WS] Client disconnected")
	}()

	s.sendInitialMap(conn)
	s.sendFullState(conn)

	for {
		_, _, err := conn.ReadMessage()
		if err != nil {
			break
		}
	}
}

func (s *Server) sendInitialMap(conn *websocket.Conn) {
	cells := []MapCell{}
	for y := 0; y < s.GM.Height; y++ {
		for x := 0; x < s.GM.Width; x++ {
			t := s.GM.GetCell(x, y)
			if t != 0 {
				cells = append(cells, MapCell{X: x, Y: y, T: t})
			}
		}
	}
	msg := Message{Type: "map", Payload: cells}
	conn.WriteJSON(msg)
}

func (s *Server) sendFullState(conn *websocket.Conn) {
	agvs := s.collectAGVStates()
	msg := Message{Type: "agvs", Payload: agvs}
	conn.WriteJSON(msg)

	orders := s.collectOrderStates()
	omsg := Message{Type: "orders", Payload: orders}
	conn.WriteJSON(omsg)
}

func (s *Server) collectAGVStates() []AGVState {
	states := []AGVState{}
	for _, a := range s.AGVManager.GetAll() {
		x, y, hasCargo := a.GetPosition()
		bat := a.GetBattery()
		st := a.GetStatus()
		isCharging := st == agv.StatusCharging || a.IsCharging()
		isReturning := a.IsReturning()
		lowBat := bat.SOC < 30 || st == agv.StatusLowBattery
		states = append(states, AGVState{
			ID:          a.ID,
			X:           x,
			Y:           y,
			Status:      st,
			HasCargo:    hasCargo,
			Battery:     bat.SOC,
			TaskID:      a.CurrentTask,
			Temperature: bat.Temperature,
			Voltage:     bat.Voltage,
			IsCharging:  isCharging,
			IsReturning: isReturning,
			LowBattery:  lowBat,
		})
	}
	return states
}

func (s *Server) collectOrderStates() []OrderState {
	states := []OrderState{}
	for _, o := range s.OrderManager.GetAll() {
		states = append(states, OrderState{
			ID:        o.ID,
			Type:      o.Type,
			Status:    o.Status,
			AGVID:     o.AGVID,
			PickupX:   o.PickupX,
			PickupY:   o.PickupY,
			DropoffX:  o.DropoffX,
			DropoffY:  o.DropoffY,
		})
	}
	return states
}

func (s *Server) StartBroadcast() {
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	for range ticker.C {
		s.broadcastState()
	}
}

func (s *Server) broadcastState() {
	agvs := s.collectAGVStates()
	msg := Message{Type: "agvs", Payload: agvs}
	s.broadcast(msg)

	orders := s.collectOrderStates()
	omsg := Message{Type: "orders", Payload: orders}
	s.broadcast(omsg)
}

func (s *Server) broadcast(msg Message) {
	data, err := json.Marshal(msg)
	if err != nil {
		return
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	for c := range s.clients {
		err := c.WriteMessage(websocket.TextMessage, data)
		if err != nil {
			continue
		}
	}
}
