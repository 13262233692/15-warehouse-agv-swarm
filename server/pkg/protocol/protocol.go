package protocol

import (
	"encoding/binary"
	"errors"
)

const (
	MagicNumber   = uint16(0xABCD)
	HeaderSize    = 8
	MaxPacketSize = 4096
)

const (
	CmdHeartbeat  = uint8(0x01)
	CmdMoveTo     = uint8(0x02)
	CmdStatus     = uint8(0x03)
	CmdPickUp     = uint8(0x04)
	CmdPutDown    = uint8(0x05)
	CmdPath       = uint8(0x06)
	CmdAck        = uint8(0x07)
)

type Packet struct {
	Magic    uint16
	Length   uint16
	Seq      uint16
	Cmd      uint8
	Reserved uint8
	Payload  []byte
}

type MoveCommand struct {
	AGVID uint32
	X     int32
	Y     int32
}

type StatusReport struct {
	AGVID     uint32
	X         int32
	Y         int32
	Status    uint8
	HasCargo  uint8
	Battery   uint8
	Timestamp uint64
}

type PathCommand struct {
	AGVID   uint32
	PathLen uint32
	Path    []Point
}

type Point struct {
	X int32
	Y int32
}

func Encode(p *Packet) ([]byte, error) {
	if p.Length != uint16(len(p.Payload)) {
		p.Length = uint16(len(p.Payload))
	}
	total := HeaderSize + int(p.Length)
	if total > MaxPacketSize {
		return nil, errors.New("packet too large")
	}
	buf := make([]byte, total)
	binary.LittleEndian.PutUint16(buf[0:2], p.Magic)
	binary.LittleEndian.PutUint16(buf[2:4], p.Length)
	binary.LittleEndian.PutUint16(buf[4:6], p.Seq)
	buf[6] = p.Cmd
	buf[7] = p.Reserved
	if p.Length > 0 {
		copy(buf[HeaderSize:], p.Payload)
	}
	return buf, nil
}

func Decode(data []byte) (*Packet, int, error) {
	if len(data) < HeaderSize {
		return nil, 0, errors.New("incomplete header")
	}
	magic := binary.LittleEndian.Uint16(data[0:2])
	if magic != MagicNumber {
		return nil, 1, errors.New("invalid magic number")
	}
	length := binary.LittleEndian.Uint16(data[2:4])
	total := HeaderSize + int(length)
	if len(data) < total {
		return nil, 0, errors.New("incomplete payload")
	}
	p := &Packet{
		Magic:    magic,
		Length:   length,
		Seq:      binary.LittleEndian.Uint16(data[4:6]),
		Cmd:      data[6],
		Reserved: data[7],
	}
	if length > 0 {
		p.Payload = make([]byte, length)
		copy(p.Payload, data[HeaderSize:total])
	}
	return p, total, nil
}

func EncodeMoveCmd(mc *MoveCommand) []byte {
	buf := make([]byte, 12)
	binary.LittleEndian.PutUint32(buf[0:4], mc.AGVID)
	binary.LittleEndian.PutUint32(buf[4:8], uint32(mc.X))
	binary.LittleEndian.PutUint32(buf[8:12], uint32(mc.Y))
	return buf
}

func DecodeStatusReport(payload []byte) *StatusReport {
	if len(payload) < 19 {
		return nil
	}
	return &StatusReport{
		AGVID:     binary.LittleEndian.Uint32(payload[0:4]),
		X:         int32(binary.LittleEndian.Uint32(payload[4:8])),
		Y:         int32(binary.LittleEndian.Uint32(payload[8:12])),
		Status:    payload[12],
		HasCargo:  payload[13],
		Battery:   payload[14],
		Timestamp: binary.LittleEndian.Uint64(payload[15:23]),
	}
}

func EncodePathCmd(pc *PathCommand) []byte {
	buf := make([]byte, 8+8*len(pc.Path))
	binary.LittleEndian.PutUint32(buf[0:4], pc.AGVID)
	binary.LittleEndian.PutUint32(buf[4:8], uint32(len(pc.Path)))
	for i, pt := range pc.Path {
		off := 8 + i*8
		binary.LittleEndian.PutUint32(buf[off:off+4], uint32(pt.X))
		binary.LittleEndian.PutUint32(buf[off+4:off+8], uint32(pt.Y))
	}
	return buf
}
