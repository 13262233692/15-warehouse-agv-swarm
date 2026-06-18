package main

import (
	"log"
	"net/http"
	"warehouse-agv-swarm/internal/agv"
	"warehouse-agv-swarm/internal/gridmap"
	"warehouse-agv-swarm/internal/order"
	"warehouse-agv-swarm/internal/scheduler"
	"warehouse-agv-swarm/internal/tcp"
	"warehouse-agv-swarm/internal/ws"
)

func main() {
	log.Println("=== Warehouse AGV Swarm Central Control Platform ===")

	gm := gridmap.NewGridMap()
	log.Printf("[Grid] 100x100 map initialized, %d shelves, %d charging points",
		len(gm.GetAllShelves()), len(gm.GetChargingPositions()))

	agvManager := agv.NewManager(55, gm)
	log.Printf("[AGV] %d AGV(s) initialized", len(agvManager.GetAll()))

	orderManager := order.NewManager()
	log.Println("[Order] Order manager initialized")

	sched := scheduler.New(gm, agvManager, orderManager)
	sched.Start()
	log.Println("[Scheduler] Started")

	tcpServer := tcp.NewServer(":8888")
	err := tcpServer.Start()
	if err != nil {
		log.Printf("[TCP] Failed to start: %v", err)
	} else {
		log.Println("[TCP] Server running on :8888")
	}

	wsServer := ws.NewServer(gm, agvManager, orderManager)

	mux := http.NewServeMux()
	mux.HandleFunc("/ws", wsServer.HandleWebSocket)
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"status":"ok","agvs":` + itoa(len(agvManager.GetAll())) + `}`))
	})

	go wsServer.StartBroadcast()

	log.Println("[HTTP] WebSocket + API running on :8080")
	log.Println("[System] All services started successfully")

	if err := http.ListenAndServe(":8080", mux); err != nil {
		log.Fatalf("[HTTP] Server failed: %v", err)
	}
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	neg := i < 0
	if neg {
		i = -i
	}
	var buf [20]byte
	pos := len(buf)
	for i > 0 {
		pos--
		buf[pos] = byte('0' + i%10)
		i /= 10
	}
	if neg {
		pos--
		buf[pos] = '-'
	}
	return string(buf[pos:])
}
