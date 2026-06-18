package tcp

import (
	"log"
	"net"
	"sync"
	"warehouse-agv-swarm/pkg/protocol"
)

type AGVConnection struct {
	ID   int
	Conn net.Conn
	mu   sync.Mutex
}

type Server struct {
	ListenAddr string
	Conns      map[int]*AGVConnection
	connByAddr map[string]*AGVConnection
	mu         sync.RWMutex
	nextSeq    uint16
	seqMu      sync.Mutex
	onStatus   func(*protocol.StatusReport)
}

func NewServer(addr string) *Server {
	return &Server{
		ListenAddr: addr,
		Conns:      make(map[int]*AGVConnection),
		connByAddr: make(map[string]*AGVConnection),
	}
}

func (s *Server) SetStatusHandler(h func(*protocol.StatusReport)) {
	s.onStatus = h
}

func (s *Server) Start() error {
	listener, err := net.Listen("tcp", s.ListenAddr)
	if err != nil {
		return err
	}
	log.Printf("[TCP] Listening on %s", s.ListenAddr)
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				log.Printf("[TCP] Accept error: %v", err)
				continue
			}
			go s.handleConnection(conn)
		}
	}()
	return nil
}

func (s *Server) handleConnection(conn net.Conn) {
	defer conn.Close()
	addr := conn.RemoteAddr().String()
	log.Printf("[TCP] New connection from %s", addr)

	var assignedID int
	s.mu.Lock()
	agvConn := &AGVConnection{ID: len(s.Conns), Conn: conn}
	s.connByAddr[addr] = agvConn
	assignedID = agvConn.ID
	s.Conns[assignedID] = agvConn
	s.mu.Unlock()

	defer func() {
		s.mu.Lock()
		delete(s.Conns, assignedID)
		delete(s.connByAddr, addr)
		s.mu.Unlock()
	}()

	buffer := make([]byte, 0, 8192)
	tmp := make([]byte, 4096)
	for {
		n, err := conn.Read(tmp)
		if err != nil {
			log.Printf("[TCP] Read error from %s: %v", addr, err)
			return
		}
		buffer = append(buffer, tmp[:n]...)

		for {
			pkt, consumed, err := protocol.Decode(buffer)
			if err != nil {
				if err.Error() == "incomplete header" || err.Error() == "incomplete payload" {
					break
				}
				buffer = buffer[consumed:]
				continue
			}
			buffer = buffer[consumed:]
			s.handlePacket(agvConn, pkt)
		}
	}
}

func (s *Server) handlePacket(c *AGVConnection, pkt *protocol.Packet) {
	switch pkt.Cmd {
	case protocol.CmdHeartbeat:
		s.sendAck(c, pkt.Seq)
	case protocol.CmdStatus:
		sr := protocol.DecodeStatusReport(pkt.Payload)
		if sr != nil {
			if s.onStatus != nil {
				s.onStatus(sr)
			}
		}
		s.sendAck(c, pkt.Seq)
	}
}

func (s *Server) sendAck(c *AGVConnection, seq uint16) {
	pkt := &protocol.Packet{
		Magic: protocol.MagicNumber,
		Seq:   seq,
		Cmd:   protocol.CmdAck,
	}
	s.sendPacket(c, pkt)
}

func (s *Server) nextSeqNum() uint16 {
	s.seqMu.Lock()
	defer s.seqMu.Unlock()
	s.nextSeq++
	return s.nextSeq
}

func (s *Server) sendPacket(c *AGVConnection, pkt *protocol.Packet) error {
	data, err := protocol.Encode(pkt)
	if err != nil {
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	_, err = c.Conn.Write(data)
	return err
}

func (s *Server) SendMoveCommand(agvID int, x, y int) error {
	s.mu.RLock()
	c, ok := s.Conns[agvID]
	s.mu.RUnlock()
	if !ok {
		return nil
	}
	mc := &protocol.MoveCommand{
		AGVID: uint32(agvID),
		X:     int32(x),
		Y:     int32(y),
	}
	pkt := &protocol.Packet{
		Magic:   protocol.MagicNumber,
		Seq:     s.nextSeqNum(),
		Cmd:     protocol.CmdMoveTo,
		Payload: protocol.EncodeMoveCmd(mc),
	}
	return s.sendPacket(c, pkt)
}

func (s *Server) SendPathCommand(agvID int, path []protocol.Point) error {
	s.mu.RLock()
	c, ok := s.Conns[agvID]
	s.mu.RUnlock()
	if !ok {
		return nil
	}
	pc := &protocol.PathCommand{
		AGVID: uint32(agvID),
		Path:  path,
	}
	pkt := &protocol.Packet{
		Magic:   protocol.MagicNumber,
		Seq:     s.nextSeqNum(),
		Cmd:     protocol.CmdPath,
		Payload: protocol.EncodePathCmd(pc),
	}
	return s.sendPacket(c, pkt)
}
